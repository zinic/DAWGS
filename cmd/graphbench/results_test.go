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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/specterops/dawgs/drivers/pg"
	"github.com/stretchr/testify/require"
)

func TestComputeDurationStatsRejectsEmptyDurations(t *testing.T) {
	_, err := computeDurationStats(nil)

	require.ErrorContains(t, err, "at least one duration")
}

func TestApplyBaselineRequiresCompatibleCasesAndPostgresFacts(t *testing.T) {
	rowCount := int64(1)
	base := CaseResult{Source: "case.json", Dataset: "base", Name: "case", Category: "read",
		ExecutionMode: ModePostgresSQL, Cypher: "match (n) return n", Params: map[string]any{"n": float64(1)},
		ExpectedRowCount: &rowCount, LogicalDigest: "digest", Stats: DurationStats{Iterations: 3, Median: time.Millisecond},
		PostgresLayout: &pg.LayoutHealth{LayoutVersion: 2, TopologyFingerprint: "topology", GraphParentCount: 1,
			KindCount: 2, LeafCount: 2, ServerVersionNum: 180000}, PostgresStorage: &PostgresStorage{LeafCount: 2}}
	path := filepath.Join(t.TempDir(), "baseline.jsonl")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(file).Encode(base))
	require.NoError(t, file.Close())

	current := []CaseResult{base}
	current[0].Stats.Median = 2 * time.Millisecond
	require.NoError(t, applyBaseline(path, current))
	require.Equal(t, 2.0, current[0].Baseline.Ratio)

	current = []CaseResult{base}
	current[0].PostgresLayout = &pg.LayoutHealth{LayoutVersion: 2, TopologyFingerprint: "different", GraphParentCount: 1,
		KindCount: 2, LeafCount: 2, ServerVersionNum: 180000}
	require.ErrorContains(t, applyBaseline(path, current), "topology")

	current = []CaseResult{base}
	current[0].LogicalDigest = "different"
	require.ErrorContains(t, applyBaseline(path, current), "logical result")
}

func TestComputeDurationStatsCopiesAndSortsDurations(t *testing.T) {
	durations := []time.Duration{
		30 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
	}

	stats, err := computeDurationStats(durations)

	require.NoError(t, err)
	require.Equal(t, 3, stats.Iterations)
	require.Equal(t, 20*time.Millisecond, stats.Median)
	require.Equal(t, 30*time.Millisecond, stats.P95)
	require.Equal(t, 30*time.Millisecond, stats.Max)
	require.Equal(t, 30*time.Millisecond, durations[0])
	require.Equal(t, 10*time.Millisecond, durations[1])
	require.Equal(t, 20*time.Millisecond, durations[2])
}

func TestComputeDurationStatsUsesNearestRankP95(t *testing.T) {
	durations := make([]time.Duration, 20)
	for idx := range durations {
		durations[idx] = time.Duration(idx+1) * time.Millisecond
	}

	stats, err := computeDurationStats(durations)

	require.NoError(t, err)
	require.Equal(t, 19*time.Millisecond, stats.P95)
	require.Equal(t, 20*time.Millisecond, stats.Max)
}
