package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	frozenV1Artifact = "drivers/pg/migrate/testdata/v1_schema.sql"
	frozenV1SHA256   = "8bea76272e005639d34643f8311599440e1eccc8c3152ca4da1e6dda42c87e0f"
)

type SourceConfiguration struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
}
type OwnerAttestation struct {
	OwnerID          string    `json:"owner_id"`
	AttestedAt       time.Time `json:"attested_at"`
	StatementVersion int       `json:"statement_version"`
}
type ManifestGraph struct {
	Name            string                   `json:"name"`
	EdgeKinds       []string                 `json:"edge_kinds"`
	EdgeConstraints []ManifestEdgeConstraint `json:"edge_constraints,omitempty"`
}
type ManifestEdgeConstraint struct {
	LogicalName    string `json:"logical_name"`
	PropertyName   string `json:"property_name"`
	ConstraintType string `json:"constraint_type"`
	CollationName  string `json:"collation_name"`
}
type Manifest struct {
	Version                      int                 `json:"version"`
	InstallationID               string              `json:"installation_id"`
	SourceConfiguration          SourceConfiguration `json:"source_configuration"`
	OwnerAttestation             OwnerAttestation    `json:"owner_attestation"`
	Graphs                       []ManifestGraph     `json:"graphs"`
	ExcludedUnmaterializedGraphs []string            `json:"excluded_unmaterialized_graphs"`
}

func DecodeManifest(reader io.Reader) (Manifest, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return Manifest{}, err
	}
	if !utf8.Valid(content) {
		return Manifest{}, fmt.Errorf("migration manifest is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decoding migration manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if manifest.Version != 1 || manifest.InstallationID == "" || manifest.SourceConfiguration.Artifact == "" || manifest.SourceConfiguration.SHA256 == "" || manifest.OwnerAttestation.OwnerID == "" || manifest.OwnerAttestation.StatementVersion != 1 || manifest.OwnerAttestation.AttestedAt.IsZero() {
		return Manifest{}, fmt.Errorf("migration manifest is missing required versioned authority fields")
	}
	if len(manifest.SourceConfiguration.SHA256) != sha256.Size*2 || strings.ToLower(manifest.SourceConfiguration.SHA256) != manifest.SourceConfiguration.SHA256 {
		return Manifest{}, fmt.Errorf("source configuration SHA-256 must be 64 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(manifest.SourceConfiguration.SHA256); err != nil {
		return Manifest{}, fmt.Errorf("source configuration SHA-256: %w", err)
	}
	graphNames := map[string]struct{}{}
	for graphIndex := range manifest.Graphs {
		graph := &manifest.Graphs[graphIndex]
		if graph.Name == "" {
			return Manifest{}, fmt.Errorf("manifest graph name is empty")
		}
		if _, duplicate := graphNames[graph.Name]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate manifest graph %q", graph.Name)
		}
		graphNames[graph.Name] = struct{}{}
		kindNames := map[string]struct{}{}
		for _, kind := range graph.EdgeKinds {
			if kind == "" {
				return Manifest{}, fmt.Errorf("manifest graph %q has an empty edge kind", graph.Name)
			}
			if _, duplicate := kindNames[kind]; duplicate {
				return Manifest{}, fmt.Errorf("manifest graph %q has duplicate edge kind %q", graph.Name, kind)
			}
			kindNames[kind] = struct{}{}
		}
		sort.Strings(graph.EdgeKinds)
		constraintNames := map[string]struct{}{}
		constraintProperties := map[string]struct{}{}
		for _, constraint := range graph.EdgeConstraints {
			if constraint.LogicalName == "" || constraint.PropertyName == "" || constraint.ConstraintType != "btree" || constraint.CollationName != "default" {
				return Manifest{}, fmt.Errorf("manifest graph %q has an invalid edge constraint; logical/property names and btree/default semantics are required", graph.Name)
			}
			if _, duplicate := constraintNames[constraint.LogicalName]; duplicate {
				return Manifest{}, fmt.Errorf("manifest graph %q has duplicate edge constraint name %q", graph.Name, constraint.LogicalName)
			}
			if _, duplicate := constraintProperties[constraint.PropertyName]; duplicate {
				return Manifest{}, fmt.Errorf("manifest graph %q has duplicate constrained property %q", graph.Name, constraint.PropertyName)
			}
			constraintNames[constraint.LogicalName] = struct{}{}
			constraintProperties[constraint.PropertyName] = struct{}{}
		}
		sort.Slice(graph.EdgeConstraints, func(i, j int) bool {
			return graph.EdgeConstraints[i].LogicalName < graph.EdgeConstraints[j].LogicalName
		})
	}
	sort.Slice(manifest.Graphs, func(i, j int) bool { return manifest.Graphs[i].Name < manifest.Graphs[j].Name })
	exclusions := map[string]struct{}{}
	for _, name := range manifest.ExcludedUnmaterializedGraphs {
		if name == "" {
			return Manifest{}, fmt.Errorf("manifest has an empty excluded graph name")
		}
		if _, duplicate := exclusions[name]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate excluded graph %q", name)
		}
		if _, materialized := graphNames[name]; materialized {
			return Manifest{}, fmt.Errorf("graph %q is both materialized and excluded", name)
		}
		exclusions[name] = struct{}{}
	}
	sort.Strings(manifest.ExcludedUnmaterializedGraphs)
	return manifest, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON input must contain exactly one value")
		}
		return fmt.Errorf("reading trailing JSON input: %w", err)
	}
	return nil
}

func (s Manifest) CanonicalDigest() (string, error) {
	content, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func (s Manifest) ValidateFrozenV1Lineage() error {
	if s.SourceConfiguration.Artifact != frozenV1Artifact || s.SourceConfiguration.SHA256 != frozenV1SHA256 {
		return fmt.Errorf("unrecognized_v1_lineage: source must be %s at SHA-256 %s", frozenV1Artifact, frozenV1SHA256)
	}
	return nil
}

func rejectDuplicateKeys(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key := keyToken.(string)
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
	}
	return walk()
}
