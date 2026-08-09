package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg"
	"github.com/specterops/dawgs/opengraph"
	"github.com/specterops/dawgs/util/size"
)

type exportMetadata struct {
	Version        int             `json:"version"`
	Format         string          `json:"format"`
	GeneratedAt    time.Time       `json:"generated_at"`
	Artifact       string          `json:"artifact"`
	ArtifactBytes  int64           `json:"artifact_bytes"`
	ArtifactSHA256 string          `json:"artifact_sha256"`
	Postgres       pg.LayoutHealth `json:"postgres"`
}

func main() {
	connStr := os.Getenv("PGCONN")
	if connStr == "" {
		fmt.Fprintln(os.Stderr, "PGCONN is required; transition export never uses a default application DSN")
		os.Exit(1)
	}

	poolCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse pool configuration: %v\n", err)
		os.Exit(1)
	}
	pool, err := pg.NewPool(poolCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	db := pg.NewDriver(size.Gibibyte, pool)
	if err := db.AssertRuntime(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "PostgreSQL startup validation failed: %v\n", err)
		os.Exit(1)
	}
	health, err := db.Readiness(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "PostgreSQL readiness failed: %v\n", err)
		os.Exit(1)
	}

	outFile := "graph_export.json"
	if len(os.Args) > 1 {
		outFile = os.Args[1]
	}

	f, err := os.OpenFile(outFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create file: %v\n", err)
		os.Exit(1)
	}
	hash := sha256.New()
	counting := &countingWriter{writer: io.MultiWriter(f, hash)}
	if err := opengraph.Export(context.Background(), db, counting); err != nil {
		_ = f.Close()
		_ = os.Remove(outFile)
		fmt.Fprintf(os.Stderr, "export failed: %v\n", err)
		os.Exit(1)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(outFile)
		fmt.Fprintf(os.Stderr, "sync export: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(outFile)
		fmt.Fprintf(os.Stderr, "close export: %v\n", err)
		os.Exit(1)
	}
	metadata := exportMetadata{Version: 1, Format: "dawgs-opengraph-transition-export-v1", GeneratedAt: time.Now().UTC(),
		Artifact: filepath.Base(outFile), ArtifactBytes: counting.count,
		ArtifactSHA256: hex.EncodeToString(hash.Sum(nil)), Postgres: health}
	if err := writeExportMetadata(outFile+".metadata.json", metadata); err != nil {
		_ = os.Remove(outFile)
		fmt.Fprintf(os.Stderr, "write export metadata: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "exported graph to %s with checksummed metadata\n", outFile)
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (s *countingWriter) Write(value []byte) (int, error) {
	written, err := s.writer.Write(value)
	s.count += int64(written)
	return written, err
}

func writeExportMetadata(path string, value exportMetadata) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".dawgs-export-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Link is an atomic, no-replace publication on the same filesystem. Rename
	// would silently replace an existing sidecar on Unix.
	if err := os.Link(name, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
