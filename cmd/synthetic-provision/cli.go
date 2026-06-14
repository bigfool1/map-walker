package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/joho/godotenv"

	"map-walker/internal/storage"
	"map-walker/internal/synthetic"
)

const syntheticPasswordEnv = "MAP_WALKER_SYNTHETIC_PASSWORD"

type runDeps struct {
	openDB    func(driver, dsn string) (*storage.DB, error)
	provision func(ctx context.Context, db *storage.DB, count, workers int, password string) (synthetic.ProvisionResult, error)
}

func defaultRunDeps() runDeps {
	return runDeps{
		openDB: storage.Open,
		provision: func(ctx context.Context, db *storage.DB, count, workers int, password string) (synthetic.ProvisionResult, error) {
			return synthetic.NewProvisioner(db).Provision(ctx, count, workers, password)
		},
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, stdout, stderr, defaultRunDeps())
}

func runWith(args []string, stdout, stderr io.Writer, deps runDeps) int {
	_ = godotenv.Load()

	fs := flag.NewFlagSet("synthetic-provision", flag.ContinueOnError)
	fs.SetOutput(stderr)

	defaultDriver := envDefault("DB_DRIVER", "sqlite")
	defaultDSN := envDefault("DB_DSN", storage.DefaultDBPath)

	count := fs.Int("count", 0, "ensure synthetic accounts 1..N exist")
	workers := fs.Int("workers", runtime.GOMAXPROCS(0), "provisioning worker count")
	dbDriver := fs.String("db-driver", defaultDriver, "database driver (sqlite / mysql)")
	dbDSN := fs.String("db-dsn", defaultDSN, "database DSN")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *count < 0 {
		fmt.Fprintln(stderr, "count must be non-negative")
		return 2
	}
	if *workers < 1 {
		fmt.Fprintln(stderr, "workers must be at least 1")
		return 2
	}
	if *count > 0 && os.Getenv(syntheticPasswordEnv) == "" {
		fmt.Fprintf(stderr, "%s is required when count is greater than 0\n", syntheticPasswordEnv)
		return 2
	}
	if *count == 0 {
		writeResult(stdout, stderr, 0, synthetic.ProvisionResult{})
		return 0
	}

	db, err := deps.openDB(*dbDriver, *dbDSN)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer db.Close()

	result, err := deps.provision(context.Background(), db, *count, *workers, os.Getenv(syntheticPasswordEnv))
	if err != nil {
		fmt.Fprintf(stderr, "provision: %v\n", err)
		return 1
	}

	writeResult(stdout, stderr, *count, result)
	if result.Failed > 0 {
		return 1
	}
	return 0
}

func writeResult(stdout, stderr io.Writer, count int, result synthetic.ProvisionResult) {
	if count == 0 {
		fmt.Fprintln(stdout, "synthetic provision: count 0, no accounts processed")
		return
	}
	fmt.Fprintf(stdout, "synthetic provision complete: created=%d reused=%d corrected=%d failed=%d\n",
		result.Created, result.Reused, result.Corrected, result.Failed)
	if result.Failed > 0 {
		fmt.Fprintln(stderr, "synthetic provision finished with account failures")
	}
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
