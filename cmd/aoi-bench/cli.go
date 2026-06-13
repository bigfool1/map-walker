package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
	"map-walker/internal/benchmark/aoirunner"
)

type cliConfig struct {
	mode        aoirunner.Mode
	scale       int
	moverCount  int
	density     aoiworkload.Density
	seed        int64
	repeat      int
	cpuProfile  string
	heapProfile string
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aoi-bench", flag.ContinueOnError)
	fs.SetOutput(stderr)

	modeFlag := fs.String("mode", "", "benchmark mode: build, core_tick, world_aoi")
	scaleFlag := fs.Int("scale", 0, "observer count")
	moversFlag := fs.Int("movers", 0, "moving player count")
	densityFlag := fs.String("density", "normal", "density: sparse, normal, hotspot")
	seedFlag := fs.Int64("seed", aoirunner.BenchmarkSeed, "scenario seed")
	repeatFlag := fs.Int("repeat", 1, "scenario repeat index")
	cpuProfileFlag := fs.String("cpu-profile", "", "write CPU profile to file")
	heapProfileFlag := fs.String("heap-profile", "", "write heap profile to file")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := parseCLIConfig(*modeFlag, *scaleFlag, *moversFlag, *densityFlag, *seedFlag, *repeatFlag, *cpuProfileFlag, *heapProfileFlag)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	profileExecution := cfg.cpuProfile != "" || cfg.heapProfile != ""
	var profileRequiredExplicitGC bool

	if cfg.cpuProfile != "" {
		file, err := os.Create(cfg.cpuProfile)
		if err != nil {
			fmt.Fprintf(stderr, "create cpu profile: %v\n", err)
			return 1
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			file.Close()
			fmt.Fprintf(stderr, "start cpu profile: %v\n", err)
			return 1
		}
		defer func() {
			pprof.StopCPUProfile()
			file.Close()
		}()
		fmt.Fprintf(stderr, "cpu profile: %s\n", cfg.cpuProfile)
	}

	envOpts := aoirunner.EnvironmentCaptureOptions{
		Arguments: append([]string{"aoi-bench"}, args...),
		Now:       time.Now().UTC(),
	}

	var result aoirunner.Result
	var phase aoirunner.Phase = aoirunner.PhaseGeneration

	defer func() {
		if profileExecution {
			result.ProfileExecution = true
			result.ProfileRequiredExplicitGC = profileRequiredExplicitGC
		}
		if result.Phase == "" {
			result.Phase = phase
		}
		if err := aoirunner.WriteJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "write result: %v\n", err)
		}
	}()

	workloadConfig := aoiworkload.Config{
		Scale:      cfg.scale,
		MoverCount: cfg.moverCount,
		Density:    cfg.density,
		Seed:       cfg.seed,
	}

	if !workloadConfig.IsApplicable() {
		result = notApplicableResult(cfg, envOpts)
		fmt.Fprintln(stderr, "scenario not applicable: mover count exceeds scale")
		return 0
	}

	fmt.Fprintf(stderr, "generating workload %s\n", workloadConfig.String())
	scenario, err := aoiworkload.Generate(workloadConfig)
	if err != nil {
		result = runtimeErrorResult(cfg, envOpts, phase, err)
		fmt.Fprintf(stderr, "generation failed: %v\n", err)
		return 1
	}

	switch cfg.mode {
	case aoirunner.ModeBuild:
		phase = aoirunner.PhaseBuild
		fmt.Fprintln(stderr, "running core build")
		result = aoirunner.RunCoreBuild(scenario, aoirunner.CoreOptions{
			Repeat:      cfg.repeat,
			Environment: envOpts,
			OnBuildProgress: func(event aoirunner.BuildProgressEvent) {
				fmt.Fprintf(stderr, "build progress %d%% elapsed=%dns\n", event.PercentComplete, event.ElapsedNs)
			},
		})
	case aoirunner.ModeCoreTick:
		phase = aoirunner.PhaseWarmup
		fmt.Fprintln(stderr, "running core tick")
		result = aoirunner.RunCoreTick(scenario, aoirunner.CoreOptions{
			Repeat:      cfg.repeat,
			Environment: envOpts,
		})
	case aoirunner.ModeWorldAOI:
		phase = aoirunner.PhaseWarmup
		fmt.Fprintln(stderr, "running world plus aoi")
		result = aoirunner.RunWorldAOI(scenario, aoirunner.WorldOptions{
			Repeat:      cfg.repeat,
			Environment: envOpts,
		})
	default:
		fmt.Fprintf(stderr, "unsupported mode %q\n", cfg.mode)
		return 2
	}

	if cfg.heapProfile != "" {
		runtime.GC()
		profileRequiredExplicitGC = true
		file, err := os.Create(cfg.heapProfile)
		if err != nil {
			result.Status = aoirunner.StatusRuntimeError
			result.ErrorSummary = fmt.Sprintf("create heap profile: %v", err)
			fmt.Fprintf(stderr, "create heap profile: %v\n", err)
			return 1
		}
		if err := pprof.WriteHeapProfile(file); err != nil {
			file.Close()
			result.Status = aoirunner.StatusRuntimeError
			result.ErrorSummary = fmt.Sprintf("write heap profile: %v", err)
			fmt.Fprintf(stderr, "write heap profile: %v\n", err)
			return 1
		}
		file.Close()
		fmt.Fprintf(stderr, "heap profile: %s (explicit gc)\n", cfg.heapProfile)
	}

	if result.Status == aoirunner.StatusRuntimeError {
		return 1
	}
	fmt.Fprintf(stderr, "status=%s phase=%s\n", result.Status, result.Phase)
	return 0
}

func parseCLIConfig(
	mode string,
	scale, movers int,
	density string,
	seed int64,
	repeat int,
	cpuProfile, heapProfile string,
) (cliConfig, error) {
	if mode == "" {
		return cliConfig{}, fmt.Errorf("missing required flag -mode")
	}
	if scale <= 0 {
		return cliConfig{}, fmt.Errorf("missing or invalid -scale")
	}
	if movers <= 0 {
		return cliConfig{}, fmt.Errorf("missing or invalid -movers")
	}
	if repeat <= 0 {
		return cliConfig{}, fmt.Errorf("invalid -repeat")
	}

	var benchMode aoirunner.Mode
	switch mode {
	case "build":
		benchMode = aoirunner.ModeBuild
	case "core_tick":
		benchMode = aoirunner.ModeCoreTick
	case "world_aoi":
		benchMode = aoirunner.ModeWorldAOI
	default:
		return cliConfig{}, fmt.Errorf("invalid -mode %q", mode)
	}

	var benchDensity aoiworkload.Density
	switch density {
	case "sparse":
		benchDensity = aoiworkload.DensitySparse
	case "normal":
		benchDensity = aoiworkload.DensityNormal
	case "hotspot":
		benchDensity = aoiworkload.DensityHotspot
	default:
		return cliConfig{}, fmt.Errorf("invalid -density %q", density)
	}

	return cliConfig{
		mode:        benchMode,
		scale:       scale,
		moverCount:  movers,
		density:     benchDensity,
		seed:        seed,
		repeat:      repeat,
		cpuProfile:  cpuProfile,
		heapProfile: heapProfile,
	}, nil
}

func notApplicableResult(cfg cliConfig, env aoirunner.EnvironmentCaptureOptions) aoirunner.Result {
	return aoirunner.Result{
		Identity:    aoirunner.ScenarioIdentityFromConfig(cfg.mode, workloadConfigFromCLI(cfg), cfg.repeat),
		Environment: aoirunner.CaptureEnvironment(env),
		Status:      aoirunner.StatusNotApplicable,
		Phase:       aoirunner.PhaseGeneration,
		ErrorSummary: "mover count exceeds scale",
	}
}

func runtimeErrorResult(cfg cliConfig, env aoirunner.EnvironmentCaptureOptions, phase aoirunner.Phase, err error) aoirunner.Result {
	return aoirunner.Result{
		Identity:     aoirunner.ScenarioIdentityFromConfig(cfg.mode, workloadConfigFromCLI(cfg), cfg.repeat),
		Environment:  aoirunner.CaptureEnvironment(env),
		Status:       aoirunner.StatusRuntimeError,
		Phase:        phase,
		ErrorSummary: err.Error(),
	}
}

func workloadConfigFromCLI(cfg cliConfig) aoiworkload.Config {
	return aoiworkload.Config{
		Scale:      cfg.scale,
		MoverCount: cfg.moverCount,
		Density:    cfg.density,
		Seed:       cfg.seed,
	}
}
