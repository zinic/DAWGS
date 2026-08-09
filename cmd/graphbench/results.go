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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/specterops/dawgs/cypher/models/pgsql/translate"
	"github.com/specterops/dawgs/drivers/pg"
)

const (
	StatusOK             = "ok"
	StatusRowMismatch    = "row_mismatch"
	StatusError          = "error"
	StatusNotImplemented = "not_implemented"
)

type DurationStats struct {
	Iterations int           `json:"iterations"`
	Median     time.Duration `json:"median"`
	P95        time.Duration `json:"p95"`
	Max        time.Duration `json:"max"`
}

type PostgresPlanMetrics struct {
	PlanningMS  *float64 `json:"planning_ms,omitempty"`
	ExecutionMS *float64 `json:"execution_ms,omitempty"`
	Buffers     Buffers  `json:"buffers,omitempty"`
}

type Buffers struct {
	SharedHit     int64 `json:"shared_hit,omitempty"`
	SharedRead    int64 `json:"shared_read,omitempty"`
	SharedDirtied int64 `json:"shared_dirtied,omitempty"`
	TempRead      int64 `json:"temp_read,omitempty"`
	TempWritten   int64 `json:"temp_written,omitempty"`
}

type PostgresStorage struct {
	LeafCount  int64     `json:"leaf_count"`
	HeapBytes  int64     `json:"heap_bytes"`
	IndexBytes int64     `json:"index_bytes"`
	ToastBytes int64     `json:"toast_bytes"`
	TotalBytes int64     `json:"total_bytes"`
	AnalyzedAt time.Time `json:"analyzed_at"`
}

type ClientPhaseMetrics struct {
	ParseNS     int64 `json:"parse_ns"`
	TranslateNS int64 `json:"translate_ns"`
	RenderNS    int64 `json:"render_ns"`
}

type CaseResult struct {
	Source           string                         `json:"source"`
	Dataset          string                         `json:"dataset"`
	Name             string                         `json:"name"`
	Category         string                         `json:"category"`
	ExecutionMode    ExecutionMode                  `json:"execution_mode"`
	Status           string                         `json:"status"`
	Cypher           string                         `json:"cypher"`
	Params           map[string]any                 `json:"params,omitempty"`
	NodeParams       map[string]string              `json:"node_params,omitempty"`
	ExpectedRowCount *int64                         `json:"expected_row_count,omitempty"`
	RowCount         int64                          `json:"row_count,omitempty"`
	LogicalDigest    string                         `json:"logical_digest,omitempty"`
	Stats            DurationStats                  `json:"stats,omitempty"`
	ClientPhases     *ClientPhaseMetrics            `json:"client_phases,omitempty"`
	SQL              string                         `json:"sql,omitempty"`
	PostgresPlan     []string                       `json:"postgres_plan,omitempty"`
	PostgresPlanJSON json.RawMessage                `json:"postgres_plan_json,omitempty"`
	PostgresMetrics  *PostgresPlanMetrics           `json:"postgres_metrics,omitempty"`
	PostgresLeaves   *pg.ExplainLeafSummary         `json:"postgres_leaves,omitempty"`
	PostgresLayout   *pg.LayoutHealth               `json:"postgres_layout,omitempty"`
	PostgresStorage  *PostgresStorage               `json:"postgres_storage,omitempty"`
	Neo4jPlan        *Neo4jPlanNode                 `json:"neo4j_plan,omitempty"`
	Neo4jOperators   []string                       `json:"neo4j_operators,omitempty"`
	Optimization     *translate.OptimizationSummary `json:"optimization,omitempty"`
	Baseline         *BaselineComparison            `json:"baseline,omitempty"`
	FallbackReason   string                         `json:"fallback_reason,omitempty"`
	Error            string                         `json:"error,omitempty"`
}

type BaselineComparison struct {
	BaselineMedian time.Duration `json:"baseline_median"`
	CurrentMedian  time.Duration `json:"current_median"`
	Change         time.Duration `json:"change"`
	Ratio          float64       `json:"ratio"`
}

func newCaseResult(testCase ScaleCase, mode ExecutionMode, params map[string]any) CaseResult {
	return CaseResult{
		Source:           testCase.Source,
		Dataset:          testCase.Dataset,
		Name:             testCase.Name,
		Category:         testCase.Category,
		ExecutionMode:    mode,
		Status:           StatusOK,
		Cypher:           testCase.Cypher,
		Params:           params,
		NodeParams:       testCase.NodeParams,
		ExpectedRowCount: testCase.Expected.RowCount,
	}
}

func computeDurationStats(durations []time.Duration) (DurationStats, error) {
	if len(durations) == 0 {
		return DurationStats{}, fmt.Errorf("duration stats require at least one duration")
	}

	sortedDurations := append([]time.Duration(nil), durations...)
	sort.Slice(sortedDurations, func(i, j int) bool {
		return sortedDurations[i] < sortedDurations[j]
	})

	n := len(sortedDurations)
	p95Index := (95*n+99)/100 - 1
	return DurationStats{
		Iterations: n,
		Median:     sortedDurations[n/2],
		P95:        sortedDurations[p95Index],
		Max:        sortedDurations[n-1],
	}, nil
}

func applyRowExpectation(result *CaseResult) {
	if result.ExpectedRowCount != nil && result.RowCount != *result.ExpectedRowCount {
		result.Status = StatusRowMismatch
		result.Error = fmt.Sprintf("expected %d rows, got %d", *result.ExpectedRowCount, result.RowCount)
	}
}

func writeJSONLFile(path string, records []CaseResult) (err error) {
	if path == "" {
		return writeJSONL(os.Stdout, records)
	}

	if err := ensureOutputDir(path); err != nil {
		return err
	}

	output, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	return writeJSONL(output, records)
}

func writeJSONL(w io.Writer, records []CaseResult) error {
	encoder := json.NewEncoder(w)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}

	return nil
}

func readJSONLFile(path string) ([]CaseResult, error) {
	input, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer input.Close()

	var (
		decoder = json.NewDecoder(input)
		records []CaseResult
	)

	for {
		var record CaseResult
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		records = append(records, record)
	}

	return records, nil
}

func ensureOutputDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}

	return os.MkdirAll(dir, 0o755)
}

func applyBaseline(path string, records []CaseResult) error {
	baseline, err := readJSONLFile(path)
	if err != nil {
		return err
	}

	byKey := make(map[string]CaseResult, len(baseline))
	for _, record := range baseline {
		key := resultKey(record.Dataset, record.Name, record.ExecutionMode)
		if _, duplicate := byKey[key]; duplicate {
			return fmt.Errorf("baseline contains duplicate case %q", key)
		}
		byKey[key] = record
	}
	if len(byKey) != len(records) {
		return fmt.Errorf("baseline case count %d differs from current case count %d", len(byKey), len(records))
	}

	for idx := range records {
		record := &records[idx]
		key := resultKey(record.Dataset, record.Name, record.ExecutionMode)
		previous, found := byKey[key]
		if !found {
			return fmt.Errorf("baseline is missing current case %q", key)
		}
		if err := compatibleBaselineCase(previous, *record); err != nil {
			return fmt.Errorf("baseline case %q is incompatible: %w", key, err)
		}
		if previous.Stats.Iterations == 0 || record.Stats.Iterations == 0 || previous.Stats.Median == 0 {
			continue
		}

		record.Baseline = &BaselineComparison{
			BaselineMedian: previous.Stats.Median,
			CurrentMedian:  record.Stats.Median,
			Change:         record.Stats.Median - previous.Stats.Median,
			Ratio:          float64(record.Stats.Median) / float64(previous.Stats.Median),
		}
	}

	return nil
}

func compatibleBaselineCase(previous, current CaseResult) error {
	if previous.Source != current.Source || previous.Category != current.Category || previous.Cypher != current.Cypher ||
		!canonicalJSONEqual(previous.Params, current.Params) || !canonicalJSONEqual(previous.NodeParams, current.NodeParams) ||
		!canonicalJSONEqual(previous.ExpectedRowCount, current.ExpectedRowCount) || previous.Stats.Iterations != current.Stats.Iterations {
		return fmt.Errorf("fixture source, query, parameters, expectation, or iteration schedule differs")
	}
	if previous.LogicalDigest != "" && current.LogicalDigest != "" && previous.LogicalDigest != current.LogicalDigest {
		return fmt.Errorf("exact logical result digest differs")
	}
	if (previous.PostgresLayout == nil) != (current.PostgresLayout == nil) {
		return fmt.Errorf("PostgreSQL layout metadata presence differs")
	}
	if previous.PostgresLayout != nil {
		left, right := previous.PostgresLayout, current.PostgresLayout
		if left.LayoutVersion != right.LayoutVersion || left.TopologyFingerprint != right.TopologyFingerprint ||
			left.GraphParentCount != right.GraphParentCount || left.KindCount != right.KindCount || left.LeafCount != right.LeafCount ||
			left.ServerVersionNum != right.ServerVersionNum {
			return fmt.Errorf("PostgreSQL layout, topology, or server facts differ")
		}
	}
	if (previous.PostgresStorage == nil) != (current.PostgresStorage == nil) ||
		(previous.PostgresStorage != nil && previous.PostgresStorage.LeafCount != current.PostgresStorage.LeafCount) {
		return fmt.Errorf("PostgreSQL storage topology differs")
	}
	return nil
}

func canonicalJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func resultKey(dataset, name string, mode ExecutionMode) string {
	return dataset + "\x00" + name + "\x00" + string(mode)
}
