package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"map-walker/internal/benchmark/aoirunner"
)

func TestRunSmallCoreBuildJSON(t *testing.T) {
	stdout, stderr, code := runScenario(t, "build", 128, 16)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	result := parseResult(t, stdout)
	if result.Status != aoirunner.StatusSuccess {
		t.Fatalf("status=%s", result.Status)
	}
	if result.Identity.Mode != aoirunner.ModeBuild {
		t.Fatalf("mode=%s", result.Identity.Mode)
	}
	if result.BuildDurationNs <= 0 {
		t.Fatal("expected build duration")
	}
	if !strings.Contains(stderr, "build progress") {
		t.Fatalf("expected build progress on stderr: %s", stderr)
	}
}

func TestRunSmallCoreTickJSON(t *testing.T) {
	stdout, stderr, code := runScenario(t, "core_tick", 128, 16)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	result := parseResult(t, stdout)
	if result.Status != aoirunner.StatusSuccess || result.TickDuration == nil {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(stdout, "generating workload") {
		t.Fatal("diagnostics leaked to stdout")
	}
	if !strings.Contains(stderr, "generating workload") {
		t.Fatalf("expected diagnostics on stderr: %s", stderr)
	}
}

func TestRunSmallWorldAOIJSON(t *testing.T) {
	stdout, _, code := runScenario(t, "world_aoi", 128, 16)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	result := parseResult(t, stdout)
	if result.Status != aoirunner.StatusSuccess || result.SimulationDuration == nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestInvalidModeRejected(t *testing.T) {
	_, stderr, code := runDirect(t, []string{"-mode", "invalid", "-scale", "128", "-movers", "16"})
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
	if !strings.Contains(stderr, "invalid -mode") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestNotApplicableScenario(t *testing.T) {
	stdout, stderr, code := runDirect(t, []string{
		"-mode", "core_tick", "-scale", "100", "-movers", "200", "-seed", "1",
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	result := parseResult(t, stdout)
	if result.Status != aoirunner.StatusNotApplicable {
		t.Fatalf("status=%s", result.Status)
	}
	if !strings.Contains(stderr, "not applicable") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestOptionalProfilesCreated(t *testing.T) {
	dir := t.TempDir()
	cpuPath := filepath.Join(dir, "cpu.pprof")
	heapPath := filepath.Join(dir, "heap.pprof")
	stdout, stderr, code := runDirect(t, []string{
		"-mode", "core_tick",
		"-scale", "128",
		"-movers", "16",
		"-cpu-profile", cpuPath,
		"-heap-profile", heapPath,
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	result := parseResult(t, stdout)
	if !result.ProfileExecution || !result.ProfileRequiredExplicitGC {
		t.Fatalf("profile flags=%v explicit_gc=%v", result.ProfileExecution, result.ProfileRequiredExplicitGC)
	}
	if _, err := os.Stat(cpuPath); err != nil {
		t.Fatalf("cpu profile missing: %v", err)
	}
	if _, err := os.Stat(heapPath); err != nil {
		t.Fatalf("heap profile missing: %v", err)
	}
	if !strings.Contains(stderr, "explicit gc") {
		t.Fatalf("expected heap profile gc note: %s", stderr)
	}
}

func TestProfilesNotCreatedByDefault(t *testing.T) {
	dir := t.TempDir()
	cpuPath := filepath.Join(dir, "unused-cpu.pprof")
	heapPath := filepath.Join(dir, "unused-heap.pprof")
	_, _, code := runDirect(t, []string{
		"-mode", "build", "-scale", "128", "-movers", "16",
	})
	if code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(cpuPath); !os.IsNotExist(err) {
		t.Fatal("unexpected cpu profile")
	}
	if _, err := os.Stat(heapPath); !os.IsNotExist(err) {
		t.Fatal("unexpected heap profile")
	}
}

func TestBuiltBinaryStdoutIsJSON(t *testing.T) {
	binary := buildBinary(t)
	cmd := exec.Command(binary,
		"-mode", "core_tick",
		"-scale", "128",
		"-movers", "16",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run binary: %v stderr=%s", err, stderr.String())
	}
	parseResult(t, stdout.String())
}

func runScenario(t *testing.T, mode string, scale, movers int) (stdout, stderr string, code int) {
	t.Helper()
	return runDirect(t, []string{
		"-mode", mode,
		"-scale", itoa(scale),
		"-movers", itoa(movers),
		"-seed", "31",
	})
}

func runDirect(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code = run(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

func parseResult(t *testing.T, stdout string) aoirunner.Result {
	t.Helper()
	result, err := aoirunner.ParseJSON([]byte(stdout))
	if err != nil {
		t.Fatalf("parse json: %v stdout=%s", err, stdout)
	}
	return result
}

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "aoi-bench")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = repoRoot(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
