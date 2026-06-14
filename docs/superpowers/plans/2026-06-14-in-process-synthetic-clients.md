# In-process Synthetic Clients Task Plan

> **For agentic workers:** Implement tasks in order. A new session should read
> `docs/superpowers/specs/2026-06-14-in-process-synthetic-clients-design.md`,
> this plan, `AGENTS.md`, and `docs/concurrency-debugging.md` before changing
> Hub or Manager concurrency.

**Goal:** Add persistent in-process Synthetic Clients that exercise the real
Hub, World, AOI, replication, send queue, and position persistence paths,
with aggregate operational statistics and no HTTP/WebSocket client transport.

---

### Task 1: Reserve Synthetic Account Names

**Task boundary:**

- Define the canonical `synthetic_<positive integer>` naming and strict parsing
  rules in `internal/synthetic`.
- Reject the `synthetic_` prefix case-insensitively through public
  registration using the existing invalid/unavailable username behavior.
- Keep login, session authentication, existing real-user accounts, and
  non-strict lookalike usernames unchanged.

**Behavioral goals:**

- Strict parsing accepts only positive base-10 suffixes.
- Public registration cannot create any username under the reserved prefix.
- Internal provisioning remains able to create canonical synthetic usernames.

**Affected modules:**

- Create `internal/synthetic/naming.go`
- Create `internal/synthetic/naming_test.go`
- Modify `internal/auth/username.go`
- Modify `internal/auth/service_test.go`

**Verification:**

- Run `go test ./internal/auth ./internal/synthetic`.
- Verify mixed-case reserved prefixes are rejected.
- Verify zero, negative, missing, and non-numeric suffixes are rejected by the
  parser.
- Verify unrelated usernames containing or resembling the prefix remain
  unaffected.

---

### Task 2: Add Synthetic Bulk Storage Operations

**Task boundary:**

- Add one bulk loader for rows selected by normalized synthetic username
  prefix, returning identity, saved position, and appearance.
- Add the storage operation needed to create a missing canonical account,
  correct fixed appearance, and initialize position only when absent.
- Keep suffix validation, numeric sorting, target-range filtering, password
  hashing, and worker orchestration outside `internal/storage`.
- Support both SQLite and MySQL without adding a schema migration.

**Behavioral goals:**

- Existing saved positions are never overwritten.
- Existing passwords are never read for comparison or changed.
- Appearance correction is idempotent.
- Bulk loading does not issue one query per target account.

**Affected modules:**

- Create `internal/storage/synthetic.go`
- Create `internal/storage/synthetic_test.go`
- Reuse types from `internal/storage/users.go`

**Verification:**

- Run `go test ./internal/storage`.
- Verify bulk loading returns all prefix matches and leaves strict filtering to
  the caller.
- Verify preparation creates missing rows, fills only absent positions,
  corrects appearance, and preserves existing password hashes and positions.
- Verify repeated preparation produces the same stored state.

---

### Task 3: Implement Placement And Provisioning

**Task boundary:**

- Implement fixed synthetic appearance and deterministic account-number-based
  placement around the configured Shanghai spawn.
- Implement the reusable bounded-worker Provisioner over the bulk storage
  operations.
- Fill every missing account in `1..N`, including gaps, while reusing existing
  accounts and ignoring higher-numbered accounts.
- Hash the configured password only for accounts that must be created.
- Continue after individual account failures and return created, reused,
  corrected, and failed totals plus per-account readiness results.

**Behavioral goals:**

- Placement is stable, remains inside the 10km by 10km activity region, and
  avoids one fully connected hotspot.
- Provisioning is idempotent and numerically ordered at its external result
  boundary.
- Worker concurrency never exceeds the configured bound.
- Successful account work is preserved when another account fails.

**Affected modules:**

- Create `internal/synthetic/placement.go`
- Create `internal/synthetic/placement_test.go`
- Create `internal/synthetic/provisioner.go`
- Create `internal/synthetic/provisioner_test.go`
- Use `internal/auth/password.go`
- Use `internal/storage/synthetic.go`

**Verification:**

- Run `go test ./internal/synthetic ./internal/storage`.
- Verify gap filling, expansion, shrink-without-delete, repeat execution,
  fixed appearance correction, and saved-position preservation.
- Verify deterministic positions for representative low and high account
  numbers remain in bounds and are spatially distributed.
- Verify injected account failures do not cancel unrelated work.

---

### Task 4: Add The Dedicated Provisioning Command

**Task boundary:**

- Add `cmd/synthetic-provision` with count, worker, database driver, and DSN
  configuration.
- Read the password only from `MAP_WALKER_SYNTHETIC_PASSWORD`.
- Report created, reused, corrected, and failed totals.
- Exit non-zero when configuration is invalid, the password is missing, or any
  account failed, without rolling back successful account work.
- Do not start a Hub, HTTP server, Synthetic Manager, or WebSocket client.

**Behavioral goals:**

- The command is safe to rerun against the same database.
- Default worker count follows `runtime.GOMAXPROCS(0)`.
- Command output clearly distinguishes full success from partial failure.

**Affected modules:**

- Create `cmd/synthetic-provision/main.go`
- Create `cmd/synthetic-provision/cli.go`
- Create `cmd/synthetic-provision/main_test.go`
- Use `internal/synthetic/provisioner.go`
- Use `internal/storage/storage.go`

**Verification:**

- Run `go test ./cmd/synthetic-provision ./internal/synthetic`.
- Run the command twice against a temporary SQLite database and verify the
  second run reuses the complete pool.
- Verify missing password and partial account failure produce a non-zero exit.
- Verify `-count 0`, negative counts, and invalid worker counts follow the
  approved configuration rules.

---

### Task 5: Implement The Synthetic ClientSender

**Task boundary:**

- Export the realtime default send-buffer capacity and use it for both browser
  and Synthetic Clients.
- Implement a Synthetic Client satisfying `realtime.ClientSender`, with one
  bounded send queue, one immediate drain goroutine, idempotent close, readiness
  notification, and client-local atomic counters.
- Establish readiness only after two initialization messages have been
  drained, without decoding JSON.
- Keep slow drain behavior test-only and do not expose it as service
  configuration.

**Behavioral goals:**

- Synthetic Clients pay the same Hub enqueue and queue-capacity cost as real
  clients.
- Normal drain counts messages and bytes, tracks queue high-water, and never
  sleeps or unmarshals payloads.
- Queue close before readiness and readiness timeout are observable activation
  failures.
- Client close and lifecycle completion are safe under repeated calls.

**Affected modules:**

- Modify `internal/realtime/client.go`
- Modify `internal/realtime/client_test.go`
- Create `internal/synthetic/client.go`
- Create `internal/synthetic/client_test.go`

**Verification:**

- Run `go test ./internal/realtime ./internal/synthetic`.
- Verify both client types use the same exported capacity.
- Verify exactly two drained messages trigger readiness.
- Verify early close, timeout, queue-full behavior, counters, high-water, and
  repeated close.
- Run targeted tests repeatedly to detect send/close races.

---

### Task 6: Implement Deterministic Movement Behavior

**Task boundary:**

- Implement account-derived deterministic direction schedules independent of
  `internal/benchmark`.
- Implement one-to-five-second direction-change intervals, neutral/cardinal/
  diagonal selection, deterministic staggering, and monotonically increasing
  input sequences.
- Implement the Manager-side estimated-position update using World movement
  speed, diagonal normalization, and local-coordinate assumptions.
- Apply inward direction filtering at the 4.5km soft boundary without
  teleporting or rewriting persisted positions.

**Behavioral goals:**

- Inputs are sent only when state changes.
- Direction generation is repeatable for the same account and seed.
- Estimated straight and diagonal movement matches authoritative World
  formulas closely enough for boundary guidance.
- Persisted positions outside the region are guided inward through normal
  input rather than reset.

**Affected modules:**

- Create `internal/synthetic/behavior.go`
- Create `internal/synthetic/behavior_test.go`
- Use `internal/game/world.go` configuration and input types
- Do not import `internal/benchmark`

**Verification:**

- Run `go test ./internal/synthetic ./internal/game`.
- Verify interval bounds, deterministic staggering, sequence progression, and
  neutral/cardinal/diagonal coverage.
- Compare estimated straight and diagonal movement with `game.World` over
  controlled 100ms steps.
- Verify each soft-boundary edge filters toward the activity region.

---

### Task 7: Implement Manager Activation, Ramp And Shutdown

**Task boundary:**

- Implement one Manager goroutine that serializes provisioning results,
  ascending activation, 100ms token-budget ramp-up, behavior scheduling, and
  lifecycle accounting.
- Bulk-load target identities before activation; when auto-provisioning is
  enabled, admit each account as soon as it is ready.
- Register each client through the real Hub and wait up to five seconds for
  readiness before marking it active.
- On activation failure or unexpected disconnect, unregister and record the
  outcome without retry, reconnect, or replacement.
- Stop scheduling and input generation before unregistering all activating and
  active clients; wait for client lifecycle completion before returning.

**Behavioral goals:**

- Ramp rate `0` activates without rate limiting; positive rates respect the
  configured clients-per-second budget.
- A client begins behavior only after both initialization messages are drained.
- Missing accounts fail activation when auto-provisioning is disabled.
- Shutdown is idempotent and is not counted as failure or unexpected
  disconnect.
- Registration, AOI, replication encoding, queue-full removal, periodic save,
  and final save remain real Hub behavior.

**Affected modules:**

- Create `internal/synthetic/manager.go`
- Create `internal/synthetic/manager_test.go`
- Integrate with `internal/synthetic/client.go`
- Integrate with `internal/synthetic/provisioner.go`
- Use `internal/realtime/hub.go`

**Verification:**

- Run `go test ./internal/synthetic ./internal/realtime`.
- Verify ascending activation, rate-limited and unlimited ramp-up, readiness
  timeout, Hub stop, missing account, and partial provisioning cases.
- Verify real Hub input changes World movement and Synthetic Clients receive
  symmetric AOI initialization and replication payloads.
- Verify queue-full removal performs final persistence and does not reconnect.
- Verify shutdown unregisters clients before Hub stop and waits for drains.
- Run concurrency-sensitive tests repeatedly and with `go test -race` where
  the local Go toolchain supports it.

---

### Task 8: Publish Immutable Synthetic Stats

**Task boundary:**

- Add immutable once-per-second Manager snapshots containing approved gauges,
  recent rates, lifetime totals, queue high-water, and sampling timestamp.
- Aggregate per-client atomic drain counters once per second.
- Keep lifecycle and input counters owned by the Manager goroutine.
- Do not retain history, enumerate clients in snapshots, or add dynamic
  controls.

**Behavioral goals:**

- Snapshot reads are concurrent and do not block Manager scheduling.
- Recent rates represent the latest completed one-second interval.
- Lifetime totals are monotonic.
- Moving and idle gauges classify active clients only.

**Affected modules:**

- Create `internal/synthetic/stats.go`
- Create `internal/synthetic/stats_test.go`
- Modify `internal/synthetic/client.go`
- Modify `internal/synthetic/manager.go`

**Verification:**

- Run `go test ./internal/synthetic`.
- Verify gauge transitions through provisioning, activating, active,
  disconnect, failure, and shutdown.
- Verify per-client deltas aggregate into correct recent rates and lifetime
  totals.
- Verify previously returned snapshots do not mutate after later ticks.
- Verify concurrent snapshot reads under the race detector where supported.

---

### Task 9: Publish The Latest Completed Hub Stats

**Task boundary:**

- Extend existing Hub interval statistics with connected-client count and an
  immutable latest-completed snapshot.
- Publish exactly the interval values already emitted by one-second Hub
  logging.
- Keep the Hub unaware of synthetic identity and preserve the single actor-loop
  ownership model.
- Do not add live traversal APIs over World, AOI, or the client map.

**Behavioral goals:**

- Hub snapshots describe all clients without classification.
- Snapshot values and the corresponding completed log interval agree.
- Concurrent readers cannot mutate or race with actor-owned counters.
- Existing Hub logging cadence and reset behavior remain unchanged.

**Affected modules:**

- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go`
- Create `internal/realtime/stats.go`

**Verification:**

- Run `go test ./internal/realtime`.
- Verify connected clients, simulation ticks, moved players, AOI checks,
  relationship changes, replication messages, recipients, and bytes.
- Verify the published snapshot remains stable until the next completed stats
  interval.
- Verify snapshot reads after Hub stop return the last completed interval.

---

### Task 10: Wire Service Configuration And Lifecycle

**Task boundary:**

- Add service flags for synthetic client count, ramp rate, and
  auto-provisioning with the approved defaults and validation.
- Construct and start the Manager only after database, Hub, and HTTP service
  dependencies are available.
- Run optional provisioning in the background without delaying or terminating
  real-user service availability.
- Implement shutdown ordering: HTTP shutdown, Manager stop and client
  unregister, Hub stop, database close.
- Keep current opaque-session authentication and default zero-client behavior
  unchanged.

**Behavioral goals:**

- Negative count and ramp rate fail startup configuration validation.
- Missing synthetic password or provisioning failure affects synthetic status
  only when service auto-provisioning is enabled.
- With `-synthetic-clients 0`, no synthetic goroutines or account work are
  started.
- Hub final persistence completes before the database closes.

**Affected modules:**

- Modify `cmd/map-walker/main.go`
- Add focused command configuration/lifecycle tests under `cmd/map-walker`
- Use `internal/synthetic/manager.go`
- Use `internal/synthetic/provisioner.go`

**Verification:**

- Run `go test ./cmd/map-walker ./internal/synthetic ./internal/realtime`.
- Verify flag defaults and invalid negative values.
- Verify zero-client startup preserves current behavior.
- Verify real-user HTTP availability during background provisioning failure.
- Verify shutdown ordering with active and activating Synthetic Clients.

---

### Task 11: Add The Token-Protected Admin API

**Task boundary:**

- Add optional `/admin` and `GET /api/admin/synthetic-stats` routes controlled
  by `MAP_WALKER_ADMIN_TOKEN`.
- Return `404` for both routes when the token is missing or empty.
- Require `Authorization: Bearer <token>` for the stats endpoint and compare
  tokens with `subtle.ConstantTimeCompare`.
- Return only the latest Synthetic Manager snapshot, latest all-client Hub
  snapshot, and sampling timestamps.
- Do not enumerate live clients, traverse actor state, retain history, or
  expose control endpoints.

**Behavioral goals:**

- Missing or invalid configured credentials return `401`.
- A valid token returns aggregate JSON only.
- Admin handlers read immutable snapshots and do not coordinate with actor
  loops.
- Existing public routes and session authentication behavior remain unchanged.

**Affected modules:**

- Modify `internal/server/server.go`
- Create `internal/server/admin.go`
- Create `internal/server/admin_test.go`
- Modify `server.New` to receive the admin token and immutable snapshot
  providers
- Use `internal/synthetic/stats.go`
- Use `internal/realtime/stats.go` or Hub snapshot accessor

**Verification:**

- Run `go test ./internal/server`.
- Verify unconfigured routes return `404`.
- Verify missing, malformed, and wrong Bearer tokens return `401`.
- Verify a correct token returns both aggregate snapshots and timestamps.
- Verify the response contains no client identities, positions, or controls.

---

### Task 12: Add The Read-only Admin Page And Complete Project Verification

**Task boundary:**

- Add a dedicated admin page that accepts an operator token, stores it only in
  tab-scoped `sessionStorage`, polls once per second, and renders numeric cards
  with simple status color.
- Label Hub values as all-client statistics.
- Exclude charts, history, client lists, start/stop actions, and resize
  controls.
- Update project documentation for the new command, flags, environment
  variables, routes, architecture, included/excluded costs, and shutdown
  behavior.

**Behavioral goals:**

- The admin page never persists the token in cookies, localStorage, URL, or
  server-rendered markup.
- Polling sends the configured Bearer token and handles unauthorized or
  unavailable stats without affecting the main application.
- Documentation keeps the Synthetic Client phase distinct from deferred
  localhost WebSocket bots and JWT authentication.
- Existing map UI and WebSocket protocol remain unchanged.

**Affected modules:**

- Create `web/admin.html`
- Create `web/admin.js`
- Create `web/admin.css`
- Modify `internal/server/admin.go`
- Modify `internal/server/admin_test.go`
- Modify `README.md`
- Modify `AGENTS.md`
- Modify `docs/map-walker-handoff.md`

**Verification:**

- Run `go test ./...`.
- Run `go vet ./...`.
- Start the service with zero Synthetic Clients and verify current registration,
  login, movement, appearance, AOI, and shutdown behavior.
- Start with a pre-provisioned pool and verify ramp-up, movement, aggregate
  stats, admin authorization, and graceful shutdown.
- Verify the admin page polls once per second and uses `sessionStorage`.
- Verify `/admin` and the stats API both return `404` without admin
  configuration.
- Confirm no HTTP/WebSocket bot, JWT migration, dynamic resizing, gameplay AI,
  or multi-Hub behavior was introduced.
