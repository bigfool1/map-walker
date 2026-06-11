# Account And Position Persistence Design

Date: 2026-06-12

## Goal

Add traditional username/password accounts, persistent Cookie sessions, and
last-position restoration to Map Walker.

This phase establishes a stable player identity before multiplayer scaling work.
Area of interest (AOI) is intentionally deferred to a separate design.

## Scope

This phase includes:

- Username and password registration.
- Login, logout, and current-session HTTP APIs.
- A 30-day persistent Cookie backed by a server-side session.
- Cookie authentication for WebSocket upgrades.
- SQLite persistence using `database/sql`.
- Forward-only embedded SQL migrations.
- Periodic batched position saves and final saves on genuine disconnect.
- Position restoration when an offline user returns.
- A centered login/register card over the map.
- A small authenticated account control with logout.

This phase excludes:

- AOI, spatial indexes, or interest-filtered broadcasts.
- Multiple characters per account.
- Simultaneous control of one player from multiple connections.
- Email verification, password reset, or account recovery.
- Login or registration rate limiting.
- Sliding session expiration.
- Seamless session continuation after a session expires.
- ORM adoption.

## Identity Model

Each user has a stable server-generated user ID. That ID is also the player's
stable ID in the realtime World and WebSocket protocol.

The username is used for login and display. Usernames are case-insensitive for
uniqueness and authentication:

- Registration preserves the user's original spelling for display.
- A normalized username is stored for lookup and uniqueness.
- Registering `Alice` prevents a second registration of `alice`.
- Either spelling can be used to log in.

The browser no longer generates or submits a `playerId`. The `/ws` handler
derives the user and player identity only from the authenticated Cookie session.
An unauthenticated WebSocket request is rejected before upgrade.

## Registration And Login

Registration requirements:

- Username length is 3 to 32 characters.
- Password length is at least 8 characters.
- Username comparison is case-insensitive.
- Passwords are hashed with bcrypt.
- Plaintext or reversibly encrypted passwords are never stored.

Successful registration immediately creates a session and signs the user in.
The user does not have to submit the login form after registering.

The HTTP JSON API is:

- `POST /api/register`
- `POST /api/login`
- `POST /api/logout`
- `GET /api/session`

Authentication errors are returned as short user-facing failures and do not
expose password hashes, session tokens, SQL details, or internal errors.

## Session Model

The server creates a cryptographically random session token after registration
or login.

- The browser receives the original token in a Cookie.
- The database stores only a hash of the token.
- The session expires 30 days after creation.
- Session expiration does not slide forward during use.
- Logout deletes or revokes the server-side session and clears the Cookie.

The Cookie is:

- Persistent for 30 days.
- `HttpOnly`.
- `SameSite=Lax`.
- `Secure` when the application is served over HTTPS.

A session that expires while a WebSocket is already connected does not
immediately disconnect that socket. The next HTTP session check or WebSocket
reconnect must authenticate again. If authentication fails, the frontend
returns to the login card.

## Database

SQLite is accessed through Go's `database/sql`; no ORM is introduced.

The default database path is:

```text
data/map-walker.db
```

The `data/` directory is excluded from version control.

Initial persisted data includes:

### Users

- Stable user ID.
- Display username in its registered spelling.
- Normalized username with a uniqueness constraint.
- Bcrypt password hash.
- Creation time.
- Last latitude.
- Last longitude.

The last-position fields may represent a not-yet-positioned user. A newly
registered user without a saved position uses the configured World spawn.

### Sessions

- Session token hash.
- User ID.
- Creation time.
- Expiration time.

### Schema Migrations

- Applied migration version.
- Application timestamp if needed by the migration mechanism.

## Migration Strategy

SQL migration files are embedded in the Go binary and applied automatically at
service startup in numeric order.

Migrations are forward-only:

```text
001_initial.sql
002_add_player_name.sql
003_create_inventory.sql
```

Applied versions are recorded in `schema_migrations`. An applied migration file
is never edited or rerun.

Future database evolution uses a new migration:

- Create new tables with `CREATE TABLE`.
- Add compatible fields with `ALTER TABLE ... ADD COLUMN`.
- Backfill or transform existing data in the new migration.
- For unsupported destructive SQLite changes, create a replacement table,
  migrate data, and replace the old table.

This phase does not add automatic migration rollback. Database backup is an
operational prerequisite before applying destructive future migrations.

## Realtime Connection Semantics

One account corresponds to one online player.

When a new WebSocket connects for an already-online user:

- The new connection replaces the old connection.
- The existing in-memory World position is retained.
- The database position is not reloaded.
- The player is not removed and recreated.
- The replacement connection may restart its input sequence.
- The obsolete connection's later shutdown cannot remove the replacement.
- The obsolete connection cannot save an older position over the current one.

When a user is not currently in the World:

- The server loads the saved position before registration.
- A saved position becomes the player's initial World position.
- A user without a saved position uses the configured spawn position.

This preserves continuity across refreshes and device changes while keeping one
authoritative online player per account.

## Position Persistence

`Hub.Run()` remains the only owner of World state and online player mutation.
Database operations must not run synchronously inside the simulation actor
loop.

An independent persistence worker receives immutable position records and
writes them to SQLite.

Every five seconds:

- The Hub identifies players whose positions changed since the previous save.
- It creates immutable position records for those players.
- It submits one batch to the persistence worker.
- Submission does not transfer World ownership to the worker.

On a genuine player departure:

- The Hub captures the final authoritative position.
- The final position is submitted for persistence.
- The player is then removed according to the realtime lifecycle.

A connection replacement for the same online user is not a genuine departure.
It must not remove the player or submit an obsolete final position.

An abnormal process or machine failure may lose up to one five-second save
interval of movement.

## Persistence Worker Lifecycle

The worker:

- Accepts immutable user ID and position data.
- Batches database updates supplied by the Hub.
- Never reads or modifies World.
- Preserves update ordering so an older save cannot overwrite a newer save.
- Reports unrecoverable write failures through the application's existing
  logging and shutdown policy without mutating realtime state.

During graceful service shutdown:

1. Stop accepting new connections.
2. Capture final positions for all online players.
3. Submit those final positions.
4. Stop accepting new persistence work.
5. Wait for submitted work to complete.
6. Close the database.

The 20 Hz simulation and 10 Hz broadcast schedules must not wait on SQLite
latency.

## Frontend Flow

The map remains the page background for both authenticated and unauthenticated
states.

On page load:

1. Call `GET /api/session`.
2. If authenticated, hide the auth card, show the account control, and connect
   the WebSocket.
3. If unauthenticated, do not connect the WebSocket and show the centered auth
   card.

The centered card switches between login and registration forms without
navigating to a separate page.

After successful registration or login:

- Hide the auth card.
- Show the authenticated account control.
- Connect the WebSocket.
- Render the returned world snapshot.

The authenticated control appears in the upper-right corner and contains:

```text
用户名 | 退出
```

It remains small on desktop and mobile and does not introduce a sidebar or
menu.

Logout:

- The logout API asks the realtime lifecycle to remove the current player and
  submit the final authoritative position.
- The logout API waits for the persistence worker to commit that final
  position.
- The server revokes the session and clears the Cookie.
- The frontend closes or abandons the current WebSocket after logout succeeds.
- Hide the account control.
- Show the centered login card.

The frontend must prevent automatic reconnect after intentional logout.

## Error Handling

- Duplicate registration reports that the username is unavailable.
- Invalid login reports a generic username-or-password failure.
- Expired or missing sessions are treated as unauthenticated.
- Failed WebSocket authentication returns the frontend to the login state.
- Authentication errors are displayed within the centered card.
- Database and internal server details are logged server-side, not exposed in
  JSON responses.
- Position persistence failures do not grant the worker ownership of World or
  block simulation ticks.

No separate error page, password recovery flow, modal, or fallback identity is
added.

## Testing

Automated tests cover:

- Username normalization and case-insensitive uniqueness.
- Username and password validation.
- Bcrypt password verification.
- Session creation, token hashing, expiration, and revocation.
- Cookie login and logout behavior.
- Unauthenticated WebSocket rejection.
- Authenticated WebSocket identity derived from the session.
- Saved-position restoration for an offline user.
- Configured spawn for a user without a saved position.
- Same-account replacement preserving the current in-memory position.
- Obsolete connection shutdown not removing or overwriting its replacement.
- Five-second dirty-player batching.
- Final save on genuine disconnect.
- Persistence ordering that prevents stale overwrites.
- Graceful shutdown flushing online positions and draining the worker.

Project verification:

```bash
go test ./...
go vet ./...
```

Manual browser verification:

1. Register and enter the map without a second login.
2. Refresh and confirm the persistent Cookie restores the authenticated state.
3. Move, wait for a periodic save, restart the service, and confirm the saved
   position is restored.
4. Move and log out, then log in and confirm the logout position is restored.
5. Open the same account in another window and confirm the new connection
   replaces the old connection while retaining the current in-memory position.
6. Expire or revoke the session, disconnect, and confirm reconnect returns to
   the login card.
7. Confirm the upper-right account control and centered auth card work at
   desktop and mobile widths.

## Deferred AOI Phase

AOI is a separate next phase because it changes snapshot and delta visibility,
not identity or persistence.

The account phase should provide AOI with stable user IDs and restored
authoritative positions. The later AOI design can then focus on spatial
membership, enter/leave visibility events, broadcast reduction, and load
measurement without also changing authentication or database ownership.
