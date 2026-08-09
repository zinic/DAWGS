package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ArchiveManifest is the portable, content-addressed inventory used by the
// late-upgrade retrieval drill. It deliberately contains no credentials.
type ArchiveManifest struct {
	Version              int               `json:"version"`
	ArchiveID            string            `json:"archive_id"`
	SourceRevision       string            `json:"source_revision"`
	CreatedAt            time.Time         `json:"created_at"`
	EOLAt                time.Time         `json:"eol_at"`
	PublicationOwner     string            `json:"publication_owner"`
	RetentionOwner       string            `json:"retention_owner"`
	IncidentOwner        string            `json:"incident_owner"`
	RetrievalCoordinates string            `json:"retrieval_coordinates"`
	Cadence              string            `json:"cadence"`
	PostgreSQLMajors     []int             `json:"postgresql_majors"`
	Artifacts            []ArchiveArtifact `json:"artifacts"`
}

type ArchiveArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Kind   string `json:"kind"`
}

type ArchiveVerification struct {
	Digest        string `json:"digest"`
	Artifacts     int    `json:"artifacts"`
	Bytes         int64  `json:"bytes"`
	FilesVerified bool   `json:"files_verified"`
}

func DecodeArchiveManifest(reader io.Reader) (ArchiveManifest, error) {
	var value ArchiveManifest
	if err := decodeStrictEvidence(reader, &value, "archive manifest"); err != nil {
		return ArchiveManifest{}, err
	}
	if _, err := value.Validate(); err != nil {
		return ArchiveManifest{}, err
	}
	return value, nil
}

func (s ArchiveManifest) Validate() (ArchiveVerification, error) {
	if s.Version != 1 || s.ArchiveID == "" || s.SourceRevision == "" || s.CreatedAt.IsZero() || s.EOLAt.IsZero() || !s.EOLAt.After(s.CreatedAt) {
		return ArchiveVerification{}, fmt.Errorf("invalid versioned archive manifest header")
	}
	if s.PublicationOwner == "" || s.RetentionOwner == "" || s.IncidentOwner == "" || s.RetrievalCoordinates == "" || s.Cadence != "weekly" {
		return ArchiveVerification{}, fmt.Errorf("archive owners, retrieval coordinates, and weekly cadence are required")
	}
	if s.PublicationOwner == s.RetentionOwner {
		return ArchiveVerification{}, fmt.Errorf("publication and retention owners must be separated")
	}
	majors := append([]int(nil), s.PostgreSQLMajors...)
	sort.Ints(majors)
	if len(majors) != 2 || majors[0] != 16 || majors[1] != 18 {
		return ArchiveVerification{}, fmt.Errorf("archive must retain exactly PostgreSQL majors 16 and 18")
	}
	seen := map[string]struct{}{}
	verification := ArchiveVerification{Artifacts: len(s.Artifacts)}
	requiredKinds := map[string]bool{"binary": false, "module": false, "frozen_v1": false, "schema": false, "runbook": false, "evidence_schema": false}
	for _, artifact := range s.Artifacts {
		if !safeArchivePath(artifact.Path) || !isSHA256(artifact.SHA256) || artifact.Bytes < 0 || artifact.Kind == "" {
			return ArchiveVerification{}, fmt.Errorf("invalid archive artifact %q", artifact.Path)
		}
		if _, found := seen[artifact.Path]; found {
			return ArchiveVerification{}, fmt.Errorf("duplicate archive artifact %q", artifact.Path)
		}
		seen[artifact.Path] = struct{}{}
		verification.Bytes += artifact.Bytes
		if _, found := requiredKinds[artifact.Kind]; found {
			requiredKinds[artifact.Kind] = true
		}
	}
	for kind, found := range requiredKinds {
		if !found {
			return ArchiveVerification{}, fmt.Errorf("archive lacks required %s artifact", kind)
		}
	}
	digest, _, err := canonicalDigest(s)
	verification.Digest = digest
	return verification, err
}

func (s ArchiveManifest) VerifyFiles(root string) (ArchiveVerification, error) {
	verification, err := s.Validate()
	if err != nil {
		return ArchiveVerification{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return ArchiveVerification{}, err
	}
	for _, artifact := range s.Artifacts {
		path := filepath.Join(root, filepath.FromSlash(artifact.Path))
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return ArchiveVerification{}, fmt.Errorf("resolve archive artifact %q: %w", artifact.Path, err)
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || !filepath.IsLocal(relative) || relative != filepath.FromSlash(artifact.Path) {
			return ArchiveVerification{}, fmt.Errorf("archive artifact %q resolves outside or through a link", artifact.Path)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return ArchiveVerification{}, fmt.Errorf("archive artifact %q is not a regular file", artifact.Path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return ArchiveVerification{}, fmt.Errorf("read archive artifact %q: %w", artifact.Path, err)
		}
		if int64(len(content)) != artifact.Bytes {
			return ArchiveVerification{}, fmt.Errorf("archive artifact %q size mismatch", artifact.Path)
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != artifact.SHA256 {
			return ArchiveVerification{}, fmt.Errorf("archive artifact %q checksum mismatch", artifact.Path)
		}
	}
	verification.FilesVerified = true
	return verification, nil
}

func safeArchivePath(path string) bool {
	return path != "" && filepath.IsLocal(filepath.FromSlash(path)) && filepath.ToSlash(filepath.Clean(path)) == path && !strings.ContainsRune(path, '\\')
}
