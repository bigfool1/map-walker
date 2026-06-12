# Player Appearance Sync Design

Date: 2026-06-12

## Goal

Allow each user to choose a marker shape and color, persist that appearance,
and synchronize saved appearance changes to all online players.

This is the first phase of the next multiplayer roadmap:

1. Player appearance synchronization.
2. Room isolation.
3. Room-local area of interest (AOI).

The phase deliberately extends player state before changing broadcast
visibility. Room and AOI behavior remain separate implementation batches.

## Scope

This phase includes:

- Four server-defined marker shapes: `circle`, `square`, `diamond`, and
  `triangle`.
- Any opaque color represented as a six-digit `#RRGGBB` value.
- A native browser color picker with local preview.
- Explicit save and cancel actions.
- Persistent appearance fields on each user.
- Complete appearance in world snapshots.
- Position-only movement deltas.
- A separate realtime message for saved appearance changes.
- A clickable account menu in the upper-right corner.

This phase excludes:

- Room assignment or room-filtered broadcasts.
- AOI, spatial indexes, or distance-filtered visibility.
- Arbitrary SVG, image uploads, or custom marker definitions.
- Alpha channels, gradients, animation, or marker sizing.
- Appearance history, presets, inventory, unlocks, or payments.
- Realtime submission while dragging the color picker.
- Username display in the realtime player protocol.

## Appearance Model

An appearance contains:

```text
color: #3388ff
shape: circle
```

The default for new and existing users is blue `#3388ff` with shape `circle`.

Colors must match `#[0-9a-fA-F]{6}` exactly. The server normalizes accepted
colors to lowercase before storing or broadcasting them. Transparency is not
supported.

Shapes are stable protocol and storage enum values:

- `circle`
- `square`
- `diamond`
- `triangle`

The server validates both fields. The client may only render supported values
received from the server; it does not invent fallback appearance values for
invalid protocol data.

## State Ownership

Appearance is part of the online player state owned by `game.World`.

An online player therefore has:

- A stable player ID.
- An authoritative position.
- An authoritative appearance.
- Current movement input and sequence state.

`Hub.Run()` remains the only owner of mutations to online players. HTTP
handlers do not mutate World directly and do not add locks around World.

The Hub loads persisted position and appearance when it adds a user who is not
already online. A replacement connection for an already-online user retains
the existing in-memory position and appearance.

Keeping position and appearance in one player entity allows a future
room/AOI `player_entered` event to carry one complete player state without
joining state from World and a separate Hub map.

## Storage

A forward-only migration adds two non-null user columns:

- `appearance_color`
- `appearance_shape`

The migration gives both columns defaults so existing users are backfilled as
`#3388ff` and `circle`. The existing `001_initial.sql` migration is not edited.

Storage provides operations to:

- Read a user's position and appearance for initial World registration.
- Save a complete appearance by user ID.

Appearance writes are synchronous in the HTTP request path. They do not run in
the Hub actor loop and do not use the asynchronous position persistence
worker. Appearance changes are rare, user-driven profile writes rather than
simulation output.

## HTTP API

The authenticated endpoint is:

```http
PUT /api/appearance
Content-Type: application/json

{"color":"#ff6600","shape":"diamond"}
```

`PUT` replaces the complete appearance, so both fields are required.

The successful response returns the normalized authoritative appearance:

```json
{
  "color": "#ff6600",
  "shape": "diamond"
}
```

Registration, login, and `GET /api/session` responses also include the current
appearance. This gives the browser an authoritative value for initializing the
account control and editor before the WebSocket snapshot arrives.

### Save Ordering

The request flow is:

1. Authenticate the session.
2. Decode and validate the complete appearance.
3. Normalize the color to lowercase.
4. Persist the appearance in the database.
5. Submit an appearance update event to the Hub.
6. Wait until the Hub has applied the event or confirmed that the user is
   offline.
7. Return the authoritative appearance.

The Hub event never performs database I/O. Waiting only synchronizes with one
short actor-loop operation; it does not wait for every WebSocket client to
write the broadcast to its socket.

For an offline user, the Hub confirms the request without creating a World
player or broadcasting. The persisted appearance is loaded on the user's next
connection.

### Errors

- Missing or invalid session: `401 Unauthorized`.
- Invalid JSON or missing fields: `400 Bad Request`.
- Invalid color or unsupported shape: `400 Bad Request`.
- Database failure: `500 Internal Server Error`; the Hub is not updated.
- Database success followed by an unavailable or stopped Hub:
  `503 Service Unavailable`.

If the database succeeds but the Hub cannot apply the event, the database is
not rolled back. The temporary inconsistency is accepted for this demo; the
persisted value becomes authoritative when the player next joins the World.
No distributed transaction, retry queue, or compensation mechanism is added.

Submitting an appearance equal to the current World value succeeds but does
not broadcast an `appearance_changed` message.

## Realtime Protocol

The protocol separates frequently changing position data from rarely changing
appearance data.

### Complete Snapshot

`world_snapshot` contains complete online player states:

```json
{
  "type": "world_snapshot",
  "tick": 42,
  "players": [
    {
      "id": "user-id",
      "lat": 31.2304,
      "lng": 121.4737,
      "appearance": {
        "color": "#3388ff",
        "shape": "circle"
      }
    }
  ]
}
```

### Position Delta

`players_delta` remains a position-only hot-path message:

```json
{
  "type": "players_delta",
  "tick": 43,
  "players": [
    {
      "id": "user-id",
      "lat": 31.2305,
      "lng": 121.4737
    }
  ],
  "removedPlayerIds": []
}
```

Appearance fields are not repeated at the movement broadcast rate.

### Appearance Change

After the Hub applies a changed online appearance, it broadcasts:

```json
{
  "type": "appearance_changed",
  "playerId": "user-id",
  "appearance": {
    "color": "#ff6600",
    "shape": "diamond"
  }
}
```

The message is sent to all currently connected clients because this phase
still has one flat world. Room and AOI phases will later limit recipients.

The HTTP response means the Hub has accepted the new authoritative state and
attempted to enqueue its broadcast through the existing client send path. It
does not guarantee that every remote browser has received or rendered it.

## Hub Integration

The Hub gains an appearance-update channel carrying:

- User ID.
- Validated appearance.
- A completion result channel.

The `Run()` loop handles the request as follows:

1. Look up the online player.
2. If absent, acknowledge success without broadcasting.
3. If the appearance is unchanged, acknowledge success without broadcasting.
4. Update the World-owned appearance.
5. Encode and broadcast `appearance_changed`.
6. Acknowledge the HTTP request.

Encoding or client send failures follow existing Hub behavior. A failed client
send removes that client through the same actor-owned lifecycle used by other
broadcasts.

The appearance update is not placed in `game.World.TakeDelta()`. Movement and
appearance have separate dirty and protocol paths so static appearance data is
not repeatedly included in position broadcasts.

## Frontend Interaction

The upper-right account area becomes an explicit menu button instead of plain
username text.

Its collapsed state contains:

- A small rendering of the user's current appearance.
- The username.
- A downward indicator.

The entire control has a visible border, background, pointer cursor, and
hover/focus treatment so it reads as clickable. It has an adequate touch
target on mobile and does not rely on hover. The indicator points upward while
expanded, and the control exposes `aria-expanded`.

The expanded menu contains:

- A "修改外观" action.
- A logout action.

The appearance editor contains:

- A marker preview.
- Four shape choices.
- Native `<input type="color">` color selection.
- Save and cancel buttons.
- A local error area.

Changing shape or color updates only the editor preview. It does not alter the
map marker or send network traffic.

Canceling or closing the editor discards unsaved values. Reopening initializes
the editor from the last authoritative appearance.

While saving:

- The save button is disabled.
- One `PUT /api/appearance` request is active for that editor.
- On success, the response updates the user's authoritative frontend
  appearance and map marker, then closes the editor.
- On failure, the editor remains open with its current preview and displays an
  error so the user can retry.

Other players' markers update only from `appearance_changed`. Receiving the
same update through the WebSocket after the initiating browser handled its
HTTP response is harmless and idempotent.

Logout closes the menu/editor and clears all local appearance editing state
along with the existing authenticated state.

## Marker Rendering

Leaflet's fixed image marker is replaced with `L.divIcon` and CSS-rendered
shapes.

All four shapes use the same logical size and anchor so changing appearance
does not move the represented geographic point. Shape-specific CSS may differ;
in particular, the triangle can use borders and a CSS custom property for its
color.

Marker update logic distinguishes:

- Position update: call `setLatLng` only.
- Appearance update: replace or update the marker icon without changing its
  position or tooltip.
- New snapshot player: create a marker with complete position and appearance.

The current player's map-follow behavior remains unchanged.

## Testing

### Game World

- Add a player with loaded appearance.
- Read appearance as part of complete player state.
- Update an online player's appearance.
- Treat an identical appearance update as unchanged.
- Keep position deltas free of appearance fields.

### Storage And Migration

- Apply the new migration on an existing database.
- Verify existing users receive the default appearance.
- Create and read a user with default appearance.
- Save and reload a custom normalized appearance.
- Preserve existing position persistence behavior.

### HTTP And Authentication

- Registration, login, and session lookup return appearance.
- Reject unauthenticated appearance updates.
- Accept and normalize a valid complete appearance.
- Reject malformed JSON, missing fields, invalid colors, and unsupported
  shapes.
- Do not update the Hub after a database failure.
- Return `503` when persistence succeeds but the Hub cannot confirm.

### Hub And Protocol

- Snapshot JSON contains complete appearance.
- Position delta JSON does not contain appearance.
- Online changed appearance updates World and broadcasts once.
- Identical appearance acknowledges without broadcast.
- Offline appearance acknowledges without adding a player or broadcasting.
- A stopped Hub reports failure to the waiting HTTP path.
- Duplicate-connection replacement retains current appearance.

### Frontend Manual Verification

- The collapsed account control visibly reads as clickable on desktop and
  mobile.
- The menu toggles and reports the correct expanded state.
- Color and shape edits affect only the local preview.
- Cancel discards edits.
- Save disables duplicate submission.
- Failed save preserves the preview and allows retry.
- Two browser windows observe one saved appearance change.
- Movement after an appearance change keeps the selected appearance.
- Refresh and reconnect restore the persisted appearance.
- Logout closes and resets the appearance UI.

## Acceptance Criteria

The phase is complete when:

- Every user has a persisted valid appearance with the documented defaults.
- A signed-in user can preview, cancel, and save a color and shape.
- Saving waits for both database persistence and Hub application before
  returning success.
- All online clients update the changed marker without reconnecting.
- Snapshots contain complete player state while movement deltas remain
  position-only.
- Existing authoritative movement, connection replacement, logout, final
  position persistence, and graceful shutdown tests continue to pass.
- `go test ./...` and `go vet ./...` pass.

