package main

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const envelopeVersion = "dawgs-benchdiff-v1"

type envelope struct {
	Version       string               `json:"version"`
	Revision      string               `json:"revision"`
	Kind          string               `json:"kind"`
	Driver        string               `json:"driver,omitempty"`
	FixtureSHA256 string               `json:"fixture_sha256"`
	AdapterSHA256 string               `json:"adapter_sha256"`
	Parameters    parameters           `json:"parameters"`
	Seed          int64                `json:"seed"`
	Schedule      string               `json:"schedule"`
	Environment   environment          `json:"environment"`
	Cases         []string             `json:"cases"`
	Metrics       map[string][]float64 `json:"metrics"`
}

type parameters struct {
	Bench     string `json:"bench"`
	Count     int    `json:"count"`
	BenchTime string `json:"benchtime"`
}

type environment struct {
	GOOS       string              `json:"goos"`
	GOARCH     string              `json:"goarch"`
	GoVersion  string              `json:"go_version"`
	CPUCount   int                 `json:"cpu_count"`
	GOMAXPROCS int                 `json:"gomaxprocs"`
	Postgres   postgresEnvironment `json:"postgres,omitempty"`
}

type postgresEnvironment struct {
	ServerVersionNum       int    `json:"server_version_num,omitempty"`
	BlockSize              string `json:"block_size,omitempty"`
	MaxConnections         string `json:"max_connections,omitempty"`
	MaxLocksPerTransaction string `json:"max_locks_per_transaction,omitempty"`
	SharedBuffers          string `json:"shared_buffers,omitempty"`
	WorkMem                string `json:"work_mem,omitempty"`
	EffectiveCacheSize     string `json:"effective_cache_size,omitempty"`
	ServerEncoding         string `json:"server_encoding,omitempty"`
	Collation              string `json:"collation,omitempty"`
	Extensions             string `json:"extensions,omitempty"`
}

type comparison struct {
	Case         string  `json:"case"`
	BaseMedian   float64 `json:"base_median"`
	TargetMedian float64 `json:"target_median"`
	Ratio        float64 `json:"ratio"`
	Regression   float64 `json:"regression_percent"`
}

type report struct {
	Version        string       `json:"version"`
	BaseRevision   string       `json:"base_revision"`
	TargetRevision string       `json:"target_revision"`
	Comparisons    []comparison `json:"comparisons"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("benchdiff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baseRef := flags.String("base", "main", "committed base Git revision")
	targetRef := flags.String("target", "HEAD", "committed target Git revision")
	kind := flags.String("kind", "unit", "benchmark kind: unit, integration, or all")
	driver := flags.String("driver", "pg", "integration driver")
	bench := flags.String("bench", ".", "Go benchmark regular expression")
	count := flags.Int("bench-count", 10, "sample count")
	benchTime := flags.String("benchtime", "1s", "Go benchmark duration")
	outputDir := flags.String("output-dir", ".bench/runs", "retained output directory")
	failRegression := flags.String("fail-regression", "", "fail threshold such as 10%")
	seed := flags.Int64("seed", 1, "fixed benchmark seed recorded in the envelope")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *kind != "unit" && *kind != "integration" && *kind != "all" {
		return fmt.Errorf("--kind must be unit, integration, or all")
	}
	if *count < 1 || strings.TrimSpace(*benchTime) == "" {
		return fmt.Errorf("--bench-count must be positive and --benchtime is required")
	}
	threshold, err := parsePercent(*failRegression)
	if err != nil {
		return err
	}
	if (*kind == "integration" || *kind == "all") && os.Getenv("DAWGS_BENCHMARK_DISPOSABLE") != "1" {
		return fmt.Errorf("integration comparison requires DAWGS_BENCHMARK_DISPOSABLE=1")
	}

	baseRevision, err := resolveRevision(*baseRef)
	if err != nil {
		return err
	}
	targetRevision, err := resolveRevision(*targetRef)
	if err != nil {
		return err
	}
	tempRoot, err := os.MkdirTemp("", "dawgs-benchdiff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)
	baseDir := filepath.Join(tempRoot, "base")
	targetDir := filepath.Join(tempRoot, "target")
	if err := exportRevision(baseRevision, baseDir); err != nil {
		return err
	}
	if err := exportRevision(targetRevision, targetDir); err != nil {
		return err
	}
	params := parameters{Bench: *bench, Count: *count, BenchTime: *benchTime}
	base, err := capture(baseDir, baseRevision, *kind, *driver, params, *seed, true, stderr)
	if err != nil {
		return err
	}
	target, err := capture(targetDir, targetRevision, *kind, *driver, params, *seed, false, stderr)
	if err != nil {
		return err
	}
	if err := compatible(base, target); err != nil {
		return fmt.Errorf("benchmark envelopes are incompatible: %w", err)
	}
	result := compare(base, target)
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}
	runID := time.Now().UTC().Format("20060102T150405Z") + "-" + targetRevision[:12]
	for name, value := range map[string]any{"base-envelope.json": base, "target-envelope.json": target, "comparison.json": result} {
		if err := writeJSON(filepath.Join(*outputDir, runID+"-"+name), value); err != nil {
			return err
		}
	}
	if err := writeMarkdown(stdout, result); err != nil {
		return err
	}
	for _, value := range result.Comparisons {
		if threshold >= 0 && value.Regression > threshold {
			return fmt.Errorf("benchmark %s regressed %.2f%%, exceeding %.2f%%", value.Case, value.Regression, threshold)
		}
	}
	return nil
}

func resolveRevision(ref string) (string, error) {
	output, err := exec.Command("git", "rev-parse", "--verify", ref+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("resolve Git revision %q: %w", ref, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func exportRevision(revision, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	command := exec.Command("git", "archive", "--format=tar", revision)
	pipe, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	reader := tar.NewReader(pipe)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(header.Name)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path %q in Git archive", header.Name)
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("export revision %s: %w", revision, err)
	}
	return nil
}

func capture(directory, revision, kind, driver string, params parameters, seed int64, base bool, stderr io.Writer) (envelope, error) {
	fixtureDigest, err := digestPaths(directory, []string{"benchmark/testdata/scale", "integration/testdata"})
	if err != nil {
		return envelope{}, err
	}
	adapterPaths := []string{"cmd/benchdiff"}
	if kind == "integration" || kind == "all" {
		adapterPaths = append(adapterPaths, "cmd/benchmark")
	}
	adapterDigest, err := digestPaths(directory, adapterPaths)
	if err != nil {
		return envelope{}, err
	}
	metrics := map[string][]float64{}
	var postgresFacts postgresEnvironment
	if kind == "unit" || kind == "all" {
		args := []string{"test", "-run", "^$", "-bench", params.Bench, "-benchmem", "-count", strconv.Itoa(params.Count), "-benchtime", params.BenchTime, "./..."}
		output, err := runCommand(directory, nil, stderr, "go", args...)
		if err != nil {
			return envelope{}, err
		}
		mergeMetrics(metrics, parseBenchfmt(output))
	}
	if kind == "integration" || kind == "all" {
		connectionName := "BENCH_TARGET_CONNECTION_STRING"
		if base {
			connectionName = "BENCH_BASE_CONNECTION_STRING"
		}
		connection := os.Getenv(connectionName)
		other := os.Getenv(map[bool]string{true: "BENCH_TARGET_CONNECTION_STRING", false: "BENCH_BASE_CONNECTION_STRING"}[base])
		if connection == "" || other == "" || connection == other {
			return envelope{}, fmt.Errorf("integration comparison requires distinct BENCH_BASE_CONNECTION_STRING and BENCH_TARGET_CONNECTION_STRING")
		}
		if driver == "pg" {
			postgresFacts, err = readPostgresEnvironment(connection)
			if err != nil {
				return envelope{}, err
			}
		}
		env := append(os.Environ(), "CONNECTION_STRING="+connection)
		args := []string{"run", "./cmd/benchmark", "-driver", driver, "-iterations", strconv.Itoa(params.Count), "-format", "benchfmt", "-dataset-dir", "integration/testdata"}
		output, err := runCommand(directory, env, stderr, "go", args...)
		if err != nil {
			return envelope{}, err
		}
		mergeMetrics(metrics, parseBenchfmt(output))
	}
	if len(metrics) == 0 {
		return envelope{}, fmt.Errorf("revision %s produced no benchmark metrics", revision)
	}
	cases := make([]string, 0, len(metrics))
	for name := range metrics {
		cases = append(cases, name)
	}
	sort.Strings(cases)
	return envelope{Version: envelopeVersion, Revision: revision, Kind: kind, Driver: driver,
		FixtureSHA256: fixtureDigest, AdapterSHA256: adapterDigest, Parameters: params, Seed: seed,
		Schedule: "sequential_base_then_target", Environment: environment{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			GoVersion: runtime.Version(), CPUCount: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), Postgres: postgresFacts}, Cases: cases, Metrics: metrics}, nil
}

func readPostgresEnvironment(connection string) (postgresEnvironment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, connection)
	if err != nil {
		return postgresEnvironment{}, fmt.Errorf("inspect PostgreSQL benchmark environment: %w", err)
	}
	defer conn.Close(ctx)
	var value postgresEnvironment
	err = conn.QueryRow(ctx, `select current_setting('server_version_num')::integer,
current_setting('block_size'),current_setting('max_connections'),current_setting('max_locks_per_transaction'),
current_setting('shared_buffers'),current_setting('work_mem'),current_setting('effective_cache_size'),
current_setting('server_encoding'),current_setting('lc_collate'),
coalesce((select string_agg(extname||'='||extversion,',' order by extname) from pg_catalog.pg_extension),'')`).Scan(
		&value.ServerVersionNum, &value.BlockSize, &value.MaxConnections, &value.MaxLocksPerTransaction,
		&value.SharedBuffers, &value.WorkMem, &value.EffectiveCacheSize, &value.ServerEncoding, &value.Collation, &value.Extensions)
	if err != nil {
		return postgresEnvironment{}, fmt.Errorf("read PostgreSQL benchmark environment: %w", err)
	}
	return value, nil
}

func runCommand(directory string, environment []string, stderr io.Writer, name string, args ...string) ([]byte, error) {
	command := exec.Command(name, args...)
	command.Dir = directory
	if environment != nil {
		command.Env = environment
	}
	command.Stderr = stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("run %s in %s: %w", name, directory, err)
	}
	return output, nil
}

func parseBenchfmt(content []byte) map[string][]float64 {
	result := map[string][]float64{}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		for index := 2; index+1 < len(fields); index += 2 {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				continue
			}
			key := fields[0] + " " + fields[index+1]
			result[key] = append(result[key], value)
		}
	}
	return result
}

func mergeMetrics(destination, source map[string][]float64) {
	for name, values := range source {
		destination[name] = append(destination[name], values...)
	}
}

func compatible(base, target envelope) error {
	switch {
	case base.Version != envelopeVersion || target.Version != envelopeVersion:
		return fmt.Errorf("unsupported envelope version")
	case base.Kind != target.Kind || base.Driver != target.Driver:
		return fmt.Errorf("benchmark kind or driver differs")
	case base.FixtureSHA256 != target.FixtureSHA256:
		return fmt.Errorf("fixture checksum differs")
	case base.AdapterSHA256 != target.AdapterSHA256:
		return fmt.Errorf("adapter checksum differs")
	case base.Parameters != target.Parameters || base.Seed != target.Seed:
		return fmt.Errorf("parameters or seed differ")
	case base.Schedule != target.Schedule:
		return fmt.Errorf("schedule differs")
	case base.Environment != target.Environment:
		return fmt.Errorf("environment facts differ")
	case strings.Join(base.Cases, "\x00") != strings.Join(target.Cases, "\x00"):
		return fmt.Errorf("case names or units differ")
	}
	return nil
}

func compare(base, target envelope) report {
	result := report{Version: envelopeVersion, BaseRevision: base.Revision, TargetRevision: target.Revision}
	for _, name := range base.Cases {
		baseMedian := median(base.Metrics[name])
		targetMedian := median(target.Metrics[name])
		ratio := 0.0
		regression := 0.0
		if baseMedian != 0 {
			ratio = targetMedian / baseMedian
			regression = (ratio - 1) * 100
		}
		result.Comparisons = append(result.Comparisons, comparison{Case: name, BaseMedian: baseMedian, TargetMedian: targetMedian, Ratio: ratio, Regression: regression})
	}
	return result
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	if len(ordered) == 0 {
		return 0
	}
	if len(ordered)%2 == 0 {
		return (ordered[len(ordered)/2-1] + ordered[len(ordered)/2]) / 2
	}
	return ordered[len(ordered)/2]
}

func digestPaths(root string, paths []string) (string, error) {
	hash := sha256.New()
	var files []string
	for _, relative := range paths {
		path := filepath.Join(root, relative)
		err := filepath.WalkDir(path, func(name string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				files = append(files, name)
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("hash benchmark input %s: %w", relative, err)
		}
	}
	sort.Strings(files)
	for _, name := range files {
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return "", err
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(relative), len(content))
		hash.Write(content)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parsePercent(value string) (float64, error) {
	if value == "" {
		return -1, nil
	}
	if !strings.HasSuffix(value, "%") {
		return 0, fmt.Errorf("--fail-regression must use a percent such as 10%%")
	}
	parsed, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid --fail-regression %q", value)
	}
	return parsed, nil
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o600)
}

func writeMarkdown(writer io.Writer, value report) error {
	if _, err := fmt.Fprintf(writer, "# Benchmark comparison\n\nBase `%s`; target `%s`.\n\n| Case | Base median | Target median | Change |\n|---|---:|---:|---:|\n", value.BaseRevision, value.TargetRevision); err != nil {
		return err
	}
	for _, comparison := range value.Comparisons {
		if _, err := fmt.Fprintf(writer, "| `%s` | %.3f | %.3f | %+.2f%% |\n", comparison.Case, comparison.BaseMedian, comparison.TargetMedian, comparison.Regression); err != nil {
			return err
		}
	}
	return nil
}
