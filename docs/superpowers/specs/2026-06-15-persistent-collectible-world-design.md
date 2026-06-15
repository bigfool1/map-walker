# Persistent Collectible World Design

Date: 2026-06-15

## Goal

Add a small, continuously available collection activity to the existing
server-authoritative movement world.

Players explore three visible collection regions, approach collectible points,
and explicitly pick them up for one permanent score point. Synthetic clients
continue to move through the same world but do not collect items or appear in
the online leaderboard.

The feature should make the public demo engaging without introducing
inventories, item abilities, rounds, or a second gameplay service.

## Player Experience

The map displays three translucent circular collection regions and the
collectibles currently visible within the player's 500-meter AOI.

Each collectible:

- is rendered as a gold glowing point
- is worth exactly one point
- has no name, rarity, type, or inventory representation
- highlights when it is the nearest collectible within 10 meters

The player picks up the highlighted collectible by pressing `J` on desktop or
using a circular touch button in the lower-right corner.

The client sends only a pickup intent. It does not remove the collectible or
increase the score optimistically. A successful server response produces a
short `+1` animation and updates the player's permanent score.

The touch button remains visible but is disabled and dimmed when there is no
eligible nearby collectible, the WebSocket is disconnected, or the client
pickup cooldown is active.

## Scope

This phase includes:

- Three fixed circular collection regions loaded from JSON.
- Twenty live collectibles per region.
- Random collectible placement within each region.
- Explicit server-authoritative pickup requests.
- AOI-filtered collectible visibility.
- Delayed replacement of collected items.
- One permanent point per successful pickup.
- Asynchronous, idempotent score persistence with retry.
- A session-authenticated online leaderboard requested when the player opens
  it.
- Persistent synthetic-account identity and exclusion from collection
  behavior and leaderboard results.
- Keyboard and touch pickup controls.

This phase excludes:

- Inventories, item stacking, item use, or item dropping.
- Multiple collectible types or values.
- Round timers, match resets, winners, seasons, or score decay.
- A global leaderboard containing offline users.
- Automatic leaderboard polling or push updates.
- Runtime region editing or configuration reload.
- A region generator command.
- Persistent collectible instances or respawn schedules.
- Synthetic-client pickup load.
- Cross-process Hub or collectible coordination.

## Region Configuration

The repository contains a pre-generated configuration at:

```text
config/collectible-regions.json
```

The service accepts a `-collectible-regions` flag whose default is that path.
Configuration is loaded once during startup. The committed coordinates are
fixed; the server does not randomize region centers after startup.

The file contains three regions:

```json
{
  "regions": [
    {
      "id": "region-1",
      "center": {
        "lat": 31.2304,
        "lng": 121.4737
      },
      "radiusMeters": 200,
      "targetCount": 20,
      "respawnMinSeconds": 5,
      "respawnMaxSeconds": 15
    }
  ]
}
```

`id` is a stable internal identifier used by the server, protocol, tests, and
logs. Regions have no display name and the UI does not claim that they
represent real landmarks.

The loader rejects startup when:

- the file cannot be read or decoded
- there are not exactly three regions
- an ID is empty or duplicated
- latitude, longitude, radius, target count, or respawn bounds are invalid
- the minimum respawn delay exceeds the maximum
- two circular regions overlap

The initial committed configuration uses three non-overlapping 200-meter
regions in the Shanghai play area, with 20 collectibles and a 5-15 second
replacement delay in each region.

## Game Model

`internal/game` owns a pure-logic collectible field. It has no database,
connection, goroutine, timer, or locking dependency.

The model contains:

```go
type CollectibleRegion struct {
    ID                string
    Center            Point
    RadiusMeters      float64
    TargetCount       int
    RespawnMin        time.Duration
    RespawnMax        time.Duration
}

type Collectible struct {
    ID       uint64
    RegionID string
    Lat      float64
    Lng      float64
}
```

The field owns:

- validated region definitions
- current collectible instances indexed by ID
- a 600-meter grid spatial index for collectibles
- pending replacement deadlines by region
- monotonically increasing process-local collectible IDs

At startup, each region is filled to its target count. A collected item is
removed immediately and schedules one replacement in the same region after a
random delay between 5 and 15 seconds.

When a replacement deadline is due, the field samples a point uniformly over
the area of the owning circle. It makes a bounded number of attempts to avoid
placing the new item too close to another collectible. If every attempt is too
close, the final valid in-region position is accepted so replacement cannot
stall.

Collectibles and pending deadlines are in-memory world state. A restart creates
new collectible IDs, positions, and replacement timing while preserving player
scores.

## Ownership And Tick Integration

`Hub.Run()` remains the only actor loop that changes online player and
collectible state.

The Hub:

- initializes the collectible field before accepting gameplay
- advances due replacements from an existing Hub tick
- handles pickup intents inside the actor loop
- updates the successful player's in-memory score
- accumulates per-client collectible replication changes
- submits immutable score snapshots to the score persistence worker

No collectible method starts a goroutine or mutates Hub state outside
`Hub.Run()`. No lock is added around World or collectible state.

The collection feature may add focused Hub state grouped by responsibility,
but it does not split state ownership across actors.

## Spatial Visibility

Collectibles use a separate 600-meter grid index rather than becoming players
inside the existing symmetric player AOI relationship graph.

Visibility uses the same distance policy as player AOI:

- enter at 500 meters
- leave beyond 600 meters
- inspect the surrounding nine grid cells

The Hub tracks each client's currently visible collectible IDs so movement can
produce entered and left changes with hysteresis.

The expected work is:

```text
moving players discovering collectibles: O(M * k)
changed collectibles finding recipients: O(C * v)
```

where `M` is the number of moved players, `C` is the number of spawned or
collected items, `k` is nearby collectible density, and `v` is nearby player
density.

Spawn and collection notifications use reverse fan-out data flow:

```text
changed collectible -> nearby players from the player spatial index
                    -> per-client replication accumulation
```

The first implementation does not maintain a collectible-to-visible-player
reverse cache. It performs a nine-cell query against the existing player
spatial index when a collectible changes. This avoids global player scans while
keeping the new state model small.

## Pickup Protocol And Validation

The client sends:

```json
{
  "type": "collect",
  "collectibleId": 123
}
```

The message expresses intent only. The Hub accepts it when:

- the sender is still the current connection for the player
- the account is not synthetic
- the collectible still exists
- the collectible is visible to that player
- authoritative player and collectible positions are no more than 10 meters
  apart
- at least 300 milliseconds have passed since the player's previous pickup
  request processed by the Hub

The Hub resolves competing requests serially. The first valid request removes
the item; later requests for the same ID find no item and fail.

Requests rejected by the server cooldown do no further lookup work. Other
failed requests are ignored without a result message. The normal collectible
replication stream corrects stale client state.

On success, the Hub sends only the collecting player:

```json
{
  "type": "collect_result",
  "collectibleId": 123,
  "score": 42
}
```

This message triggers the `+1` feedback and provides the new authoritative
permanent score. It is not broadcast.

The browser independently throttles normal users:

- `J` and the touch button share one 300-millisecond cooldown
- repeated triggers during cooldown are ignored
- no request is queued for later delivery
- no request is sent without a selected visible target or active WebSocket

The server cooldown remains authoritative because clients are untrusted.

## Replication Protocol

On connection, the server sends:

1. Existing `self_state`, extended with the player's permanent collectible
   score.
2. Existing `visible_entities_snapshot`.
3. A `collectible_regions` message containing only public geometry.
4. A `visible_collectibles_snapshot` containing items visible to the player.

Public region data includes `id`, center, and radius. Target count and respawn
timing remain server-only.

Ongoing `replication_update` messages gain collectible changes:

- `collectiblesEntered`: items newly visible because the player moved
- `collectibleIdsLeft`: items that left visibility without being collected
- `collectiblesSpawned`: new items visible to the recipient
- `collectibleIdsCollected`: collected items visible to the recipient

These collections are built independently for every client, normalized to
avoid contradictory changes in one update, and consumed and cleared on the
broadcast tick with the existing pending replication buffers.

Collected and left are distinct so the UI can remove both while reserving
collection feedback for authoritative events. Only the successful collector
receives `collect_result`.

## Permanent Score

A forward migration adds:

```sql
collectible_score BIGINT NOT NULL DEFAULT 0
is_synthetic BOOLEAN NOT NULL DEFAULT FALSE
```

to `users`.

Session authentication and saved-player loading carry both values into the
trusted WebSocket connection context. Hub player state keeps the score and the
synthetic marker; `is_synthetic` does not need to be exposed to ordinary
clients.

A successful pickup increments the in-memory score before any database work.
The Hub submits an immutable absolute snapshot:

```go
type ScoreUpdate struct {
    UserID int64
    Score  int64
}
```

The score persistence worker owns a pending map keyed by user ID. Newer,
higher snapshots replace older pending snapshots, so repeated failures do not
create a queue of `41`, `42`, and `43`; the worker retries only the latest
known score.

Storage uses a monotonic idempotent update. The backend-specific expression may
differ, but its behavior is equivalent to:

```sql
UPDATE users
SET collectible_score = GREATEST(collectible_score, ?)
WHERE id = ?
```

Executing the same snapshot repeatedly cannot duplicate points, and an old
snapshot cannot overwrite a newer persisted score. `RowsAffected == 0` is not a
failure because the database may already contain the same or a higher score.

Database failures use exponential backoff capped at 30 seconds. Pending updates
remain coalesced by user while the database is unavailable.

Normal disconnect performs a synchronous submission of that player's latest
score. Graceful shutdown submits all online scores and drains pending score
work within the existing shutdown deadline.

The pickup path never waits for the database. A forced process termination may
lose recently accepted points that have not reached the database; this bounded
durability gap is accepted for this demo.

## Synthetic Identity

Synthetic identity is a persistent, server-trusted user property.

The synthetic provisioner:

- creates automatic synthetic accounts with `is_synthetic = true`
- corrects existing provisioned synthetic accounts to `true`

Normal registration always creates `is_synthetic = false`. Login does not
allow clients to choose or modify this field.

In-process synthetic clients continue to use normal accounts, sessions,
cookies, WebSocket connections, AOI, and replication. Their trusted database
identity reaches the Hub through the same authentication and saved-player
loading path as other account data.

Synthetic players:

- remain visible and move normally
- never send pickup requests under the default behavior
- are rejected by the Hub if a pickup request is sent
- do not appear in online leaderboard rankings

An account remains synthetic even when a person logs into it. This account
property is intentional for the first version.

## Online Leaderboard

The leaderboard is hidden by default. Opening it performs one
session-authenticated HTTP request:

```text
GET /api/leaderboard/online
```

The server asks the Hub actor for an immutable view of currently connected
players. The Hub filters synthetic accounts and sorts real online players by:

1. score descending
2. player ID ascending

The response contains the first five entries and the requesting player's own
online rank and score:

```json
{
  "top": [
    {
      "playerId": 12,
      "username": "alice",
      "score": 42
    }
  ],
  "self": {
    "rank": 8,
    "score": 17
  }
}
```

If the authenticated user is not currently connected, `self` is omitted. The
endpoint does no database ranking and excludes offline users.

The UI does not poll, subscribe, or auto-refresh. Closing and reopening the
leaderboard makes a new request. Therefore no leaderboard cache, heap, tree,
LRU, or ranking work is added to the pickup path.

For the intended demo scale, an on-demand `O(P log P)` sort is sufficient.

## Failure And Boundary Behavior

- Two players race for one item: the first valid Hub event succeeds.
- A stale item ID is submitted: ignore it.
- The player is too far away: ignore it.
- A synthetic account submits a request: ignore it.
- The client repeats input rapidly: client throttling reduces traffic and the
  server cooldown remains authoritative.
- Score persistence is unavailable: gameplay continues and latest snapshots
  retry with backoff.
- A player disconnects: synchronously submit the latest score.
- The service shuts down gracefully: submit online scores and drain the worker
  within the shutdown deadline.
- The process is killed: recently accepted but unpersisted score may be lost.
- A region replacement position is crowded: retry a bounded number of times,
  then accept a valid in-region position.
- Region configuration is missing, invalid, duplicated, or overlapping: fail
  startup.

## Testing

### Game

- Region configuration validation, including overlap rejection.
- Initial population reaches 20 items per region.
- Random placement remains inside its owning circle.
- Collection removes exactly one item and schedules one replacement.
- Replacement occurs only after its deadline and restores target population.
- Grid queries and 500/600-meter visibility hysteresis.
- Deterministic random and clock seams for repeatable tests.

### Realtime

- Pickup accepts one eligible real player and increments score once.
- Competing pickups produce one winner.
- Stale, distant, repeated, obsolete-connection, and synthetic requests fail.
- `collect_result` is sent only to the winner.
- Spawned and collected items reverse-fan out only to nearby players.
- Player movement produces collectible entered and left changes.
- Per-client normalization avoids contradictory collectible changes.
- Pending collectible buffers are consumed and cleared on broadcast.
- Existing player replication behavior remains unchanged.

Hub tick tests use explicit channels or direct deterministic seams and follow
`docs/concurrency-debugging.md`.

### Storage And Auth

- Forward migration adds score and synthetic columns for SQLite and MySQL.
- Normal registration cannot create synthetic accounts.
- Provisioning creates and corrects synthetic accounts.
- Saved player/session identity carries score and synthetic status.
- Pending score updates coalesce to the highest score.
- Retry is idempotent and uses capped backoff.
- Successful persistence clears only the corresponding latest pending state.
- Disconnect and shutdown synchronous submission behavior.

### Server And Web

- Leaderboard requires an authenticated session.
- Leaderboard excludes synthetic and offline users.
- Ordering and self rank are deterministic.
- Region and collectible initialization messages render correctly.
- `J` and touch controls share client cooldown behavior.
- No optimistic score or collectible mutation occurs before server messages.

### Verification

```bash
go test ./internal/game
go test ./internal/storage
go test ./internal/realtime
go test ./internal/server
go test ./...
go test -race ./internal/game ./internal/storage ./internal/realtime ./internal/server
go vet ./...
```

Manual browser verification covers:

- Region circles and gold collectible rendering.
- AOI entry and exit while moving.
- `J` and touch pickup controls.
- Two-player competition for one item.
- Synthetic players moving without collecting.
- Score restoration after reconnect and process restart.
- On-demand leaderboard filtering and self rank.

## Success Criteria

The phase succeeds when:

- Three configured regions each maintain 20 in-memory collectibles.
- Real players can explicitly collect a nearby item for exactly one permanent
  point.
- Clients cannot award themselves points or collect distant or missing items.
- Synthetic players remain visible but neither collect nor rank.
- Collectible replication is AOI-filtered and avoids global player-item scans.
- Score persistence failures do not block Hub simulation or replication.
- Repeated score writes are monotonic and idempotent.
- The online Top 5 is computed only when an authenticated player opens it.
- Existing movement, player AOI, appearance, replacement, and shutdown
  behavior continue to pass their tests.

## Documentation

After implementation, update:

- `README.md`
- `AGENTS.md`
- `docs/map-walker-handoff.md`

The README should explain the collection loop, controls, permanent score,
online-only leaderboard, synthetic exclusion, region configuration, and the
fact that collectible instances reset when the service restarts.
