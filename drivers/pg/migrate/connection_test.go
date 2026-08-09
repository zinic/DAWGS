package migrate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeConnectionEnvelopeStrictParsing(t *testing.T) {
	valid := `{"version":1,"connection_string":"postgresql://example/db"}`
	envelope, err := DecodeConnectionEnvelope(strings.NewReader(valid))
	require.NoError(t, err)
	require.Equal(t, "postgresql://example/db", envelope.ConnectionString)

	for name, input := range map[string]string{
		"trailing value":   valid + `{}`,
		"duplicate key":    `{"version":1,"version":1,"connection_string":"postgresql://example/db"}`,
		"unknown field":    `{"version":1,"connection_string":"postgresql://example/db","password":"secret"}`,
		"missing version":  `{"connection_string":"postgresql://example/db"}`,
		"empty connection": `{"version":1,"connection_string":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeConnectionEnvelope(strings.NewReader(input))
			require.Error(t, err)
		})
	}
}

func TestDecodeConnectionEnvelopeRejectsInvalidUTF8(t *testing.T) {
	_, err := DecodeConnectionEnvelope(strings.NewReader("{\"version\":1,\"connection_string\":\"\xff\"}"))
	require.ErrorContains(t, err, "UTF-8")
}

func TestDecodeWriterProbeStrictAndSecretBearing(t *testing.T) {
	valid := `{"version":1,"probe_id":"primary-writer","role_name":"legacy_writer","connection_string":"postgresql://legacy:secret@example/db","retirement":"postgres_role_nologin"}`
	probe, err := DecodeWriterProbe(strings.NewReader(valid))
	require.NoError(t, err)
	require.Equal(t, "primary-writer", probe.ProbeID)

	for name, input := range map[string]string{
		"trailing":     valid + `{}`,
		"duplicate":    `{"version":1,"probe_id":"a","probe_id":"b","role_name":"writer","connection_string":"postgresql://example/db","retirement":"postgres_role_nologin"}`,
		"unknown":      `{"version":1,"probe_id":"a","role_name":"writer","connection_string":"postgresql://example/db","retirement":"postgres_role_nologin","password":"secret"}`,
		"bad strategy": `{"version":1,"probe_id":"a","role_name":"writer","connection_string":"postgresql://example/db","retirement":"attestation"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeWriterProbe(strings.NewReader(input))
			require.Error(t, err)
		})
	}
}
