package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

type ConnectionEnvelope struct {
	Version          int    `json:"version"`
	ConnectionString string `json:"connection_string"`
}

type WriterProbe struct {
	Version          int    `json:"version"`
	ProbeID          string `json:"probe_id"`
	RoleName         string `json:"role_name"`
	ConnectionString string `json:"connection_string"`
	Retirement       string `json:"retirement"`
}

// DecodeWriterProbe reads a secret transport envelope. Only the stable probe
// identifier, authenticated role identity, and retirement strategy may enter
// durable facts; ConnectionString must never be serialized to a report.
func DecodeWriterProbe(reader io.Reader) (WriterProbe, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return WriterProbe{}, err
	}
	if !utf8.Valid(content) {
		return WriterProbe{}, fmt.Errorf("writer probe is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return WriterProbe{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var probe WriterProbe
	if err := decoder.Decode(&probe); err != nil {
		return WriterProbe{}, fmt.Errorf("decoding writer probe: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return WriterProbe{}, err
	}
	if probe.Version != 1 || probe.ProbeID == "" || probe.RoleName == "" || probe.ConnectionString == "" {
		return WriterProbe{}, fmt.Errorf("invalid versioned writer probe")
	}
	if probe.Retirement != "postgres_role_nologin" {
		return WriterProbe{}, fmt.Errorf("unsupported writer retirement strategy %q", probe.Retirement)
	}
	return probe, nil
}

// DecodeConnectionEnvelope applies the same strict single-value JSON contract
// as manifests. Connection strings are accepted as input authority but are
// never included in reports, facts digests, or receipts.
func DecodeConnectionEnvelope(reader io.Reader) (ConnectionEnvelope, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return ConnectionEnvelope{}, err
	}
	if !utf8.Valid(content) {
		return ConnectionEnvelope{}, fmt.Errorf("connection envelope is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return ConnectionEnvelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var envelope ConnectionEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return ConnectionEnvelope{}, fmt.Errorf("decoding connection envelope: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ConnectionEnvelope{}, err
	}
	if envelope.Version != 1 || envelope.ConnectionString == "" {
		return ConnectionEnvelope{}, fmt.Errorf("invalid versioned connection envelope")
	}
	return envelope, nil
}
