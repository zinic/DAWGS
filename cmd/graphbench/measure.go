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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/specterops/dawgs/graph"
)

func countCypherRows(tx graph.Transaction, cypher string, params map[string]any) (int64, string, error) {
	result := tx.Query(cypher, params)
	defer result.Close()

	var rowCount int64
	var rowDigests []string
	for result.Next() {
		rowCount++
		encoded, err := json.Marshal(result.Values())
		if err != nil {
			return 0, "", fmt.Errorf("encode result row %d for logical digest: %w", rowCount, err)
		}
		digest := sha256.Sum256(encoded)
		rowDigests = append(rowDigests, hex.EncodeToString(digest[:]))
	}
	if err := result.Error(); err != nil {
		return 0, "", err
	}
	sort.Strings(rowDigests)
	logicalHash := sha256.New()
	for _, digest := range rowDigests {
		_, _ = logicalHash.Write([]byte(digest))
		_, _ = logicalHash.Write([]byte{'\n'})
	}
	return rowCount, hex.EncodeToString(logicalHash.Sum(nil)), nil
}

func measureCypher(ctx context.Context, db graph.Database, cypher string, params map[string]any, iterations int) (int64, string, DurationStats, error) {
	if iterations < 1 {
		return 0, "", DurationStats{}, fmt.Errorf("iterations must be at least 1")
	}

	var warmupRows int64
	var warmupDigest string
	if err := db.ReadTransaction(ctx, func(tx graph.Transaction) error {
		var err error
		warmupRows, warmupDigest, err = countCypherRows(tx, cypher, params)
		return err
	}); err != nil {
		return 0, "", DurationStats{}, err
	}

	durations := make([]time.Duration, iterations)
	for idx := range iterations {
		start := time.Now()
		if err := db.ReadTransaction(ctx, func(tx graph.Transaction) error {
			rows, digest, err := countCypherRows(tx, cypher, params)
			if err != nil {
				return err
			}
			if rows != warmupRows || digest != warmupDigest {
				return fmt.Errorf("timed result differs from warmup: rows=%d digest=%s, want rows=%d digest=%s", rows, digest, warmupRows, warmupDigest)
			}
			return nil
		}); err != nil {
			return 0, "", DurationStats{}, err
		}
		durations[idx] = time.Since(start)
	}

	stats, err := computeDurationStats(durations)
	if err != nil {
		return 0, "", DurationStats{}, err
	}

	return warmupRows, warmupDigest, stats, nil
}
