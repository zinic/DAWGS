package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type ownerIdentity struct {
	OID  uint32
	Name string
}

func databaseOwnerIdentity(ctx context.Context, queryer rowQuerier) (ownerIdentity, error) {
	var owner ownerIdentity
	err := queryer.QueryRow(ctx, `select r.oid,r.rolname from pg_catalog.pg_database d
join pg_catalog.pg_roles r on r.oid=d.datdba where d.datname=pg_catalog.current_database()`).Scan(&owner.OID, &owner.Name)
	if err != nil {
		return ownerIdentity{}, fmt.Errorf("reading database owner identity: %w", err)
	}
	return owner, nil
}

func (s *Engine) connectHandoffCoordinator(ctx context.Context, expected databaseIdentity) (*pgx.Conn, error) {
	if s.RetirementConnectionString == "" {
		return nil, fmt.Errorf("handoff_coordinator_required: directly authenticated database coordinator connection is required")
	}
	connection, err := pgx.Connect(ctx, s.RetirementConnectionString)
	if err != nil {
		return nil, fmt.Errorf("opening handoff coordinator: %w", err)
	}
	identity, identityErr := readSessionIdentity(ctx, connection)
	database, databaseErr := currentDatabaseIdentity(ctx, connection)
	if identityErr != nil || databaseErr != nil {
		connection.Close(ctx)
		if identityErr != nil {
			return nil, identityErr
		}
		return nil, databaseErr
	}
	if database != expected {
		connection.Close(ctx)
		return nil, fmt.Errorf("handoff coordinator targets a different database incarnation")
	}
	if identity.SessionUser != identity.CurrentUser || identity.RoleName != identity.SessionUser || !identity.Superuser {
		connection.Close(ctx)
		return nil, fmt.Errorf("handoff coordinator must be a directly authenticated superuser")
	}
	return connection, nil
}

func transferManagedOwnership(ctx context.Context, tx pgx.Tx, operator sessionIdentity, owner ownerIdentity, targetRole string) error {
	if operator.RoleOID == 0 || owner.OID == 0 || operator.RoleOID == owner.OID || operator.RoleName == targetRole || owner.Name == targetRole {
		return fmt.Errorf("owner, operator, and target identities must be non-empty and distinct")
	}
	operatorSQL := pgx.Identifier{operator.RoleName}.Sanitize()
	ownerSQL := pgx.Identifier{owner.Name}.Sanitize()
	targetSQL := pgx.Identifier{targetRole}.Sanitize()
	if _, err := tx.Exec(ctx, fmt.Sprintf(`reassign owned by %s to %s`, operatorSQL, ownerSQL)); err != nil {
		return fmt.Errorf("transferring managed object ownership: %w", err)
	}
	statements := []string{
		fmt.Sprintf(`revoke create on schema public from public,%s,%s`, operatorSQL, targetSQL),
		fmt.Sprintf(`grant usage on schema public to %s,%s`, operatorSQL, targetSQL),
		fmt.Sprintf(`grant select on all tables in schema public to %s`, operatorSQL),
		fmt.Sprintf(`grant select on all sequences in schema public to %s`, operatorSQL),
		fmt.Sprintf(`grant insert,update on table public.dawgs_operation_confirmation to %s`, operatorSQL),
		fmt.Sprintf(`grant insert on table public.dawgs_schema_receipt to %s`, operatorSQL),
		fmt.Sprintf(`grant update on table public.dawgs_schema_layout,public.dawgs_schema_migration to %s`, operatorSQL),
		fmt.Sprintf(`grant execute on function public.dawgs_current_smoke_receipt_valid(text,text,text) to %s`, operatorSQL),
		fmt.Sprintf(`grant execute on function public.dawgs_current_cleanup_receipt_valid(text,text,text) to %s`, operatorSQL),
		fmt.Sprintf(`do $grant$ begin if pg_catalog.to_regclass('public.dawgs_schema_fence_migration') is not null then execute pg_catalog.format('grant update on table public.dawgs_schema_fence_migration to %%I',%s); end if; end $grant$`, quoteLiteral(operator.RoleName)),
		fmt.Sprintf(`alter default privileges for role %s in schema public revoke execute on functions from public`, ownerSQL),
		fmt.Sprintf(`alter default privileges for role %s in schema public revoke all on tables from %s`, ownerSQL, targetSQL),
		fmt.Sprintf(`alter default privileges for role %s in schema public revoke all on sequences from %s`, ownerSQL, targetSQL),
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("applying owner handoff ACLs: %w", err)
		}
	}
	var foreignRelations, foreignRoutines int64
	if err := tx.QueryRow(ctx, `select
  (select count(*) from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
    where n.nspname='public' and c.relkind in ('r','p','S','v','m','f') and c.relowner<>$1),
  (select count(*) from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
    where n.nspname='public' and p.proowner<>$1 and not exists (
      select 1 from pg_catalog.pg_depend d where d.classid='pg_catalog.pg_proc'::pg_catalog.regclass
        and d.objid=p.oid and d.deptype='e'))`, owner.OID).Scan(&foreignRelations, &foreignRoutines); err != nil {
		return err
	}
	if foreignRelations != 0 || foreignRoutines != 0 {
		return fmt.Errorf("owner handoff left %d relations and %d routines under another owner", foreignRelations, foreignRoutines)
	}
	return nil
}
