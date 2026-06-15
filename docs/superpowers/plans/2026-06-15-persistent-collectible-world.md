# Persistent Collectible World Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Track each
> task with its checkbox.

**Goal:** Add a server-authoritative, continuously available map collection
activity with permanent `+1` scoring, AOI-filtered collectibles, synthetic
account exclusion, and an on-demand online leaderboard.

**Architecture:** Keep collectible simulation and spatial queries as pure
`internal/game` logic owned by the existing Hub actor. Load fixed regions from
JSON, replicate collectible changes per client, and persist coalesced absolute
score snapshots through an independent asynchronous worker. Carry
`is_synthetic` from storage through authentication into trusted connection
identity so synthetic clients remain visible but cannot collect or rank.

**Tech Stack:** Go 1.26, existing Hub actor and AOI grid patterns,
`database/sql`, SQLite/MySQL forward migrations, JSON configuration,
WebSocket protocol, Leaflet, vanilla JavaScript and CSS.

**Required context:** Read
`docs/superpowers/specs/2026-06-15-persistent-collectible-world-design.md`,
`AGENTS.md`, and `docs/concurrency-debugging.md` before implementation.

**Execution order:** Complete tasks in order. Task 4 freezes the protocol and
must be committed before any protocol consumer changes. Tasks 5-7 all modify
the Hub and must be executed serially by one realtime owner.

---

### Task 1: Add Region Configuration And Pure Collectible Field

- [ ] Implement validated region loading, collectible lifecycle, replacement
  scheduling, and collectible spatial queries entirely inside `internal/game`.

**Task boundary:**

- Add JSON types and a loader for exactly three fixed circular regions.
- Reject unreadable or malformed files, empty or duplicate IDs, invalid
  coordinates or numeric bounds, inverted respawn bounds, and overlapping
  circles.
- Add a committed `config/collectible-regions.json` containing three
  non-overlapping Shanghai-area regions.
- Add a pure collectible field with process-local IDs, 600-meter cells,
  initial target population, pickup removal, delayed replacement, and bounded
  placement retries.
- Expose queries needed for 500-meter entry, 600-meter leave, 10-meter pickup,
  ID lookup, and region geometry.
- Inject deterministic clock/random seams for tests.
- Do not add Hub, protocol, database, or frontend behavior.

**Behavioral goals:**

- Each region starts with and returns to 20 live collectibles.
- Every generated point remains inside its owning 200-meter circle.
- A collected item disappears immediately and produces exactly one
  replacement after its configured 5-15 second delay.
- Nearby queries inspect grid-local candidates rather than all collectibles.
- The model owns no goroutine, lock, connection, or database handle.

**Affected modules:**

- Create `config/collectible-regions.json`
- Create `internal/game/collectible_config.go`
- Create `internal/game/collectible_config_test.go`
- Create `internal/game/collectible.go`
- Create `internal/game/collectible_test.go`
- Reuse coordinate conversion helpers from `internal/game/aoi.go` or
  `internal/game/world.go` without changing player AOI behavior

**Verification:**

- Run `go test ./internal/game`.
- Repeat replacement and boundary tests with `-count=20`.
- Run `go test -race ./internal/game`.
- Run `go vet ./internal/game`.

---

### Task 2: Persist Score And Trusted Synthetic Identity

- [ ] Add user score and synthetic-account identity to storage, auth, and the
  synthetic provisioner.

**Task boundary:**

- Add a forward migration for `collectible_score BIGINT NOT NULL DEFAULT 0`
  and `is_synthetic BOOLEAN NOT NULL DEFAULT FALSE`.
- Extend user reads and saved-player state with score and synthetic identity.
- Keep normal registration permanently non-synthetic.
- Make single and bulk synthetic creation set `is_synthetic=true`.
- Make provisioning correct existing synthetic accounts whose marker is
  false, independently of appearance and position correction.
- Load synthetic accounts by the trusted marker rather than username prefix;
  retain username parsing only for mapping managed account numbers.
- Carry score and synthetic identity through `auth.User` without exposing a
  client-controlled write path.
- Do not add score update queries or Hub behavior in this task.

**Behavioral goals:**

- Existing users migrate with score zero and non-synthetic identity.
- New and reused provisioned accounts are reliably marked synthetic.
- A human logging into a synthetic account retains its synthetic identity.
- Session authentication returns server-trusted score and identity.
- SQLite and MySQL migrations remain forward-only and compatible.

**Affected modules:**

- Create `internal/storage/migrations/002_collectibles.sql`
- Modify `internal/storage/users.go`
- Modify `internal/storage/users_test.go`
- Modify `internal/storage/synthetic.go`
- Modify `internal/storage/synthetic_test.go`
- Modify `internal/auth/service.go`
- Modify `internal/auth/service_test.go`
- Modify `internal/synthetic/provisioner.go`
- Modify `internal/synthetic/provisioner_test.go`

**Verification:**

- Run `go test ./internal/storage ./internal/auth ./internal/synthetic`.
- Run focused migration tests against a pre-002 SQLite database.
- Repeat synthetic correction tests with `-count=20`.
- Run `go test -race ./internal/storage ./internal/auth ./internal/synthetic`.
- Run `go vet ./internal/storage ./internal/auth ./internal/synthetic`.

---

### Task 3: Add Coalescing Score Persistence Worker

- [ ] Implement asynchronous monotonic score persistence with latest-snapshot
  coalescing, retry backoff, drain, and synchronous final submission.

**Task boundary:**

- Add `ScoreUpdate` and a score persister lifecycle separate from position
  persistence.
- Keep at most one pending highest score per user.
- Use backend-appropriate monotonic updates so repeated or stale snapshots
  cannot reduce or duplicate score.
- Treat a successful statement with zero affected rows as success.
- Retry failures with exponential backoff capped at 30 seconds.
- Ensure a successful older in-flight write cannot clear a newer pending
  snapshot.
- Provide asynchronous submit for pickups and synchronous submit/drain for
  disconnect and shutdown.
- Keep one worker goroutine; do not parallelize retries or block the Hub.
- Do not integrate the worker into realtime yet.

**Behavioral goals:**

- Failed `42`, followed by `43` and `44`, retries only the latest score `44`.
- Duplicate delivery of score `44` remains idempotent.
- Persisted score never decreases.
- One user's failure does not discard another user's pending score.
- Drain waits for pending work within the caller-controlled shutdown path.

**Affected modules:**

- Create `internal/storage/score.go`
- Create `internal/storage/score_test.go`
- Create `internal/storage/score_persistence.go`
- Create `internal/storage/score_persistence_test.go`

**Verification:**

- Run `go test ./internal/storage`.
- Repeat coalescing, stale completion, retry, and drain tests with `-count=20`.
- Run `go test -race ./internal/storage`.
- Run `go vet ./internal/storage`.

---

### Task 4: Freeze Collectible WebSocket Protocol

- [ ] Define and test all collectible message shapes before changing Hub,
  Client, or browser consumers.

**Task boundary:**

- Add client `collect` decoding with `collectibleId`.
- Extend `self_state` with the authoritative permanent score.
- Add `collectible_regions` initialization encoding with public geometry only.
- Add `visible_collectibles_snapshot` initialization encoding.
- Add `collect_result` success encoding.
- Extend per-client `replication_update` with entered, left, spawned, and
  collected collectible changes.
- Define normalization rules that prevent the same collectible appearing in
  contradictory collections in one update.
- Preserve existing player protocol fields and empty-update behavior.
- Do not modify Hub, Client read loops, server endpoints, or frontend
  consumers in this task.

**Behavioral goals:**

- Protocol JSON is deterministic and omits empty optional collections.
- Region messages never expose target count or respawn timing.
- Collected and left remain distinct wire events.
- Existing movement and appearance messages remain backward-compatible within
  this repository.

**Affected modules:**

- Modify `internal/realtime/messages.go`
- Modify `internal/realtime/messages_test.go`

**Verification:**

- Run `go test ./internal/realtime -run 'Message|Replication|Collectible'`.
- Repeat normalization tests with `-count=20`.
- Run `go test ./internal/realtime`.
- Commit the protocol task before starting Task 5.

---

### Task 5: Carry Trusted Player Identity And Initialize Collectibles

- [ ] Wire region configuration, score persistence, trusted synthetic identity,
  and collectible initialization into application startup and Hub connection
  setup.

**Task boundary:**

- Add `-collectible-regions` with default
  `config/collectible-regions.json`.
- Load and validate regions before starting the HTTP server.
- Construct the collectible field and score worker in `cmd/map-walker`.
- Extend trusted client identity with synthetic status; browser clients use
  authenticated storage identity and in-process synthetic clients always
  identify as synthetic.
- Extend saved-player loading with permanent score and synthetic status.
- Add score to authoritative player state while keeping synthetic status
  server-only.
- On connection, send the existing player initialization followed by region
  geometry and the AOI-filtered collectible snapshot.
- Update synthetic-client readiness to require all four initialization
  messages rather than the existing two.
- Preserve same-account replacement state and initialization semantics.
- Do not yet process pickup requests, respawn changes, or leaderboard queries.

**Behavioral goals:**

- Configuration failure prevents partial service startup.
- Real and synthetic clients traverse the same connection lifecycle.
- A reconnect restores the database score; same-account replacement retains
  the newer in-memory score.
- Every client receives only collectible instances within its initial AOI.
- Synthetic identity cannot be supplied by browser protocol data.

**Affected modules:**

- Modify `cmd/map-walker/main.go`
- Modify `cmd/map-walker/main_test.go`
- Modify `internal/game/world.go`
- Modify `internal/game/world_test.go`
- Modify `internal/realtime/client.go`
- Modify `internal/realtime/client_test.go`
- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go`
- Modify `internal/realtime/manual_hub.go`
- Modify `internal/realtime/player_load.go`
- Modify `internal/realtime/persistence.go`
- Modify `internal/server/websocket.go`
- Modify `internal/server/websocket_test.go`
- Modify `internal/synthetic/client.go`
- Modify `internal/synthetic/client_test.go`

**Verification:**

- Run `go test ./cmd/map-walker ./internal/game ./internal/realtime ./internal/server ./internal/synthetic`.
- Repeat connection replacement and initialization tests with `-count=20`.
- Run `go test -race ./internal/realtime ./internal/server ./internal/synthetic`.
- Confirm the Hub remains the only mutable gameplay-state owner.

---

### Task 6: Add Collectible AOI Replication And Respawn Fan-Out

- [ ] Integrate collectible visibility changes and delayed respawns into the
  Hub's existing simulation and broadcast flow.

**Task boundary:**

- Track each client's visible collectible IDs with 500/600-meter hysteresis.
- Recalculate collectible visibility only for players moved by simulation.
- Advance due collectible replacements from an existing Hub tick.
- For newly spawned items, query nearby players through the player AOI grid
  and accumulate per-recipient spawned changes.
- Add a focused AOI query that returns players from the nine cells around an
  arbitrary latitude/longitude for collectible reverse fan-out; do not expose
  AOI internal maps to realtime.
- Accumulate entered, left, spawned, and collected buffers per client and
  consume them on the broadcast tick.
- Clear obsolete pending state on disconnect and same-account replacement.
- Extend replication stats only where needed to preserve existing totals.
- Do not scan every player for every collectible or every collectible for every
  player.
- Do not add pickup requests in this task.

**Behavioral goals:**

- Moving players receive correct collectible entered and left events.
- New items are sent only to nearby recipients.
- Respawn restores region target counts without creating extra goroutines.
- One client cannot receive contradictory collectible changes in one update.
- Existing player reverse fan-out payloads and AOI behavior remain unchanged.

**Affected modules:**

- Modify `internal/game/aoi.go`
- Modify `internal/game/aoi_test.go`
- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go`
- Modify `internal/realtime/aoi_scale_test.go` only for collectible logical
  metrics or regression coverage
- Modify `internal/realtime/stats.go` only if aggregate payload accounting
  requires new fields

**Verification:**

- Run focused collectible visibility and respawn tests with `-count=20`.
- Run `go test ./internal/game ./internal/realtime`.
- Run `go test -race ./internal/game ./internal/realtime`.
- Run the existing deterministic AOI scale test.
- Confirm no all-player/all-collectible nested scan is introduced.

---

### Task 7: Add Authoritative Pickup And Score Lifecycle

- [ ] Process explicit pickup requests in the Hub, award exactly one point,
  send winner-only feedback, and persist scores without blocking gameplay.

**Task boundary:**

- Decode `collect` in the realtime Client read loop and submit a typed Hub
  event tied to the sending connection.
- Apply a 300-millisecond server cooldown before item lookup work.
- Record the cooldown timestamp for every request that passes the cooldown,
  before collectible validation, so invalid IDs cannot bypass throttling.
- Reject obsolete connections, synthetic accounts, missing or invisible
  items, and authoritative distances greater than 10 meters.
- Resolve competing requests serially in `Hub.Run()`.
- On success, remove the item, schedule replacement, increment score, submit
  the absolute score asynchronously, and send `collect_result` only to the
  winner.
- Reverse-fan out collected-item removal only to nearby visible recipients.
- On genuine disconnect, logout, and graceful shutdown, synchronously submit
  the latest score and drain the score worker alongside position persistence.
- Preserve same-account replacement without final score submission or score
  rollback.
- Do not add leaderboard calculation in this task.

**Behavioral goals:**

- One collectible produces at most one awarded point.
- Client-supplied position, score, and synthetic identity are ignored.
- Invalid request floods are bounded by per-player server cooldown.
- Database latency and retry never enter the simulation or pickup critical
  path.
- Disconnect and shutdown retain existing ordering guarantees for position
  saves while adding score submission.

**Affected modules:**

- Modify `internal/realtime/client.go`
- Modify `internal/realtime/client_test.go`
- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go`
- Modify `internal/realtime/persistence.go`
- Modify `internal/realtime/persistence_test.go`
- Modify `internal/server/auth_test.go` only if logout ordering assertions need
  score coverage
- Modify `cmd/map-walker/main.go`

**Verification:**

- Run pickup winner, cooldown, synthetic, stale ID, distance, and obsolete
  connection tests with `-count=20`.
- Run disconnect, logout, replacement, and shutdown persistence tests with
  `-count=20`.
- Run `go test ./internal/realtime ./internal/server ./cmd/map-walker`.
- Run `go test -race ./internal/storage ./internal/realtime ./internal/server`.
- Confirm no database call executes inside `Hub.Run()`.

---

### Task 8: Add On-Demand Online Leaderboard

- [ ] Add one authenticated HTTP query that asks the Hub for a current
  real-player ranking without caching or polling.

**Task boundary:**

- Add an actor request/response path that returns immutable online ranking
  data.
- Filter synthetic accounts and offline users.
- Sort by score descending and player ID ascending.
- Return Top 5 plus the requesting player's online rank and score.
- Omit `self` when the authenticated account is not currently connected.
- Add `GET /api/leaderboard/online` with session authentication and method
  validation.
- Perform no database ranking, periodic sort, cache, heap, tree, or push
  update.

**Behavioral goals:**

- Ranking work occurs only when an authenticated user opens the leaderboard.
- Synthetic clients never appear in `top` or affect real-player rank.
- Ties are deterministic.
- HTTP reads do not race with Hub mutations.

**Affected modules:**

- Create `internal/server/leaderboard.go`
- Create `internal/server/leaderboard_test.go`
- Modify `internal/server/server.go`
- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go`

**Verification:**

- Run `go test ./internal/realtime ./internal/server`.
- Repeat ordering and concurrent connect/disconnect tests with `-count=20`.
- Run `go test -race ./internal/realtime ./internal/server`.
- Verify unauthenticated and wrong-method responses.

---

### Task 9: Add Browser Collection Interaction

- [ ] Render regions and collectible state, add explicit pickup controls and
  score feedback, and expose the on-demand leaderboard.

**Task boundary:**

- Draw the three public regions as non-interactive translucent Leaflet circles.
- Render visible collectibles as gold glowing points.
- Track authoritative collectible snapshots and replication changes.
- Select and highlight the nearest visible item within 10 meters using the
  latest authoritative self position.
- Bind desktop `J` and a circular lower-right touch button to the same pickup
  action.
- Disable pickup when disconnected, no target exists, or the shared
  300-millisecond client cooldown is active.
- Do not queue requests or optimistically remove items or increment score.
- Use `collect_result` for the `+1` animation and authoritative score display.
- Add an online leaderboard control that fetches once when opened, displays
  Top 5 and self rank, and does not automatically refresh.
- Keep synthetic behavior unchanged; synthetic clients do not decode browser
  UI state or send collect messages.

**Behavioral goals:**

- Desktop and touch users have equivalent pickup behavior.
- Normal repeat input produces at most one request per client cooldown.
- Stale client targets are corrected by server replication.
- Region, collectible, score, and leaderboard UI reset cleanly on logout and
  reconnect.
- Existing movement, authentication, appearance, and account controls remain
  usable.

**Affected modules:**

- Modify `web/index.html`
- Modify `web/app.js`
- Modify `web/styles.css`

**Verification:**

- Run existing Go tests to catch embedded protocol/server regressions.
- Start the service and verify desktop `J`, touch button, disabled states,
  `+1` feedback, logout reset, and reconnect restoration.
- Verify two browsers racing for one item show one winner.
- Verify opening the leaderboard performs one request and closing it stops all
  activity.
- Verify synthetic players move through collection regions without removing
  items.

---

### Task 10: Add Scale Regression Coverage

- [ ] Extend deterministic load coverage to prove collectible work stays local
  and does not regress existing player replication.

**Task boundary:**

- Add a deterministic scenario with many moving players, three regions, and
  the configured 60 live collectibles.
- Assert collectible candidate checks or equivalent diagnostics scale with
  moved players and local density, not total player-item Cartesian product.
- Exercise spawn and collection reverse fan-out with dense and sparse player
  placement.
- Confirm synthetic pickup rejection does not alter movement or player
  replication totals.
- Keep this as functional/performance regression evidence, not a production
  capacity claim.
- Do not optimize unless the measured scenario identifies a regression.

**Behavioral goals:**

- The implementation has evidence against accidental `O(P*C)` broadcast
  scans.
- Existing deterministic player replication metrics remain stable.
- Collectible payloads reach only intended recipients.

**Affected modules:**

- Create `internal/realtime/collectible_scale_test.go`
- Modify `internal/game/collectible.go` only if diagnostic counters require a
  read-and-reset API
- Modify `internal/realtime/stats.go` only if the scenario consumes immutable
  counters

**Verification:**

- Run `go test ./internal/realtime -run CollectibleScale -count=5`.
- Run `go test ./internal/realtime -run 'AOIScale|CollectibleScale'`.
- Run `go test -race ./internal/realtime`.
- Record observed diagnostic counts in test logs without adding unsupported
  capacity claims.

---

### Task 11: Document Gameplay And Complete Verification

- [ ] Update project documentation and verify the complete collectible world
  against both storage backends and existing workflows.

**Task boundary:**

- Document the collection loop, `J` and touch controls, permanent `+1` score,
  three configured regions, delayed respawn, and restart behavior.
- Document synthetic account marking, non-participation in collection, and
  leaderboard exclusion.
- Add region configuration and leaderboard endpoint to README operational and
  API sections.
- Update protocol documentation with collectible initialization, replication,
  pickup intent, and success result.
- Update the project layout, message list, migration list, and handoff status.
- Preserve the recorded MySQL position batch benchmark and existing
  limitations.
- Do not claim zero-loss score durability or multi-process world support.

**Behavioral goals:**

- A reviewer can start the service, understand the gameplay, and exercise it
  without reading source code.
- Documentation distinguishes permanent player score from ephemeral item
  instances.
- Claims match implemented AOI, retry, leaderboard, and shutdown behavior.

**Affected modules:**

- Modify `README.md`
- Modify `AGENTS.md`
- Modify `.env.example` only if the collectible config path is exposed there
- Modify `docs/map-walker-handoff.md`
- Review
  `docs/superpowers/specs/2026-06-15-persistent-collectible-world-design.md`
  for implementation drift

**Verification:**

- Run `go test ./internal/game`.
- Run `go test ./internal/storage`.
- Run `go test ./internal/realtime`.
- Run `go test ./internal/server`.
- Run `go test ./internal/synthetic`.
- Run `go test ./...`.
- Run
  `go test -race ./internal/game ./internal/storage ./internal/realtime ./internal/server ./internal/synthetic`.
- Run `go vet ./...`.
- Run `git diff --check`.
- Verify SQLite local startup and MySQL migration/startup.
- Complete two-browser and synthetic-client manual verification described in
  the design.
