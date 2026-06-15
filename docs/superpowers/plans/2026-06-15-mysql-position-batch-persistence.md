# MySQL Position Batch Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Track each
> task with its checkbox.

**Goal:** Replace MySQL periodic per-player position writes with independent
500-row bulk-update chunks while preserving the existing SQLite and final-save
behavior.

**Architecture:** Keep Hub dirty-player collection and the single
`PersistenceWorker` actor unchanged. Filter and collapse sequence updates in
the worker, dispatch MySQL batches to a storage bulk-update path, and retain
the existing ordered per-row path for SQLite.

**Tech Stack:** Go 1.26, `database/sql`, MySQL `UPDATE ... JOIN`, existing
SQLite legacy backend, Go tests and benchmarks.

**Required context:** Read
`docs/superpowers/specs/2026-06-15-mysql-position-batch-persistence-design.md`,
`AGENTS.md`, and `docs/concurrency-debugging.md` before implementation.

---

### Task 1: Freeze Persistence Batch And Sequence Semantics

- [ ] Add focused worker tests for filtering, collapsing, and chunk-level
  sequence behavior before changing the write strategy.

**Task boundary:**

- Add tests that exclude updates whose sequence is not newer than the last
  successfully persisted sequence.
- Add a test where one submitted batch contains multiple updates for the same
  user and only the highest sequence remains.
- Add tests proving successful groups advance `lastSeq` and failed groups do
  not.
- Add a failure-isolation test where a middle group fails and a later group
  still executes.
- Preserve existing `Submit`, `SubmitSync`, `Drain`, and `Stop` signatures and
  lifecycle semantics.
- Do not add MySQL SQL generation in this task.

**Behavioral goals:**

- Database work receives at most one update per user from a submitted batch.
- Stale filtering is based only on previously successful persistence.
- Failed work remains eligible for a later newer save.
- Failure of one group does not prevent later independent work from running.
- Existing SQLite worker tests remain valid.

**Affected modules:**

- Modify `internal/storage/persistence_test.go`
- Modify test-only worker seams in `internal/storage/persistence.go` only if
  required to observe grouped writes without introducing production behavior

**Verification:**

- Run `go test ./internal/storage`.
- Repeat the new sequence and failure-isolation tests with `-count=20`.
- Run `go test -race ./internal/storage`.

---

### Task 2: Add MySQL Bulk Position Query Construction

- [ ] Add a storage operation that updates one chunk of distinct user
  positions with a parameterized MySQL `UPDATE ... JOIN`.

**Task boundary:**

- Define the maximum MySQL position chunk size as 500 rows.
- Generate one derived table using `SELECT ? AS id, ? AS lat, ? AS lng`
  followed by `UNION ALL SELECT ?, ?, ?` for remaining rows.
- Build arguments in the same order as the derived-table rows.
- Execute each chunk inside its own transaction.
- Roll back on begin, execution, or commit failure where applicable.
- Treat successful execution and commit as chunk success regardless of
  `RowsAffected`.
- Keep SQL generation and execution inside `internal/storage`.
- Do not add a SQLite equivalent or introduce ORM dependencies.

**Behavioral goals:**

- One accepted chunk produces one bulk update statement and one commit.
- SQL values use placeholders rather than interpolation.
- Empty input performs no database work.
- Chunk failures return enough context for the worker to log the failure and
  continue.
- Unchanged MySQL row values are not misclassified as missing users.

**Affected modules:**

- Create `internal/storage/position_batch.go`
- Create `internal/storage/position_batch_test.go`
- Reuse the existing `DB.Driver()` identity from `internal/storage/storage.go`

**Verification:**

- Run `go test ./internal/storage`.
- Verify generated SQL and argument ordering for one row, multiple rows, and
  500 rows.
- Verify begin, execute, and commit failure paths with a deterministic database
  test seam or SQL mock appropriate to the existing package.
- Run `go vet ./internal/storage`.

---

### Task 3: Route PersistenceWorker By Storage Backend

- [ ] Integrate the MySQL chunked bulk path while retaining SQLite's existing
  ordered per-row writes.

**Task boundary:**

- Refactor worker application into shared filtering/collapse logic followed by
  a backend-specific write strategy.
- For MySQL, split accepted distinct-user updates into chunks of at most 500
  and process chunks sequentially.
- Continue to later chunks after a failed chunk.
- Advance `lastSeq` only after the corresponding chunk commits.
- Log one error per failed chunk, including chunk size, rather than one error
  per row.
- For SQLite, preserve the current sequential `SaveUserPosition` path and
  per-update error behavior.
- Keep one worker goroutine; do not parallelize chunks.
- Do not change Hub code or periodic scheduling.
- Do not change `SubmitSync` behavior or final-save paths.

**Behavioral goals:**

- MySQL uses no more than one database update statement per 500 accepted users.
- SQLite behavior remains compatible with current tests and local development.
- Worker ordering and stale-update protection remain intact.
- A failed MySQL chunk affects at most 500 users and does not poison later
  chunks.
- Periodic Hub saves remain asynchronous and do not block simulation or
  replication.

**Affected modules:**

- Modify `internal/storage/persistence.go`
- Modify `internal/storage/persistence_test.go`
- Modify `internal/storage/position_batch.go`
- Modify `internal/storage/position_batch_test.go`
- Modify `internal/realtime/persistence_test.go` only if an existing integration
  assertion requires clarification; do not change realtime behavior

**Verification:**

- Run `go test ./internal/storage`.
- Run `go test ./internal/realtime`.
- Run focused chunking and failure tests with `-count=20`.
- Run `go test -race ./internal/storage ./internal/realtime`.
- Confirm no lock or database call is added to the Hub actor.

---

### Task 4: Add MySQL Persistence Baseline And Optimized Benchmarks

- [ ] Measure 1,000- and 4,000-update batches against an explicitly configured
  MySQL test database.

**Task boundary:**

- Add benchmark fixtures that create or reset the required user rows outside
  the timed section.
- Add a per-row baseline benchmark using the pre-optimization write strategy.
- Add the chunked bulk benchmark using identical rows, coordinates, and batch
  sizes.
- Require an explicit MySQL DSN environment variable; do not silently use
  SQLite.
- Skip MySQL integration benchmarks with a clear message when the DSN is
  absent.
- Report elapsed batch time and standard benchmark allocation metrics.
- Report submitted rows and executed statement/chunk count as benchmark
  diagnostics.
- Record database version, Go version, machine, command, and repetition count.

**Behavioral goals:**

- Baseline and optimized benchmarks exercise equivalent logical updates.
- Timed work excludes schema migration and fixture creation.
- 1,000 updates produce two optimized chunks.
- 4,000 updates produce eight optimized chunks.
- Results demonstrate database-call reduction independently of Hub and
  replication work.

**Affected modules:**

- Create `internal/storage/position_batch_benchmark_test.go`
- Create `docs/benchmarks/mysql-position-batch-persistence.md`

**Verification:**

- Run `go test ./internal/storage`.
- With MySQL configured, run:
  `go test -run '^$' -bench 'BenchmarkPositionPersistence/(1000|4000)$' -benchmem -count=5 ./internal/storage`.
- Confirm baseline and optimized runs update the same row counts.
- Confirm optimized diagnostic chunk counts are 2 and 8.
- Save baseline and optimized measurements in the benchmark report.

---

### Task 5: Document Backend Support And Complete Project Verification

- [ ] Document MySQL as the production target, SQLite as legacy/dev, and the
  new periodic batch behavior.

**Task boundary:**

- Update backend support language without removing SQLite commands or
  dependencies.
- Document that new performance-sensitive storage features need not have an
  equivalent SQLite implementation.
- Document the 500-row MySQL periodic position chunks and independent
  transaction/failure boundary.
- State explicitly that `SubmitSync`, disconnect, logout, replacement, and
  shutdown semantics were not changed in this phase.
- Add benchmark results and remaining persistence limitations to the handoff.
- Do not claim final-save durability improvements.

**Behavioral goals:**

- Operators understand MySQL is the intended production backend.
- Developers can continue using SQLite for existing local and test workflows.
- Documentation matches the implemented chunk size, transaction boundary, and
  failure behavior.
- Performance claims are tied to recorded MySQL measurements.

**Affected modules:**

- Modify `README.md`
- Modify `AGENTS.md`
- Modify `.env.example`
- Modify `docs/map-walker-handoff.md`
- Finalize `docs/benchmarks/mysql-position-batch-persistence.md`

**Verification:**

- Run `go test ./internal/storage`.
- Run `go test ./...`.
- Run `go test -race ./internal/storage ./internal/realtime`.
- Run `go vet ./...`.
- Run `git diff --check`.
- Confirm existing SQLite tests still pass.
- Confirm benchmark documentation contains environment, commands, baseline,
  optimized results, chunk counts, and conclusion.
