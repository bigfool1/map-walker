# Player Appearance Sync Implementation Plan

> **For agentic workers:** Implement tasks in order. Use the approved design as
> the source of truth and track each task with its checkbox.

**Goal:** Add persistent player marker appearance with local preview, explicit
save, and realtime synchronization to all online clients.

**Architecture:** Appearance is part of the online player state owned by
`game.World`. HTTP persists a complete appearance first, then waits for the Hub
actor to apply it. Snapshots carry complete player state, movement deltas remain
position-only, and saved appearance changes use a separate realtime message.

**Tech Stack:** Go 1.26, `database/sql`, SQLite/MySQL migrations, `net/http`,
WebSocket, plain JavaScript, Leaflet `L.divIcon`, CSS.

**Required context:** Read `AGENTS.md`, `docs/map-walker-handoff.md`, and
`docs/superpowers/specs/2026-06-12-player-appearance-sync-design.md` before
implementation. The approved design is authoritative when this plan is
ambiguous.

---

## Scope Guardrails

This plan includes persistent marker color and shape, complete snapshot state,
position-only movement deltas, independent appearance broadcasts, the
authenticated appearance API, and the account-menu editor.

This plan excludes Room, AOI, spatial indexing, usernames in the realtime
protocol, arbitrary SVG or image uploads, alpha colors, presets, inventory, and
continuous network submission while editing.

---

### Task 1: Establish Durable Appearance Data

- [ ] Deliver migration, storage, and authenticated-user support for complete
  appearances.

**Task boundary:**

- Add forward-only migration `002` with non-null `appearance_color` and
  `appearance_shape` columns.
- Backfill existing users and default new users to `#3388ff` and `circle`.
- Extend user reads to return appearance and add a complete appearance save by
  user ID.
- Extend the authenticated user model so registration, login, and session
  lookup can carry stored appearance.
- Do not add HTTP endpoint behavior, Hub updates, or frontend changes.

**Behavioral goals:**

- Existing SQLite and MySQL databases migrate forward without editing
  `001_initial.sql`.
- Existing and new users always have a complete appearance.
- Saving replaces both appearance fields atomically for one user.
- Missing users preserve the storage package's existing not-found semantics.
- Existing account, session, and position behavior remains unchanged.

**Affected modules:**

- `internal/storage/migrations/`
- `internal/storage/users.go`
- `internal/storage/users_test.go`
- `internal/storage/storage_test.go`
- `internal/auth/service.go`
- `internal/auth/service_test.go`

**Verification:**

- Test migration from the existing schema and clean database creation.
- Test default appearance, custom appearance save/reload, and missing-user
  behavior.
- Test registration, login, and session authentication return stored
  appearance.
- Run `go test ./internal/storage ./internal/auth`.

---

### Task 2: Make Appearance Part Of World State And Split The Protocol

- [ ] Deliver complete player snapshots, position-only deltas, and the
  independent appearance message contract.

**Task boundary:**

- Define the appearance model and supported shape values at the game-domain
  boundary.
- Add appearance to each online World player and complete snapshot player
  state.
- Add World operations to read and update appearance without changing
  position.
- Keep `game.Delta` and `players_delta` position-only.
- Add encoding for `appearance_changed`.
- Do not add the HTTP save flow or Hub event channel in this task.

**Behavioral goals:**

- A player enters World with a complete appearance.
- Updating to a different appearance changes only appearance.
- Updating to the same appearance reports no change.
- `world_snapshot` contains `id`, `lat`, `lng`, and nested `appearance`.
- `players_delta` never repeats appearance fields.
- `appearance_changed` contains `playerId` and the complete appearance.
- Movement, dirty-position tracking, removal, and ticks preserve current
  behavior.

**Affected modules:**

- `internal/game/world.go`
- `internal/game/world_test.go`
- `internal/realtime/messages.go`
- `internal/realtime/messages_test.go`

**Verification:**

- Test World add/read/update/unchanged appearance behavior.
- Test movement after an appearance change preserves the chosen appearance.
- Assert exact JSON shape for snapshot, movement delta, and appearance change.
- Run `go test ./internal/game ./internal/realtime`.

---

### Task 3: Integrate Appearance With The Hub Actor

- [ ] Deliver initial appearance loading and synchronous Hub application of
  saved updates.

**Task boundary:**

- Extend initial player loading to provide both saved position and appearance.
- Preserve in-memory appearance during same-account connection replacement.
- Add an appearance update request channel owned by `Hub.Run()`.
- Let callers wait for the Hub to apply an online update, confirm an offline
  user, or report that the Hub is unavailable.
- Broadcast `appearance_changed` only when an online value actually changes.
- Keep database I/O outside the Hub and keep appearance out of the movement
  delta path.

**Behavioral goals:**

- First connection loads persisted appearance with persisted position.
- Replacement connections retain current in-memory state instead of reloading
  storage.
- Online changed appearance updates World and broadcasts exactly once.
- An identical online update succeeds without broadcasting.
- An offline update succeeds without creating a player or broadcasting.
- A stopped Hub returns failure instead of blocking a caller.
- Existing slow-client removal, duplicate connection safety, persistence ticks,
  logout, and shutdown behavior remain intact.

**Affected modules:**

- `internal/realtime/hub.go`
- `internal/realtime/hub_test.go`
- `internal/realtime/messages.go`
- `internal/storage/users.go`
- `cmd/map-walker/main.go`

**Verification:**

- Use deterministic Hub channels/ticks in tests; do not wait on real timers.
- Test online changed, online unchanged, offline, replacement, and stopped-Hub
  cases.
- Test broadcast payload and recipient behavior with multiple clients.
- Run `go test ./internal/realtime ./internal/storage`.
- Consult `docs/concurrency-debugging.md` before changing tick-based Hub tests.

---

### Task 4: Expose The Authenticated Appearance HTTP Contract

- [ ] Deliver `PUT /api/appearance` with persistence-before-Hub ordering.

**Task boundary:**

- Add strict validation for complete appearance requests.
- Accept only six-digit `#RRGGBB` colors and the four approved shapes.
- Normalize accepted colors to lowercase.
- Persist first, then wait for the Hub result before returning.
- Include appearance in registration, login, and `GET /api/session` responses.
- Do not implement frontend controls in this task.

**Behavioral goals:**

- `PUT /api/appearance` requires an authenticated session and both fields.
- Success returns the normalized complete authoritative appearance.
- Invalid session returns `401`.
- Invalid JSON, missing fields, invalid color, or unsupported shape returns
  `400`.
- Storage failure returns `500` and does not notify the Hub.
- Storage success followed by unavailable Hub returns `503`; the database value
  remains saved and is not rolled back.
- Offline authenticated users can save successfully without a WebSocket.

**Affected modules:**

- `internal/server/server.go`
- `internal/server/auth.go`
- `internal/server/auth_test.go`
- `internal/auth/`
- `internal/storage/`
- `internal/realtime/hub.go`

**Verification:**

- HTTP tests cover authentication, all validation failures, lowercase
  normalization, success response, storage failure, stopped Hub, and offline
  success.
- Assert storage-before-Hub ordering and that failed storage never mutates
  online World state.
- Assert registration, login, and session JSON include appearance.
- Run `go test ./internal/server ./internal/auth ./internal/realtime`.

---

### Task 5: Replace Fixed Leaflet Markers With Appearance-Aware Markers

- [ ] Deliver CSS-rendered Leaflet markers that respond independently to
  position and appearance messages.

**Task boundary:**

- Replace the fixed Leaflet image marker with `L.divIcon`.
- Render `circle`, `square`, `diamond`, and `triangle` using the authoritative
  color.
- Create markers from complete snapshot state.
- Apply `players_delta` only to marker position.
- Apply `appearance_changed` only to marker appearance.
- Preserve tooltip labels, marker anchors, current-player map following, and
  marker removal.
- Do not add the account menu or editor in this task.

**Behavioral goals:**

- All shapes use a consistent logical size and geographic anchor.
- Changing appearance does not move a marker or replace its tooltip.
- Moving a marker does not reset its appearance.
- Snapshot replacement and reconnect remove stale markers and restore complete
  current state.
- Receiving the same appearance repeatedly is harmless.

**Affected modules:**

- `web/app.js`
- `web/styles.css`
- `web/index.html`

**Verification:**

- Run `go test ./...` to catch protocol/server regressions.
- Start the service and verify all four shapes and several colors on the map.
- Verify movement preserves appearance and appearance updates preserve
  position.
- Verify refresh, reconnect, and player removal continue to work.

---

### Task 6: Add The Account Menu And Local Appearance Editor

- [ ] Deliver the discoverable account menu, local preview, save, cancel, and
  failure behavior.

**Task boundary:**

- Turn the upper-right account control into an explicit menu trigger showing
  current appearance, username, and direction indicator.
- Add visible hover, focus, pointer, expanded-state, and mobile touch affordance.
- Add menu actions for appearance editing and logout.
- Add editor preview, four shape options, native `<input type="color">`, save,
  cancel, and error display.
- Keep edits local until save; send one complete `PUT /api/appearance` request.
- Integrate logout and auth bootstrap with appearance UI state.

**Behavioral goals:**

- The collapsed control visibly communicates that it can be opened and exposes
  accurate `aria-expanded` state.
- Opening the editor starts from the last authoritative appearance.
- Editing changes only the preview and sends no network traffic.
- Cancel or close discards unsaved changes.
- Saving disables duplicate submission.
- Success applies the response to the current user's marker and closes the
  editor.
- Failure keeps the preview, displays an error, and permits retry.
- Logout closes and resets the menu/editor and preserves the existing
  no-reconnect behavior.

**Affected modules:**

- `web/index.html`
- `web/styles.css`
- `web/app.js`

**Verification:**

- Verify keyboard, mouse, and touch opening/closing behavior.
- Verify preview, cancel, successful save, failed save, retry, and duplicate
  submission prevention.
- Use two authenticated browser windows to verify realtime remote updates.
- Verify refresh restores persisted appearance and logout clears editor state.
- Run `go test ./...` and `go vet ./...`.

---

## Final Acceptance

- [ ] Review implementation against every acceptance criterion in
  `docs/superpowers/specs/2026-06-12-player-appearance-sync-design.md`.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Manually verify two-window appearance synchronization, movement after
  appearance changes, reconnect restoration, account-menu discoverability, and
  mobile interaction.
- [ ] Update `docs/map-walker-handoff.md` only after implementation and
  verification are complete.

