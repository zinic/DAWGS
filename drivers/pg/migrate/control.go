package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const confirmationTTL = 15 * time.Minute

const confirmationDDL = `
create table if not exists public.dawgs_operation_confirmation (
  token_sha256 text primary key check (length(token_sha256)=64),
  action text not null,
  scope_id text not null,
  database_oid oid not null,
  database_name text not null,
  system_identifier text not null,
  facts_sha256 text not null check (length(facts_sha256)=64),
  issued_at timestamptz not null,
  expires_at timestamptz not null,
  consumed_at timestamptz,
  check (expires_at > issued_at)
);
revoke all on public.dawgs_operation_confirmation from public;`

type databaseIdentity struct {
	OID              uint32
	Name             string
	SystemIdentifier string
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type operationFacts struct {
	Version          int    `json:"version"`
	Action           string `json:"action"`
	MigrationID      string `json:"migration_id"`
	State            string `json:"state"`
	ManifestSHA256   string `json:"manifest_sha256"`
	FenceSHA256      string `json:"fence_sha256"`
	DatabaseOID      uint32 `json:"database_oid"`
	DatabaseName     string `json:"database_name"`
	SystemIdentifier string `json:"system_identifier"`
}

func operationFactsDigest(ctx context.Context, queryer rowQuerier, action, migrationID string) (string, databaseIdentity, error) {
	var state, manifest, fence string
	if err := queryer.QueryRow(ctx, `select state,manifest_sha256,coalesce(fence_catalog_sha256,'')
from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).Scan(&state, &manifest, &fence); err != nil {
		return "", databaseIdentity{}, err
	}
	allowed := false
	switch action {
	case "verify-fence":
		allowed = state == "fence_installed_unverified" || state == "fence_database_verified_retirement_pending"
	case "resume":
		allowed = state == "fence_verified" || state == "copying" || state == "pending_validation"
	case "rollback":
		allowed = state == "fence_installed_unverified" ||
			state == "fence_database_verified_retirement_pending" ||
			state == "fence_verified" || state == "copying" ||
			state == "pending_validation" || state == "validated"
	case "complete-v1-handoff":
		allowed = state == "rolled_back_handoff_pending"
	}
	if !allowed {
		return "", databaseIdentity{}, fmt.Errorf("action %q is not legal from migration state %q", action, state)
	}
	database, err := currentDatabaseIdentity(ctx, queryer)
	if err != nil {
		return "", databaseIdentity{}, err
	}
	digest, _, err := canonicalDigest(operationFacts{1, action, migrationID, state, manifest, fence,
		database.OID, database.Name, database.SystemIdentifier})
	return digest, database, err
}

func (s *Engine) ConfirmOperation(ctx context.Context, action, migrationID string) (Report, error) {
	if action != "verify-fence" && action != "resume" && action != "rollback" && action != "complete-v1-handoff" {
		return Report{}, fmt.Errorf("unsupported confirmed operation %q", action)
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	digest, database, err := operationFactsDigest(ctx, tx, action, migrationID)
	if err != nil {
		return Report{}, err
	}
	token, err := issueConfirmation(ctx, tx, action, migrationID, digest, database)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: action, MigrationID: migrationID,
		ConfirmationToken: token, PreflightDigest: digest, CreatedAt: time.Now().UTC()}, nil
}

func consumeOperationConfirmation(ctx context.Context, tx pgx.Tx, action, migrationID, token string) error {
	if token == "" {
		return fmt.Errorf("%s confirmation token is required", action)
	}
	digest, database, err := operationFactsDigest(ctx, tx, action, migrationID)
	if err != nil {
		return err
	}
	return consumeConfirmation(ctx, tx, token, action, migrationID, digest, database)
}

func currentDatabaseIdentity(ctx context.Context, tx rowQuerier) (databaseIdentity, error) {
	var identity databaseIdentity
	err := tx.QueryRow(ctx, `select d.oid,d.datname,(pg_catalog.pg_control_system()).system_identifier::text
from pg_catalog.pg_database d where d.datname=pg_catalog.current_database()`).Scan(
		&identity.OID, &identity.Name, &identity.SystemIdentifier)
	if err != nil {
		return databaseIdentity{}, fmt.Errorf("reading database identity: %w", err)
	}
	return identity, nil
}

func canonicalFactsDigest(manifestDigest, catalogDigest string, graphs []graphFact, identity databaseIdentity, writers []writerProbeFact, roles preflightRoleFact) (string, error) {
	type canonicalGraph struct {
		ID          int32    `json:"id"`
		Name        string   `json:"name"`
		StoredKinds []string `json:"stored_kinds"`
	}
	canonicalGraphs := make([]canonicalGraph, len(graphs))
	for index, graph := range graphs {
		canonicalGraphs[index] = canonicalGraph{graph.ID, graph.Name, graph.StoredKinds}
	}
	facts := struct {
		Version          int               `json:"version"`
		ManifestSHA256   string            `json:"manifest_sha256"`
		DatabaseOID      uint32            `json:"database_oid"`
		DatabaseName     string            `json:"database_name"`
		SystemIdentifier string            `json:"system_identifier"`
		CatalogSHA256    string            `json:"catalog_sha256"`
		Graphs           []canonicalGraph  `json:"graphs"`
		Writers          []writerProbeFact `json:"writers"`
		Roles            preflightRoleFact `json:"roles"`
	}{1, manifestDigest, identity.OID, identity.Name, identity.SystemIdentifier, catalogDigest, canonicalGraphs, writers, roles}
	content, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func issueConfirmation(ctx context.Context, tx pgx.Tx, action, scopeID, factsDigest string, identity databaseIdentity) (string, error) {
	token, err := randomID()
	if err != nil {
		return "", err
	}
	tokenDigest := sha256.Sum256([]byte(token))
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `insert into public.dawgs_operation_confirmation
(token_sha256,action,scope_id,database_oid,database_name,system_identifier,facts_sha256,issued_at,expires_at)
values($1,$2,$3,$4,$5,$6,$7,$8,$9)`, hex.EncodeToString(tokenDigest[:]), action, scopeID,
		identity.OID, identity.Name, identity.SystemIdentifier, factsDigest, now, now.Add(confirmationTTL))
	return token, err
}

func consumeConfirmation(ctx context.Context, tx pgx.Tx, token, action, scopeID, factsDigest string, identity databaseIdentity) error {
	tokenDigest := sha256.Sum256([]byte(token))
	tag, err := tx.Exec(ctx, `update public.dawgs_operation_confirmation set consumed_at=pg_catalog.clock_timestamp()
where token_sha256=$1 and action=$2 and scope_id=$3 and facts_sha256=$4
  and database_oid=$5 and database_name=$6 and system_identifier=$7
  and consumed_at is null and expires_at>pg_catalog.clock_timestamp()`,
		hex.EncodeToString(tokenDigest[:]), action, scopeID, factsDigest,
		identity.OID, identity.Name, identity.SystemIdentifier)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("confirmation is absent, expired, consumed, or bound to different facts")
	}
	return nil
}
