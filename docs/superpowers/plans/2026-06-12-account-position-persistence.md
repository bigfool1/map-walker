# Account And Position Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add username/password accounts, persistent Cookie sessions, authenticated WebSocket identity, and reliable restoration of each user's last saved position.

**Architecture:** SQLite owns durable users, sessions, migration versions, and last positions. HTTP authentication establishes a stable user identity that the WebSocket endpoint passes into the existing Hub actor. The Hub remains the sole owner of World state and submits immutable position batches to an independent persistence worker so database latency never blocks simulation.

**Tech Stack:** Go 1.26, `database/sql`, SQLite, bcrypt, `net/http`, `github.com/coder/websocket`, Go tests, plain JavaScript, Leaflet/Amap.

**Required context:** Read `AGENTS.md`, `docs/map-walker-handoff.md`, and `docs/superpowers/specs/2026-06-12-account-position-persistence-design.md` before implementation. The approved design is authoritative when this plan is ambiguous.

---

## Module Map

- Create: `internal/storage/`
  - Own SQLite opening, embedded forward-only migrations, user/session records,
    saved positions, and persistence-worker database operations.
- Create: `internal/auth/`
  - Own username normalization and validation, password hashing, session-token
    handling, Cookie policy, and authenticated-user lookup.
- Modify: `internal/game/`
  - Allow a newly offline player to enter World at a restored position while
    preserving server authority and existing movement behavior.
- Modify: `internal/realtime/`
  - Use authenticated user IDs, preserve same-account replacement semantics,
    track positions requiring persistence, and coordinate final saves.
- Modify: `internal/server/`
  - Add authentication APIs, protect `/ws`, and coordinate logout with realtime
    departure.
- Modify: `cmd/map-walker/main.go`
  - Wire database, migrations, authentication, persistence worker, Hub, server,
    and graceful shutdown in dependency order.
- Modify: `web/`
  - Add the centered authentication card, session bootstrap, authenticated
    account control, logout, and authentication-aware reconnect behavior.
- Modify: `README.md`, `AGENTS.md`, and `docs/map-walker-handoff.md`
  - Document the new identity, persistence, operational, and package contracts.

AOI, spatial filtering, ORM adoption, account recovery, rate limiting, and
multiple characters remain outside this plan.

---

### Task 1: Establish SQLite And Migration Ownership

- [ ] Deliver an idempotent SQLite startup and forward-only migration boundary.

**Task boundary:**

- Introduce the storage package and the initial embedded, forward-only schema.
- Open the default database at `data/map-walker.db`.
- Apply only migrations not yet recorded in `schema_migrations`.
- Keep migration behavior independent from HTTP, Hub, and World.

**Behavioral goals:**

- A clean startup creates the users, sessions, and migration metadata required
  by the approved design.
- Restarting against the same database is idempotent.
- Applied migration files are never edited or replayed.
- Startup fails clearly when the database cannot open or migrate.

**Affected modules:**

- `internal/storage/`
- `cmd/map-walker/main.go`
- `go.mod`
- `go.sum`
- `.gitignore`

**Verification:**

- Storage tests cover clean migration, repeated startup, ordered migration
  application, and migration failure.
- Start the service with no `data/` directory and confirm the database is
  created.
- Restart the service and confirm no migration is reapplied.
- Run `go test ./internal/storage`.

---

### Task 2: Add Users, Passwords, And Sessions

- [ ] Deliver durable account credentials and 30-day server-side sessions.

**Task boundary:**

- Add storage operations for users, sessions, session expiration/revocation,
  and saved positions.
- Add authentication policy for usernames, bcrypt passwords, random session
  tokens, token hashing, and 30-day persistent Cookies.
- Keep HTTP response formatting and WebSocket behavior out of this task.

**Behavioral goals:**

- Usernames are 3–32 characters and unique case-insensitively while preserving
  registered spelling for display.
- Passwords shorter than eight characters are rejected.
- Only bcrypt password hashes are persisted.
- Only session-token hashes are persisted.
- Sessions expire 30 days after creation and do not slide during use.
- Expired and revoked sessions cannot authenticate.

**Affected modules:**

- `internal/auth/`
- `internal/storage/`
- `go.mod`
- `go.sum`

**Verification:**

- Tests cover normalization, case-insensitive duplicate registration, password
  validation, successful and failed password checks, token hashing, session
  creation, expiration, and revocation.
- Storage integration tests use an isolated SQLite database.
- Run `go test ./internal/auth ./internal/storage`.

---

### Task 3: Expose The Authentication HTTP Contract

- [ ] Deliver the approved registration, login, logout, and session APIs.

**Task boundary:**

- Add `POST /api/register`, `POST /api/login`, `POST /api/logout`, and
  `GET /api/session`.
- Successful registration signs the user in immediately.
- Establish and clear the approved Cookie attributes.
- Keep authentication failures concise and prevent internal details from
  reaching clients.

**Behavioral goals:**

- Registration returns an authenticated session without requiring a second
  login.
- Login accepts any case variant of the registered username.
- Session lookup returns the authenticated user's stable ID and display
  username.
- Invalid credentials return a generic failure.
- Logout revokes the server-side session and clears the Cookie only after the
  realtime final-save contract introduced later can be satisfied.

**Affected modules:**

- `internal/server/`
- `internal/auth/`
- `internal/storage/`

**Verification:**

- HTTP tests cover successful registration, duplicate registration, validation
  failures, successful login, invalid login, authenticated session lookup,
  expired session lookup, and Cookie clearing.
- Tests assert `HttpOnly`, `SameSite=Lax`, 30-day persistence, and HTTPS-aware
  `Secure` behavior.
- Run `go test ./internal/server ./internal/auth ./internal/storage`.

---

### Task 4: Make WebSocket Identity Server-Owned

- [ ] Deliver Cookie-authenticated WebSocket identity with no client-selected player ID.

**Task boundary:**

- Require an authenticated Cookie session before `/ws` upgrades.
- Pass the stable user ID and display username into realtime registration.
- Remove the browser-generated `playerId` identity path.
- Preserve the current one-player-per-ID replacement guarantees.

**Behavioral goals:**

- Unauthenticated and expired-session requests cannot establish a WebSocket.
- Client query parameters cannot choose or impersonate a player ID.
- World snapshots and deltas identify a player by stable user ID.
- A second connection for the same account replaces the old connection without
  removing the in-memory player or resetting its position.
- The obsolete connection cannot unregister or save over its replacement.

**Affected modules:**

- `internal/server/`
- `internal/realtime/`
- `internal/auth/`
- `web/app.js`

**Verification:**

- Server tests cover authenticated and unauthenticated WebSocket upgrade paths.
- Realtime tests cover same-account replacement, input-sequence restart, old
  connection shutdown, and retained in-memory position.
- Tests confirm a client-supplied `playerId` has no authority.
- Run `go test ./internal/server ./internal/realtime`.

---

### Task 5: Restore Offline Players At Saved Positions

- [ ] Deliver saved-position restoration without weakening World authority.

**Task boundary:**

- Load a user's saved position only when that user is not already in World.
- Allow World registration at either a restored position or configured spawn.
- Keep the World free of SQL and authentication dependencies.

**Behavioral goals:**

- A returning offline user starts at the last saved position.
- A user without a saved position starts at the configured spawn.
- An already-online user's replacement connection keeps the current in-memory
  position and does not reload stale database state.
- Existing movement, tick, dirty-delta, and removal semantics remain intact.

**Affected modules:**

- `internal/game/`
- `internal/realtime/`
- `internal/storage/`

**Verification:**

- World tests cover explicit initial positions and default spawn behavior.
- Realtime integration tests cover offline restoration and online replacement.
- Existing authoritative movement tests continue to pass unchanged in
  behavior.
- Run `go test ./internal/game ./internal/realtime ./internal/storage`.

---

### Task 6: Add Asynchronous Position Persistence

- [ ] Deliver periodic and final position saves outside the Hub simulation loop.

**Task boundary:**

- Add an independent persistence worker that accepts immutable position data.
- Have the Hub identify position changes for five-second batches.
- Submit a final position on genuine player departure.
- Preserve ordering so stale work cannot overwrite newer positions.

**Behavioral goals:**

- The 20 Hz simulation and 10 Hz broadcasts never wait for SQLite writes.
- Only players moved since the previous persistence interval are included in
  periodic work.
- A genuine disconnect persists the final authoritative position.
- Same-account connection replacement does not submit a final save or remove
  the player.
- The worker never reads or modifies World.
- Older queued saves cannot overwrite a newer saved position.

**Affected modules:**

- `internal/realtime/`
- `internal/storage/`
- `internal/game/`

**Verification:**

- Deterministic tests drive persistence timing without waiting five real
  seconds.
- Tests cover dirty-player batching, unchanged-player omission, final
  disconnect save, replacement exclusion, write ordering, and worker drain.
- Tests demonstrate that delayed storage work does not stop simulation ticks or
  broadcasts.
- Run `go test ./internal/realtime ./internal/storage`.

---

### Task 7: Complete Logout And Graceful Shutdown Semantics

- [ ] Deliver ordered logout and shutdown flows that commit final positions.

**Task boundary:**

- Coordinate logout with realtime departure and a committed final position.
- Prevent intentional logout from starting frontend auto-reconnect.
- Flush all online positions and drain persistence work during service shutdown.
- Close dependencies in an order that cannot lose accepted final saves.

**Behavioral goals:**

- Logout commits the current authoritative position before revoking the session
  and clearing the Cookie.
- After logout, the user returns to the unauthenticated state and the socket
  stays closed.
- Graceful shutdown stops new connections, captures online positions, commits
  them, drains the worker, and then closes SQLite.
- Session expiry during an existing WebSocket does not proactively disconnect
  it; the next reconnect must authenticate again.

**Affected modules:**

- `internal/server/`
- `internal/realtime/`
- `internal/storage/`
- `cmd/map-walker/main.go`
- `web/app.js`

**Verification:**

- Integration tests cover final-save-before-session-revocation ordering.
- Shutdown tests cover multiple online players and worker drainage.
- Browser verification confirms logout does not reconnect automatically.
- Restart after logout and confirm the logout position is restored.
- Run `go test ./...`.

---

### Task 8: Add The Authentication User Interface

- [ ] Deliver the centered auth card and authenticated account control.

**Task boundary:**

- Add a centered login/register card over the existing map.
- Bootstrap authentication through `GET /api/session`.
- Add the upper-right `用户名 | 退出` account control.
- Integrate authentication state with the existing connection-status and
  reconnect lifecycle.

**Behavioral goals:**

- Unauthenticated pages show the centered card and do not open a WebSocket.
- Login and registration switch within one card without navigation.
- Registration enters the map immediately.
- Authenticated page refresh restores the session and reconnects automatically.
- Authentication errors remain inside the card.
- A failed reconnect caused by an invalid session returns to the login card.
- The account control does not conflict with connection status or the mobile
  joystick.

**Affected modules:**

- `web/index.html`
- `web/app.js`
- `web/styles.css`
- `internal/server/`

**Verification:**

- Manually verify login, registration, validation errors, session refresh,
  logout, and expired-session recovery.
- Verify desktop and mobile-sized layouts.
- Confirm keyboard, joystick, connection status, marker retention, and
  reconnect behavior still work after authentication.

---

### Task 9: Update Project Contracts And Complete End-To-End Verification

- [ ] Deliver updated documentation and verify the complete account lifecycle.

**Task boundary:**

- Document the new packages, routes, database location, migration policy,
  identity source, position-save policy, and known limitations.
- Record completion in the handoff without claiming AOI or seamless session
  continuation.
- Perform full automated and manual verification.

**Behavioral goals:**

- A new engineer can understand how identity reaches World and how positions
  reach SQLite without reading every implementation file.
- Operational documentation explains that `data/` is local state and
  migrations are forward-only.
- The handoff clearly identifies AOI as the next independent phase.

**Affected modules:**

- `README.md`
- `AGENTS.md`
- `docs/map-walker-handoff.md`
- `docs/superpowers/plans/2026-06-12-account-position-persistence.md`

**Verification:**

- Run `go test ./...`.
- Run `go vet ./...`.
- Register and enter the map without a second login.
- Refresh and confirm the persistent session restores authentication.
- Move, wait for a periodic save, restart, and confirm position restoration.
- Move, log out, log back in, and confirm final-position restoration.
- Open the same account in another window and confirm replacement retains the
  current in-memory position.
- Revoke or expire a session, disconnect, and confirm reconnect returns to the
  login card.
- Confirm existing heartbeat, authoritative movement, delta broadcasts,
  keyboard input, and joystick behavior have not regressed.

---

## Suggested Commit Boundaries

1. SQLite migrations and storage operations.
2. Authentication policy and HTTP APIs.
3. Authenticated WebSocket identity and saved-position restoration.
4. Persistence worker, logout, and graceful shutdown.
5. Authentication UI.
6. Documentation and final verification adjustments.

Each commit should leave its affected package tests passing. Do not mix AOI,
spatial indexing, ORM adoption, password recovery, or unrelated refactoring
into this phase.
