package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"map-walker/internal/benchmark/aoirunner"
)

func TestMatrixCLIBuilt(t *testing.T) {
	buildMatrixBinary(t)
	buildBenchBinary(t)
}

func TestMatrixCLIInvalidArgs(t *testing.T) {
	code := run([]string{"-timeout", "nope"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func buildMatrixBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "aoi-bench-matrix")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build matrix: %v\n%s", err, output)
	}
	return binary
}

func buildBenchBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "aoi-bench")
	cmd := exec.Command("go", "build", "-o", binary, "../aoi-bench")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build bench: %v\n%s", err, output)
	}
	return binary
}

func TestMatrixDefaultTimeoutFlag(t *testing.T) {
	if aoirunner.DefaultMatrixTimeout != 15*time.Minute {
		t.Fatalf("timeout=%v", aoirunner.DefaultMatrixTimeout)
	}
}
