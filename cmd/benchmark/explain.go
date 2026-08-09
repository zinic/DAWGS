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
	"encoding/json"
	"errors"

	"github.com/specterops/dawgs/cypher/frontend"
	"github.com/specterops/dawgs/cypher/models/pgsql"
	"github.com/specterops/dawgs/cypher/models/pgsql/translate"
	"github.com/specterops/dawgs/drivers/pg"
	"github.com/specterops/dawgs/graph"
)

// ExplainResult captures PostgreSQL-specific plan diagnostics for a scenario.
type ExplainResult struct {
	SQL          string                        `json:"sql"`
	Plan         []string                      `json:"plan"`
	PlanJSON     json.RawMessage               `json:"plan_json"`
	Leaves       pg.ExplainLeafSummary         `json:"leaves"`
	Metrics      pg.ExplainMetrics             `json:"metrics"`
	Optimization translate.OptimizationSummary `json:"optimization"`
}

func newPostgresExplainer(kindMapper pgsql.KindMapper, graphID int32) ExplainFunc {
	return func(ctx context.Context, tx graph.Transaction, cypherQuery string) (*ExplainResult, error) {
		regularQuery, err := frontend.ParseCypher(frontend.NewContext(), cypherQuery)
		if err != nil {
			return nil, err
		}
		if err := frontend.RequireReadOnly(regularQuery); err != nil {
			return nil, err
		}

		translation, err := translate.Translate(ctx, regularQuery, kindMapper, nil, graphID)
		if err != nil {
			return nil, err
		}
		if translation.HasMutations {
			return nil, frontend.ErrMutationNotAllowed
		}

		sqlQuery, err := translate.Translated(translation)
		if err != nil {
			return nil, err
		}

		inventoryResult := tx.Raw(`select graph_id,kind_id from public.dawgs_edge_kind_leaf
where lifecycle_state='active' order by graph_id,kind_id`, nil)
		var inventory []pg.LeafReference
		for inventoryResult.Next() {
			values := inventoryResult.Values()
			if len(values) != 2 {
				continue
			}
			graphValue, graphOK := benchmarkInteger(values[0])
			kindValue, kindOK := benchmarkInteger(values[1])
			if graphOK && kindOK {
				inventory = append(inventory, pg.LeafReference{GraphID: int32(graphValue), KindID: int16(kindValue)})
			}
		}
		if err := inventoryResult.Error(); err != nil {
			inventoryResult.Close()
			return nil, err
		}
		inventoryResult.Close()
		result := tx.Raw("EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON) "+sqlQuery, translation.Parameters)
		defer result.Close()

		var structured pg.StructuredExplain
		for result.Next() {
			values := result.Values()
			if len(values) == 0 {
				continue
			}

			structured, err = pg.DecodeStructuredExplain(values[0], inventory)
			if err != nil {
				return nil, err
			}
			break
		}

		if err := result.Error(); err != nil {
			return nil, err
		}
		if len(structured.Plan) == 0 {
			return nil, errors.New("PostgreSQL EXPLAIN returned no JSON plan")
		}

		return &ExplainResult{
			SQL:          sqlQuery,
			Plan:         []string{string(structured.Plan)},
			PlanJSON:     structured.Plan,
			Leaves:       structured.Leaves,
			Metrics:      structured.Metrics,
			Optimization: translation.Optimization,
		}, nil
	}
}

func benchmarkInteger(value any) (int64, bool) {
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
