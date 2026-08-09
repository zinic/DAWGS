-- Canonical V2 teardown: drop triggers
drop trigger if exists dawgs_refuse_graph_delete on public.graph;
drop function if exists public.dawgs_refuse_graph_delete;
drop trigger if exists dawgs_refuse_receipt_mutation on public.dawgs_schema_receipt;
drop function if exists public.dawgs_refuse_receipt_mutation;
drop trigger if exists dawgs_refuse_cleanup_scope_mutation on public.dawgs_legacy_cleanup_scope;
drop trigger if exists dawgs_refuse_cleanup_map_mutation on public.dawgs_legacy_cleanup_map;
drop trigger if exists dawgs_refuse_cleanup_completion_mutation on public.dawgs_legacy_cleanup_completion;
drop function if exists public.dawgs_refuse_cleanup_map_mutation;
drop function if exists public.dawgs_current_smoke_receipt_valid(text,text,text);
drop function if exists public.dawgs_current_cleanup_receipt_valid(text,text,text);
drop trigger if exists delete_node_edges on public.node;
drop function if exists public.delete_node_edges;
drop trigger if exists dawgs_10_claim_edge on public.edge;
drop trigger if exists dawgs_20_update_edge_claims on public.edge;
drop function if exists public.dawgs_claim_edge;
drop function if exists public.dawgs_update_edge_claims;
drop function if exists public.dawgs_wipe_graph();

-- Drop functions
drop aggregate if exists public.cypher_min(jsonb);
drop aggregate if exists public.cypher_max(jsonb);
drop function if exists public.query_perf;
drop function if exists public.lock_details;
drop function if exists public.table_sizes;
drop function if exists public.jsonb_to_text_array;
drop function if exists public.cypher_min_transition(jsonb, jsonb);
drop function if exists public.cypher_max_transition(jsonb, jsonb);
drop function if exists public.cypher_value_compare(jsonb, jsonb);
drop function if exists public.cypher_jsonb_type_rank(jsonb);
drop function if exists public.cypher_contains(text, text);
drop function if exists public.cypher_starts_with(text, text);
drop function if exists public.cypher_ends_with(text, text);
drop function if exists public.kind_name(smallint);
drop function if exists public.start_node(public.edgeComposite);
drop function if exists public.end_node(public.edgeComposite);
drop function if exists public.start_node(integer, public.edgeComposite);
drop function if exists public.end_node(integer, public.edgeComposite);
drop function if exists public.get_node;
drop function if exists public.node_prop;
drop function if exists public.kinds;
drop function if exists public.has_kind;
drop function if exists public.mt_get_root;
drop function if exists public.index_utilization;
drop function if exists public._format_asp_where_clause;
drop function if exists public._format_asp_query;
drop function if exists public.asp_harness;
drop function if exists public.create_traversal_filter_tables;
drop function if exists public.create_traversal_filter_tables(text, text, text);
drop function if exists public.create_traversal_filter_tables(text, text);
drop function if exists public.create_traversal_filter_tables(int8[], int8[]);
drop function if exists public.shortest_path_self_endpoint_error(int8, int8);
drop function if exists public.unidirectional_sp_harness(text, text, int4);
drop function if exists public.unidirectional_sp_harness(text, text, int4, int8);
drop function if exists public.unidirectional_sp_harness(text, text, int4, text, text);
drop function if exists public.unidirectional_sp_harness(text, text, int4, text, text, int8);
drop function if exists public.unidirectional_sp_harness(text, text, int4, int8[]);
drop function if exists public.unidirectional_sp_harness(text, text, int4, int8[], int8);
drop function if exists public.unidirectional_sp_harness(text, text, int4, int8[], int8[]);
drop function if exists public.unidirectional_sp_harness(text, text, int4, int8[], int8[], int8);
drop function if exists public.unidirectional_asp_harness(text, text, int4);
drop function if exists public.unidirectional_asp_harness(text, text, int4, text, text);
drop function if exists public.unidirectional_asp_harness(text, text, int4, int8[]);
drop function if exists public.unidirectional_asp_harness(text, text, int4, int8[], int8[]);
drop function if exists public.bidirectional_asp_harness(text, text, text, text, int4);
drop function if exists public.bidirectional_asp_harness(text, text, text, text, int4, text, text, text);
drop function if exists public.bidirectional_asp_harness(text, text, text, text, int4, text, text);
drop function if exists public.bidirectional_asp_harness(text, text, text, text, int4, int8[]);
drop function if exists public.bidirectional_asp_harness(text, text, text, text, int4, int8[], int8[]);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, int8);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, text, text, text, int8);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, text, text, text);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, text, text, int8);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, text, text);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, int8[], int8);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, int8[]);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, int8[], int8[], int8);
drop function if exists public.bidirectional_sp_harness(text, text, text, text, int4, int8[], int8[]);
drop function if exists public._bidirectional_sp_harness(text, text, text, text, int4, text, text, text, int8[], int8[], int8, bool);
drop function if exists public._bidirectional_sp_harness(text, text, text, text, int4, text, text, text, int8[], int8[], bool);
drop function if exists public._bidirectional_sp_harness(text, text, text, text, int4, text, text, int8[], int8[], bool);
drop function if exists public.create_bidirectional_shortest_path_tables;
drop function if exists public.create_bidirectional_pair_pathspace_indexes;
drop function if exists public._all_shortest_paths;
drop function if exists public.all_shortest_paths;
drop function if exists public.traversal_step;
drop function if exists public._format_traversal_continuation_termination;
drop function if exists public._format_traversal_query;
drop function if exists public._format_traversal_initial_query;
drop function if exists public.expand_traversal_step;
drop function if exists public.traverse;
drop function if exists public.ordered_edges_to_path(public.nodeComposite, public.edgeComposite[], public.nodeComposite[]);
drop function if exists public.edges_to_path;
drop function if exists public.traverse_paths;

-- Drop all tables in order of dependency.
drop table if exists public.edge_property_unique_value;
drop table if exists public.edge_property_constraint;
drop table if exists public.edge_identity;
drop table if exists public.dawgs_edge_kind_leaf;
drop table if exists public.dawgs_legacy_cleanup_map;
drop table if exists public.dawgs_legacy_cleanup_completion;
drop table if exists public.dawgs_restore_rebind_map;
drop table if exists public.dawgs_restore_rebind_scope;
drop table if exists public.dawgs_legacy_cleanup_scope;
drop table if exists public.dawgs_schema_migration_graph;
drop table if exists public.dawgs_schema_migration;
drop table if exists public.dawgs_schema_receipt;
drop table if exists public.dawgs_operation_confirmation;
drop table if exists public.dawgs_schema_layout;
drop table if exists public.node;
drop table if exists public.edge;
drop table if exists public.kind;
drop table if exists public.graph;

-- Remove custom types
do
$$
  begin
    drop type public.pathComposite;
  exception
    when undefined_object then null;
  end
$$;

do
$$
  begin
    drop type public.nodeComposite;
  exception
    when undefined_object then null;
  end
$$;

do
$$
  begin
    drop type public.edgeComposite;
  exception
    when undefined_object then null;
  end
$$;

do
$$
  begin
    drop type public._traversal_step;
  exception
    when undefined_object then null;
  end
$$;

-- Pull the tri-gram and intarray extensions.
drop
  extension if exists pg_trgm;
drop
  extension if exists intarray;
drop
  extension if exists pg_stat_statements;
