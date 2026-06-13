package aoirunner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"map-walker/internal/benchmark/aoiworkload"
)

func TestBenchmarkScenariosUseFrozenConfigs(t *testing.T) {
	representative := Representative100kBenchmarkConfig()
	baseline := aoiworkload.BaselineMatrixConfigs(BenchmarkSeed)[0]
	if representative != baseline {
		t.Fatalf("representative config=%v want baseline=%v", representative, baseline)
	}
	for name, config := range map[string]aoiworkload.Config{
		"Small128":       SmallBenchmarkConfig,
		"Profile1024":    ProfileBenchmarkConfig,
		"Representative": representative,
	} {
		if !config.IsApplicable() {
			t.Fatalf("%s config not applicable: %+v", name, config)
		}
		scenario := mustBenchmarkScenario(config)
		if scenario.Config != config {
			t.Fatalf("%s scenario config mismatch", name)
		}
	}
}

func BenchmarkCoreBuild_Small128(b *testing.B) {
	scenario := mustBenchmarkScenario(SmallBenchmarkConfig)
	opts := CoreOptions{Repeat: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RunCoreBuild(scenario, opts)
	}
}

func BenchmarkCoreBuild_Profile1024(b *testing.B) {
	scenario := mustBenchmarkScenario(ProfileBenchmarkConfig)
	opts := CoreOptions{Repeat: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RunCoreBuild(scenario, opts)
	}
}

func BenchmarkCoreBuild_Representative100k_10k_Normal(b *testing.B) {
	scenario := mustBenchmarkScenario(Representative100kBenchmarkConfig())
	opts := CoreOptions{Repeat: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RunCoreBuild(scenario, opts)
	}
}

func BenchmarkCoreTickMove_Small128(b *testing.B) {
	scenario := mustBenchmarkScenario(SmallBenchmarkConfig)
	targets := preExpandCoreTargets(scenario)
	aoi := buildAOIIndex(scenario)
	warmupCoreTicks(aoi, targets)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMeasuredCoreTicks(aoi, targets)
	}
}

func BenchmarkCoreTickMove_Profile1024(b *testing.B) {
	scenario := mustBenchmarkScenario(ProfileBenchmarkConfig)
	targets := preExpandCoreTargets(scenario)
	aoi := buildAOIIndex(scenario)
	warmupCoreTicks(aoi, targets)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMeasuredCoreTicks(aoi, targets)
	}
}

func BenchmarkCoreTickMove_Representative100k_10k_Normal(b *testing.B) {
	scenario := mustBenchmarkScenario(Representative100kBenchmarkConfig())
	targets := preExpandCoreTargets(scenario)
	aoi := buildAOIIndex(scenario)
	warmupCoreTicks(aoi, targets)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMeasuredCoreTicks(aoi, targets)
	}
}

func BenchmarkWorldAOI_Small128(b *testing.B) {
	scenario := mustBenchmarkScenario(SmallBenchmarkConfig)
	world, aoi, updates := newWorldBenchmarkState(scenario)
	warmupWorldSimulation(world, aoi, scenario, updates)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMeasuredWorldSimulation(world, aoi, scenario, updates)
	}
}

func BenchmarkWorldAOI_Profile1024(b *testing.B) {
	scenario := mustBenchmarkScenario(ProfileBenchmarkConfig)
	world, aoi, updates := newWorldBenchmarkState(scenario)
	warmupWorldSimulation(world, aoi, scenario, updates)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMeasuredWorldSimulation(world, aoi, scenario, updates)
	}
}

func BenchmarkWorldAOI_Representative100k_10k_Normal(b *testing.B) {
	scenario := mustBenchmarkScenario(Representative100kBenchmarkConfig())
	world, aoi, updates := newWorldBenchmarkState(scenario)
	warmupWorldSimulation(world, aoi, scenario, updates)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMeasuredWorldSimulation(world, aoi, scenario, updates)
	}
}

func TestProfileArtifactsReadable(t *testing.T) {
	if os.Getenv("AOI_BENCH_PROFILE_SMOKE") == "" {
		t.Skip("set AOI_BENCH_PROFILE_SMOKE=1 to run profile smoke test")
	}
	dir := t.TempDir()
	cpuProfile := filepath.Join(dir, "cpu.pprof")
	heapProfile := filepath.Join(dir, "heap.pprof")

	runBench := func(args ...string) {
		t.Helper()
		cmdArgs := append([]string{
			"test", "./internal/benchmark/aoirunner",
			"-run", "^$",
			"-bench", "BenchmarkCoreTickMove_Small128$",
			"-benchtime", "1x",
		}, args...)
		cmd := exec.Command("go", cmdArgs...)
		cmd.Dir = mustRepoRoot(t)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test %v failed: %v\n%s", cmdArgs, err, output)
		}
	}

	runBench("-cpuprofile", cpuProfile)
	verifyPprof(t, cpuProfile)

	runBench("-memprofile", heapProfile)
	verifyPprof(t, heapProfile)
}

func verifyPprof(t *testing.T, profilePath string) {
	t.Helper()
	cmd := exec.Command("go", "tool", "pprof", "-top", profilePath)
	cmd.Dir = mustRepoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go tool pprof %s failed: %v\n%s", profilePath, err, output)
	}
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}
