# MySQL Position Batch Persistence Design

Date: 2026-06-15

## Goal

Reduce database round trips for the Hub's five-second periodic position save by
updating MySQL rows in chunks with one `UPDATE ... JOIN` statement per chunk.

SQLite remains available as a legacy and development backend. New persistence
performance work targets MySQL only.

## Current Behavior

Every five seconds, the Hub collects players that moved since the previous
interval and submits one `[]PositionUpdate` to `PersistenceWorker`.

The worker runs one background goroutine and preserves per-user sequence
ordering, but currently executes one `UPDATE users` statement per player. A
4,000-player periodic batch therefore produces up to 4,000 serial database
calls.

## Scope

This phase includes:

- A MySQL-specific bulk position update operation.
- Filtering stale sequence numbers before database work.
- Splitting accepted updates into chunks of at most 500 rows.
- One independent transaction per chunk.
- One parameterized `UPDATE ... JOIN` statement per chunk.
- Continuing with later chunks after a chunk failure.
- Advancing `lastSeq` only for updates in successfully committed chunks.
- Storage-level tests for chunking, stale filtering, failure isolation, and
  sequence advancement.
- Deterministic 1,000- and 4,000-update persistence benchmarks.
- Documentation of MySQL as the production target and SQLite as legacy/dev.

This phase excludes:

- Changing the five-second persistence interval.
- Changing Hub dirty-player collection.
- Changing `SubmitSync` behavior or signature.
- Changing normal disconnect, logout, replacement, or shutdown lifecycle.
- Micro-batching final saves.
- Removing SQLite dependencies or tests.
- Adding a SQLite bulk-update implementation.
- Introducing Gin, GORM, or another database abstraction.

## Backend Behavior

`PersistenceWorker` selects its write strategy from the storage driver's
existing identity:

- MySQL uses chunked bulk position updates.
- SQLite retains the current ordered per-row update path.

The MySQL implementation remains inside `internal/storage`; realtime code does
not branch on database driver and keeps submitting `[]PositionUpdate`.

## Sequence Filtering

Before splitting a submitted batch, the worker removes updates whose sequence
is not newer than the last successfully persisted sequence for that user.

If the same submitted batch contains multiple accepted updates for one user,
only the highest-sequence update is written. This avoids duplicate IDs in the
derived table and ensures the newest accepted position wins.

Filtering does not mutate `lastSeq`. Sequence state advances only after the
corresponding chunk commits successfully.

## MySQL Bulk Update

Each chunk contains at most 500 distinct users. The storage layer builds one
parameterized statement in this shape:

```sql
UPDATE users AS u
JOIN (
    SELECT ? AS id, ? AS lat, ? AS lng
    UNION ALL
    SELECT ?, ?, ?
) AS positions ON positions.id = u.id
SET
    u.last_lat = positions.lat,
    u.last_lng = positions.lng
```

The query uses placeholders for every ID, latitude, and longitude. Values are
not interpolated into SQL text.

The implementation may generate the repeated `SELECT ?, ?, ?` fragments for
the actual chunk length. It does not retain unbounded query variants.

## Transaction And Failure Semantics

Each chunk runs in its own transaction:

1. Begin transaction.
2. Execute one bulk update statement.
3. Commit transaction.
4. Advance `lastSeq` for that chunk.

If begin, execute, or commit fails:

- Roll back that chunk where applicable.
- Do not advance `lastSeq` for any update in that chunk.
- Log one chunk-level error with enough context to identify the chunk size.
- Continue processing later chunks from the same submitted batch.

This bounds one database failure to at most 500 players rather than rolling
back the entire five-second batch.

Periodic position persistence is best-effort. A failed player's later movement
will make the player dirty again and a future interval can submit a newer
position.

## Missing Rows

Bulk updates may match fewer rows than the chunk contains, for example when a
synthetic user was removed from the database while still active in memory.

`RowsAffected` is diagnostic only and is not used to classify unchanged MySQL
rows as missing. The chunk is considered successfully persisted when the
statement executes and commits without error.

The worker advances `lastSeq` for all updates in a successfully committed
chunk. Exact missing-user detection is not added to this hot path.

## Concurrency

The persistence worker remains a single background goroutine. Chunks are
processed sequentially, preserving the existing ordered-worker model and
avoiding concurrent write bursts against MySQL.

Hub simulation and replication remain non-blocking because periodic saves
continue to use `Submit`.

## Benchmark

Add storage benchmarks for:

- 1,000 position updates.
- 4,000 position updates.

The benchmark must measure the persistence batch operation rather than Hub or
replication work and report:

- elapsed time per submitted batch
- allocations where meaningful
- executed chunk count
- rows submitted

The MySQL benchmark requires an explicitly configured test database and must
not silently fall back to SQLite. If MySQL is unavailable, ordinary tests may
skip the integration benchmark while retaining unit coverage of query
construction, chunking, and failure semantics.

Baseline measurements use the current per-row MySQL path. Optimized
measurements use the same database, rows, batch sizes, Go version, and command.

## Testing

Tests cover:

- A batch at or below 500 rows produces one chunk.
- A 1,001-row batch produces three chunks.
- Stale updates are excluded before chunking.
- Multiple updates for one user collapse to the highest sequence.
- Successful chunks advance `lastSeq`.
- Failed chunks do not advance `lastSeq`.
- A failed middle chunk does not prevent a later chunk from executing.
- SQLite continues to use the existing ordered per-row save path.
- Existing worker drain and stale-sequence behavior remains valid.

Project verification:

```bash
go test ./internal/storage
go test ./...
go vet ./...
```

## Success Criteria

The phase succeeds when:

- Existing SQLite tests continue to pass.
- MySQL periodic position batches use at most one bulk update statement per
  500 accepted users.
- A failed chunk does not block later chunks or advance failed sequences.
- The 1,000- and 4,000-update MySQL benchmarks show a clear reduction in
  database calls and elapsed batch time compared with per-row updates.
- Hub, protocol, and final-save behavior remain unchanged.

## Documentation

Update:

- `README.md`
- `AGENTS.md`
- `.env.example`
- `docs/map-walker-handoff.md`

The documentation must state:

- MySQL is the production-target backend.
- SQLite is retained for legacy and local development use.
- New performance-sensitive storage features are not required to have an
  equivalent SQLite implementation.
- Periodic MySQL position persistence uses chunked bulk updates.
- `SubmitSync` and final-save lifecycle semantics remain unchanged in this
  phase.
