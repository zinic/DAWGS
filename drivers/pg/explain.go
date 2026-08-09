package pg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
)

type LeafReference struct {
	GraphID int32 `json:"graph_id"`
	KindID  int16 `json:"kind_id"`
}

type ExplainBuffers struct {
	SharedHit     int64 `json:"shared_hit,omitempty"`
	SharedRead    int64 `json:"shared_read,omitempty"`
	SharedDirtied int64 `json:"shared_dirtied,omitempty"`
	SharedWritten int64 `json:"shared_written,omitempty"`
	TempRead      int64 `json:"temp_read,omitempty"`
	TempWritten   int64 `json:"temp_written,omitempty"`
}

type ExplainMetrics struct {
	PlanningMS  *float64       `json:"planning_ms,omitempty"`
	ExecutionMS *float64       `json:"execution_ms,omitempty"`
	Buffers     ExplainBuffers `json:"buffers,omitempty"`
}

type ExplainLeafSummary struct {
	All           []LeafReference `json:"all"`
	Planned       []LeafReference `json:"planned"`
	Removed       []LeafReference `json:"removed"`
	Executed      []LeafReference `json:"executed"`
	AllCount      int             `json:"all_count"`
	PlannedCount  int             `json:"planned_count"`
	RemovedCount  int             `json:"removed_count"`
	ExecutedCount int             `json:"executed_count"`
}

type StructuredExplain struct {
	Plan      json.RawMessage    `json:"plan"`
	Metrics   ExplainMetrics     `json:"metrics"`
	Leaves    ExplainLeafSummary `json:"leaves"`
	Operators []string           `json:"operators"`
}

var managedLeafPattern = regexp.MustCompile(`^edge_([1-9][0-9]*)_([1-9][0-9]*)$`)

func canonicalLeaves(values map[LeafReference]struct{}) []LeafReference {
	result := make([]LeafReference, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].GraphID != result[j].GraphID {
			return result[i].GraphID < result[j].GraphID
		}
		return result[i].KindID < result[j].KindID
	})
	return result
}

func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func leafFromRelation(value any) (LeafReference, bool) {
	name, ok := value.(string)
	if !ok {
		return LeafReference{}, false
	}
	match := managedLeafPattern.FindStringSubmatch(name)
	if match == nil {
		return LeafReference{}, false
	}
	graphID, graphErr := strconv.ParseInt(match[1], 10, 32)
	kindID, kindErr := strconv.ParseInt(match[2], 10, 16)
	if graphErr != nil || kindErr != nil {
		return LeafReference{}, false
	}
	return LeafReference{GraphID: int32(graphID), KindID: int16(kindID)}, true
}

func walkExplainPlan(node any, planned, executed map[LeafReference]struct{}, operators map[string]struct{}) {
	switch typed := node.(type) {
	case map[string]any:
		if operator, ok := typed["Node Type"].(string); ok && operator != "" {
			operators[operator] = struct{}{}
		}
		if leaf, ok := leafFromRelation(typed["Relation Name"]); ok {
			planned[leaf] = struct{}{}
			if loops, found := numeric(typed["Actual Loops"]); found && loops > 0 {
				executed[leaf] = struct{}{}
			}
		}
		for _, value := range typed {
			walkExplainPlan(value, planned, executed, operators)
		}
	case []any:
		for _, value := range typed {
			walkExplainPlan(value, planned, executed, operators)
		}
	}
}

func normalizeExplainJSON(value any) ([]byte, error) {
	switch typed := value.(type) {
	case []byte:
		return typed, nil
	case string:
		return []byte(typed), nil
	case json.RawMessage:
		return typed, nil
	default:
		return json.Marshal(value)
	}
}

// DecodeStructuredExplain decodes PostgreSQL FORMAT JSON output and combines
// it with the registry inventory taken in the same read transaction. Removed
// is the exact inventory complement of executed, covering both plan-time and
// execution-time partition pruning without relying on relation-name regexes in
// human-formatted plans.
func DecodeStructuredExplain(value any, inventory []LeafReference) (StructuredExplain, error) {
	content, err := normalizeExplainJSON(value)
	if err != nil {
		return StructuredExplain{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var documents []map[string]any
	if err := decoder.Decode(&documents); err != nil || len(documents) != 1 {
		if err == nil {
			err = fmt.Errorf("expected one PostgreSQL JSON plan document")
		}
		return StructuredExplain{}, fmt.Errorf("decoding PostgreSQL JSON plan: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected trailing JSON value")
		}
		return StructuredExplain{}, fmt.Errorf("decoding PostgreSQL JSON plan: %w", err)
	}
	document := documents[0]
	planned := map[LeafReference]struct{}{}
	executed := map[LeafReference]struct{}{}
	operators := map[string]struct{}{}
	walkExplainPlan(document["Plan"], planned, executed, operators)
	all := map[LeafReference]struct{}{}
	for _, leaf := range inventory {
		all[leaf] = struct{}{}
	}
	removed := map[LeafReference]struct{}{}
	for leaf := range all {
		if _, found := executed[leaf]; !found {
			removed[leaf] = struct{}{}
		}
	}
	metrics := ExplainMetrics{}
	if value, ok := numeric(document["Planning Time"]); ok {
		metrics.PlanningMS = &value
	}
	if value, ok := numeric(document["Execution Time"]); ok {
		metrics.ExecutionMS = &value
	}
	if root, ok := document["Plan"].(map[string]any); ok {
		for key, destination := range map[string]*int64{
			"Shared Hit Blocks": &metrics.Buffers.SharedHit, "Shared Read Blocks": &metrics.Buffers.SharedRead,
			"Shared Dirtied Blocks": &metrics.Buffers.SharedDirtied, "Shared Written Blocks": &metrics.Buffers.SharedWritten,
			"Temp Read Blocks": &metrics.Buffers.TempRead, "Temp Written Blocks": &metrics.Buffers.TempWritten,
		} {
			if value, ok := numeric(root[key]); ok {
				*destination = int64(value)
			}
		}
	}
	allValues := canonicalLeaves(all)
	plannedValues := canonicalLeaves(planned)
	removedValues := canonicalLeaves(removed)
	executedValues := canonicalLeaves(executed)
	operatorValues := make([]string, 0, len(operators))
	for operator := range operators {
		operatorValues = append(operatorValues, operator)
	}
	sort.Strings(operatorValues)
	return StructuredExplain{Plan: append(json.RawMessage(nil), content...), Metrics: metrics,
		Leaves: ExplainLeafSummary{All: allValues, Planned: plannedValues, Removed: removedValues,
			Executed: executedValues, AllCount: len(allValues), PlannedCount: len(plannedValues),
			RemovedCount: len(removedValues), ExecutedCount: len(executedValues)}, Operators: operatorValues}, nil
}
