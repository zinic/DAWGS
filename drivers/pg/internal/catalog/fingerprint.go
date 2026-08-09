package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalRelation struct {
	Schema            string     `json:"schema"`
	Name              string     `json:"name"`
	Owner             string     `json:"owner"`
	Kind              string     `json:"kind"`
	Persistence       string     `json:"persistence"`
	ReplicaIdentity   string     `json:"replica_identity"`
	Tablespace        string     `json:"tablespace"`
	Comment           string     `json:"comment"`
	RowSecurity       bool       `json:"row_security"`
	ForceRowSecurity  bool       `json:"force_row_security"`
	StorageParameters []string   `json:"storage_parameters"`
	Parent            string     `json:"parent,omitempty"`
	Partition         *Partition `json:"partition,omitempty"`
	ACL               []ACL      `json:"acl"`
}

type canonicalIndex struct {
	Relation   string `json:"relation"`
	Name       string `json:"name"`
	Unique     bool   `json:"unique"`
	Primary    bool   `json:"primary"`
	Valid      bool   `json:"valid"`
	Ready      bool   `json:"ready"`
	Definition string `json:"definition"`
}

type canonicalConstraint struct {
	Relation   string `json:"relation"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Validated  bool   `json:"validated"`
	Deferrable bool   `json:"deferrable"`
	Deferred   bool   `json:"deferred"`
	Definition string `json:"definition"`
}

type canonicalTrigger struct {
	Relation       string `json:"relation"`
	Name           string `json:"name"`
	Enabled        string `json:"enabled"`
	Internal       bool   `json:"internal"`
	FunctionSchema string `json:"function_schema"`
	FunctionName   string `json:"function_name"`
	Definition     string `json:"definition"`
}

type canonicalDependency struct {
	ObjectType         string `json:"object_type"`
	ObjectSchema       string `json:"object_schema"`
	ObjectName         string `json:"object_name"`
	ObjectIdentity     string `json:"object_identity"`
	ReferencedType     string `json:"referenced_type"`
	ReferencedSchema   string `json:"referenced_schema"`
	ReferencedName     string `json:"referenced_name"`
	ReferencedIdentity string `json:"referenced_identity"`
	Type               string `json:"type"`
}

// Fingerprint returns an OID-independent digest over the ordered structural
// and authorization facts in a catalog snapshot. OIDs are used only to resolve
// parent and owning-relation names within the captured incarnation.
func Fingerprint(snapshot Snapshot) (string, error) {
	return FingerprintWithExclusions(snapshot)
}

func FingerprintWithExclusions(snapshot Snapshot, excludedRelations ...string) (string, error) {
	excludedNames := make(map[string]struct{}, len(excludedRelations))
	for _, name := range excludedRelations {
		excludedNames[name] = struct{}{}
	}
	relationNames := make(map[uint32]string, len(snapshot.Relations))
	excludedOIDs := make(map[uint32]struct{}, len(excludedRelations))
	for _, relation := range snapshot.Relations {
		qualifiedName := relation.Schema + "." + relation.Name
		relationNames[relation.OID] = qualifiedName
		if _, excluded := excludedNames[qualifiedName]; excluded {
			excludedOIDs[relation.OID] = struct{}{}
		}
	}
	// Indexes are also pg_class relations. Excluding a control table must
	// exclude its index relations from both the relation and index projections.
	for _, index := range snapshot.Indexes {
		if _, excluded := excludedOIDs[index.RelationOID]; excluded {
			excludedOIDs[index.OID] = struct{}{}
		}
	}
	facts := struct {
		Version      int                   `json:"version"`
		Relations    []canonicalRelation   `json:"relations"`
		Indexes      []canonicalIndex      `json:"indexes"`
		Constraints  []canonicalConstraint `json:"constraints"`
		Triggers     []canonicalTrigger    `json:"triggers"`
		Dependencies []canonicalDependency `json:"dependencies"`
		Routines     []Routine             `json:"routines"`
		Sequences    []Sequence            `json:"sequences"`
		Rules        []Rule                `json:"rules"`
		Publications []Publication         `json:"publications"`
		Memberships  []Membership          `json:"memberships"`
		RoleSettings []RoleSetting         `json:"role_settings"`
		DefaultACLs  []DefaultACL          `json:"default_acls"`
	}{Version: 1, Rules: snapshot.Rules, Publications: snapshot.Publications, Memberships: snapshot.Memberships,
		RoleSettings: snapshot.RoleSettings, DefaultACLs: snapshot.DefaultACLs}
	for _, routine := range snapshot.Routines {
		routine.OID = 0
		facts.Routines = append(facts.Routines, routine)
	}
	for _, sequence := range snapshot.Sequences {
		sequence.OID = 0
		facts.Sequences = append(facts.Sequences, sequence)
	}
	for _, relation := range snapshot.Relations {
		if _, excluded := excludedOIDs[relation.OID]; excluded {
			continue
		}
		parent := ""
		if relation.ParentOID != nil {
			parent = relationNames[*relation.ParentOID]
		}
		facts.Relations = append(facts.Relations, canonicalRelation{
			Schema: relation.Schema, Name: relation.Name, Owner: relation.Owner, Kind: relation.Kind,
			Persistence: relation.Persistence, ReplicaIdentity: relation.ReplicaIdentity,
			Tablespace: relation.Tablespace, Comment: relation.Comment, RowSecurity: relation.RowSecurity,
			ForceRowSecurity: relation.ForceRowSecurity, StorageParameters: relation.StorageParameters,
			Parent: parent, Partition: relation.Partition, ACL: relation.ACL,
		})
	}
	for _, index := range snapshot.Indexes {
		if _, excluded := excludedOIDs[index.RelationOID]; excluded {
			continue
		}
		facts.Indexes = append(facts.Indexes, canonicalIndex{Relation: relationNames[index.RelationOID], Name: index.Name,
			Unique: index.Unique, Primary: index.Primary, Valid: index.Valid, Ready: index.Ready, Definition: index.Definition})
	}
	for _, constraint := range snapshot.Constraints {
		if _, excluded := excludedOIDs[constraint.RelationOID]; excluded {
			continue
		}
		facts.Constraints = append(facts.Constraints, canonicalConstraint{Relation: relationNames[constraint.RelationOID],
			Name: constraint.Name, Type: constraint.Type, Validated: constraint.Validated, Deferrable: constraint.Deferrable,
			Deferred: constraint.Deferred, Definition: constraint.Definition})
	}
	for _, trigger := range snapshot.Triggers {
		if _, excluded := excludedOIDs[trigger.RelationOID]; excluded {
			continue
		}
		facts.Triggers = append(facts.Triggers, canonicalTrigger{Relation: relationNames[trigger.RelationOID], Name: trigger.Name,
			Enabled: trigger.Enabled, Internal: trigger.Internal, FunctionSchema: trigger.FunctionSchema,
			FunctionName: trigger.FunctionName, Definition: trigger.Definition})
	}
	for _, dependency := range snapshot.Dependencies {
		if _, excluded := excludedOIDs[dependency.ObjectOID]; excluded {
			continue
		}
		if _, excluded := excludedOIDs[dependency.ReferencedObjectOID]; excluded {
			continue
		}
		facts.Dependencies = append(facts.Dependencies, canonicalDependency{
			ObjectType: dependency.ObjectType, ObjectSchema: dependency.ObjectSchema,
			ObjectName: dependency.ObjectName, ObjectIdentity: dependency.ObjectIdentity,
			ReferencedType: dependency.ReferencedType, ReferencedSchema: dependency.ReferencedSchema,
			ReferencedName: dependency.ReferencedName, ReferencedIdentity: dependency.ReferencedIdentity,
			Type: dependency.Type,
		})
	}
	content, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}
