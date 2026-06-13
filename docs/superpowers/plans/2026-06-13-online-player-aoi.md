# Online Player AOI Implementation Plan

> **For agentic workers:** Implement tasks in order. Use the approved design as
> the source of truth and track each task with its checkbox.

**Goal:** Replace global realtime broadcasts with symmetric, distance-based AOI
replication for online players.

**Architecture:** `game.World` remains the 20 Hz authoritative simulation.
A reusable `AOIIndex` owns the 600m Cell grid and symmetric visibility
relationships. `Hub.Run()` updates AOI and sends at most one non-empty,
client-specific replication batch every 100ms.

**Tech Stack:** Go 1.26, WebSocket, plain JavaScript, Leaflet, deterministic Go
tests.

**Required context:** Read `AGENTS.md`, `docs/map-walker-handoff.md`,
`docs/concurrency-debugging.md`, and
`docs/superpowers/specs/2026-06-13-online-player-aoi-design.md` before
implementation. The approved design is authoritative when this plan is
ambiguous.

---

## Scope Guardrails

This plan covers AOI for connected players in the current single Hub and
World. It includes local spatial indexing, visibility lifecycle, protocol
replacement, client rendering, AOI statistics, and a 1,000-client in-memory
functional scale test.

It excludes synthetic entities, million-entity benchmarks, localhost
WebSocket load generation, multiple Shards, cross-Shard migration, Gateway
routing, interaction/combat AOI, client prediction, and interpolation.

---

### Task 1: Build The Local Cell And Visibility Index

- [ ] Deliver a reusable AOI index with local coordinates, Cell membership,
  exact distance checks, hysteresis, and symmetric relationships.

**Task boundary:**

- Add `AOIIndex` under `internal/game/` without WebSocket, JSON, Hub, storage,
  or client dependencies.
- Use `game.Config.SpawnLat` and `SpawnLng` as the local coordinate origin;
  defaults remain `31.2304, 121.4737`.
- Do not load Shanghai administrative boundaries or data from Gaode or another
  map provider.
- Define each Cell as a 600m square aligned with local east-west and
  north-south axes.
- Calculate Cell coordinates with `floor(localCoordinate / cellSizeMeters)`;
  negative Cell coordinates are valid.
- Treat the grid as mathematically extendable in all directions. “Shanghai
  area” is a projection accuracy assumption, not a movement boundary.
- Maintain player position, Cell membership, and a symmetric string-ID
  adjacency set.
- Support insert, move, visible-neighbor lookup, relationship recalculation,
  and removal.

**Behavioral goals:**

- Nine-Cell candidate lookup contains every possible player within the 500m
  enter radius.
- Invisible pairs enter at `<=500m`.
- Visible pairs remain visible from 500m through 600m.
- Visible pairs leave at `>600m`.
- Every relationship add or remove updates both sides.
- Existing visible neighbors are checked even after a large move puts them
  outside the new nine Cells.
- Reprocessing the same pair is idempotent and creates no duplicate changes.
- AOIIndex remains reusable by the later load-testing phase.

**Affected modules:**

- Create `internal/game/aoi.go`
- Create `internal/game/aoi_test.go`
- Modify `internal/game/world.go` only if shared coordinate constants or
  configuration access require it.

**Verification:**

- Test origin conversion and positive/negative Cell coordinates.
- Test same-Cell and cross-Cell movement.
- Test nine-Cell candidate coverage and exact-distance filtering.
- Test exact 500m entry, hysteresis retention, and beyond-600m removal.
- Test symmetric add/remove, stationary-peer updates, duplicate processing,
  large moves, and player removal.
- Run `go test ./internal/game`.

---

### Task 2: Prepare World State For AOI Replication

- [ ] Deliver focused World queries and movement-change consumption without
  retaining the global broadcast model.

**Task boundary:**

- Preserve World as the sole owner of authoritative player state and 20 Hz
  movement.
- Provide focused lookup of complete player state and position by player ID for
  AOI snapshots and replication assembly.
- Preserve movement dirty information across multiple simulation Ticks until
  the 10 Hz replication path consumes it.
- Separate complete public player state from position-only replication data.
- Retire World APIs and data structures whose only purpose is global
  `world_snapshot` or `players_delta` broadcasting.
- Do not put Cell membership, visibility sets, clients, or JSON encoding into
  World.

**Behavioral goals:**

- Two 50ms simulation Steps can produce one latest position update at the next
  100ms replication Tick.
- Static players are not reported as moved.
- Complete state retains ID, username, position, and appearance.
- Position replication carries only ID, latitude, and longitude.
- Movement, input sequencing, appearance mutation, removal, spawn, and
  persistence position lookup preserve current semantics.

**Affected modules:**

- `internal/game/world.go`
- `internal/game/world_test.go`
- `internal/game/appearance.go`

**Verification:**

- Test moved-player accumulation and one-time consumption.
- Test complete-state and position-only lookups.
- Test movement preserves username and appearance.
- Test static, removed, and missing players are handled consistently.
- Run `go test ./internal/game`.

---

### Task 3: Replace The Realtime Server Protocol

- [ ] Deliver `self_state`, `visible_entities_snapshot`, and
  `replication_update` encoding contracts.

**Task boundary:**

- Remove server encoding for `world_snapshot`, `players_delta`, and standalone
  `appearance_changed`.
- Add complete self initialization and complete visible-entity snapshot
  messages.
- Add one optional-field replication message containing `selfPosition`,
  `entered`, `leftPlayerIds`, `positions`, and `appearances`.
- Omit unchanged fields and skip encoding/sending an entirely empty update.
- Encode entered entities as complete public state and positions as
  position-only state.
- Keep the existing client-to-server `input` message unchanged.

**Behavioral goals:**

- Self never appears in public entity collections.
- `entered` excludes the same ID from `positions` and `appearances`.
- `leftPlayerIds` excludes the same ID from every other public field.
- A continuously visible player may appear in both `positions` and
  `appearances`.
- Repeated appearance updates within one period encode only the final value.
- JSON field names and `omitempty` behavior match the approved spec exactly.

**Affected modules:**

- `internal/realtime/messages.go`
- `internal/realtime/messages_test.go`
- `internal/game/world.go`

**Verification:**

- Assert exact JSON for all three server message types.
- Assert empty optional fields are omitted.
- Assert empty replication updates are skipped.
- Assert precedence and self-exclusion rules.
- Confirm input decoding still ignores client coordinates.
- Run `go test ./internal/realtime ./internal/game`.

---

### Task 4: Integrate AOI With Connection Lifecycle

- [ ] Deliver AOI-aware first connection, connection replacement, disconnect,
  and initialization snapshots.

**Task boundary:**

- Construct AOIIndex from the World origin and AOI configuration when creating
  the Hub.
- On first connection or true offline reconnect, add the World player, insert
  it into AOI, and establish `<=500m` relationships.
- Immediately send `self_state` followed by
  `visible_entities_snapshot`.
- Queue the new player's complete state for existing visible neighbors on the
  next replication Tick.
- On same-account replacement, preserve World state, Cell membership, and
  visibility relationships.
- Build the replacement snapshot from the retained visible set, including
  players in the hysteresis band.
- Treat initialization snapshots as the new connection baseline and clear
  older pending changes addressed to that player ID.
- On true disconnect, preserve final position save ordering, remove all AOI
  relationships, and queue left notifications for connected former neighbors.

**Behavioral goals:**

- First connection receives only self and currently visible players.
- Existing neighbors see a newly connected player on the next Tick.
- Replacement refresh causes no entered/left flicker for neighbors.
- A replacement snapshot may retain a player at 500m-600m.
- A true disconnect produces left updates within the next 100ms.
- A true offline reconnect establishes only relationships currently within
  500m.
- Initialization enqueue failure and slow-client behavior continue to close
  the affected connection.
- Final position persistence, logout, and shutdown ordering do not regress.

**Affected modules:**

- `internal/realtime/hub.go`
- `internal/realtime/hub_test.go`
- `internal/realtime/player_load.go`
- `internal/realtime/persistence_test.go`
- `cmd/map-walker/main.go`

**Verification:**

- Test first connection, nearby and distant snapshots, and neighbor entry.
- Test same-account replacement in the hysteresis band without neighbor
  messages.
- Test true disconnect and true offline reconnect.
- Test pending-change baseline reset on replacement.
- Test initialization backpressure and obsolete-client unregister safety.
- Run `go test ./internal/realtime`.

---

### Task 5: Build The 10 Hz Client-Specific Replication Pipeline

- [ ] Deliver moved-player AOI recalculation and one batched update per affected
  client per replication Tick.

**Task boundary:**

- Keep 20 Hz simulation limited to World movement and moved-ID accumulation.
- At the 10 Hz Tick, update AOI only for players moved during the period.
- Apply relationship changes to both the moving and stationary players.
- Assemble final per-client changes after AOI relationships are updated.
- Include `selfPosition` only when that client moved.
- Include positions only for moved players visible both before and after the
  Tick.
- Apply left, entered, position, and appearance precedence before encoding.
- Send at most one non-empty replication message per client per Tick.
- Clear consumed movement and pending replication state after the Tick.

**Behavioral goals:**

- Cost is driven by moved players and local candidates rather than every
  online player.
- A stationary player receives symmetric entry or exit caused by a moving
  peer.
- A new entry carries complete state and no duplicate position or appearance.
- A leaving entity contributes only its ID.
- Invisible clients receive no public data for that player.
- Static clients with no changes receive no empty frame.
- Slow clients remain subject to the existing bounded-send-queue
  disconnection policy.

**Affected modules:**

- `internal/realtime/hub.go`
- `internal/realtime/hub_test.go`
- `internal/realtime/messages.go`
- `internal/game/aoi.go`
- `internal/game/world.go`

**Verification:**

- Use deterministic simulation and replication channels.
- Test two simulation Ticks feeding one replication Tick.
- Test moved-only recalculation, symmetric stationary-peer results, entry,
  hysteresis, exit, and no-change behavior.
- Test per-client filtering and one-message maximum.
- Test precedence and absence of duplicate IDs across fields.
- Test slow-client removal does not corrupt iteration or AOI relationships.
- Run `go test ./internal/realtime ./internal/game`.

---

### Task 6: Batch Appearance Replication And Add AOI Statistics

- [ ] Deliver appearance changes through the 10 Hz replication batch and expose
  useful AOI counters.

**Task boundary:**

- Preserve HTTP persistence-before-Hub ordering for appearance updates.
- Have Hub apply appearance immediately to authoritative World state and mark
  the final appearance pending for replication.
- Do not send standalone `appearance_changed`.
- At the next Tick, send appearance to the owner and players visible at the end
  of the Tick.
- Suppress duplicate appearance data for entered or departed entities.
- Extend realtime statistics with moved players, candidate pairs, exact
  distance checks, entered/left relationships, non-empty replication messages,
  recipients, and encoded payload bytes.
- Do not add strict production latency thresholds in this phase.

**Behavioral goals:**

- HTTP success still waits for Hub application but not for the next network
  Tick.
- Several changes in one 100ms period collapse to the final appearance.
- Newly visible players receive appearance in `entered`.
- Departed or invisible clients receive no appearance update.
- The owner receives its own saved appearance through replication.
- Existing `PUT /api/appearance` status and persistence semantics remain
  unchanged.

**Affected modules:**

- `internal/realtime/hub.go`
- `internal/realtime/hub_test.go`
- `internal/realtime/messages.go`
- `internal/server/auth_test.go`
- `internal/game/world.go`

**Verification:**

- Test owner and visible-neighbor delivery.
- Test invisible-neighbor suppression.
- Test repeated updates collapsing to the final value.
- Test same-Tick entered and left precedence.
- Test HTTP success does not wait for a replication Tick.
- Assert AOI statistics for deterministic scenarios.
- Run `go test ./internal/realtime ./internal/server`.

---

### Task 7: Migrate The Browser To AOI Replication

- [ ] Deliver frontend handling for separate self state, local snapshots, and
  atomic replication batches.

**Task boundary:**

- Replace handlers for `world_snapshot`, `players_delta`, and
  `appearance_changed`.
- Initialize the current marker and authoritative appearance from `self_state`.
- Replace all other markers from `visible_entities_snapshot`.
- Apply one `replication_update` using the server precedence rules.
- Move self from `selfPosition`, add complete entered markers, remove left
  markers, move visible positions, and update appearances independently.
- Preserve current map following, tooltips, account appearance preview,
  reconnect, logout, keyboard, and joystick behavior.
- Do not add interpolation or client prediction.

**Behavioral goals:**

- The browser never displays players absent from its AOI messages.
- Position updates do not reset appearance or username.
- Appearance updates do not move markers.
- Replacement snapshots remove stale public markers without removing self.
- Refresh does not cause visible flicker for neighboring clients.
- Current-player movement remains authoritative and updates at up to 10 Hz.
- Existing authentication and appearance editor behavior remains intact.

**Affected modules:**

- `web/app.js`
- `web/index.html` only if script/cache metadata needs updating.

**Verification:**

- Run `go test ./...` before manual browser checks.
- Verify two-window enter, hysteresis retention, exit, refresh replacement, true
  logout/login, position filtering, and appearance filtering.
- Verify current-player following and account appearance preview.
- Verify reconnect replaces visible markers from the new snapshot.

---

### Task 8: Add The 1,000-Client Functional Scale Scenario

- [ ] Deliver deterministic functional-scale coverage and complete regression
  verification.

**Task boundary:**

- Add an in-memory 1,000-client test using the real World, AOIIndex, Hub
  replication assembly, encoding, and bounded client send path.
- Use deterministic player placement with sparse areas and one deliberately
  dense local area.
- Include stationary players, moved players, entry, hysteresis, exit,
  appearance changes, disconnects, and connection replacement.
- Record candidate checks, relationship changes, replication messages, and
  encoded payload bytes.
- Do not enforce a wall-clock threshold or claim production capacity.
- Do not introduce synthetic entities without clients or real WebSocket load
  generation.

**Behavioral goals:**

- Every in-memory client receives only self and current visible entities.
- No global snapshot or global delta remains.
- Relationship symmetry holds after the complete scenario.
- Empty client updates are skipped.
- The test is deterministic and avoids timing sleeps for Hub Tick control.
- Existing connection, persistence, auth, appearance, logout, and shutdown
  tests remain green.

**Affected modules:**

- Create `internal/realtime/aoi_scale_test.go`
- `internal/realtime/hub_test.go`
- `internal/game/aoi_test.go`
- `docs/map-walker-handoff.md` after implementation is verified.

**Verification:**

- Run the 1,000-client test repeatedly to check determinism.
- Run `go test ./internal/game ./internal/realtime`.
- Run `go test ./...`.
- Run `go vet ./...`.
- Complete the multi-browser manual AOI verification from the approved spec.

---

## Final Acceptance

- [ ] Review implementation against every acceptance criterion in
  `docs/superpowers/specs/2026-06-13-online-player-aoi-design.md`.
- [ ] Confirm the Cell origin comes only from server World configuration and no
  Shanghai boundary provider was introduced.
- [ ] Confirm only moved players trigger AOI relationship recalculation.
- [ ] Confirm each changed client receives at most one update per 100ms Tick.
- [ ] Confirm invisible clients receive no position or appearance data.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Update `docs/map-walker-handoff.md` only after all automated and manual
  verification passes.
