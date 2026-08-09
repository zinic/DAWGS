// Copyright 2026 Specter Ops, Inc.
//
// Licensed under the Apache License, Version 2.0
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs"
	"github.com/specterops/dawgs/cypher/frontend"
	"github.com/specterops/dawgs/cypher/models/pgsql/translate"
	"github.com/specterops/dawgs/drivers/pg"
	"github.com/specterops/dawgs/graph"
	"github.com/specterops/dawgs/opengraph"
	"github.com/specterops/dawgs/util/size"
)

type postgresSQLRunner struct {
	datasetDir            string
	db                    graph.Database
	pgDriver              *pg.Driver
	graphID               int32
	health                pg.LayoutHealth
	maintenanceConnection string
	storage               PostgresStorage
}

func newPostgresSQLRunner(ctx context.Context, datasetDir, connection, maintenanceConnection string, corpus ScaleCorpus) (*postgresSQLRunner, error) {
	if maintenanceConnection == "" {
		return nil, fmt.Errorf("PostgreSQL GraphBench requires a separate direct maintenance connection")
	}
	poolCfg, err := pgxpool.ParseConfig(connection)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: %w", err)
	}
	pool, err := pg.NewPool(poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}

	db, err := dawgs.Open(ctx, pg.DriverName, dawgs.Config{
		GraphQueryMemoryLimit: size.Gibibyte,
		ConnectionString:      connection,
		Pool:                  pool,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open PostgreSQL database: %w", err)
	}
	pgDriver, ok := db.(*pg.Driver)
	if !ok {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("expected *pg.Driver, got %T", db)
	}
	nodeKinds, edgeKinds, err := scanDatasetKinds(datasetDir, scaleCorpusDatasets(corpus))
	if err != nil {
		_ = db.Close(ctx)
		return nil, err
	}
	if err := db.AssertSchema(ctx, benchmarkSchema(nodeKinds, edgeKinds)); err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("assert PostgreSQL schema: %w", err)
	}
	health, err := pgDriver.Readiness(ctx)
	if err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("PostgreSQL readiness: %w", err)
	}
	maintenance, err := pgx.Connect(ctx, maintenanceConnection)
	if err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("open PostgreSQL maintenance connection: %w", err)
	}
	var direct, authorized, sameDatabase bool
	if err := maintenance.QueryRow(ctx, `select session_user=current_user,
(r.rolsuper or r.oid=d.datdba or not exists(
  select 1 from public.dawgs_edge_kind_leaf l
  where l.lifecycle_state='active' and not pg_catalog.has_table_privilege(
    r.rolname,pg_catalog.format('public.edge_%s_%s',l.graph_id,l.kind_id),'MAINTAIN'))),
d.oid=$1 from pg_catalog.pg_roles r cross join pg_catalog.pg_database d
where r.rolname=session_user and d.datname=pg_catalog.current_database()`, health.DatabaseOID).Scan(
		&direct, &authorized, &sameDatabase); err != nil || !direct || !authorized || !sameDatabase {
		maintenance.Close(ctx)
		_ = db.Close(ctx)
		if err != nil {
			return nil, fmt.Errorf("validate PostgreSQL maintenance connection: %w", err)
		}
		return nil, fmt.Errorf("PostgreSQL maintenance connection must be direct, same-database, and authorized for every leaf")
	}
	if err := maintenance.Close(ctx); err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("close PostgreSQL maintenance validation connection: %w", err)
	}

	defaultGraph, ok := pgDriver.DefaultGraph()
	if !ok {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("PostgreSQL default graph is not set")
	}

	return &postgresSQLRunner{
		datasetDir:            datasetDir,
		db:                    db,
		pgDriver:              pgDriver,
		graphID:               defaultGraph.ID,
		health:                health,
		maintenanceConnection: maintenanceConnection,
	}, nil
}

func (s *postgresSQLRunner) Close(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	return s.db.Close(ctx)
}

func (s *postgresSQLRunner) openMaintenance(ctx context.Context) (*pgx.Conn, error) {
	maintenance, err := pgx.Connect(ctx, s.maintenanceConnection)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL maintenance connection: %w", err)
	}
	return maintenance, nil
}

func (s *postgresSQLRunner) Run(ctx context.Context, iterations int, corpus ScaleCorpus) ([]CaseResult, error) {
	var (
		records        []CaseResult
		casesByDataset = scaleCasesByDataset(corpus)
	)

	for _, datasetName := range scaleCorpusDatasets(corpus) {
		if err := clearGraph(ctx, s.db); err != nil {
			return nil, fmt.Errorf("clear graph for %s: %w", datasetName, err)
		}
		if err := s.verifyReset(ctx); err != nil {
			return nil, fmt.Errorf("verify graph reset for %s: %w", datasetName, err)
		}

		idMap, err := loadDataset(ctx, s.db, s.datasetDir, datasetName)
		if err != nil {
			return nil, err
		}
		if err := s.vacuumAnalyzeAndMeasure(ctx); err != nil {
			return nil, fmt.Errorf("prepare PostgreSQL leaves for %s: %w", datasetName, err)
		}

		for _, testCase := range casesByDataset[datasetName] {
			if !testCase.Supports(ModePostgresSQL) {
				continue
			}

			record := s.runCase(ctx, iterations, testCase, idMap)
			records = append(records, record)
		}
	}

	return records, nil
}

func (s *postgresSQLRunner) runCase(ctx context.Context, iterations int, testCase ScaleCase, idMap opengraph.IDMap) CaseResult {
	params, err := resolveCaseParams(testCase, idMap)
	record := newCaseResult(testCase, ModePostgresSQL, params)
	if err != nil {
		record.Status = StatusError
		record.Error = err.Error()
		return record
	}
	phases := &ClientPhaseMetrics{}
	phaseStarted := time.Now()
	regularQuery, err := frontend.ParseCypher(frontend.NewContext(), testCase.Cypher)
	phases.ParseNS = time.Since(phaseStarted).Nanoseconds()
	if err == nil {
		err = frontend.RequireReadOnly(regularQuery)
	}
	var translation translate.Result
	if err == nil {
		phaseStarted = time.Now()
		translation, err = translate.Translate(ctx, regularQuery, s.pgDriver.KindMapper(), params, s.graphID)
		phases.TranslateNS = time.Since(phaseStarted).Nanoseconds()
	}
	if err == nil && translation.HasMutations {
		err = frontend.ErrMutationNotAllowed
	}
	if err != nil {
		record.Status = StatusError
		record.Error = err.Error()
		return record
	}
	phaseStarted = time.Now()
	if _, err := translate.Translated(translation); err != nil {
		record.Status = StatusError
		record.Error = err.Error()
		return record
	}
	phases.RenderNS = time.Since(phaseStarted).Nanoseconds()
	record.ClientPhases = phases

	rowCount, logicalDigest, stats, err := measureCypher(ctx, s.db, testCase.Cypher, params, iterations)
	if err != nil {
		record.Status = StatusError
		record.Error = err.Error()
		return record
	}

	record.RowCount = rowCount
	record.LogicalDigest = logicalDigest
	record.Stats = stats
	applyRowExpectation(&record)

	explain, err := s.explain(ctx, translation)
	if err != nil {
		if record.Status == StatusOK {
			record.Status = StatusError
			record.Error = err.Error()
		}
		return record
	}

	record.SQL = explain.SQL
	record.PostgresPlan = explain.Plan
	record.PostgresPlanJSON = explain.Structured.Plan
	record.PostgresMetrics = &explain.Metrics
	record.PostgresLeaves = &explain.Structured.Leaves
	record.PostgresLayout = &s.health
	storage := s.storage
	record.PostgresStorage = &storage
	record.Optimization = &explain.Optimization
	return record
}

func (s *postgresSQLRunner) verifyReset(ctx context.Context) error {
	maintenance, err := s.openMaintenance(ctx)
	if err != nil {
		return err
	}
	defer maintenance.Close(ctx)
	var edges, identities, propertyClaims int64
	if err := maintenance.QueryRow(ctx, `select
(select count(*) from public.edge),
(select count(*) from public.edge_identity),
(select count(*) from public.edge_property_unique_value)`).Scan(&edges, &identities, &propertyClaims); err != nil {
		return err
	}
	if edges != 0 || identities != 0 || propertyClaims != 0 {
		return fmt.Errorf("reset left edges=%d identity_claims=%d property_claims=%d", edges, identities, propertyClaims)
	}
	return nil
}

func (s *postgresSQLRunner) vacuumAnalyzeAndMeasure(ctx context.Context) error {
	maintenance, err := s.openMaintenance(ctx)
	if err != nil {
		return err
	}
	defer maintenance.Close(ctx)
	rows, err := maintenance.Query(ctx, `select graph_id,kind_id from public.dawgs_edge_kind_leaf
where lifecycle_state='active' order by graph_id,kind_id`)
	if err != nil {
		return err
	}
	var leaves []pg.LeafReference
	for rows.Next() {
		var leaf pg.LeafReference
		if err := rows.Scan(&leaf.GraphID, &leaf.KindID); err != nil {
			rows.Close()
			return err
		}
		leaves = append(leaves, leaf)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, leaf := range leaves {
		relation := pgx.Identifier{"public", fmt.Sprintf("edge_%d_%d", leaf.GraphID, leaf.KindID)}.Sanitize()
		if _, err := maintenance.Exec(ctx, "VACUUM (ANALYZE) "+relation); err != nil {
			return err
		}
	}
	var analyzedAt pgtype.Timestamptz
	storage := PostgresStorage{LeafCount: int64(len(leaves))}
	if err := maintenance.QueryRow(ctx, `with leaves as (
  select c.oid,c.reltoastrelid from public.dawgs_edge_kind_leaf l
  join pg_catalog.pg_class c on c.relname=pg_catalog.format('edge_%s_%s',l.graph_id,l.kind_id)
  join pg_catalog.pg_namespace n on n.oid=c.relnamespace and n.nspname='public'
  where l.lifecycle_state='active'
), sizes as (
  select coalesce(sum(pg_catalog.pg_relation_size(oid)),0) heap,
         coalesce(sum(pg_catalog.pg_indexes_size(oid)),0) indexes,
         coalesce(sum(case when reltoastrelid=0 then 0 else pg_catalog.pg_total_relation_size(reltoastrelid) end),0) toast
  from leaves
)
select heap,indexes,toast,heap+indexes+toast,
  (select min(greatest(coalesce(s.last_analyze,'epoch'::timestamptz),coalesce(s.last_autoanalyze,'epoch'::timestamptz)))
   from leaves l join pg_catalog.pg_stat_all_tables s on s.relid=l.oid)
from sizes`).Scan(&storage.HeapBytes, &storage.IndexBytes, &storage.ToastBytes, &storage.TotalBytes, &analyzedAt); err != nil {
		return err
	}
	if len(leaves) > 0 && (!analyzedAt.Valid || analyzedAt.Time.IsZero()) {
		return fmt.Errorf("one or more active leaves have no analyze timestamp")
	}
	if analyzedAt.Valid {
		storage.AnalyzedAt = analyzedAt.Time.UTC()
	}
	s.storage = storage
	return nil
}

type postgresExplain struct {
	SQL          string
	Plan         []string
	Metrics      PostgresPlanMetrics
	Structured   pg.StructuredExplain
	Optimization translate.OptimizationSummary
}

func (s *postgresSQLRunner) explain(ctx context.Context, translation translate.Result) (postgresExplain, error) {
	if translation.HasMutations {
		return postgresExplain{}, frontend.ErrMutationNotAllowed
	}
	sqlQuery, err := translate.Translated(translation)
	if err != nil {
		return postgresExplain{}, err
	}

	var structured pg.StructuredExplain
	if err := s.db.ReadTransaction(ctx, func(tx graph.Transaction) error {
		inventoryResult := tx.Raw(`select graph_id,kind_id from public.dawgs_edge_kind_leaf
where lifecycle_state='active' order by graph_id,kind_id`, nil)
		var inventory []pg.LeafReference
		for inventoryResult.Next() {
			values := inventoryResult.Values()
			if len(values) != 2 {
				continue
			}
			graphID, graphOK := postgresInteger(values[0])
			kindID, kindOK := postgresInteger(values[1])
			if graphOK && kindOK {
				inventory = append(inventory, pg.LeafReference{GraphID: int32(graphID), KindID: int16(kindID)})
			}
		}
		if err := inventoryResult.Error(); err != nil {
			inventoryResult.Close()
			return err
		}
		inventoryResult.Close()
		result := tx.Raw("EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON) "+sqlQuery, translation.Parameters)
		defer result.Close()

		for result.Next() {
			values := result.Values()
			if len(values) == 0 {
				continue
			}
			var err error
			structured, err = pg.DecodeStructuredExplain(values[0], inventory)
			return err
		}
		if err := result.Error(); err != nil {
			return err
		}
		if len(structured.Plan) == 0 {
			return fmt.Errorf("PostgreSQL EXPLAIN returned no JSON plan")
		}
		return nil
	}); err != nil {
		return postgresExplain{}, err
	}

	return postgresExplain{
		SQL:          sqlQuery,
		Plan:         []string{string(structured.Plan)},
		Metrics:      structuredPlanMetrics(structured.Metrics),
		Structured:   structured,
		Optimization: translation.Optimization,
	}, nil
}

func postgresInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}

func structuredPlanMetrics(metrics pg.ExplainMetrics) PostgresPlanMetrics {
	return PostgresPlanMetrics{PlanningMS: metrics.PlanningMS, ExecutionMS: metrics.ExecutionMS,
		Buffers: Buffers{SharedHit: metrics.Buffers.SharedHit, SharedRead: metrics.Buffers.SharedRead,
			SharedDirtied: metrics.Buffers.SharedDirtied, TempRead: metrics.Buffers.TempRead,
			TempWritten: metrics.Buffers.TempWritten}}
}

var (
	postgresPlanningPattern  = regexp.MustCompile(`Planning Time: ([0-9.]+) ms`)
	postgresExecutionPattern = regexp.MustCompile(`Execution Time: ([0-9.]+) ms`)
	postgresBufferPattern    = regexp.MustCompile(`(?:(shared|temp) )?(hit|read|dirtied|written)=([0-9]+)`)
)

func parsePostgresPlanMetrics(plan []string) PostgresPlanMetrics {
	var metrics PostgresPlanMetrics
	for _, line := range plan {
		if metrics.PlanningMS == nil {
			if match := postgresPlanningPattern.FindStringSubmatch(line); match != nil {
				if parsed, err := strconv.ParseFloat(match[1], 64); err == nil {
					metrics.PlanningMS = &parsed
				}
			}
		}

		if metrics.ExecutionMS == nil {
			if match := postgresExecutionPattern.FindStringSubmatch(line); match != nil {
				if parsed, err := strconv.ParseFloat(match[1], 64); err == nil {
					metrics.ExecutionMS = &parsed
				}
			}
		}

		if strings.Contains(line, "Buffers:") && metrics.Buffers == (Buffers{}) {
			metrics.Buffers = parsePostgresBuffers(line)
		}
	}

	return metrics
}

func parsePostgresBuffers(line string) Buffers {
	var (
		buffers     Buffers
		bufferScope string
	)

	for _, match := range postgresBufferPattern.FindAllStringSubmatch(line, -1) {
		value, err := strconv.ParseInt(match[3], 10, 64)
		if err != nil {
			continue
		}

		if match[1] != "" {
			bufferScope = match[1]
		}

		switch bufferScope + "_" + match[2] {
		case "shared_hit":
			buffers.SharedHit = value
		case "shared_read":
			buffers.SharedRead = value
		case "shared_dirtied":
			buffers.SharedDirtied = value
		case "temp_read":
			buffers.TempRead = value
		case "temp_written":
			buffers.TempWritten = value
		}
	}

	return buffers
}
