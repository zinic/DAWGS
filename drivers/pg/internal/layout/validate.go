package layout

import (
	"fmt"
	"sort"
	"strings"

	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
)

// ValidateAcceptedSnapshot applies catalog-wide invariants that are too broad
// for the scalar accepted-state classifier. It intentionally runs only during
// startup assertion, explicit Readiness, migration validation, and acceptance
// revalidation.
func ValidateAcceptedSnapshot(snapshot catalog.Snapshot, ownerRole, targetRole string) []string {
	var drift []string
	targetSecurityDefinerExecutions := map[string]struct{}{
		"dawgs_current_smoke_receipt_valid":   {},
		"dawgs_current_cleanup_receipt_valid": {},
		"dawgs_provision_graph":               {},
		"dawgs_provision_edge_kind_leaf":      {},
		"dawgs_wipe_graph":                    {},
	}
	managedRelations := make(map[uint32]string, len(snapshot.Relations))
	for _, relation := range snapshot.Relations {
		managedRelations[relation.OID] = relation.Schema + "." + relation.Name
		if relation.Schema != "public" {
			continue
		}
		if relation.Owner != ownerRole {
			drift = append(drift, fmt.Sprintf("relation %s.%s owner is %s, expected %s", relation.Schema, relation.Name, relation.Owner, ownerRole))
		}
		if relation.Persistence != "p" {
			drift = append(drift, fmt.Sprintf("relation %s.%s persistence is %s", relation.Schema, relation.Name, relation.Persistence))
		}
		if relation.Kind == "r" || relation.Kind == "p" {
			if relation.RowSecurity || relation.ForceRowSecurity {
				drift = append(drift, fmt.Sprintf("relation %s.%s has row security enabled", relation.Schema, relation.Name))
			}
			if relation.ReplicaIdentity != "d" {
				drift = append(drift, fmt.Sprintf("relation %s.%s replica identity is %s", relation.Schema, relation.Name, relation.ReplicaIdentity))
			}
			if len(relation.StorageParameters) != 0 {
				drift = append(drift, fmt.Sprintf("relation %s.%s has unexpected storage parameters", relation.Schema, relation.Name))
			}
		}
		if relation.Partition != nil && relation.Partition.Default {
			drift = append(drift, fmt.Sprintf("relation %s.%s is a forbidden default partition", relation.Schema, relation.Name))
		}
		for _, acl := range relation.ACL {
			if acl.Grantee == "PUBLIC" {
				drift = append(drift, fmt.Sprintf("relation %s.%s grants %s to PUBLIC", relation.Schema, relation.Name, acl.Privilege))
			}
		}
	}

	for _, index := range snapshot.Indexes {
		if _, managed := managedRelations[index.RelationOID]; managed && (!index.Valid || !index.Ready) {
			drift = append(drift, fmt.Sprintf("index %s is not valid and ready", index.Name))
		}
	}
	for _, constraint := range snapshot.Constraints {
		if _, managed := managedRelations[constraint.RelationOID]; managed && !constraint.Validated {
			drift = append(drift, fmt.Sprintf("constraint %s is not validated", constraint.Name))
		}
	}
	for _, trigger := range snapshot.Triggers {
		if _, managed := managedRelations[trigger.RelationOID]; managed && !trigger.Internal && trigger.Enabled != "O" && trigger.Enabled != "A" {
			drift = append(drift, fmt.Sprintf("trigger %s is not enabled", trigger.Name))
		}
		if _, managed := managedRelations[trigger.RelationOID]; managed && !trigger.Internal && trigger.FunctionSchema != "public" {
			drift = append(drift, fmt.Sprintf("trigger %s resolves outside public", trigger.Name))
		}
	}
	for _, routine := range snapshot.Routines {
		if routine.Schema != "public" {
			continue
		}
		if routine.Extension {
			continue
		}
		if routine.Owner != ownerRole {
			drift = append(drift, fmt.Sprintf("routine public.%s(%s) owner is %s, expected %s", routine.Name, routine.IdentityArguments, routine.Owner, ownerRole))
		}
		if routine.SecurityDefiner && !hasSafeRoutineSearchPath(routine.Configuration) {
			drift = append(drift, fmt.Sprintf("security-definer routine public.%s(%s) lacks the canonical search_path", routine.Name, routine.IdentityArguments))
		}
		for _, acl := range routine.ACL {
			if routine.SecurityDefiner && acl.Grantee == "PUBLIC" {
				drift = append(drift, fmt.Sprintf("security-definer routine public.%s(%s) grants %s to PUBLIC", routine.Name, routine.IdentityArguments, acl.Privilege))
			}
			if routine.SecurityDefiner && acl.Grantee == targetRole {
				if _, allowed := targetSecurityDefinerExecutions[routine.Name]; !allowed || acl.Privilege != "EXECUTE" || acl.Grantable {
					drift = append(drift, fmt.Sprintf("security-definer routine public.%s(%s) has an unsafe target grant", routine.Name, routine.IdentityArguments))
				}
			}
		}
	}
	for _, sequence := range snapshot.Sequences {
		if sequence.Schema == "public" && sequence.Owner != ownerRole {
			drift = append(drift, fmt.Sprintf("sequence public.%s owner is %s, expected %s", sequence.Name, sequence.Owner, ownerRole))
		}
		if sequence.Schema == "public" && sequence.Cycle {
			drift = append(drift, fmt.Sprintf("sequence public.%s unexpectedly cycles", sequence.Name))
		}
	}
	for _, rule := range snapshot.Rules {
		if rule.Schema == "public" {
			drift = append(drift, fmt.Sprintf("managed relation %s.%s has unexpected rule %s", rule.Schema, rule.Relation, rule.Name))
		}
	}
	for _, publication := range snapshot.Publications {
		drift = append(drift, fmt.Sprintf("unexpected logical publication %s", publication.Name))
	}
	for _, membership := range snapshot.Memberships {
		if membership.Role == ownerRole || membership.Member == ownerRole || membership.Role == targetRole || membership.Member == targetRole {
			drift = append(drift, fmt.Sprintf("owner or target participates in role membership %s->%s", membership.Role, membership.Member))
		}
	}
	for _, setting := range snapshot.RoleSettings {
		if setting.Role != ownerRole && setting.Role != targetRole {
			continue
		}
		for _, value := range setting.Values {
			normalized := strings.ToLower(strings.ReplaceAll(value, " ", ""))
			if strings.HasPrefix(normalized, "role=") || strings.HasPrefix(normalized, "session_authorization=") {
				drift = append(drift, fmt.Sprintf("role %s has forbidden startup identity setting", setting.Role))
			}
			if strings.HasPrefix(normalized, "search_path=") && normalized != "search_path=pg_catalog,public" {
				drift = append(drift, fmt.Sprintf("role %s has noncanonical search_path", setting.Role))
			}
		}
	}
	for _, defaultACL := range snapshot.DefaultACLs {
		if defaultACL.Owner != ownerRole || defaultACL.Schema != "public" {
			continue
		}
		for _, acl := range defaultACL.ACL {
			if acl.Grantee == "PUBLIC" ||
				(acl.Grantee == targetRole && (defaultACL.ObjectType == "r" || defaultACL.ObjectType == "S")) {
				drift = append(drift, fmt.Sprintf("unsafe default ACL for owner %s object type %s grantee %s", ownerRole, defaultACL.ObjectType, acl.Grantee))
			}
		}
	}

	sort.Strings(drift)
	return drift
}

func hasSafeRoutineSearchPath(configuration []string) bool {
	for _, value := range configuration {
		normalized := strings.ToLower(strings.ReplaceAll(value, " ", ""))
		if normalized == "search_path=pg_catalog,public,pg_temp" {
			return true
		}
	}
	return false
}
