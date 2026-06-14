package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"map-walker/internal/storage"
	"map-walker/internal/synthetic"
)

const testPassword = "password123"

func TestRunCountZeroRequiresNoPassword(t *testing.T) {
	t.Setenv(syntheticPasswordEnv, "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-count", "0"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "count 0") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunRejectsNegativeCount(t *testing.T) {
	t.Setenv(syntheticPasswordEnv, testPassword)

	var stderr bytes.Buffer
	code := run([]string{"-count", "-1"}, bytes.NewBuffer(nil), &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "count must be non-negative") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRunRejectsInvalidWorkerCount(t *testing.T) {
	t.Setenv(syntheticPasswordEnv, testPassword)

	var stderr bytes.Buffer
	code := run([]string{"-count", "1", "-workers", "0"}, bytes.NewBuffer(nil), &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "workers must be at least 1") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRunRejectsMissingPasswordWhenCountPositive(t *testing.T) {
	t.Setenv(syntheticPasswordEnv, "")

	dbPath := filepath.Join(t.TempDir(), "test.db")
	var stderr bytes.Buffer
	code := run(
		[]string{"-count", "1", "-db-dsn", dbPath},
		bytes.NewBuffer(nil),
		&stderr,
	)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), syntheticPasswordEnv) {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRunProvisionsAndReusesOnSecondRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv(syntheticPasswordEnv, testPassword)

	firstStdout, firstStderr, firstCode := runDirect(t, []string{
		"-count", "2",
		"-workers", "2",
		"-db-dsn", dbPath,
	})
	if firstCode != 0 {
		t.Fatalf("first run exit=%d stderr=%s", firstCode, firstStderr)
	}
	if !strings.Contains(firstStdout, "created=2") || !strings.Contains(firstStdout, "failed=0") {
		t.Fatalf("first stdout=%q", firstStdout)
	}

	secondStdout, secondStderr, secondCode := runDirect(t, []string{
		"-count", "2",
		"-workers", "2",
		"-db-dsn", dbPath,
	})
	if secondCode != 0 {
		t.Fatalf("second run exit=%d stderr=%s", secondCode, secondStderr)
	}
	if !strings.Contains(secondStdout, "reused=2") || !strings.Contains(secondStdout, "created=0") {
		t.Fatalf("second stdout=%q", secondStdout)
	}
}

func TestRunPartialFailureExitsNonZero(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv(syntheticPasswordEnv, testPassword)

	deps := defaultRunDeps()
	deps.provision = func(ctx context.Context, db *storage.DB, count, workers int, password string) (synthetic.ProvisionResult, error) {
		return synthetic.ProvisionResult{
			Created: 1,
			Failed:  1,
			Accounts: []synthetic.AccountReadiness{
				{AccountNumber: 1, Created: true},
				{AccountNumber: 2, Err: os.ErrPermission},
			},
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := runWith(
		[]string{"-count", "2", "-db-dsn", dbPath},
		&stdout,
		&stderr,
		deps,
	)
	if code != 1 {
		t.Fatalf("exit=%d want 1 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "failed=1") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "account failures") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func runDirect(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code = run(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}
