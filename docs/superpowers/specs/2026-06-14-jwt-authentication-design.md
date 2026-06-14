# JWT Authentication Design

Date: 2026-06-14

## Goal

Replace database-backed opaque sessions with stateless JWT authentication for
the short-lived Map Walker demo while preserving the existing browser login,
cookie, HTTP API, WebSocket, and player-state restoration behavior.

This is an authentication migration only. In-process Synthetic Clients are a
separate follow-up design.

## Context

The current authentication flow creates a random opaque token, stores its
SHA-256 hash in the `sessions` table, and reads or deletes that session record
for authentication and logout.

The demo does not require:

- Active token revocation.
- User bans.
- Refresh tokens.
- Long-term login continuity.
- Per-message WebSocket reauthentication.

Stateless JWT authentication avoids session-record accumulation and allows
future in-process clients to use the same signed identity format without
requiring persistent session rows.

## Scope

This phase includes:

- JWT signing and verification using the Go standard library.
- A 24-hour token lifetime.
- JWT issuance after registration and login.
- JWT authentication for HTTP and WebSocket requests.
- Database-backed user-profile loading after authentication where current API
  behavior requires it.
- Logout without server-side token revocation.
- Removal of session storage code and tests.
- Updated startup configuration and documentation.

This phase excludes:

- Refresh tokens.
- Revocation lists or token deny lists.
- User banning.
- JWT key rotation.
- Multiple signing algorithms.
- Clock-skew tolerance.
- Proactive WebSocket disconnection when a token expires.
- Reauthentication of individual WebSocket messages.
- Synthetic Client implementation.

## JWT Format

JWTs use fixed `HS256` signing implemented with the Go standard library:

- Base64url-encoded JSON header.
- Base64url-encoded JSON payload.
- HMAC-SHA256 signature.
- `hmac.Equal` for signature comparison.

The verifier requires the decoded header's `alg` to equal `HS256`. It does not
select algorithms based on untrusted token input.

Claims:

- `sub`: stable database user ID.
- `username`: current immutable username at issue time.
- `iat`: Unix timestamp when issued.
- `exp`: Unix timestamp exactly 24 hours after issue.

The token does not contain:

- Appearance.
- Position.
- Password data.
- Session identifiers.
- Mutable gameplay state.

## Secret Configuration

The signing secret comes only from `MAP_WALKER_JWT_SECRET`.

- Missing or empty secret causes service startup to fail with a clear error.
- The application does not provide a built-in default.
- The application does not generate a random secret at startup.
- The application does not enforce a minimum secret length.
- Documentation recommends generating a random high-entropy secret.
- Tests inject a fixed non-empty secret through configuration rather than
  mutating process environment.
- The secret is never logged or included in benchmark metadata.

All service instances that must accept the same login cookie must share the
same secret.

## Auth Service

`auth.Service` owns:

- JWT signing secret.
- A 24-hour token lifetime.
- An injectable `now` function for deterministic time tests.

Registration:

1. Validate username and password.
2. Hash the password with bcrypt.
3. Create the database user.
4. Load the created user record.
5. Sign and return a JWT.

Login:

1. Validate credentials.
2. Load the database user by normalized username.
3. Verify the bcrypt password hash.
4. Sign and return a JWT.

Authentication:

1. Parse the three JWT segments.
2. Decode and validate the fixed header.
3. Verify the HMAC-SHA256 signature.
4. Decode and validate all required claims.
5. Reject the token when `now >= exp`.
6. Return authenticated identity from `sub` and `username`.

JWT authentication does not query the database to confirm that the user still
exists.

## User Profile And Player State

Identity authentication and state loading remain separate.

`GET /api/session`:

1. Verify the JWT.
2. Load the user record by `sub`.
3. Return the current user ID, stored username, and latest appearance.

If the user record no longer exists, the endpoint returns `401`.

WebSocket handshake:

1. Verify the JWT without a user lookup.
2. Construct the realtime client using `sub` and claim username.
3. Register the client with the Hub.

On first World registration, the existing `loadSavedPlayer(userID)` path still
loads persisted position, appearance, and username. The stored username is
authoritative when present; the claim username remains a fallback.

Appearance updates continue to use the authenticated `sub` and existing
storage behavior.

## Cookie And Browser Behavior

The cookie name remains `map_walker_session`.

- Registration and login place the JWT in the existing HttpOnly,
  SameSite=Lax cookie.
- Cookie lifetime matches the 24-hour JWT lifetime.
- Existing Secure-cookie behavior remains unchanged.
- Old opaque session tokens fail JWT parsing and return `401`.
- Existing logged-in users must log in again after deployment.

The browser does not need to know that the cookie value changed from an opaque
token to a JWT.

## Logout And Expiry

Logout keeps the current user-visible sequence:

1. Disconnect the current WebSocket.
2. Save final position through the existing realtime lifecycle.
3. Clear the JWT cookie.

Logout does not revoke the JWT. A copied token remains valid until its `exp`
timestamp. This limitation is accepted for the demo.

JWT expiry is checked at HTTP requests and WebSocket handshakes:

- Existing WebSocket connections remain active after token expiry.
- A later reconnect with the expired token receives `401`.
- HTTP session and appearance requests with the expired token receive `401`.
- The frontend can return to login and obtain a new token.

There is no refresh token and no proactive connection-expiry timer.

## Error Semantics

Return `ErrUnauthenticated` for:

- Missing token.
- Wrong JWT segment count.
- Invalid base64url.
- Invalid JSON.
- Missing required header or claim fields.
- Any algorithm other than `HS256`.
- Invalid signature.
- Invalid claim types or timestamps.

Return `ErrSessionExpired` when `now >= exp`.

Existing HTTP handlers continue mapping unauthenticated and expired
authentication to `401`.

## Session Storage Removal

Runtime code no longer creates, reads, or deletes database session records.

Remove:

- `internal/storage/sessions.go`.
- Session storage unit tests.
- Session creation, lookup, hashing, and deletion helpers that are no longer
  used.
- Auth service tests tied to persistent opaque sessions.

Keep:

- The existing `sessions` table in `001_initial.sql`.
- Existing applied database schemas.
- Forward-only migration history.

No migration drops the table in this demo phase.

## Verification

### JWT Unit Tests

- Sign and verify a valid JWT.
- Verify `sub`, `username`, `iat`, and `exp`.
- Reject a modified signature.
- Reject a modified payload.
- Reject a non-`HS256` algorithm.
- Reject malformed segment counts, base64url, JSON, and claim types.
- Reject missing required claims.
- Accept immediately before expiry.
- Return `ErrSessionExpired` exactly at expiry.
- Use an injected clock without sleeping.

### Auth Service Tests

- Registration creates a user and returns an authentic JWT.
- Login verifies bcrypt and returns an authentic JWT.
- Invalid credentials remain rejected.
- Authentication does not require a session database record.
- Authentication does not require a user lookup after token verification.

### HTTP And WebSocket Tests

- Registration and login retain the existing response format.
- Registration and login set the existing cookie name with a 24-hour lifetime.
- `GET /api/session` loads the latest stored appearance after JWT
  authentication.
- A missing user record after valid JWT authentication produces `401` from the
  session endpoint.
- Logout disconnects the WebSocket, saves position, and clears the cookie.
- Logout does not invalidate a separately retained JWT.
- Valid JWTs can open WebSockets.
- Expired, malformed, and forged JWTs receive `401`.
- An old opaque session token receives `401`.

### Storage And Project Verification

- Storage migrations still create the historical `sessions` table.
- No runtime auth path reads or writes the table.
- `go test ./...` passes.
- `go vet ./...` passes.

## Documentation

Update:

- `AGENTS.md` startup instructions to require `MAP_WALKER_JWT_SECRET`.
- `docs/map-walker-handoff.md` authentication, logout, and limitation sections.
- Local run examples with a non-secret placeholder.

Document a recommended secret-generation command such as:

```sh
openssl rand -base64 32
```

Do not commit a real signing secret.

## Follow-up

After JWT migration is complete, design the In-process Synthetic Client as a
separate phase.

Synthetic users will remain real database users with persisted position and
appearance. The design will decide how in-process clients obtain signed
identity, enter the Hub, generate deterministic movement, drain replication
queues, ramp to a target count, and expose load metrics without using
WebSocket transport.
