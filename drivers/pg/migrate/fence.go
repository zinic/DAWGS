package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
)

const fenceControlDDL = `
create table if not exists public.dawgs_schema_fence_migration (
  migration_id text primary key,
  manifest_sha256 text not null check (length(manifest_sha256)=64),
  preflight_sha256 text not null check (length(preflight_sha256)=64),
  state text not null check (state in (
    'fence_installing','fence_installed_unverified',
    'fence_database_verified_retirement_pending','writer_retirement_in_progress','fence_verified',
    'copying','pending_validation','validated','accepted_pending_smoke','accepted',
    'rolled_back_handoff_pending','rolled_back')),
  database_oid oid not null,
  database_name text not null,
  system_identifier text not null,
	identity_epoch bigint not null default 1 check(identity_epoch>0),
	current_rebind_receipt_digest text,
  manifest jsonb not null,
  writer_facts jsonb not null,
  role_facts jsonb not null,
  inverse_catalog jsonb not null,
  fence_catalog_sha256 text,
  created_at timestamptz not null default pg_catalog.clock_timestamp(),
  updated_at timestamptz not null default pg_catalog.clock_timestamp()
);
revoke all on public.dawgs_schema_fence_migration from public;
create table if not exists public.dawgs_schema_receipt (
  scope_kind text not null check (scope_kind in ('bootstrap','migration')),
  scope_id text not null,
  receipt_type text not null,
  attempt_id text not null,
  receipt_version integer not null check (receipt_version=1),
  facts jsonb not null,
  receipt_digest text not null check (length(receipt_digest)=64),
  confirmation_token_digest text,
  created_at timestamptz not null default pg_catalog.clock_timestamp(),
  primary key(scope_kind,scope_id,receipt_type,attempt_id),
  unique(receipt_digest)
);
revoke all on public.dawgs_schema_receipt from public;
create or replace function public.dawgs_refuse_receipt_mutation() returns trigger as
$$ begin raise exception using errcode='55000',message='DAWGS receipts are append-only'; end $$
language plpgsql security definer set search_path=pg_catalog,public,pg_temp;
drop trigger if exists dawgs_refuse_receipt_mutation on public.dawgs_schema_receipt;
create trigger dawgs_refuse_receipt_mutation before update or delete on public.dawgs_schema_receipt
for each statement execute function public.dawgs_refuse_receipt_mutation();`

type writerProbeFact struct {
	ProbeID    string `json:"probe_id"`
	RoleName   string `json:"role_name"`
	RoleOID    uint32 `json:"role_oid"`
	Retirement string `json:"retirement"`
}

type preflightRoleFact struct {
	OperatorOID      uint32 `json:"operator_oid"`
	OperatorName     string `json:"operator_name"`
	TargetOID        uint32 `json:"target_oid"`
	TargetName       string `json:"target_name"`
	PreparationOID   uint32 `json:"preparation_oid"`
	PreparationName  string `json:"preparation_name"`
	PreparationLogin bool   `json:"preparation_login"`
	CapacitySHA256   string `json:"capacity_sha256"`
	BackupSHA256     string `json:"backup_sha256"`
}

func (s *Engine) preflightRoleFacts(ctx context.Context, operatorQuery rowQuerier, expectedDatabase databaseIdentity, writers []writerProbeFact) (preflightRoleFact, error) {
	if s.TargetConnectionString == "" || s.PreparationRole == "" {
		return preflightRoleFact{}, fmt.Errorf("preflight_role_required: direct target connection and dormant preparation role are required")
	}
	operator, err := readSessionIdentity(ctx, operatorQuery)
	if err != nil {
		return preflightRoleFact{}, err
	}
	if operator.SessionUser != operator.CurrentUser || operator.RoleName != operator.SessionUser || operator.Superuser || operator.RoleOID == operator.DatabaseOwner || operator.HasMembership {
		return preflightRoleFact{}, fmt.Errorf("migration operator must be directly authenticated, restricted, non-owner, and membership-free")
	}
	targetConnection, err := connectTarget(ctx, s.TargetConnectionString)
	if err != nil {
		return preflightRoleFact{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return preflightRoleFact{}, err
	}
	if err := validateTargetIdentity(target, operator); err != nil {
		return preflightRoleFact{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || targetDatabase != expectedDatabase {
		return preflightRoleFact{}, fmt.Errorf("preflight target resolves a different database incarnation")
	}
	var preparationOID uint32
	var preparationLogin, preparationMembership bool
	if err := operatorQuery.QueryRow(ctx, `select r.oid,r.rolcanlogin,
exists(select 1 from pg_catalog.pg_auth_members m where m.member=r.oid or m.roleid=r.oid)
from pg_catalog.pg_roles r where r.rolname=$1`, s.PreparationRole).Scan(
		&preparationOID, &preparationLogin, &preparationMembership); err != nil {
		return preflightRoleFact{}, fmt.Errorf("reading preparation role: %w", err)
	}
	if preparationLogin || preparationMembership || preparationOID == operator.RoleOID || preparationOID == target.RoleOID {
		return preflightRoleFact{}, fmt.Errorf("preparation role must be dormant, membership-free, and distinct from operator and target")
	}
	for _, writer := range writers {
		if writer.RoleOID == operator.RoleOID || writer.RoleOID == target.RoleOID || writer.RoleOID == preparationOID {
			return preflightRoleFact{}, fmt.Errorf("writer role %q collides with operator, target, or preparation identity", writer.RoleName)
		}
	}
	capacityDigest, backupDigest, err := validatePreflightEvidence(s.CapacityEvidence, s.BackupEvidence, expectedDatabase, time.Now().UTC())
	if err != nil {
		return preflightRoleFact{}, err
	}
	return preflightRoleFact{OperatorOID: operator.RoleOID, OperatorName: operator.RoleName,
		TargetOID: target.RoleOID, TargetName: target.RoleName, PreparationOID: preparationOID,
		PreparationName: s.PreparationRole, PreparationLogin: preparationLogin,
		CapacitySHA256: capacityDigest, BackupSHA256: backupDigest}, nil
}

func (s *Engine) authenticateWriterProbes(ctx context.Context, expected databaseIdentity, requireWriteRoute bool) ([]writerProbeFact, error) {
	if len(s.WriterProbes) == 0 {
		return nil, fmt.Errorf("writer_probe_required: at least one directly authenticated V1 writer probe is required")
	}
	seenIDs := map[string]struct{}{}
	seenRoles := map[uint32]struct{}{}
	facts := make([]writerProbeFact, 0, len(s.WriterProbes))
	for _, probe := range s.WriterProbes {
		if probe.Version != 1 || probe.ProbeID == "" || probe.RoleName == "" || probe.ConnectionString == "" || probe.Retirement != "postgres_role_nologin" {
			return nil, fmt.Errorf("writer probe %q is not a valid version 1 postgres_role_nologin probe", probe.ProbeID)
		}
		if _, duplicate := seenIDs[probe.ProbeID]; duplicate {
			return nil, fmt.Errorf("duplicate writer probe ID %q", probe.ProbeID)
		}
		seenIDs[probe.ProbeID] = struct{}{}
		connection, err := pgx.Connect(ctx, probe.ConnectionString)
		if err != nil {
			return nil, fmt.Errorf("authenticating writer probe %q: %w", probe.ProbeID, err)
		}
		identity, identityErr := readSessionIdentity(ctx, connection)
		database, databaseErr := currentDatabaseIdentity(ctx, connection)
		writeRoute := false
		if identityErr == nil && databaseErr == nil && requireWriteRoute {
			writeRoute, err = hasEffectiveWriterRoute(ctx, connection)
		}
		closeErr := connection.Close(ctx)
		if identityErr != nil {
			return nil, fmt.Errorf("reading writer probe %q identity: %w", probe.ProbeID, identityErr)
		}
		if databaseErr != nil {
			return nil, fmt.Errorf("reading writer probe %q database identity: %w", probe.ProbeID, databaseErr)
		}
		if err != nil {
			return nil, fmt.Errorf("inspecting writer probe %q routes: %w", probe.ProbeID, err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if identity.SessionUser != probe.RoleName || identity.CurrentUser != probe.RoleName || identity.RoleName != probe.RoleName {
			return nil, fmt.Errorf("writer probe %q must directly authenticate exact role %q", probe.ProbeID, probe.RoleName)
		}
		if identity.Superuser || identity.RoleOID == identity.DatabaseOwner || identity.HasMembership {
			return nil, fmt.Errorf("writer probe %q is owner, superuser, or has inherited role membership", probe.ProbeID)
		}
		if database != expected {
			return nil, fmt.Errorf("writer probe %q targets a different database incarnation", probe.ProbeID)
		}
		if _, duplicate := seenRoles[identity.RoleOID]; duplicate {
			return nil, fmt.Errorf("multiple writer probes resolve to role OID %d", identity.RoleOID)
		}
		seenRoles[identity.RoleOID] = struct{}{}
		if requireWriteRoute && !writeRoute {
			return nil, fmt.Errorf("writer probe %q has no effective public-schema write route to fence", probe.ProbeID)
		}
		facts = append(facts, writerProbeFact{ProbeID: probe.ProbeID, RoleName: probe.RoleName, RoleOID: identity.RoleOID, Retirement: probe.Retirement})
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].ProbeID < facts[j].ProbeID })
	return facts, nil
}

func hasEffectiveWriterRoute(ctx context.Context, connection *pgx.Conn) (bool, error) {
	var table, sequence, schema, routine bool
	err := connection.QueryRow(ctx, `select
	  exists(select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
    where n.nspname='public' and c.relkind in ('r','p')
      and pg_catalog.has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE')),
  exists(select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
    where n.nspname='public' and c.relkind='S'
      and pg_catalog.has_sequence_privilege(current_user,c.oid,'USAGE,UPDATE')),
  pg_catalog.has_schema_privilege(current_user,'public','CREATE'),
  exists(select 1 from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
    where n.nspname='public' and not exists (
      select 1 from pg_catalog.pg_depend d where d.classid='pg_catalog.pg_proc'::pg_catalog.regclass
        and d.objid=p.oid and d.deptype='e')
      and pg_catalog.has_function_privilege(current_user,p.oid,'EXECUTE'))`).Scan(&table, &sequence, &schema, &routine)
	return table || sequence || schema || routine, err
}

func effectiveWriterRouteNames(ctx context.Context, connection *pgx.Conn) (string, error) {
	rows, err := connection.Query(ctx, `
select 'table:'||n.nspname||'.'||c.relname from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relkind in ('r','p') and pg_catalog.has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE')
union all select 'sequence:'||n.nspname||'.'||c.relname from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relkind='S' and pg_catalog.has_sequence_privilege(current_user,c.oid,'USAGE,UPDATE')
union all select 'schema:public' where pg_catalog.has_schema_privilege(current_user,'public','CREATE')
union all select 'routine:'||n.nspname||'.'||p.proname from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
where n.nspname='public' and not exists (
  select 1 from pg_catalog.pg_depend d where d.classid='pg_catalog.pg_proc'::pg_catalog.regclass
    and d.objid=p.oid and d.deptype='e')
  and pg_catalog.has_function_privilege(current_user,p.oid,'EXECUTE') order by 1`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var routes []string
	for rows.Next() {
		var route string
		if err := rows.Scan(&route); err != nil {
			return "", err
		}
		routes = append(routes, route)
	}
	return strings.Join(routes, ","), rows.Err()
}

// Migrate performs only the crash-safe database-local fence phase. It cannot
// detach a partition, change the root key, install V2 runtime DDL, or copy data.
func (s *Engine) Migrate(ctx context.Context, manifest Manifest, confirmation string) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: unreleased migration fencing is restricted to the identity-guarded disposable test harness")
	}
	if confirmation == "" {
		return Report{}, fmt.Errorf("confirmation token is required")
	}
	if err := manifest.ValidateFrozenV1Lineage(); err != nil {
		return Report{}, err
	}
	manifestDigest, err := manifest.CanonicalDigest()
	if err != nil {
		return Report{}, err
	}
	migrationID, err := randomID()
	if err != nil {
		return Report{}, err
	}
	connection, err := s.Pool.Acquire(ctx)
	if err != nil {
		return Report{}, err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `select pg_catalog.pg_advisory_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	defer connection.Exec(context.Background(), `select pg_catalog.pg_advisory_unlock($1)`, layoutLockKey)

	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	graphs, err := graphFactsQuery(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := validateManifestGraphs(manifest, graphs); err != nil {
		return Report{}, err
	}
	identity, err := currentDatabaseIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	writers, err := s.authenticateWriterProbes(ctx, identity, true)
	if err != nil {
		return Report{}, err
	}
	roles, err := s.preflightRoleFacts(ctx, tx, identity, writers)
	if err != nil {
		return Report{}, err
	}
	before, err := catalog.ReadSnapshot(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	var foreignOwned int
	if err := tx.QueryRow(ctx, `select
  (select count(*) from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
    where n.nspname='public' and c.relkind in ('r','p','S') and c.relowner<>(select oid from pg_catalog.pg_roles where rolname=current_user))
  + (select count(*) from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
    where n.nspname='public' and p.proowner<>(select oid from pg_catalog.pg_roles where rolname=current_user)
      and not exists (select 1 from pg_catalog.pg_depend d where d.classid='pg_catalog.pg_proc'::pg_catalog.regclass
        and d.objid=p.oid and d.deptype='e'))`).Scan(&foreignOwned); err != nil {
		return Report{}, err
	}
	if foreignOwned != 0 {
		return Report{}, fmt.Errorf("writer_fence_owner_blocked: %d managed public objects are not owned by the migration operator", foreignOwned)
	}
	catalogDigest, err := catalog.FingerprintWithExclusions(before, "public.dawgs_operation_confirmation")
	if err != nil {
		return Report{}, err
	}
	factsDigest, err := canonicalFactsDigest(manifestDigest, catalogDigest, graphs, identity, writers, roles)
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "migrate", manifestDigest, factsDigest, identity); err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, fenceControlDDL); err != nil {
		return Report{}, fmt.Errorf("installing fence control journal: %w", err)
	}
	manifestJSON, _ := json.Marshal(manifest)
	writersJSON, _ := json.Marshal(writers)
	inverseJSON, err := json.Marshal(before)
	if err != nil {
		return Report{}, err
	}
	rolesJSON, _ := json.Marshal(roles)
	if _, err := tx.Exec(ctx, `insert into public.dawgs_schema_fence_migration
(migration_id,manifest_sha256,preflight_sha256,state,database_oid,database_name,system_identifier,manifest,writer_facts,role_facts,inverse_catalog)
values($1,$2,$3,'fence_installing',$4,$5,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10::jsonb)`, migrationID, manifestDigest,
		factsDigest, identity.OID, identity.Name, identity.SystemIdentifier, string(manifestJSON), string(writersJSON), string(rolesJSON), string(inverseJSON)); err != nil {
		return Report{}, fmt.Errorf("journaling fence inverse facts: %w", err)
	}
	if err := s.hitFailpoint("after_fence_journal_before_acl"); err != nil {
		return Report{}, err
	}
	for _, writer := range writers {
		role := pgx.Identifier{writer.RoleName}.Sanitize()
		statements := []string{
			fmt.Sprintf(`revoke create on schema public from %s`, role),
			fmt.Sprintf(`revoke all privileges on all tables in schema public from %s`, role),
			fmt.Sprintf(`revoke all privileges on all sequences in schema public from %s`, role),
			fmt.Sprintf(`revoke all privileges on all functions in schema public from %s`, role),
			fmt.Sprintf(`alter default privileges in schema public revoke all privileges on tables from %s`, role),
			fmt.Sprintf(`alter default privileges in schema public revoke all privileges on sequences from %s`, role),
			fmt.Sprintf(`alter default privileges in schema public revoke execute on functions from %s`, role),
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return Report{}, fmt.Errorf("installing writer fence for %s: %w", writer.RoleName, err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `alter default privileges in schema public revoke execute on functions from public`); err != nil {
		return Report{}, fmt.Errorf("fencing future public routine execution: %w", err)
	}
	// Function EXECUTE is granted to PUBLIC by default. Remove that inherited
	// path from application-managed routines, while leaving extension-member
	// routines under their extension's separately inventoried authority.
	if _, err := tx.Exec(ctx, `do $fence$
declare object_name text;
begin
  for object_name in
    select p.oid::pg_catalog.regprocedure::text
    from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
    where n.nspname='public' and not exists (
      select 1 from pg_catalog.pg_depend d where d.classid='pg_catalog.pg_proc'::pg_catalog.regclass
        and d.objid=p.oid and d.deptype='e')
  loop
    execute pg_catalog.format('revoke execute on function %s from public',object_name);
  end loop;
end
$fence$`); err != nil {
		return Report{}, fmt.Errorf("fencing public routine execution: %w", err)
	}
	after, err := catalog.ReadSnapshot(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	fenceDigest, err := catalog.FingerprintWithExclusions(after,
		"public.dawgs_operation_confirmation", "public.dawgs_schema_fence_migration")
	if err != nil {
		return Report{}, err
	}
	receiptFacts := struct {
		Version         int               `json:"version"`
		State           string            `json:"state"`
		PreflightDigest string            `json:"preflight_digest"`
		InverseCatalog  string            `json:"inverse_catalog_sha256"`
		FenceCatalog    string            `json:"fence_catalog_sha256"`
		Writers         []writerProbeFact `json:"writers"`
	}{1, "fence_installed_unverified", factsDigest, catalogDigest, fenceDigest, writers}
	receipt, err := appendReceipt(ctx, tx, "migration", migrationID, "fence_installation",
		confirmationAttemptID(confirmation), receiptFacts, confirmation)
	if err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration
set state='fence_installed_unverified',fence_catalog_sha256=$2,updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='fence_installing'`, migrationID, fenceDigest); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	if err := s.hitFailpoint("after_fence_commit"); err != nil {
		return Report{Version: 1, Action: "migrate", State: "fence_installed_unverified", MigrationID: migrationID, ManifestSHA256: manifestDigest, PreflightDigest: factsDigest, ReceiptDigest: receipt.Digest, CreatedAt: time.Now().UTC()}, err
	}
	return Report{Version: 1, Action: "migrate", State: "fence_installed_unverified", MigrationID: migrationID, ManifestSHA256: manifestDigest, PreflightDigest: factsDigest, ReceiptDigest: receipt.Digest, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) VerifyFence(ctx context.Context, migrationID, confirmation string) (Report, error) {
	var state string
	var expected databaseIdentity
	var encodedWriters []byte
	if err := s.Pool.QueryRow(ctx, `select state,database_oid,database_name,system_identifier,writer_facts
from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).Scan(
		&state, &expected.OID, &expected.Name, &expected.SystemIdentifier, &encodedWriters); err != nil {
		return Report{}, err
	}
	if state == "fence_verified" {
		return Report{Version: 1, Action: "verify-fence", State: state, MigrationID: migrationID, CreatedAt: time.Now().UTC()}, nil
	}
	if state != "fence_installed_unverified" && state != "fence_database_verified_retirement_pending" && state != "writer_retirement_in_progress" {
		return Report{}, fmt.Errorf("migration %q is not awaiting fence verification", migrationID)
	}
	var recorded []writerProbeFact
	if err := json.Unmarshal(encodedWriters, &recorded); err != nil {
		return Report{}, err
	}
	var current []writerProbeFact
	var err error
	if state == "fence_installed_unverified" {
		current, err = s.authenticateWriterProbes(ctx, expected, false)
		if err != nil {
			return Report{}, err
		}
	} else {
		current, err = bindRetiredWriterDescriptors(s.WriterProbes, recorded)
		if err != nil {
			return Report{}, err
		}
	}
	if !equalWriterFacts(recorded, current) {
		return Report{}, fmt.Errorf("writer_probe_mismatch: supplied probes do not match the journal")
	}
	if state == "fence_installed_unverified" {
		for _, writer := range recorded {
			if _, err := s.Pool.Exec(ctx, `select pg_catalog.pg_terminate_backend(pid)
from pg_catalog.pg_stat_activity where usename=$1 and datname=$2 and pid<>pg_catalog.pg_backend_pid()`, writer.RoleName, expected.Name); err != nil {
				return Report{}, fmt.Errorf("draining writer sessions for %s: %w", writer.RoleName, err)
			}
		}
		for _, probe := range s.WriterProbes {
			if err := verifyWriterDenied(ctx, probe); err != nil {
				return Report{}, err
			}
		}
		verifyTx, err := s.Pool.Begin(ctx)
		if err != nil {
			return Report{}, err
		}
		defer verifyTx.Rollback(ctx)
		if err := consumeOperationConfirmation(ctx, verifyTx, "verify-fence", migrationID, confirmation); err != nil {
			return Report{}, err
		}
		probeFacts := struct {
			Version int               `json:"version"`
			State   string            `json:"state"`
			Writers []writerProbeFact `json:"writers"`
		}{1, "fence_database_verified_retirement_pending", recorded}
		if _, err := appendReceipt(ctx, verifyTx, "migration", migrationID, "database_fence_probe", receiptDigest(probeFacts), probeFacts, ""); err != nil {
			return Report{}, err
		}
		tag, err := verifyTx.Exec(ctx, `update public.dawgs_schema_fence_migration
set state='fence_database_verified_retirement_pending',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='fence_installed_unverified'`, migrationID)
		if err != nil {
			return Report{}, err
		}
		if tag.RowsAffected() != 1 {
			return Report{}, fmt.Errorf("fence state changed concurrently")
		}
		if err := verifyTx.Commit(ctx); err != nil {
			return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
		}
		state = "fence_database_verified_retirement_pending"
		if err := s.hitFailpoint("after_database_fence_verified"); err != nil {
			return Report{Version: 1, Action: "verify-fence", State: state, MigrationID: migrationID, CreatedAt: time.Now().UTC()}, err
		}
		return Report{Version: 1, Action: "verify-fence", State: state, MigrationID: migrationID,
			CreatedAt: time.Now().UTC(), Blockers: []string{"credential_retirement_required"}}, nil
	}
	if s.RetirementConnectionString == "" {
		return Report{Version: 1, Action: "verify-fence", State: state, MigrationID: migrationID,
			CreatedAt: time.Now().UTC(), Blockers: []string{"credential_retirement_required"}}, nil
	}
	if state == "fence_database_verified_retirement_pending" {
		intentTx, err := s.Pool.Begin(ctx)
		if err != nil {
			return Report{}, err
		}
		defer intentTx.Rollback(ctx)
		if err := consumeOperationConfirmation(ctx, intentTx, "verify-fence", migrationID, confirmation); err != nil {
			return Report{}, err
		}
		intentFacts := struct {
			Version int               `json:"version"`
			State   string            `json:"state"`
			Writers []writerProbeFact `json:"writers"`
		}{1, "writer_retirement_in_progress", recorded}
		if _, err := appendReceipt(ctx, intentTx, "migration", migrationID, "writer_retirement_intent",
			confirmationAttemptID(confirmation), intentFacts, confirmation); err != nil {
			return Report{}, err
		}
		tag, err := intentTx.Exec(ctx, `update public.dawgs_schema_fence_migration
set state='writer_retirement_in_progress',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='fence_database_verified_retirement_pending'`, migrationID)
		if err != nil {
			return Report{}, err
		}
		if tag.RowsAffected() != 1 {
			return Report{}, fmt.Errorf("writer retirement intent state changed concurrently")
		}
		if err := intentTx.Commit(ctx); err != nil {
			return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
		}
		state = "writer_retirement_in_progress"
	}
	if err := retireWriterRoles(ctx, s.RetirementConnectionString, expected, recorded); err != nil {
		return Report{}, err
	}
	for _, probe := range s.WriterProbes {
		if err := verifyWriterAuthenticationRetired(ctx, probe); err != nil {
			return Report{}, err
		}
	}
	if err := s.hitFailpoint("after_writer_retirement_before_receipt"); err != nil {
		return Report{Version: 1, Action: "verify-fence", State: state, MigrationID: migrationID, CreatedAt: time.Now().UTC()}, err
	}
	retirementTx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer retirementTx.Rollback(ctx)
	retirementFacts := struct {
		Version int               `json:"version"`
		State   string            `json:"state"`
		Writers []writerProbeFact `json:"writers"`
	}{1, "fence_verified", recorded}
	retirementReceipt, err := appendReceipt(ctx, retirementTx, "migration", migrationID, "writer_retirement",
		receiptDigest(retirementFacts), retirementFacts, "")
	if err != nil {
		return Report{}, err
	}
	tag, err := retirementTx.Exec(ctx, `update public.dawgs_schema_fence_migration
set state='fence_verified',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='writer_retirement_in_progress'`, migrationID)
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("fence retirement state changed concurrently")
	}
	if err := retirementTx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "verify-fence", State: "fence_verified", MigrationID: migrationID,
		ReceiptDigest: retirementReceipt.Digest, CreatedAt: time.Now().UTC()}, nil
}

func bindRetiredWriterDescriptors(probes []WriterProbe, recorded []writerProbeFact) ([]writerProbeFact, error) {
	byID := make(map[string]writerProbeFact, len(recorded))
	for _, fact := range recorded {
		byID[fact.ProbeID] = fact
	}
	current := make([]writerProbeFact, 0, len(probes))
	seen := map[string]struct{}{}
	for _, probe := range probes {
		fact, found := byID[probe.ProbeID]
		if !found || probe.RoleName != fact.RoleName || probe.Retirement != fact.Retirement || probe.ConnectionString == "" {
			return nil, fmt.Errorf("writer_probe_mismatch: retired probe descriptor %q does not match the journal", probe.ProbeID)
		}
		if _, duplicate := seen[probe.ProbeID]; duplicate {
			return nil, fmt.Errorf("duplicate writer probe ID %q", probe.ProbeID)
		}
		seen[probe.ProbeID] = struct{}{}
		current = append(current, fact)
	}
	if len(current) != len(recorded) {
		return nil, fmt.Errorf("writer_probe_mismatch: complete retired probe set is required")
	}
	sort.Slice(current, func(i, j int) bool { return current[i].ProbeID < current[j].ProbeID })
	return current, nil
}

func equalWriterFacts(left, right []writerProbeFact) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func verifyWriterDenied(ctx context.Context, probe WriterProbe) error {
	connection, err := pgx.Connect(ctx, probe.ConnectionString)
	if err != nil {
		return fmt.Errorf("writer_probe_unavailable: %s: %w", probe.ProbeID, err)
	}
	defer connection.Close(ctx)
	if writable, err := hasEffectiveWriterRoute(ctx, connection); err != nil {
		return err
	} else if writable {
		routes, routeErr := effectiveWriterRouteNames(ctx, connection)
		if routeErr != nil {
			return routeErr
		}
		return fmt.Errorf("writer_probe_failed: %s retains effective write routes: %s", probe.ProbeID, routes)
	}
	statements := []string{
		`insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values(-9223372036854775807,1,1,1,1,'{}'::jsonb)`,
		`update public.edge set properties=properties where false`,
		`delete from public.edge where false`,
		`truncate table public.edge`,
		`create table public.dawgs_writer_fence_probe(id integer)`,
	}
	rows, err := connection.Query(ctx, `select c.relname from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on n.oid=c.relnamespace
join pg_catalog.pg_inherits i on i.inhrelid=c.oid
where n.nspname='public' and i.inhparent='public.edge'::pg_catalog.regclass order by c.relname`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var relation string
		if err := rows.Scan(&relation); err != nil {
			rows.Close()
			return err
		}
		statements = append(statements, fmt.Sprintf(`insert into %s(id,graph_id,start_id,end_id,kind_id,properties) values(-9223372036854775807,1,1,1,1,'{}'::jsonb)`, pgx.Identifier{"public", relation}.Sanitize()))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, statement := range statements {
		tx, err := connection.Begin(ctx)
		if err != nil {
			return err
		}
		_, execErr := tx.Exec(ctx, statement)
		_ = tx.Rollback(ctx)
		if !approvedFenceError(execErr) {
			return fmt.Errorf("writer_probe_failed: %s statement %q returned %v", probe.ProbeID, statement, execErr)
		}
	}
	copyTx, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	_, copyErr := copyTx.CopyFrom(ctx, pgx.Identifier{"public", "edge"},
		[]string{"id", "graph_id", "start_id", "end_id", "kind_id", "properties"},
		pgx.CopyFromRows([][]any{{int64(-9223372036854775806), int32(1), int64(1), int64(1), int16(1), []byte(`{}`)}}))
	_ = copyTx.Rollback(ctx)
	if !approvedFenceError(copyErr) {
		return fmt.Errorf("writer_probe_failed: %s COPY returned %v", probe.ProbeID, copyErr)
	}
	return nil
}

func approvedFenceError(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && (pgError.Code == "42501" || pgError.Code == "55000")
}

func retireWriterRoles(ctx context.Context, connectionString string, expected databaseIdentity, writers []writerProbeFact) error {
	connection, err := pgx.Connect(ctx, connectionString)
	if err != nil {
		return fmt.Errorf("opening retirement coordinator: %w", err)
	}
	defer connection.Close(ctx)
	identity, err := currentDatabaseIdentity(ctx, connection)
	if err != nil {
		return err
	}
	if identity != expected {
		return fmt.Errorf("retirement coordinator targets a different database incarnation")
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, writer := range writers {
		var oid uint32
		if err := tx.QueryRow(ctx, `select oid from pg_catalog.pg_roles where rolname=$1`, writer.RoleName).Scan(&oid); err != nil {
			return err
		}
		if oid != writer.RoleOID {
			return fmt.Errorf("writer role identity changed for %s", writer.RoleName)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`alter role %s nologin password null`, pgx.Identifier{writer.RoleName}.Sanitize())); err != nil {
			return fmt.Errorf("retiring writer role %s: %w", writer.RoleName, err)
		}
	}
	return tx.Commit(ctx)
}

func verifyWriterAuthenticationRetired(ctx context.Context, probe WriterProbe) error {
	connection, err := pgx.Connect(ctx, probe.ConnectionString)
	if err == nil {
		connection.Close(ctx)
		return fmt.Errorf("writer_retirement_failed: probe %s still authenticates", probe.ProbeID)
	}
	var connectError *pgconn.ConnectError
	var pgError *pgconn.PgError
	if errors.As(err, &connectError) && errors.As(connectError, &pgError) && (pgError.Code == "28P01" || pgError.Code == "28000") {
		return nil
	}
	// pgx may wrap the server authentication error without preserving a public
	// PgError on all supported majors. Accept only the explicit server-auth text;
	// network, DNS, TLS, and timeout failures remain blockers.
	if strings.Contains(err.Error(), "password authentication failed") || strings.Contains(err.Error(), "is not permitted to log in") {
		return nil
	}
	return fmt.Errorf("writer_retirement_unknown: probe %s: %w", probe.ProbeID, err)
}

func receiptDigest(value any) string {
	content, _ := json.Marshal(value)
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
