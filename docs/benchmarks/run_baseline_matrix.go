// Incremental baseline matrix runner with resume support.
// Usage: go run docs/benchmarks/run_baseline_matrix.go
//
//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"map-walker/internal/benchmark/aoirunner"
)

const (
	benchBinary = "/tmp/aoi-bench"
	progressPath = "docs/benchmarks/matrix-progress.json"
	jsonOut      = "docs/benchmarks/aoi-core-baseline-matrix.json"
	csvOut       = "docs/benchmarks/aoi-core-baseline-matrix.csv"
)

type progressState struct {
	CompletedKeys []string                        `json:"completed_keys"`
	Repeats       []aoirunner.MatrixRepeatRecord  `json:"repeats"`
	MemoryGuard   int64                           `json:"memory_guard_bytes"`
	TimeoutNs     int64                           `json:"timeout_ns"`
	StartedAt     time.Time                       `json:"started_at"`
}

func main() {
	timeout := 15 * time.Minute
	memoryGuard := aoirunner.ResolveMemoryGuard(0)

	state := progressState{
		MemoryGuard: memoryGuard,
		TimeoutNs:   timeout.Nanoseconds(),
		StartedAt:   time.Now().UTC(),
	}
	if data, err := os.ReadFile(progressPath); err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			fmt.Fprintf(os.Stderr, "ignore corrupt progress: %v\n", err)
		}
	}

	done := map[string]struct{}{}
	for _, key := range state.CompletedKeys {
		done[key] = struct{}{}
	}

	definitions := aoirunner.BaselineMatrixDefinitions(aoirunner.BenchmarkSeed)
	for _, definition := range definitions {
		key := definition.AggregateKey()
		if _, ok := done[key]; ok {
			fmt.Fprintf(os.Stderr, "skip completed %s\n", key)
			continue
		}

		fmt.Fprintf(os.Stderr, "run %s (3 repeats)\n", key)
		report, err := aoirunner.RunMatrix(aoirunner.MatrixOptions{
			Seed:        aoirunner.BenchmarkSeed,
			Repeats:     aoirunner.DefaultScenarioRepeats,
			Timeout:     timeout,
			MaxRSS:      0,
			BenchBinary: benchBinary,
			Definitions: []aoirunner.ScenarioDefinition{definition},
		}, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "matrix error %s: %v\n", key, err)
			os.Exit(1)
		}

		state.Repeats = append(state.Repeats, report.Repeats...)
		state.CompletedKeys = append(state.CompletedKeys, key)
		if err := saveProgress(state); err != nil {
			fmt.Fprintf(os.Stderr, "save progress: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "saved %s (%d repeats total)\n", key, len(state.Repeats))
	}

	final := aoirunner.MatrixReport{
		GeneratedAt:      time.Now().UTC(),
		MemoryGuardBytes: state.MemoryGuard,
		TimeoutNs:        state.TimeoutNs,
		FullMatrix:       false,
		Repeats:          state.Repeats,
		Aggregates:       aoirunner.AggregateMatrixRepeats(state.Repeats),
	}

	if err := writeFile(jsonOut, func(w *os.File) error {
		return aoirunner.WriteMatrixJSON(w, final)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write json: %v\n", err)
		os.Exit(1)
	}
	if err := writeFile(csvOut, func(w *os.File) error {
		return aoirunner.WriteMatrixCSV(w, final)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write csv: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "matrix complete repeats=%d aggregates=%d\n", len(final.Repeats), len(final.Aggregates))
}

func saveProgress(state progressState) error {
	if err := os.MkdirAll(filepath.Dir(progressPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(progressPath, data, 0o644)
}

func writeFile(path string, write func(*os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
