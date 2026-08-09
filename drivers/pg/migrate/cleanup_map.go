package migrate

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
)

type cleanupIndexFact struct {
	Name       string `json:"name"`
	Unique     bool   `json:"unique"`
	Primary    bool   `json:"primary"`
	Valid      bool   `json:"valid"`
	Ready      bool   `json:"ready"`
	Definition string `json:"definition"`
}

type cleanupConstraintFact struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Validated  bool   `json:"validated"`
	Deferrable bool   `json:"deferrable"`
	Deferred   bool   `json:"deferred"`
	Definition string `json:"definition"`
}

type cleanupTriggerFact struct {
	Name           string `json:"name"`
	Enabled        string `json:"enabled"`
	Internal       bool   `json:"internal"`
	FunctionSchema string `json:"function_schema"`
	FunctionName   string `json:"function_name"`
	Definition     string `json:"definition"`
}

type cleanupDependencyFact struct {
	ObjectType         string `json:"object_type"`
	ObjectSchema       string `json:"object_schema"`
	ObjectIdentity     string `json:"object_identity"`
	ReferencedType     string `json:"referenced_type"`
	ReferencedSchema   string `json:"referenced_schema"`
	ReferencedIdentity string `json:"referenced_identity"`
	Type               string `json:"dependency_type"`
}

type cleanupObjectFact struct {
	Version           int                     `json:"version"`
	Schema            string                  `json:"schema"`
	Name              string                  `json:"name"`
	Owner             string                  `json:"owner"`
	Kind              string                  `json:"kind"`
	Persistence       string                  `json:"persistence"`
	ReplicaIdentity   string                  `json:"replica_identity"`
	Tablespace        string                  `json:"tablespace"`
	Comment           string                  `json:"comment"`
	RowSecurity       bool                    `json:"row_security"`
	ForceRowSecurity  bool                    `json:"force_row_security"`
	StorageParameters []string                `json:"storage_parameters"`
	ACL               []catalog.ACL           `json:"acl"`
	Indexes           []cleanupIndexFact      `json:"indexes"`
	Constraints       []cleanupConstraintFact `json:"constraints"`
	Triggers          []cleanupTriggerFact    `json:"triggers"`
	Dependencies      []cleanupDependencyFact `json:"dependencies"`
}

type cleanupMapEntry struct {
	Order       int               `json:"order"`
	Schema      string            `json:"schema"`
	Name        string            `json:"name"`
	OriginalOID uint32            `json:"original_oid"`
	Relkind     string            `json:"relkind"`
	Fingerprint string            `json:"fingerprint"`
	Facts       cleanupObjectFact `json:"facts"`
}

type cleanupMapFacts struct {
	Version    int               `json:"version"`
	ScopeKind  string            `json:"scope_kind"`
	ScopeID    string            `json:"scope_id"`
	Applicable bool              `json:"applicable"`
	Entries    []cleanupMapEntry `json:"entries"`
}

func cleanupMapDigest(scopeKind, scopeID string, applicable bool, entries []cleanupMapEntry) (string, error) {
	digest, _, err := canonicalDigest(cleanupMapFacts{1, scopeKind, scopeID, applicable, entries})
	return digest, err
}

func cleanupFact(snapshot catalog.Snapshot, relation catalog.Relation) cleanupObjectFact {
	fact := cleanupObjectFact{Version: 1, Schema: relation.Schema, Name: relation.Name, Owner: relation.Owner,
		Kind: relation.Kind, Persistence: relation.Persistence, ReplicaIdentity: relation.ReplicaIdentity,
		Tablespace: relation.Tablespace, Comment: relation.Comment, RowSecurity: relation.RowSecurity,
		ForceRowSecurity: relation.ForceRowSecurity, StorageParameters: relation.StorageParameters, ACL: relation.ACL}
	relevant := map[uint32]struct{}{relation.OID: {}}
	for _, value := range snapshot.Indexes {
		if value.RelationOID == relation.OID {
			fact.Indexes = append(fact.Indexes, cleanupIndexFact{value.Name, value.Unique, value.Primary, value.Valid, value.Ready, value.Definition})
			relevant[value.OID] = struct{}{}
		}
	}
	for _, value := range snapshot.Constraints {
		if value.RelationOID == relation.OID {
			fact.Constraints = append(fact.Constraints, cleanupConstraintFact{value.Name, value.Type, value.Validated, value.Deferrable, value.Deferred, value.Definition})
			relevant[value.OID] = struct{}{}
		}
	}
	for _, value := range snapshot.Triggers {
		if value.RelationOID == relation.OID {
			fact.Triggers = append(fact.Triggers, cleanupTriggerFact{value.Name, value.Enabled, value.Internal, value.FunctionSchema, value.FunctionName, value.Definition})
			relevant[value.OID] = struct{}{}
		}
	}
	for _, value := range snapshot.Dependencies {
		if _, found := relevant[value.ObjectOID]; found {
			fact.Dependencies = append(fact.Dependencies, cleanupDependencyFact{value.ObjectType, value.ObjectSchema,
				value.ObjectIdentity, value.ReferencedType, value.ReferencedSchema, value.ReferencedIdentity, value.Type})
		}
	}
	sort.Slice(fact.Indexes, func(i, j int) bool { return fact.Indexes[i].Name < fact.Indexes[j].Name })
	sort.Slice(fact.Constraints, func(i, j int) bool { return fact.Constraints[i].Name < fact.Constraints[j].Name })
	sort.Slice(fact.Triggers, func(i, j int) bool { return fact.Triggers[i].Name < fact.Triggers[j].Name })
	sort.Slice(fact.Dependencies, func(i, j int) bool {
		left, right := fact.Dependencies[i], fact.Dependencies[j]
		return left.ObjectIdentity+left.ReferencedIdentity+left.Type < right.ObjectIdentity+right.ReferencedIdentity+right.Type
	})
	return fact
}

func sealCleanupMap(ctx context.Context, tx pgx.Tx, scopeKind, scopeID string, applicable bool, identityEpoch int64) (string, error) {
	entries := []cleanupMapEntry{}
	if applicable {
		rows, err := tx.Query(ctx, `select graph_id,legacy_relation,legacy_oid
from public.dawgs_schema_migration_graph where migration_id=$1 order by graph_id`, scopeID)
		if err != nil {
			return "", err
		}
		type retained struct {
			name string
			oid  uint32
		}
		var retainedObjects []retained
		for rows.Next() {
			var graphID int32
			var value retained
			if err := rows.Scan(&graphID, &value.name, &value.oid); err != nil {
				rows.Close()
				return "", err
			}
			retainedObjects = append(retainedObjects, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
		snapshot, err := catalog.ReadSnapshot(ctx, tx)
		if err != nil {
			return "", err
		}
		byOID := make(map[uint32]catalog.Relation, len(snapshot.Relations))
		for _, relation := range snapshot.Relations {
			byOID[relation.OID] = relation
		}
		for index, retained := range retainedObjects {
			relation, found := byOID[retained.oid]
			if !found || relation.Schema != "public" || relation.Name != retained.name {
				return "", fmt.Errorf("retained cleanup object %s OID %d changed before acceptance", retained.name, retained.oid)
			}
			facts := cleanupFact(snapshot, relation)
			fingerprint, _, err := canonicalDigest(facts)
			if err != nil {
				return "", err
			}
			entries = append(entries, cleanupMapEntry{index + 1, relation.Schema, relation.Name, relation.OID, relation.Kind, fingerprint, facts})
		}
	}
	mapDigest, err := cleanupMapDigest(scopeKind, scopeID, applicable, entries)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `insert into public.dawgs_legacy_cleanup_scope
(scope_kind,scope_id,applicable,map_digest,identity_epoch) values($1,$2,$3,$4,$5)`,
		scopeKind, scopeID, applicable, mapDigest, identityEpoch); err != nil {
		return "", err
	}
	for _, entry := range entries {
		_, factsJSON, err := canonicalDigest(entry.Facts)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `insert into public.dawgs_legacy_cleanup_map
(scope_kind,scope_id,object_order,object_schema,object_name,original_oid,relkind,object_fingerprint,logical_facts)
values($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)`, scopeKind, scopeID, entry.Order, entry.Schema,
			entry.Name, entry.OriginalOID, entry.Relkind, entry.Fingerprint, string(factsJSON)); err != nil {
			return "", err
		}
	}
	return mapDigest, nil
}
