package layout

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type State string

const (
	StateUninitialized  State = "uninitialized"
	StateV1             State = "v1"
	StateV2Accepted     State = "v2_accepted"
	StatePendingHandoff State = "v2_pending_handoff"
	StateMigrating      State = "v2_migrating"
	StatePending        State = "v2_pending_acceptance"
	StatePendingSmoke   State = "v2_pending_smoke"
	StateRestoreSmoke   State = "v2_restore_pending_smoke"
	StateRolledBack     State = "v2_rolled_back"
	StateDrifted        State = "drifted"
)

type Facts struct {
	State           State
	Version         int16
	Graph           bool
	Node            bool
	Edge            bool
	V1Shape         bool
	Marker          bool
	Structural      bool
	Drift           []string
	GuardStatements int
}

// RuntimeIdentity contains the live and recorded connection identity returned
// by the accepted-layout guard. It intentionally contains only scalar facts so
// the guard always returns exactly one bounded result row.
type RuntimeIdentity struct {
	RecordedDatabaseOID      uint32
	RecordedDatabaseName     string
	RecordedSystemIdentifier string
	RecordedTargetRoleOID    uint32
	RecordedTargetRoleName   string
	SmokeStatus              string
	SmokeReceiptDigest       string
	CurrentDatabaseOID       uint32
	CurrentDatabaseName      string
	CurrentSystemIdentifier  string
	CurrentRoleOID           uint32
	CurrentRoleName          string
	SessionUser              string
	CurrentUser              string
	CurrentRoleSuperuser     bool
	CurrentDatabaseOwnerOID  uint32
	CurrentRoleHasMembership bool
}

type AcceptedFacts struct {
	Facts
	Identity RuntimeIdentity
	Accepted bool
}

type Row interface {
	Scan(dest ...any) error
}

type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const baseClassificationSQL = `/* dawgs:guard/base */ select
  pg_catalog.to_regclass('public.graph') is not null,
  pg_catalog.to_regclass('public.node') is not null,
  pg_catalog.to_regclass('public.edge') is not null,
  coalesce((
    (select c.relkind = 'r' from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relname='graph') and
    (select c.relkind = 'p' and pg_catalog.pg_get_partkeydef(c.oid)='LIST (graph_id)'
      from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relname='node') and
    (select c.relkind = 'p' and pg_catalog.pg_get_partkeydef(c.oid)='LIST (graph_id)'
      from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relname='edge')
  ),false),
  pg_catalog.to_regclass('public.dawgs_schema_layout') is not null`

const v2ClassificationSQL = `/* dawgs:guard/structure */ select
  pg_catalog.to_regclass('public.graph') is not null,
  pg_catalog.to_regclass('public.node') is not null,
  pg_catalog.to_regclass('public.edge') is not null,
  coalesce((
    (select c.relkind = 'r' from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relname='graph') and
    (select c.relkind = 'p' and pg_catalog.pg_get_partkeydef(c.oid)='LIST (graph_id)'
      from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relname='node') and
    (select c.relkind = 'p' and pg_catalog.pg_get_partkeydef(c.oid)='LIST (graph_id)'
      from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relname='edge')
  ),false),
  pg_catalog.to_regclass('public.dawgs_schema_layout') is not null,
  case when pg_catalog.to_regclass('public.dawgs_schema_layout') is null then false else coalesce((
    coalesce((select c.relkind = 'r' from pg_catalog.pg_class c
      join pg_catalog.pg_namespace n on n.oid = c.relnamespace
      where n.nspname = 'public' and c.relname = 'dawgs_schema_layout'), false) and
    (select c.relkind = 'p' and pt.partstrat = 'l' and pg_catalog.pg_get_partkeydef(c.oid)='LIST (graph_id)'
       from pg_catalog.pg_class c
       join pg_catalog.pg_namespace n on n.oid = c.relnamespace
       join pg_catalog.pg_partitioned_table pt on pt.partrelid = c.oid
      where n.nspname = 'public' and c.relname = 'node') and
    (select c.relkind = 'p' and pt.partstrat = 'l' and pg_catalog.pg_get_partkeydef(c.oid)='LIST (graph_id)'
       from pg_catalog.pg_class c
       join pg_catalog.pg_namespace n on n.oid = c.relnamespace
       join pg_catalog.pg_partitioned_table pt on pt.partrelid = c.oid
      where n.nspname = 'public' and c.relname = 'edge') and
    not exists (select 1 from (values
      ('dawgs_edge_kind_leaf'),('edge_identity'),('edge_property_constraint'),
      ('edge_property_unique_value'),('dawgs_schema_receipt'),('dawgs_operation_confirmation'),
      ('dawgs_schema_migration'),('dawgs_schema_migration_graph'),('dawgs_legacy_cleanup_scope'),
      ('dawgs_legacy_cleanup_map'),('dawgs_legacy_cleanup_completion'),
      ('dawgs_restore_rebind_scope'),('dawgs_restore_rebind_map')) expected(name)
      where not exists (select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
        where n.nspname='public' and c.relname=expected.name and c.relkind='r')) and
    not exists (select 1 from public.graph g where g.id < 1 or g.id > 2147483647) and
    not exists (select 1 from public.graph g where not exists (
      select 1 from pg_catalog.pg_class child join pg_catalog.pg_namespace cn on cn.oid=child.relnamespace
      join pg_catalog.pg_inherits i on i.inhrelid=child.oid
      join pg_catalog.pg_class parent on parent.oid=i.inhparent
      join pg_catalog.pg_namespace pn on pn.oid=parent.relnamespace
      where cn.nspname='public' and pn.nspname='public' and parent.relname='edge'
        and child.relname=pg_catalog.format('edge_%s',g.id) and child.relkind='p'
        and pg_catalog.pg_get_partkeydef(child.oid)='LIST (kind_id)'
        and pg_catalog.pg_get_expr(child.relpartbound,child.oid) in (
          pg_catalog.format('FOR VALUES IN (%s)',g.id),pg_catalog.format('FOR VALUES IN (''%s'')',g.id)))) and
    not exists (select 1 from public.dawgs_edge_kind_leaf registry where registry.lifecycle_state='active' and not exists (
      select 1 from pg_catalog.pg_class leaf join pg_catalog.pg_namespace ln on ln.oid=leaf.relnamespace
      join pg_catalog.pg_inherits li on li.inhrelid=leaf.oid
      join pg_catalog.pg_class parent on parent.oid=li.inhparent
      join pg_catalog.pg_namespace pn on pn.oid=parent.relnamespace
      where ln.nspname='public' and pn.nspname='public'
        and leaf.relname=pg_catalog.format('edge_%s_%s',registry.graph_id,registry.kind_id)
        and parent.relname=pg_catalog.format('edge_%s',registry.graph_id)
        and pg_catalog.pg_get_expr(leaf.relpartbound,leaf.oid) in (
          pg_catalog.format('FOR VALUES IN (%s)',registry.kind_id),
          pg_catalog.format('FOR VALUES IN (''%s'')',registry.kind_id)))) and
    not exists (select 1 from pg_catalog.pg_class leaf join pg_catalog.pg_namespace ln on ln.oid=leaf.relnamespace
      join pg_catalog.pg_inherits li on li.inhrelid=leaf.oid
      join pg_catalog.pg_class parent on parent.oid=li.inhparent
      join pg_catalog.pg_namespace pn on pn.oid=parent.relnamespace
      join pg_catalog.pg_inherits pi on pi.inhrelid=parent.oid
      join pg_catalog.pg_class root on root.oid=pi.inhparent
      join pg_catalog.pg_namespace rn on rn.oid=root.relnamespace
      where ln.nspname='public' and pn.nspname='public' and rn.nspname='public' and root.relname='edge'
        and not exists (select 1 from public.dawgs_edge_kind_leaf registry
          where registry.lifecycle_state='active'
            and leaf.relname=pg_catalog.format('edge_%s_%s',registry.graph_id,registry.kind_id)
            and parent.relname=pg_catalog.format('edge_%s',registry.graph_id))) and
    not exists (select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relispartition and c.relpartbound is not null
        and pg_catalog.pg_get_expr(c.relpartbound,c.oid)='DEFAULT') and
    exists (select 1 from pg_catalog.pg_trigger t
             join pg_catalog.pg_class c on c.oid = t.tgrelid
             join pg_catalog.pg_namespace n on n.oid = c.relnamespace
             where n.nspname = 'public' and c.relname = 'edge'
               and t.tgname = 'dawgs_10_claim_edge' and not t.tgisinternal and t.tgenabled <> 'D') and
    exists (select 1 from pg_catalog.pg_trigger t
             join pg_catalog.pg_class c on c.oid = t.tgrelid
             join pg_catalog.pg_namespace n on n.oid = c.relnamespace
             where n.nspname = 'public' and c.relname = 'edge'
               and t.tgname = 'dawgs_20_update_edge_claims' and not t.tgisinternal and t.tgenabled <> 'D')
  ), false) end`

const acceptedClassificationSQL = `/* dawgs:guard/accepted */ with
structure(graph_present,node_present,edge_present,v1_shape,marker_present,structural) as (` + v2ClassificationSQL + `),
contract as (
  select l.layout_version,l.state,
    l.active_migration_id is null and l.accepted_scope_kind is not null and l.accepted_scope_id is not null
    and ((l.accepted_scope_kind='migration' and l.accepted_migration_id=l.accepted_scope_id)
      or (l.accepted_scope_kind='bootstrap' and l.bootstrap_operation_id=l.accepted_scope_id and l.accepted_migration_id is null))
    and l.database_oid is not null and l.database_name is not null and l.system_identifier is not null
    and l.database_incarnation is not null and l.identity_epoch > 0 and l.target_role_oid is not null
    and l.target_role_name is not null and l.owner_role_oid is not null and l.owner_role_name is not null
    and l.owner_role_oid=d.datdba
    and exists(select 1 from pg_catalog.pg_roles owner_role where owner_role.oid=l.owner_role_oid
      and owner_role.rolname=l.owner_role_name and not owner_role.rolsuper and not owner_role.rolcanlogin
      and not exists(select 1 from pg_catalog.pg_auth_members m where m.member=owner_role.oid or m.roleid=owner_role.oid))
    and exists(select 1 from pg_catalog.pg_roles target_role where target_role.oid=l.target_role_oid
      and target_role.rolname=l.target_role_name and not target_role.rolsuper and target_role.rolcanlogin
      and target_role.oid<>l.owner_role_oid
      and not exists(select 1 from pg_catalog.pg_auth_members m where m.member=target_role.oid or m.roleid=target_role.oid))
    and not exists(select 1 from (values ('graph'),('kind'),('node'),('edge'),('dawgs_schema_layout'),
      ('dawgs_schema_receipt'),('dawgs_operation_confirmation'),('dawgs_schema_migration'),
      ('dawgs_schema_migration_graph'),('dawgs_edge_kind_leaf'),('edge_identity'),
      ('edge_property_constraint'),('edge_property_unique_value'),('dawgs_legacy_cleanup_scope'),
      ('dawgs_legacy_cleanup_map'),('dawgs_legacy_cleanup_completion'),
      ('dawgs_restore_rebind_scope'),('dawgs_restore_rebind_map')) expected(name)
      join pg_catalog.pg_class c on c.relname=expected.name
      join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relowner<>l.owner_role_oid)
    and not exists(select 1 from (values
      ('dawgs_provision_graph','requested_name text'),
	  ('dawgs_provision_edge_kind_leaf','requested_graph_id integer, requested_kind_id smallint'),
	  ('dawgs_wipe_graph',''),
      ('dawgs_claim_edge',''),('dawgs_update_edge_claims',''),('dawgs_refuse_graph_delete',''),
      ('dawgs_refuse_cleanup_map_mutation',''),
      ('dawgs_current_cleanup_receipt_valid','expected_scope_kind text, expected_scope_id text, expected_digest text')
    ) expected(name,args)
      join pg_catalog.pg_proc p on p.proname=expected.name and pg_catalog.pg_get_function_identity_arguments(p.oid)=expected.args
      join pg_catalog.pg_namespace n on n.oid=p.pronamespace
      where n.nspname='public' and p.proowner<>l.owner_role_oid)
    and not exists(select 1 from pg_catalog.pg_inherits i join pg_catalog.pg_class c on c.oid=i.inhrelid
      join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relowner<>l.owner_role_oid)
    and not pg_catalog.has_schema_privilege(l.target_role_name,'public','CREATE')
    and pg_catalog.has_table_privilege(l.target_role_name,'public.graph','SELECT')
    and not pg_catalog.has_table_privilege(l.target_role_name,'public.graph','INSERT,UPDATE,DELETE')
    and pg_catalog.has_table_privilege(l.target_role_name,'public.node','SELECT,INSERT,UPDATE,DELETE')
    and pg_catalog.has_table_privilege(l.target_role_name,'public.edge','SELECT,INSERT,UPDATE,DELETE')
    and not pg_catalog.has_table_privilege(l.target_role_name,'public.dawgs_edge_kind_leaf','INSERT,UPDATE,DELETE')
    and pg_catalog.has_function_privilege(l.target_role_name,'public.dawgs_provision_graph(text)','EXECUTE')
	and pg_catalog.has_function_privilege(l.target_role_name,'public.dawgs_provision_edge_kind_leaf(integer,smallint)','EXECUTE')
	and pg_catalog.has_function_privilege(l.target_role_name,'public.dawgs_wipe_graph()','EXECUTE')
    and (l.accepted_scope_kind='bootstrap' or l.validation_digest is not null)
    and l.current_legacy_cleanup_map_digest is not null
    and ((l.legacy_cleanup_status='retained' and l.current_cleanup_receipt_digest is null
      and exists(select 1 from public.dawgs_legacy_cleanup_scope c
        where c.scope_kind=l.accepted_scope_kind and c.scope_id=l.accepted_scope_id
          and c.map_digest=l.current_legacy_cleanup_map_digest
          and c.applicable=(l.accepted_scope_kind='migration')
          and (c.identity_epoch=l.identity_epoch or exists(select 1 from public.dawgs_restore_rebind_scope rebind
            where rebind.identity_epoch=l.identity_epoch and rebind.scope_kind=c.scope_kind and rebind.scope_id=c.scope_id
              and rebind.map_digest=l.current_legacy_cleanup_map_digest
              and rebind.receipt_digest=l.current_rebind_receipt_digest))))
      or (l.legacy_cleanup_status='complete' and l.current_cleanup_receipt_digest is not null
        and exists(select 1 from public.dawgs_legacy_cleanup_completion x
          join public.dawgs_legacy_cleanup_scope c using(scope_kind,scope_id)
          where x.scope_kind=l.accepted_scope_kind and x.scope_id=l.accepted_scope_id
            and x.source_map_digest=c.map_digest and x.empty_map_digest=l.current_legacy_cleanup_map_digest
            and x.cleanup_receipt_digest=l.current_cleanup_receipt_digest
            and public.dawgs_current_cleanup_receipt_valid(x.scope_kind,x.scope_id,x.cleanup_receipt_digest)
            and (x.identity_epoch=l.identity_epoch or exists(select 1 from public.dawgs_restore_rebind_scope rebind
              where rebind.identity_epoch=l.identity_epoch and rebind.scope_kind=x.scope_kind and rebind.scope_id=x.scope_id
                and rebind.map_digest=x.empty_map_digest and rebind.receipt_digest=l.current_rebind_receipt_digest)))))
    and l.post_acceptance_smoke_status in ('passed','fallback') and l.current_smoke_receipt_digest is not null
    and public.dawgs_current_smoke_receipt_valid(l.accepted_scope_kind,l.accepted_scope_id,l.current_smoke_receipt_digest) coherent,
    coalesce(l.database_oid,0) recorded_database_oid,coalesce(l.database_name,'') recorded_database_name,
    coalesce(l.system_identifier,'') recorded_system_identifier,coalesce(l.target_role_oid,0) recorded_target_role_oid,
    coalesce(l.target_role_name,'') recorded_target_role_name,l.post_acceptance_smoke_status,
    coalesce(l.current_smoke_receipt_digest,'') current_smoke_receipt_digest,
    d.oid current_database_oid,d.datname current_database_name,
    (pg_catalog.pg_control_system()).system_identifier::text current_system_identifier,
    session_role.oid current_role_oid,session_role.rolname current_role_name,
    session_user::text session_user_name,current_user::text current_user_name,
    session_role.rolsuper,d.datdba,
    exists(select 1 from pg_catalog.pg_auth_members m where m.member=session_role.oid) current_role_has_membership
  from public.dawgs_schema_layout l
  join pg_catalog.pg_database d on d.datname=pg_catalog.current_database()
  join pg_catalog.pg_roles session_role on session_role.rolname=session_user
  where l.singleton
)
select structure.graph_present,structure.node_present,structure.edge_present,structure.v1_shape,
  structure.marker_present,structure.structural,contract.layout_version,contract.state,contract.coherent,
  contract.recorded_database_oid,contract.recorded_database_name,contract.recorded_system_identifier,
  contract.recorded_target_role_oid,contract.recorded_target_role_name,contract.post_acceptance_smoke_status,
  contract.current_smoke_receipt_digest,contract.current_database_oid,contract.current_database_name,
  contract.current_system_identifier,contract.current_role_oid,contract.current_role_name,
  contract.session_user_name,contract.current_user_name,contract.rolsuper,contract.datdba,
  contract.current_role_has_membership
from structure cross join contract`

// ClassifyAccepted performs the complete accepted-layout check in one database
// round trip. It deliberately retains the exhaustive topology checks until an
// authoritative cross-process runtime-contract token exists.
func ClassifyAccepted(ctx context.Context, queryer Querier) (AcceptedFacts, error) {
	result := AcceptedFacts{Facts: Facts{GuardStatements: 1}}
	var coherent bool
	err := queryer.QueryRow(ctx, acceptedClassificationSQL).Scan(
		&result.Graph, &result.Node, &result.Edge, &result.V1Shape, &result.Marker, &result.Structural,
		&result.Version, &result.State, &coherent,
		&result.Identity.RecordedDatabaseOID, &result.Identity.RecordedDatabaseName,
		&result.Identity.RecordedSystemIdentifier, &result.Identity.RecordedTargetRoleOID,
		&result.Identity.RecordedTargetRoleName, &result.Identity.SmokeStatus,
		&result.Identity.SmokeReceiptDigest, &result.Identity.CurrentDatabaseOID,
		&result.Identity.CurrentDatabaseName, &result.Identity.CurrentSystemIdentifier,
		&result.Identity.CurrentRoleOID, &result.Identity.CurrentRoleName, &result.Identity.SessionUser,
		&result.Identity.CurrentUser, &result.Identity.CurrentRoleSuperuser,
		&result.Identity.CurrentDatabaseOwnerOID, &result.Identity.CurrentRoleHasMembership,
	)
	if err != nil {
		return AcceptedFacts{}, fmt.Errorf("checking accepted DAWGS PostgreSQL layout: %w", err)
	}
	result.Accepted = result.Graph && result.Node && result.Edge && result.Marker && result.Structural &&
		result.Version == 2 && result.State == StateV2Accepted && coherent
	return result, nil
}

func Classify(ctx context.Context, queryer Querier) (Facts, error) {
	facts := Facts{GuardStatements: 1}
	var marker bool
	if err := queryer.QueryRow(ctx, baseClassificationSQL).Scan(&facts.Graph, &facts.Node, &facts.Edge, &facts.V1Shape, &marker); err != nil {
		return Facts{}, fmt.Errorf("classifying DAWGS PostgreSQL layout: %w", err)
	}
	if !marker {
		if !facts.Graph && !facts.Node && !facts.Edge {
			facts.State = StateUninitialized
		} else if facts.Graph && facts.Node && facts.Edge && facts.V1Shape {
			facts.State = StateV1
		} else {
			facts.State = StateDrifted
			facts.Drift = []string{"unmarked graph/node/edge objects do not match the frozen V1 root shape"}
		}
		return facts, nil
	}
	facts.Marker = true
	var requiredV2Objects bool
	facts.GuardStatements++
	if err := queryer.QueryRow(ctx, `/* dawgs:guard/objects */ select not exists (select 1 from (values
  ('dawgs_edge_kind_leaf'),('edge_identity'),('edge_property_constraint'),
  ('edge_property_unique_value'),('dawgs_schema_receipt'),('dawgs_operation_confirmation'),
  ('dawgs_schema_migration'),('dawgs_schema_migration_graph')) expected(name)
where pg_catalog.to_regclass('public.'||expected.name) is null)`).Scan(&requiredV2Objects); err != nil {
		return Facts{}, fmt.Errorf("reading DAWGS PostgreSQL V2 object presence: %w", err)
	}
	if !requiredV2Objects {
		var version int16
		var state State
		var coherentRollback bool
		facts.GuardStatements++
		if err := queryer.QueryRow(ctx, `/* dawgs:guard/incomplete-marker */ select layout_version,state,
state='v2_rolled_back' and active_migration_id is null and accepted_migration_id is null
from public.dawgs_schema_layout where singleton`).Scan(&version, &state, &coherentRollback); err != nil {
			return Facts{}, fmt.Errorf("reading incomplete DAWGS PostgreSQL layout marker: %w", err)
		}
		if version == 2 && state == StateRolledBack && coherentRollback && facts.V1Shape {
			facts.Version = version
			facts.State = StateRolledBack
			return facts, nil
		}
		facts.State = StateDrifted
		facts.Drift = []string{"V2 marker exists but one or more required V2 objects are missing"}
		return facts, nil
	}
	facts.GuardStatements++
	if err := queryer.QueryRow(ctx, v2ClassificationSQL).Scan(&facts.Graph, &facts.Node, &facts.Edge, &facts.V1Shape, &marker, &facts.Structural); err != nil {
		return Facts{}, fmt.Errorf("reading DAWGS PostgreSQL V2 structure: %w", err)
	}
	if !facts.Graph || !facts.Node || !facts.Edge {
		facts.State = StateDrifted
		facts.Drift = []string{"V2 marker exists but one or more graph/node/edge roots are missing"}
		return facts, nil
	}
	if !facts.Structural {
		facts.State = StateDrifted
		facts.Drift = []string{"V2 hierarchy, registry, trigger, or partition topology does not match the required structure"}
		return facts, nil
	}
	var coherent bool
	facts.GuardStatements++
	if err := queryer.QueryRow(ctx, `/* dawgs:guard/coherence */ select layout_version,state,
case state
  when 'v2_accepted' then active_migration_id is null and accepted_scope_kind is not null and accepted_scope_id is not null
    and ((accepted_scope_kind='migration' and accepted_migration_id=accepted_scope_id)
      or (accepted_scope_kind='bootstrap' and bootstrap_operation_id=accepted_scope_id and accepted_migration_id is null))
    and database_oid is not null and database_name is not null and system_identifier is not null
    and database_incarnation is not null and identity_epoch > 0 and target_role_oid is not null
    and target_role_name is not null and owner_role_oid is not null and owner_role_name is not null
    and owner_role_oid=(select d.datdba from pg_catalog.pg_database d where d.datname=pg_catalog.current_database())
    and exists(select 1 from pg_catalog.pg_roles r where r.oid=owner_role_oid and r.rolname=owner_role_name
      and not r.rolsuper and not r.rolcanlogin
      and not exists(select 1 from pg_catalog.pg_auth_members m where m.member=r.oid or m.roleid=r.oid))
    and exists(select 1 from pg_catalog.pg_roles r where r.oid=target_role_oid and r.rolname=target_role_name
      and not r.rolsuper and r.rolcanlogin and r.oid<>owner_role_oid
      and not exists(select 1 from pg_catalog.pg_auth_members m where m.member=r.oid or m.roleid=r.oid))
    and not exists(select 1 from (values ('graph'),('kind'),('node'),('edge'),('dawgs_schema_layout'),
      ('dawgs_schema_receipt'),('dawgs_operation_confirmation'),('dawgs_schema_migration'),
      ('dawgs_schema_migration_graph'),('dawgs_edge_kind_leaf'),('edge_identity'),
      ('edge_property_constraint'),('edge_property_unique_value'),('dawgs_legacy_cleanup_scope'),
      ('dawgs_legacy_cleanup_map'),('dawgs_legacy_cleanup_completion'),
      ('dawgs_restore_rebind_scope'),('dawgs_restore_rebind_map')) expected(name)
      join pg_catalog.pg_class c on c.relname=expected.name
      join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relowner<>owner_role_oid)
    and not exists(select 1 from (values
      ('dawgs_provision_graph','requested_name text'),
      ('dawgs_provision_edge_kind_leaf','requested_graph_id integer, requested_kind_id smallint'),
	  ('dawgs_wipe_graph',''),
      ('dawgs_claim_edge',''),('dawgs_update_edge_claims',''),('dawgs_refuse_graph_delete',''),
      ('dawgs_refuse_cleanup_map_mutation',''),
      ('dawgs_current_cleanup_receipt_valid','expected_scope_kind text, expected_scope_id text, expected_digest text')
    ) expected(name,args)
      join pg_catalog.pg_proc p on p.proname=expected.name and pg_catalog.pg_get_function_identity_arguments(p.oid)=expected.args
      join pg_catalog.pg_namespace n on n.oid=p.pronamespace
      where n.nspname='public' and p.proowner<>owner_role_oid)
    and not exists(select 1 from pg_catalog.pg_inherits i join pg_catalog.pg_class c on c.oid=i.inhrelid
      join pg_catalog.pg_namespace n on n.oid=c.relnamespace
      where n.nspname='public' and c.relowner<>owner_role_oid)
    and not pg_catalog.has_schema_privilege(target_role_name,'public','CREATE')
    and pg_catalog.has_table_privilege(target_role_name,'public.graph','SELECT')
    and not pg_catalog.has_table_privilege(target_role_name,'public.graph','INSERT,UPDATE,DELETE')
    and pg_catalog.has_table_privilege(target_role_name,'public.node','SELECT,INSERT,UPDATE,DELETE')
    and pg_catalog.has_table_privilege(target_role_name,'public.edge','SELECT,INSERT,UPDATE,DELETE')
    and not pg_catalog.has_table_privilege(target_role_name,'public.dawgs_edge_kind_leaf','INSERT,UPDATE,DELETE')
    and pg_catalog.has_function_privilege(target_role_name,'public.dawgs_provision_graph(text)','EXECUTE')
    and pg_catalog.has_function_privilege(target_role_name,'public.dawgs_provision_edge_kind_leaf(integer,smallint)','EXECUTE')
	and pg_catalog.has_function_privilege(target_role_name,'public.dawgs_wipe_graph()','EXECUTE')
    and (accepted_scope_kind='bootstrap' or validation_digest is not null)
    and current_legacy_cleanup_map_digest is not null
    and ((legacy_cleanup_status='retained' and current_cleanup_receipt_digest is null
      and exists(select 1 from public.dawgs_legacy_cleanup_scope c
        where c.scope_kind=accepted_scope_kind and c.scope_id=accepted_scope_id
          and c.map_digest=current_legacy_cleanup_map_digest
          and c.applicable=(accepted_scope_kind='migration')
          and (c.identity_epoch=dawgs_schema_layout.identity_epoch or exists(
            select 1 from public.dawgs_restore_rebind_scope r
            where r.identity_epoch=dawgs_schema_layout.identity_epoch
              and r.scope_kind=c.scope_kind and r.scope_id=c.scope_id
              and r.map_digest=current_legacy_cleanup_map_digest
              and r.receipt_digest=dawgs_schema_layout.current_rebind_receipt_digest))))
      or (legacy_cleanup_status='complete' and current_cleanup_receipt_digest is not null
        and exists(select 1 from public.dawgs_legacy_cleanup_completion x
          join public.dawgs_legacy_cleanup_scope c using(scope_kind,scope_id)
          where x.scope_kind=accepted_scope_kind and x.scope_id=accepted_scope_id
            and x.source_map_digest=c.map_digest
            and x.empty_map_digest=current_legacy_cleanup_map_digest
            and x.cleanup_receipt_digest=current_cleanup_receipt_digest
            and public.dawgs_current_cleanup_receipt_valid(x.scope_kind,x.scope_id,x.cleanup_receipt_digest)
            and (x.identity_epoch=dawgs_schema_layout.identity_epoch or exists(
              select 1 from public.dawgs_restore_rebind_scope r
              where r.identity_epoch=dawgs_schema_layout.identity_epoch
                and r.scope_kind=x.scope_kind and r.scope_id=x.scope_id
                and r.map_digest=x.empty_map_digest
                and r.receipt_digest=dawgs_schema_layout.current_rebind_receipt_digest))))
	  )
    and post_acceptance_smoke_status in ('passed','fallback') and current_smoke_receipt_digest is not null
    and public.dawgs_current_smoke_receipt_valid(accepted_scope_kind,accepted_scope_id,current_smoke_receipt_digest)
  when 'v2_pending_handoff' then bootstrap_operation_id is not null and active_migration_id is null and accepted_migration_id is null
  when 'v2_migrating' then active_migration_id is not null and accepted_migration_id is null
    and exists(select 1 from public.dawgs_schema_migration m where m.migration_id=active_migration_id)
  when 'v2_pending_acceptance' then active_migration_id is not null and accepted_migration_id is null
    and validation_digest is null
    and exists(select 1 from public.dawgs_schema_migration m where m.migration_id=active_migration_id)
  when 'v2_pending_smoke' then active_migration_id is not null and accepted_migration_id is null
    and target_role_oid is not null and validation_digest is not null and post_acceptance_smoke_status in ('pending','failed')
  when 'v2_restore_pending_smoke' then active_migration_id is null and accepted_scope_kind is not null and accepted_scope_id is not null
    and target_role_oid is not null and owner_role_oid is not null and current_rebind_receipt_digest is not null
    and post_acceptance_smoke_status in ('pending','failed')
  when 'v2_rolled_back' then active_migration_id is null
  else false end
from public.dawgs_schema_layout where singleton`).Scan(&facts.Version, &facts.State, &coherent); err != nil {
		return Facts{}, fmt.Errorf("reading DAWGS PostgreSQL layout marker: %w", err)
	}
	if facts.Version != 2 || !knownState(facts.State) || !coherent {
		facts.State = StateDrifted
		facts.Drift = []string{"layout version, state pointers, target identity, or current smoke receipt is inconsistent"}
	}
	return facts, nil
}

func knownState(state State) bool {
	switch state {
	case StateV2Accepted, StatePendingHandoff, StateMigrating, StatePending, StatePendingSmoke, StateRestoreSmoke, StateRolledBack:
		return true
	default:
		return false
	}
}
