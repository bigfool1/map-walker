# In-process Synthetic Clients Design

Date: 2026-06-14

## Goal

Add persistent, in-process Synthetic Clients that reproduce the server-side
cost of online players while avoiding HTTP, WebSocket, TCP, and client-side
rendering costs.

Synthetic Clients exist to:

- Make development and hosted demos visibly active.
- Exercise the real World, AOI, Hub, replication encoding, send queue, and
  position persistence paths.
- Support more logical online players on limited hardware than localhost
  WebSocket bots.
- Produce observable aggregate load statistics.

They are simulated online users, not NPCs. Each Synthetic Client remains both
an Entity and an Observer with a complete symmetric visible-neighbor set.

## Phase Boundary

This phase implements only the in-process layer.

It excludes:

- Localhost WebSocket bots.
- HTTP login load testing.
- JWT authentication migration.
- Client-side JSON decoding or marker-state maintenance.
- Gameplay behavior, targeting, pathfinding, or neighbor-aware AI.
- Dynamic runtime resizing.
- Multi-Hub or Shard execution.

The approved JWT design and plan remain documented but are intentionally
deferred. Existing real-user opaque session authentication remains unchanged.

## Architecture

```text
cmd/map-walker
  ├─ realtime.Hub
  ├─ synthetic.Manager
  │    ├─ provisioning coordination
  │    ├─ ramp-up
  │    ├─ deterministic behavior scheduler
  │    ├─ lifecycle and stats aggregation
  │    └─ one drain goroutine per Synthetic Client
  └─ admin stats HTTP endpoint

cmd/synthetic-provision
  └─ synthetic.Provisioner

internal/synthetic
  ├─ account naming and parsing
  ├─ initial placement and fixed appearance
  ├─ provisioning
  ├─ ClientSender implementation
  ├─ Manager and behavior scheduling
  └─ immutable stats snapshots
```

Both commands reuse the same Provisioner. `internal/realtime` does not depend
on `internal/synthetic`; the Hub does not know whether a ClientSender is a
browser, WebSocket bot, or Synthetic Client.

## Synthetic User Accounts

Synthetic users are real rows in the existing `users` table.

Canonical usernames:

```text
synthetic_1
synthetic_2
synthetic_3
...
```

Rules:

- The suffix is a base-10 positive integer with no fixed width.
- Target count `N` means the exact stable pool `synthetic_1..synthetic_N`.
- Higher-numbered accounts do not fill lower-numbered gaps.
- Expanding from 50 to 100 activates or provisions 51 through 100.
- Shrinking to 20 activates only 1 through 20 without deleting other accounts.
- The `synthetic_` prefix is case-insensitively reserved from public
  registration.
- Public registration returns the existing unavailable/invalid username
  behavior for a reserved name.

The internal Provisioner is the only path that may create reserved synthetic
usernames. It still uses the existing bcrypt password hashing function before
creating a user.

## Provisioning

### Dedicated Command

`cmd/synthetic-provision` ensures that every account in `1..N` exists.

Configuration:

- `-count N`
- `-workers N`, defaulting to `runtime.GOMAXPROCS(0)`
- Existing database driver and DSN options
- Password from `MAP_WALKER_SYNTHETIC_PASSWORD`

Behavior:

- Load existing synthetic users in one batch.
- Strictly parse positive numeric suffixes in Go.
- Ignore unrelated usernames that merely resemble the prefix.
- Reuse accounts already present.
- Create missing account numbers, including gaps.
- Correct existing synthetic appearance when it differs from the fixed value.
- Assign initial position only when no saved position exists.
- Never reset an existing saved position.
- Never delete accounts above the requested count.
- Never verify or change an existing account password.
- Continue after an individual account failure.
- Report created, reused, corrected, and failed totals.
- Exit non-zero when any account failed while preserving successful work.
- Be safe to run repeatedly.

### Service Auto-provisioning

`-synthetic-auto-provision` is disabled by default.

When enabled:

- Provisioning runs in the background after the database, Hub, and HTTP server
  are available.
- It uses the same Provisioner implementation and a bounded worker pool.
- Each account may enter ramp-up as soon as it is ready.
- The service remains available to real users during provisioning.
- Missing `MAP_WALKER_SYNTHETIC_PASSWORD` or provisioning errors affect
  Synthetic Client status but do not stop the real-user service.
- Formal large demos should run the dedicated command before startup to avoid
  bcrypt and database creation work competing with steady-state load.

Without auto-provisioning, missing target accounts are activation failures.

## Storage Queries

Storage adds synthetic-specific bulk operations rather than issuing one query
per target user.

The bulk loader:

- Selects users by the reserved normalized username prefix.
- Returns ID, username, saved position, and appearance.
- Leaves numeric suffix validation and sorting to Go.

The preparation operation:

- Creates a missing strict synthetic username.
- Corrects fixed appearance.
- Writes deterministic initial position only when position is absent.

The Manager first bulk-loads target identities as control-plane work. Hub
registration still invokes the existing saved-player loader for each user so
the real player-state loading path remains measured.

## Appearance

Every Synthetic Client is visibly and honestly marked:

- Username uses the `synthetic_` prefix.
- Color is fixed to `#ff8c00`.
- Shape is fixed to `diamond`.

Provisioning and activation correct appearance before Hub registration. This
avoids an appearance correction broadcast after the client is online.

There is no online appearance watcher. If an external action changes a
Synthetic Client appearance while it is active, the next activation restores
the fixed value.

## Initial Placement

New accounts receive deterministic positions on a grid centered on the current
Shanghai spawn.

Requirements:

- Placement is derived from the synthetic account number.
- Grid generation is implemented in `internal/synthetic`, not imported from
  `internal/benchmark`.
- Positions remain inside the configured activity region.
- Spacing avoids placing all new accounts into one fully connected hotspot.
- The mapping remains stable across repeated provisioning runs.
- Existing saved positions are authoritative and are never reset.

## Client Model

Each Synthetic Client implements the existing `realtime.ClientSender` contract.

It has:

- Database user ID and synthetic username.
- A send queue using the same exported default capacity as the real realtime
  Client.
- One drain goroutine.
- Idempotent close and unregister lifecycle.
- A readiness notification.
- Client-local atomic counters.

The realtime package exposes one shared default send-buffer capacity used by
both real and Synthetic Clients. `internal/synthetic` does not copy the numeric
capacity.

### Included Server Costs

Synthetic Clients use:

- Real Hub registration.
- Real saved-player loading.
- Real World state and movement.
- Real AOI membership and symmetric visible relationships.
- Real input delivery through `Hub.ApplyInput`.
- Real initialization snapshot construction and JSON encoding.
- Real per-client replication assembly and JSON encoding.
- Real bounded send queue and queue-full disconnect behavior.
- Real periodic and final position persistence.

### Excluded Costs

Synthetic Clients do not use:

- Bcrypt during normal activation.
- Session creation or validation.
- HTTP.
- WebSocket framing or heartbeat.
- TCP loopback.
- Kernel socket buffers or file descriptors.
- Client-side JSON decoding.
- Browser marker and UI work.

These exclusions are reported explicitly. A later localhost WebSocket Bot
phase measures the transport and real-client increment.

## Initialization And Readiness

The Hub continues sending its normal two initialization messages:

1. `self_state`
2. `visible_entities_snapshot`

The Synthetic Client drains but does not decode them.

Readiness is established after the drain goroutine consumes the first two
messages. The Manager counts the client as `activating` before readiness and
`active` only after readiness.

Activation fails when:

- The two messages are not drained within five seconds.
- The send queue closes before readiness.
- Hub registration fails.
- The Hub stops.

On activation failure, the Manager unregisters the client, records the failure,
continues ramp-up, and does not retry.

## Send Queue And Drain

Normal Synthetic Clients immediately drain their send queues.

The drain:

- Does not sleep.
- Does not unmarshal JSON.
- Counts messages and bytes.
- Tracks queue high-water.
- Signals readiness after two messages.

A slow-drain implementation may exist only as a deterministic test fixture for
queue-full and cleanup behavior. Slow mode is not exposed as a service startup
configuration in this phase.

Every Synthetic Client keeps its own drain goroutine. This intentionally
retains per-client goroutine and channel cost rather than using a shared worker
pool.

## Manager Lifecycle

The `synthetic.Manager` owns:

- Target account numbers.
- Provisioning and activation state.
- Ramp-up.
- Client lifecycle.
- Central behavior scheduling.
- Position estimates.
- Stats aggregation.

The Manager uses one goroutine for ramp and behavior scheduling. It does not
create one behavior goroutine or timer per client.

Startup flags:

- `-synthetic-clients`, default `0`
- `-synthetic-ramp-rate`, default `10` clients per second
- `-synthetic-auto-provision`, default `false`

Validation:

- Client count cannot be negative.
- Ramp rate cannot be negative.
- Ramp rate `0` means activate without rate limiting.

Activation order is ascending account number.

Ramp-up uses a 100ms scheduling tick and a clients-per-second token budget.
Ramp-up and behavior scheduling remain serialized through the Manager
goroutine. A client begins behavior only after readiness.

Unexpected disconnect:

- Decrements active count.
- Increments failure and disconnect totals.
- Does not reconnect.
- Does not activate a replacement.
- May leave active lower than target.

## Movement Behavior

Behavior uses deterministic, account-derived pseudo-random schedules without
depending on `internal/benchmark`.

Each active client:

- Holds one current `game.InputState`.
- Uses a monotonically increasing input sequence.
- Changes direction every deterministic interval from one through five
  seconds.
- Sends input only when state changes, matching the existing browser.
- Continues moving through World ticks while the last input remains active.

Direction selection per change:

- 20% neutral.
- 40% split across four cardinal directions.
- 40% split across four diagonal directions.

Activation and first input times are deterministically staggered.

## Activity Region And Estimated Position

Synthetic Clients remain near the Shanghai spawn inside a 10km by 10km
activity region.

- The hard boundary is 5km from the spawn on each local axis.
- A 4.5km soft boundary begins inward direction selection.
- Bots outside the region are never teleported or reset in storage.
- Direction choices guide them gradually inward through normal input.

The Manager does not query Hub or World positions and does not decode
replication messages.

It maintains an estimated position:

- Initialize from the bulk-loaded saved position or deterministic initial
  position.
- Update every 100ms using the same speed, diagonal normalization, and local
  coordinate assumptions as World movement.
- Use the estimate only for boundary direction filtering.
- Never use the estimate for persistence, replication, gameplay, or
  authoritative state.
- Do not calibrate during a run.
- Reinitialize from persisted authoritative position after service restart.

The 500m soft-boundary margin absorbs scheduler and estimate drift.

## Graceful Shutdown

Shutdown order:

1. Stop accepting new HTTP requests.
2. Stop Synthetic Manager provisioning, ramp-up, and behavior scheduling.
3. Stop new input generation.
4. Unregister every active or activating Synthetic Client.
5. Let the Hub save final positions, remove AOI and World state, and queue
   `left` updates for remaining real users.
6. Wait for all Synthetic Client drain goroutines and lifecycle completion.
7. Stop the Hub, which saves and drains remaining real users.
8. Close the database.

Shutdown is not counted as a failure or unexpected disconnect.

Client close and unregister operations are idempotent.

## Stats

### Synthetic Snapshot

The Manager publishes an immutable snapshot once per second.

Current gauges:

- Target.
- Provisioning.
- Provisioned.
- Activating.
- Active.
- Moving.
- Idle.
- Failed.
- Queue high-water.

Recent one-second rates:

- Inputs sent.
- Messages drained.
- Bytes drained.
- Disconnects.
- Queue-full disconnects.

Lifetime totals:

- Activated.
- Failed.
- Disconnects.
- Inputs sent.
- Messages drained.
- Bytes drained.

Each Client owns local atomic counters for drained messages, drained bytes, and
queue high-water. The Manager aggregates them once per second. Clients do not
contend on global per-message counters.

Manager-owned lifecycle and input counters are updated by its single
goroutine.

### Hub Snapshot

The Hub preserves its existing one-second logging and additionally publishes
its most recently completed immutable interval snapshot.

The snapshot includes:

- Connected clients.
- Simulation ticks.
- Moved players.
- AOI candidate checks.
- AOI distance checks.
- Relationships entered and left.
- Replication messages, recipients, and bytes.

Hub values describe all clients. The Hub does not classify clients as real or
synthetic.

## Admin API And Page

Admin access is configured with `MAP_WALKER_ADMIN_TOKEN`.

When the token is missing or empty:

- `/admin` is not exposed and returns `404`.
- `/api/admin/synthetic-stats` is not exposed and returns `404`.

When configured:

- `/admin` serves a dedicated read-only page.
- The page accepts the token from the operator.
- The token is stored only in the current tab's `sessionStorage`.
- API requests send `Authorization: Bearer <token>`.
- Server comparison uses `subtle.ConstantTimeCompare`.
- Missing or wrong tokens return `401`.

`GET /api/admin/synthetic-stats` returns:

- The latest Synthetic Manager snapshot.
- The latest all-client Hub snapshot.
- Sampling timestamps.

The endpoint:

- Does not enumerate clients.
- Does not traverse World, AOI, Hub client maps, or Synthetic Clients.
- Does not retain history.
- Does not expose dynamic controls.

The page:

- Polls once per second.
- Displays numeric cards and simple status color.
- Labels Hub statistics as all-client values.
- Has no chart, start, stop, or resize controls.

## Configuration Summary

Service flags:

```text
-synthetic-clients N
-synthetic-ramp-rate N
-synthetic-auto-provision
```

Environment:

```text
MAP_WALKER_SYNTHETIC_PASSWORD
MAP_WALKER_ADMIN_TOKEN
```

Defaults:

- Synthetic clients: `0`
- Ramp rate: `10` clients/second
- Auto-provision: disabled
- Activity area: 10km by 10km
- Soft boundary: 4.5km from spawn on each axis
- Behavior scheduler: 100ms
- Direction changes: one through five seconds
- Readiness timeout: five seconds
- Fixed appearance: `#ff8c00` diamond

## Verification

### Naming And Provisioning

- Strictly accept `synthetic_<positive integer>`.
- Reject the reserved prefix through public registration, case-insensitively.
- Bulk-load and numerically sort synthetic users.
- Provisioning fills gaps in `1..N`.
- Repeated provisioning is idempotent.
- Existing passwords are not checked or replaced.
- Existing saved positions are not reset.
- Missing saved positions receive stable deterministic positions.
- Appearance is corrected to the fixed value.
- Individual failures are reported while successful work remains.
- Worker concurrency stays within the configured bound.

### Client And Hub Integration

- Synthetic Client uses the realtime shared send-buffer capacity.
- Two drained initialization messages produce readiness.
- Readiness timeout and early close fail activation.
- Input reaches the real Hub and World.
- Synthetic users receive full AOI relationships and replication encoding.
- Queue full causes real Hub removal and final persistence.
- Unexpected disconnect does not reconnect or replace the client.
- Shutdown unregisters clients before Hub stop and saves final positions.

### Behavior

- Direction selection is deterministic for account number and seed.
- Distribution contains neutral, cardinal, and diagonal states.
- Sequence numbers increase only when input changes.
- Direction intervals remain within one through five seconds.
- Inputs are staggered.
- Estimated straight and diagonal movement matches World formulas.
- Soft-boundary filtering selects inward movement.
- Persisted out-of-bounds positions are not reset.

### Stats And Admin

- Client-local counters aggregate into correct one-second rates and lifetime
  totals.
- Manager snapshot is immutable and can be read concurrently.
- Hub snapshot matches the completed interval that is logged.
- Admin API combines snapshots without enumerating live state.
- Missing admin configuration produces `404`.
- Missing or invalid Bearer token produces `401`.
- Correct token produces aggregate JSON only.
- Admin page stores token in `sessionStorage` and polls once per second.

### Project Verification

- Default `-synthetic-clients 0` preserves current service behavior.
- Existing real-user authentication remains unchanged.
- `go test ./...` passes.
- `go vet ./...` passes.

## Follow-up

After this phase:

1. Run development and cloud experiments at progressively larger Synthetic
   Client counts.
2. Build a separate localhost WebSocket Bot runner using the same account pool
   and movement model.
3. Compare in-process and WebSocket steady-state results to isolate transport,
   connection, and client-decoding cost.
4. Add a small server-authoritative gameplay vertical slice before using the
   WebSocket Bot layer for representative gameplay load tests.
