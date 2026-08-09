package migrate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const validManifest = `{"version":1,"installation_id":"install-1","source_configuration":{"artifact":"drivers/pg/migrate/testdata/v1_schema.sql","sha256":"8bea76272e005639d34643f8311599440e1eccc8c3152ca4da1e6dda42c87e0f"},"owner_attestation":{"owner_id":"owner","attested_at":"2026-08-01T00:00:00Z","statement_version":1},"graphs":[{"name":"default","edge_kinds":["B","A"]}],"excluded_unmaterialized_graphs":[]}`

func TestDecodeManifestCanonicalizesKinds(t *testing.T) {
	manifest, err := DecodeManifest(strings.NewReader(validManifest))
	require.NoError(t, err)
	require.Equal(t, []string{"A", "B"}, manifest.Graphs[0].EdgeKinds)
	digest, err := manifest.CanonicalDigest()
	require.NoError(t, err)
	require.Len(t, digest, 64)
	require.NoError(t, manifest.ValidateFrozenV1Lineage())
}

func TestManifestRejectsUnrecognizedV1LineageAtPreflightBoundary(t *testing.T) {
	manifest, err := DecodeManifest(strings.NewReader(strings.Replace(validManifest, frozenV1SHA256, strings.Repeat("b", 64), 1)))
	require.NoError(t, err)
	require.ErrorContains(t, manifest.ValidateFrozenV1Lineage(), "unrecognized_v1_lineage")
}

func TestDecodeManifestRejectsTrailingJSON(t *testing.T) {
	_, err := DecodeManifest(strings.NewReader(validManifest + `{}`))
	require.ErrorContains(t, err, "exactly one")
}

func TestDecodeManifestRejectsOverlappingExclusion(t *testing.T) {
	input := strings.Replace(validManifest, `"excluded_unmaterialized_graphs":[]`, `"excluded_unmaterialized_graphs":["default"]`, 1)
	_, err := DecodeManifest(strings.NewReader(input))
	require.ErrorContains(t, err, "both materialized and excluded")
}

func TestDecodeManifestRejectsDuplicateJSONKeys(t *testing.T) {
	_, err := DecodeManifest(strings.NewReader(strings.Replace(validManifest, `"version":1`, `"version":1,"version":1`, 1)))
	require.ErrorContains(t, err, "duplicate JSON object key")
}

func TestDecodeManifestRejectsDuplicateKinds(t *testing.T) {
	_, err := DecodeManifest(strings.NewReader(strings.Replace(validManifest, `"B","A"`, `"A","A"`, 1)))
	require.ErrorContains(t, err, "duplicate edge kind")
}

func TestDecodeManifestValidatesAndCanonicalizesEdgeConstraints(t *testing.T) {
	input := strings.Replace(validManifest, `"edge_kinds":["B","A"]`,
		`"edge_kinds":["B","A"],"edge_constraints":[{"logical_name":"z","property_name":"z","constraint_type":"btree","collation_name":"default"},{"logical_name":"a","property_name":"a","constraint_type":"btree","collation_name":"default"}]`, 1)
	manifest, err := DecodeManifest(strings.NewReader(input))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "z"}, []string{manifest.Graphs[0].EdgeConstraints[0].LogicalName, manifest.Graphs[0].EdgeConstraints[1].LogicalName})

	duplicate := strings.Replace(input, `"property_name":"z"`, `"property_name":"a"`, 1)
	_, err = DecodeManifest(strings.NewReader(duplicate))
	require.ErrorContains(t, err, "duplicate constrained property")
}
