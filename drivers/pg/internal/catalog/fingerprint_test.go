package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFingerprintIsOIDIndependentAndStructureSensitive(t *testing.T) {
	parentOID := uint32(10)
	base := Snapshot{
		Relations: []Relation{
			{OID: 10, Schema: "public", Name: "edge", Owner: "owner", Kind: "p", Persistence: "p", ReplicaIdentity: "d"},
			{OID: 11, Schema: "public", Name: "edge_1", Owner: "owner", Kind: "p", Persistence: "p", ReplicaIdentity: "d", ParentOID: &parentOID,
				Partition: &Partition{Strategy: "l", KeyDefinition: "LIST (kind_id)", Bound: "FOR VALUES IN (1)"}},
		},
		Indexes: []Index{{OID: 20, RelationOID: 10, Name: "edge_pkey", Unique: true, Primary: true, Valid: true, Ready: true, Definition: "index"}},
		Dependencies: []Dependency{{ClassOID: 1259, ObjectOID: 11, ObjectType: "table", ObjectSchema: "public", ObjectName: "edge_1", ObjectIdentity: "public.edge_1",
			ReferencedClassOID: 1259, ReferencedObjectOID: 10, ReferencedType: "table", ReferencedSchema: "public", ReferencedName: "edge", ReferencedIdentity: "public.edge", Type: "n"}},
	}
	first, err := Fingerprint(base)
	require.NoError(t, err)
	require.Len(t, first, 64)

	newParentOID := uint32(110)
	restored := base
	restored.Relations = append([]Relation(nil), base.Relations...)
	restored.Relations[0].OID = 110
	restored.Relations[1].OID = 111
	restored.Relations[1].ParentOID = &newParentOID
	restored.Indexes = append([]Index(nil), base.Indexes...)
	restored.Indexes[0].OID = 120
	restored.Indexes[0].RelationOID = 110
	restored.Dependencies = append([]Dependency(nil), base.Dependencies...)
	restored.Dependencies[0].ObjectOID = 111
	restored.Dependencies[0].ReferencedObjectOID = 110
	second, err := Fingerprint(restored)
	require.NoError(t, err)
	require.Equal(t, first, second)

	restored.Relations[1].Partition.Bound = "FOR VALUES IN (2)"
	changed, err := Fingerprint(restored)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
}
