package layout

import (
	"testing"

	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
	"github.com/stretchr/testify/require"
)

func validAcceptedSnapshot() catalog.Snapshot {
	return catalog.Snapshot{
		Relations:   []catalog.Relation{{OID: 1, Schema: "public", Name: "edge", Owner: "owner", Kind: "p", Persistence: "p", ReplicaIdentity: "d"}},
		Indexes:     []catalog.Index{{RelationOID: 1, Name: "edge_idx", Valid: true, Ready: true}},
		Constraints: []catalog.Constraint{{RelationOID: 1, Name: "edge_constraint", Validated: true}},
		Triggers:    []catalog.Trigger{{RelationOID: 1, Name: "edge_trigger", Enabled: "O", FunctionSchema: "public"}},
		Routines:    []catalog.Routine{{Schema: "public", Name: "dawgs_provision_graph", Owner: "owner", SecurityDefiner: true, Configuration: []string{"search_path=pg_catalog, public, pg_temp"}, ACL: []catalog.ACL{{Grantee: "owner", Privilege: "EXECUTE"}, {Grantee: "target", Privilege: "EXECUTE"}}}},
		Sequences:   []catalog.Sequence{{Schema: "public", Name: "edge_id_seq", Owner: "owner"}},
	}
}

func TestValidateAcceptedSnapshotAcceptsCanonicalFacts(t *testing.T) {
	require.Empty(t, ValidateAcceptedSnapshot(validAcceptedSnapshot(), "owner", "target"))
}

func TestValidateAcceptedSnapshotRejectsEveryModeledDriftClass(t *testing.T) {
	tests := map[string]func(*catalog.Snapshot){
		"owner":             func(v *catalog.Snapshot) { v.Relations[0].Owner = "operator" },
		"persistence":       func(v *catalog.Snapshot) { v.Relations[0].Persistence = "u" },
		"row security":      func(v *catalog.Snapshot) { v.Relations[0].RowSecurity = true },
		"replica identity":  func(v *catalog.Snapshot) { v.Relations[0].ReplicaIdentity = "f" },
		"storage parameter": func(v *catalog.Snapshot) { v.Relations[0].StorageParameters = []string{"fillfactor=50"} },
		"default partition": func(v *catalog.Snapshot) { v.Relations[0].Partition = &catalog.Partition{Default: true} },
		"index":             func(v *catalog.Snapshot) { v.Indexes[0].Ready = false },
		"constraint":        func(v *catalog.Snapshot) { v.Constraints[0].Validated = false },
		"trigger":           func(v *catalog.Snapshot) { v.Triggers[0].Enabled = "D" },
		"trigger schema":    func(v *catalog.Snapshot) { v.Triggers[0].FunctionSchema = "hostile" },
		"routine owner":     func(v *catalog.Snapshot) { v.Routines[0].Owner = "operator" },
		"routine path":      func(v *catalog.Snapshot) { v.Routines[0].Configuration = []string{"search_path=public"} },
		"routine public acl": func(v *catalog.Snapshot) {
			v.Routines[0].ACL = append(v.Routines[0].ACL, catalog.ACL{Grantee: "PUBLIC", Privilege: "EXECUTE"})
		},
		"routine target acl": func(v *catalog.Snapshot) {
			v.Routines[0].Name = "dawgs_claim_edge"
		},
		"sequence owner": func(v *catalog.Snapshot) { v.Sequences[0].Owner = "operator" },
		"sequence cycle": func(v *catalog.Snapshot) { v.Sequences[0].Cycle = true },
		"rule": func(v *catalog.Snapshot) {
			v.Rules = []catalog.Rule{{Schema: "public", Relation: "edge", Name: "rewrite"}}
		},
		"publication": func(v *catalog.Snapshot) { v.Publications = []catalog.Publication{{Name: "pub"}} },
		"membership":  func(v *catalog.Snapshot) { v.Memberships = []catalog.Membership{{Role: "owner", Member: "operator"}} },
		"role setting": func(v *catalog.Snapshot) {
			v.RoleSettings = []catalog.RoleSetting{{Role: "target", Values: []string{"role=operator"}}}
		},
		"default acl": func(v *catalog.Snapshot) {
			v.DefaultACLs = []catalog.DefaultACL{{Owner: "owner", Schema: "public", ObjectType: "r", ACL: []catalog.ACL{{Grantee: "target"}}}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validAcceptedSnapshot()
			mutate(&value)
			require.NotEmpty(t, ValidateAcceptedSnapshot(value, "owner", "target"))
		})
	}
}
