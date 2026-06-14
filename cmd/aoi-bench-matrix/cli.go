package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"map-walker/internal/benchmark/aoirunner"
)

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aoi-bench-matrix", flag.ContinueOnError)
	fs.SetOutput(stderr)

	full := fs.Bool("full", false, "run the full frozen matrix")
	seed := fs.Int64("seed", aoirunner.BenchmarkSeed, "scenario seed")
	repeats := fs.Int("repeats", aoirunner.DefaultScenarioRepeats, "scenario repeats")
	timeout := fs.Duration("timeout", aoirunner.DefaultMatrixTimeout, "per-scenario timeout")
	maxRSS := fs.Int64("max-rss", 0, "memory guard in bytes (0=75% physical, -1=disable)")
	benchBin := fs.String("bench-bin", "", "path to aoi-bench binary")
	jsonOut := fs.String("json-out", "", "write matrix JSON report")
	csvOut := fs.String("csv-out", "", "write matrix CSV report")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	binary := *benchBin
	if binary == "" {
		var err error
		binary, err = defaultBenchBinary()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}

	fmt.Fprintf(stderr, "matrix start full=%v repeats=%d timeout=%s max-rss=%d bench=%s\n",
		*full, *repeats, *timeout, *maxRSS, binary)

	report, err := aoirunner.RunMatrix(aoirunner.MatrixOptions{
		Full:        *full,
		Seed:        *seed,
		Repeats:     *repeats,
		Timeout:     *timeout,
		MaxRSS:      *maxRSS,
		BenchBinary: binary,
	}, nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if err := aoirunner.WriteMatrixJSON(stdout, report); err != nil {
		fmt.Fprintf(stderr, "write stdout json: %v\n", err)
		return 1
	}
	if *jsonOut != "" {
		if err := writeMatrixFile(*jsonOut, report, aoirunner.WriteMatrixJSON); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if *csvOut != "" {
		if err := writeMatrixFile(*csvOut, report, aoirunner.WriteMatrixCSV); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	fmt.Fprintf(stderr, "matrix complete repeats=%d aggregates=%d\n", len(report.Repeats), len(report.Aggregates))
	return 0
}

func writeMatrixFile(path string, report aoirunner.MatrixReport, write func(io.Writer, aoirunner.MatrixReport) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := write(file, report); err != nil {
		file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return file.Close()
}

func defaultBenchBinary() (string, error) {
	if path, err := lookPath("aoi-bench"); err == nil {
		return path, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve bench binary: %w", err)
	}
	candidate := filepath.Join(filepath.Dir(exe), "aoi-bench")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("aoi-bench binary not found; pass -bench-bin")
}

var lookPath = exec.LookPath
