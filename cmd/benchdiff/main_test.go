package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBenchfmtAndCompare(t *testing.T) {
	metrics := parseBenchfmt([]byte("goos: linux\nBenchmarkOne-8 1 100 ns/op 20 B/op\nBenchmarkOne-8 1 120 ns/op 22 B/op\n"))
	require.Equal(t, []float64{100, 120}, metrics["BenchmarkOne-8 ns/op"])
	require.Equal(t, []float64{20, 22}, metrics["BenchmarkOne-8 B/op"])

	base := envelope{Version: envelopeVersion, Revision: "base", Kind: "unit", FixtureSHA256: "f", AdapterSHA256: "a",
		Parameters: parameters{Bench: ".", Count: 2, BenchTime: "1s"}, Seed: 1, Schedule: "sequential_base_then_target",
		Environment: environment{GOOS: "linux"}, Cases: []string{"BenchmarkOne-8 ns/op"}, Metrics: map[string][]float64{"BenchmarkOne-8 ns/op": {100, 120}}}
	target := base
	target.Revision = "target"
	target.Metrics = map[string][]float64{"BenchmarkOne-8 ns/op": {110, 132}}
	require.NoError(t, compatible(base, target))
	result := compare(base, target)
	require.InDelta(t, 10, result.Comparisons[0].Regression, 0.001)
}

func TestCompatibilityRejectsEveryComparisonDimension(t *testing.T) {
	base := envelope{Version: envelopeVersion, Kind: "unit", Driver: "pg", FixtureSHA256: "fixture", AdapterSHA256: "adapter",
		Parameters: parameters{Bench: ".", Count: 2, BenchTime: "1s"}, Seed: 1, Schedule: "schedule",
		Environment: environment{GOOS: "linux"}, Cases: []string{"case ns/op"}}
	tests := map[string]func(*envelope){
		"fixture":         func(value *envelope) { value.FixtureSHA256 = "different" },
		"adapter":         func(value *envelope) { value.AdapterSHA256 = "different" },
		"parameters":      func(value *envelope) { value.Parameters.Count++ },
		"seed":            func(value *envelope) { value.Seed++ },
		"schedule":        func(value *envelope) { value.Schedule = "different" },
		"environment":     func(value *envelope) { value.Environment.GOOS = "different" },
		"cases and units": func(value *envelope) { value.Cases = []string{"case B/op"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			target := base
			mutate(&target)
			require.Error(t, compatible(base, target))
		})
	}
}

func TestParsePercent(t *testing.T) {
	value, err := parsePercent("10%")
	require.NoError(t, err)
	require.Equal(t, 10.0, value)
	_, err = parsePercent("10")
	require.Error(t, err)
}
