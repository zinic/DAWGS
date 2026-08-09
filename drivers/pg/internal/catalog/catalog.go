package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func ReadSnapshot(ctx context.Context, queryer Querier) (Snapshot, error) {
	var snapshot Snapshot
	loaders := []func(context.Context, Querier) error{
		func(ctx context.Context, q Querier) (err error) { snapshot.Relations, err = Relations(ctx, q); return },
		func(ctx context.Context, q Querier) (err error) { snapshot.Indexes, err = Indexes(ctx, q); return },
		func(ctx context.Context, q Querier) (err error) {
			snapshot.Constraints, err = Constraints(ctx, q)
			return
		},
		func(ctx context.Context, q Querier) (err error) { snapshot.Triggers, err = Triggers(ctx, q); return },
		func(ctx context.Context, q Querier) (err error) { snapshot.Routines, err = Routines(ctx, q); return },
		func(ctx context.Context, q Querier) (err error) { snapshot.Sequences, err = Sequences(ctx, q); return },
		func(ctx context.Context, q Querier) (err error) {
			snapshot.Dependencies, err = Dependencies(ctx, q)
			return
		},
		func(ctx context.Context, q Querier) (err error) { snapshot.Rules, err = Rules(ctx, q); return },
		func(ctx context.Context, q Querier) (err error) {
			snapshot.Publications, err = Publications(ctx, q)
			return
		},
		func(ctx context.Context, q Querier) (err error) {
			snapshot.Memberships, err = Memberships(ctx, q)
			return
		},
		func(ctx context.Context, q Querier) (err error) {
			snapshot.RoleSettings, err = RoleSettings(ctx, q)
			return
		},
		func(ctx context.Context, q Querier) (err error) {
			snapshot.DefaultACLs, err = DefaultACLs(ctx, q)
			return
		},
	}
	for _, load := range loaders {
		if err := load(ctx, queryer); err != nil {
			return Snapshot{}, err
		}
	}
	return snapshot, nil
}

func Relations(ctx context.Context, queryer Querier) ([]Relation, error) {
	rows, err := queryer.Query(ctx, `select c.oid,n.nspname,c.relname,owner.rolname,c.relkind::text,c.relpersistence::text,
c.relreplident::text,coalesce(ts.spcname,''),coalesce(pg_catalog.obj_description(c.oid,'pg_class'),''),
c.relrowsecurity,c.relforcerowsecurity,coalesce(c.reloptions,array[]::text[]),i.inhparent,
coalesce(pt.partstrat::text,''),coalesce(pg_catalog.pg_get_partkeydef(c.oid),''),
coalesce(pg_catalog.pg_get_expr(c.relpartbound,c.oid),''),
coalesce(pg_catalog.pg_get_expr(c.relpartbound,c.oid)='DEFAULT',false)
from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
join pg_catalog.pg_roles owner on owner.oid=c.relowner
left join pg_catalog.pg_tablespace ts on ts.oid=c.reltablespace
left join pg_catalog.pg_inherits i on i.inhrelid=c.oid
left join pg_catalog.pg_partitioned_table pt on pt.partrelid=c.oid
where n.nspname='public' order by c.oid`)
	if err != nil {
		return nil, fmt.Errorf("reading relation catalog facts: %w", err)
	}
	defer rows.Close()
	var result []Relation
	for rows.Next() {
		var value Relation
		var strategy, key, bound string
		var isDefault bool
		if err := rows.Scan(&value.OID, &value.Schema, &value.Name, &value.Owner, &value.Kind, &value.Persistence,
			&value.ReplicaIdentity, &value.Tablespace, &value.Comment, &value.RowSecurity, &value.ForceRowSecurity,
			&value.StorageParameters, &value.ParentOID, &strategy, &key, &bound, &isDefault); err != nil {
			return nil, err
		}
		if strategy != "" || bound != "" {
			value.Partition = &Partition{Strategy: strategy, KeyDefinition: key, Bound: bound, Default: isDefault}
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	acls, err := relationACLs(ctx, queryer)
	if err != nil {
		return nil, err
	}
	for index := range result {
		result[index].ACL = acls[result[index].OID]
	}
	return result, nil
}

func relationACLs(ctx context.Context, queryer Querier) (map[uint32][]ACL, error) {
	rows, err := queryer.Query(ctx, `select c.oid,grantor.rolname,coalesce(grantee.rolname,'PUBLIC'),a.privilege_type,a.is_grantable
from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
cross join lateral pg_catalog.aclexplode(coalesce(c.relacl,pg_catalog.acldefault(case when c.relkind='S' then 's'::"char" else 'r'::"char" end,c.relowner))) a
join pg_catalog.pg_roles grantor on grantor.oid=a.grantor left join pg_catalog.pg_roles grantee on grantee.oid=a.grantee
where n.nspname='public' order by c.oid,a.grantee,a.privilege_type`)
	if err != nil {
		return nil, fmt.Errorf("reading relation ACL facts: %w", err)
	}
	defer rows.Close()
	result := map[uint32][]ACL{}
	for rows.Next() {
		var oid uint32
		var value ACL
		if err := rows.Scan(&oid, &value.Grantor, &value.Grantee, &value.Privilege, &value.Grantable); err != nil {
			return nil, err
		}
		result[oid] = append(result[oid], value)
	}
	return result, rows.Err()
}

func Indexes(ctx context.Context, q Querier) ([]Index, error) {
	rows, err := q.Query(ctx, `select i.indexrelid,i.indrelid,c.relname,i.indisunique,i.indisprimary,i.indisvalid,i.indisready,pg_catalog.pg_get_indexdef(i.indexrelid)
from pg_catalog.pg_index i join pg_catalog.pg_class c on c.oid=i.indexrelid join pg_catalog.pg_class r on r.oid=i.indrelid
join pg_catalog.pg_namespace n on n.oid=r.relnamespace where n.nspname='public' order by i.indrelid,i.indexrelid`)
	if err != nil {
		return nil, fmt.Errorf("reading index facts: %w", err)
	}
	defer rows.Close()
	var out []Index
	for rows.Next() {
		var v Index
		if err := rows.Scan(&v.OID, &v.RelationOID, &v.Name, &v.Unique, &v.Primary, &v.Valid, &v.Ready, &v.Definition); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Constraints(ctx context.Context, q Querier) ([]Constraint, error) {
	rows, err := q.Query(ctx, `select c.oid,c.conrelid,c.conname,c.contype::text,c.convalidated,c.condeferrable,c.condeferred,pg_catalog.pg_get_constraintdef(c.oid,true)
from pg_catalog.pg_constraint c join pg_catalog.pg_class r on r.oid=c.conrelid join pg_catalog.pg_namespace n on n.oid=r.relnamespace
where n.nspname='public' order by c.conrelid,c.oid`)
	if err != nil {
		return nil, fmt.Errorf("reading constraint facts: %w", err)
	}
	defer rows.Close()
	var out []Constraint
	for rows.Next() {
		var v Constraint
		if err := rows.Scan(&v.OID, &v.RelationOID, &v.Name, &v.Type, &v.Validated, &v.Deferrable, &v.Deferred, &v.Definition); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Triggers(ctx context.Context, q Querier) ([]Trigger, error) {
	rows, err := q.Query(ctx, `select t.oid,t.tgrelid,t.tgname,t.tgenabled::text,t.tgisinternal,pn.nspname,p.proname,pg_catalog.pg_get_triggerdef(t.oid,true)
from pg_catalog.pg_trigger t join pg_catalog.pg_class r on r.oid=t.tgrelid join pg_catalog.pg_namespace n on n.oid=r.relnamespace
join pg_catalog.pg_proc p on p.oid=t.tgfoid join pg_catalog.pg_namespace pn on pn.oid=p.pronamespace
where n.nspname='public' order by t.tgrelid,t.oid`)
	if err != nil {
		return nil, fmt.Errorf("reading trigger facts: %w", err)
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		var v Trigger
		if err := rows.Scan(&v.OID, &v.RelationOID, &v.Name, &v.Enabled, &v.Internal, &v.FunctionSchema, &v.FunctionName, &v.Definition); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Routines(ctx context.Context, q Querier) ([]Routine, error) {
	rows, err := q.Query(ctx, `select p.oid,n.nspname,p.proname,o.rolname,p.prokind::text,p.prosecdef,p.provolatile::text,p.proparallel::text,
coalesce(p.proconfig,array[]::text[]),pg_catalog.pg_get_function_identity_arguments(p.oid),pg_catalog.pg_get_function_result(p.oid),
case when p.prokind='a' then '' else pg_catalog.pg_get_functiondef(p.oid) end
from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace join pg_catalog.pg_roles o on o.oid=p.proowner
where n.nspname='public' order by p.oid`)
	if err != nil {
		return nil, fmt.Errorf("reading routine facts: %w", err)
	}
	defer rows.Close()
	var out []Routine
	for rows.Next() {
		var v Routine
		if err := rows.Scan(&v.OID, &v.Schema, &v.Name, &v.Owner, &v.Kind, &v.SecurityDefiner, &v.Volatility, &v.Parallel, &v.Configuration, &v.IdentityArguments, &v.Result, &v.Definition); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Sequences(ctx context.Context, q Querier) ([]Sequence, error) {
	rows, err := q.Query(ctx, `select c.oid,n.nspname,c.relname,o.rolname,s.seqtypid::pg_catalog.regtype::text,s.seqstart,s.seqmin,s.seqmax,s.seqincrement,s.seqcycle,s.seqcache
from pg_catalog.pg_sequence s join pg_catalog.pg_class c on c.oid=s.seqrelid join pg_catalog.pg_namespace n on n.oid=c.relnamespace
join pg_catalog.pg_roles o on o.oid=c.relowner where n.nspname='public' order by c.oid`)
	if err != nil {
		return nil, fmt.Errorf("reading sequence facts: %w", err)
	}
	defer rows.Close()
	var out []Sequence
	for rows.Next() {
		var v Sequence
		if err := rows.Scan(&v.OID, &v.Schema, &v.Name, &v.Owner, &v.DataType, &v.Start, &v.Minimum, &v.Maximum, &v.Increment, &v.Cycle, &v.Cache); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Dependencies(ctx context.Context, q Querier) ([]Dependency, error) {
	rows, err := q.Query(ctx, `select d.classid,d.objid,d.objsubid,
o.type,coalesce(o.schema,''),coalesce(o.name,''),o.identity,
d.refclassid,d.refobjid,d.refobjsubid,
r.type,coalesce(r.schema,''),coalesce(r.name,''),r.identity,d.deptype::text
from pg_catalog.pg_depend d
cross join lateral pg_catalog.pg_identify_object(d.classid,d.objid,d.objsubid) o
cross join lateral pg_catalog.pg_identify_object(d.refclassid,d.refobjid,d.refobjsubid) r
where d.objid in (select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace where n.nspname='public')
or d.refobjid in (select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace where n.nspname='public')
order by o.type,o.schema,o.name,o.identity,r.type,r.schema,r.name,r.identity,d.deptype`)
	if err != nil {
		return nil, fmt.Errorf("reading dependency facts: %w", err)
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		var v Dependency
		if err := rows.Scan(&v.ClassOID, &v.ObjectOID, &v.ObjectSubID,
			&v.ObjectType, &v.ObjectSchema, &v.ObjectName, &v.ObjectIdentity,
			&v.ReferencedClassOID, &v.ReferencedObjectOID, &v.ReferencedSubID,
			&v.ReferencedType, &v.ReferencedSchema, &v.ReferencedName, &v.ReferencedIdentity, &v.Type); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Rules(ctx context.Context, q Querier) ([]Rule, error) {
	rows, err := q.Query(ctx, `select schemaname,tablename,rulename,definition from pg_catalog.pg_rules where schemaname='public' order by tablename,rulename`)
	if err != nil {
		return nil, fmt.Errorf("reading rule facts: %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var v Rule
		if err := rows.Scan(&v.Schema, &v.Relation, &v.Name, &v.Definition); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Publications(ctx context.Context, q Querier) ([]Publication, error) {
	rows, err := q.Query(ctx, `select p.pubname,r.rolname,p.puballtables,p.pubinsert,p.pubupdate,p.pubdelete,p.pubtruncate,p.pubviaroot from pg_catalog.pg_publication p join pg_catalog.pg_roles r on r.oid=p.pubowner order by p.pubname`)
	if err != nil {
		return nil, fmt.Errorf("reading publication facts: %w", err)
	}
	defer rows.Close()
	var out []Publication
	for rows.Next() {
		var v Publication
		if err := rows.Scan(&v.Name, &v.Owner, &v.AllTables, &v.Insert, &v.Update, &v.Delete, &v.Truncate, &v.ViaRoot); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func Memberships(ctx context.Context, q Querier) ([]Membership, error) {
	rows, err := q.Query(ctx, `select role.rolname,member.rolname,grantor.rolname,m.admin_option from pg_catalog.pg_auth_members m join pg_catalog.pg_roles role on role.oid=m.roleid join pg_catalog.pg_roles member on member.oid=m.member join pg_catalog.pg_roles grantor on grantor.oid=m.grantor order by role.rolname,member.rolname`)
	if err != nil {
		return nil, fmt.Errorf("reading membership facts: %w", err)
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var v Membership
		if err := rows.Scan(&v.Role, &v.Member, &v.Grantor, &v.AdminOption); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func RoleSettings(ctx context.Context, q Querier) ([]RoleSetting, error) {
	rows, err := q.Query(ctx, `select coalesce(d.datname,''),coalesce(r.rolname,''),s.setconfig from pg_catalog.pg_db_role_setting s left join pg_catalog.pg_database d on d.oid=s.setdatabase left join pg_catalog.pg_roles r on r.oid=s.setrole order by 1,2`)
	if err != nil {
		return nil, fmt.Errorf("reading role setting facts: %w", err)
	}
	defer rows.Close()
	var out []RoleSetting
	for rows.Next() {
		var v RoleSetting
		if err := rows.Scan(&v.Database, &v.Role, &v.Values); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func DefaultACLs(ctx context.Context, q Querier) ([]DefaultACL, error) {
	rows, err := q.Query(ctx, `select owner.rolname,coalesce(n.nspname,''),d.defaclobjtype::text,grantor.rolname,coalesce(grantee.rolname,'PUBLIC'),a.privilege_type,a.is_grantable
from pg_catalog.pg_default_acl d join pg_catalog.pg_roles owner on owner.oid=d.defaclrole left join pg_catalog.pg_namespace n on n.oid=d.defaclnamespace
cross join lateral pg_catalog.aclexplode(d.defaclacl) a join pg_catalog.pg_roles grantor on grantor.oid=a.grantor left join pg_catalog.pg_roles grantee on grantee.oid=a.grantee
order by owner.rolname,n.nspname,d.defaclobjtype,a.grantee,a.privilege_type`)
	if err != nil {
		return nil, fmt.Errorf("reading default ACL facts: %w", err)
	}
	defer rows.Close()
	var out []DefaultACL
	var current *DefaultACL
	var key string
	for rows.Next() {
		var owner, schema, objectType string
		var acl ACL
		if err := rows.Scan(&owner, &schema, &objectType, &acl.Grantor, &acl.Grantee, &acl.Privilege, &acl.Grantable); err != nil {
			return nil, err
		}
		nextKey := owner + "\x00" + schema + "\x00" + objectType
		if current == nil || key != nextKey {
			out = append(out, DefaultACL{Owner: owner, Schema: schema, ObjectType: objectType})
			current = &out[len(out)-1]
			key = nextKey
		}
		current.ACL = append(current.ACL, acl)
	}
	return out, rows.Err()
}
