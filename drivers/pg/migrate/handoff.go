package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Engine) CompleteV1Handoff(ctx context.Context, migrationID, confirmation string) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: unreleased V1 handoff is restricted to the identity-guarded disposable test harness")
	}
	if s.PreparationConnectionString == "" {
		return Report{}, fmt.Errorf("preparation_connection_required: direct preparation-role connection is required")
	}
	var expected databaseIdentity
	var encodedRoles []byte
	if err := s.Pool.QueryRow(ctx, `select database_oid,database_name,system_identifier,role_facts
from public.dawgs_schema_fence_migration where migration_id=$1 and state='rolled_back_handoff_pending'`, migrationID).
		Scan(&expected.OID, &expected.Name, &expected.SystemIdentifier, &encodedRoles); err != nil {
		return Report{}, err
	}
	var roles preflightRoleFact
	if err := json.Unmarshal(encodedRoles, &roles); err != nil {
		return Report{}, err
	}
	preparation, err := pgx.Connect(ctx, s.PreparationConnectionString)
	if err != nil {
		return Report{}, fmt.Errorf("directly authenticating preparation role: %w", err)
	}
	preparationIdentity, err := readSessionIdentity(ctx, preparation)
	if err != nil {
		preparation.Close(ctx)
		return Report{}, err
	}
	preparationDatabase, err := currentDatabaseIdentity(ctx, preparation)
	closeErr := preparation.Close(ctx)
	if err != nil {
		return Report{}, err
	}
	if closeErr != nil {
		return Report{}, closeErr
	}
	if preparationDatabase != expected || preparationIdentity.RoleOID != roles.PreparationOID ||
		preparationIdentity.RoleName != roles.PreparationName || preparationIdentity.SessionUser != preparationIdentity.CurrentUser ||
		preparationIdentity.Superuser || preparationIdentity.RoleOID == preparationIdentity.DatabaseOwner || preparationIdentity.HasMembership {
		return Report{}, fmt.Errorf("preparation role identity does not match the preflighted dormant principal")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	if err := consumeOperationConfirmation(ctx, tx, "complete-v1-handoff", migrationID, confirmation); err != nil {
		return Report{}, err
	}
	role := pgx.Identifier{roles.PreparationName}.Sanitize()
	statements := []string{
		fmt.Sprintf(`grant usage on schema public to %s`, role),
		fmt.Sprintf(`grant select,insert,update on table public.graph,public.kind to %s`, role),
		fmt.Sprintf(`grant select,insert,update,delete,truncate on table public.node,public.edge to %s`, role),
		fmt.Sprintf(`grant usage,select,update on sequence public.graph_id_seq,public.kind_id_seq,public.node_id_seq,public.edge_id_seq to %s`, role),
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return Report{}, err
		}
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`do $handoff$
declare relation_name text; routine_name text;
begin
  for relation_name in select c.oid::pg_catalog.regclass::text from pg_catalog.pg_class c
    join pg_catalog.pg_namespace n on n.oid=c.relnamespace
    where n.nspname='public' and c.relispartition and c.relkind in ('r','p')
  loop execute pg_catalog.format('grant select,insert,update,delete,truncate on table %%s to %s',relation_name); end loop;
  for routine_name in select p.oid::pg_catalog.regprocedure::text from pg_catalog.pg_proc p
    join pg_catalog.pg_namespace n on n.oid=p.pronamespace where n.nspname='public'
    and not exists(select 1 from pg_catalog.pg_depend d where d.classid='pg_catalog.pg_proc'::pg_catalog.regclass
      and d.objid=p.oid and d.deptype='e')
  loop execute pg_catalog.format('grant execute on function %%s to %s',routine_name); end loop;
end $handoff$`, role, role)); err != nil {
		return Report{}, err
	}
	facts := struct {
		Version         int    `json:"version"`
		State           string `json:"state"`
		PreparationOID  uint32 `json:"preparation_oid"`
		PreparationName string `json:"preparation_name"`
	}{1, "rolled_back", roles.PreparationOID, roles.PreparationName}
	receipt, err := appendReceipt(ctx, tx, "migration", migrationID, "v1_handoff",
		confirmationAttemptID(confirmation), facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	tag, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set state='rolled_back',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='rolled_back_handoff_pending'`, migrationID)
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("V1 handoff state changed concurrently")
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "complete-v1-handoff", State: "rolled_back", MigrationID: migrationID,
		ReceiptDigest: receipt.Digest, TargetRole: roles.PreparationName, CreatedAt: time.Now().UTC()}, nil
}
