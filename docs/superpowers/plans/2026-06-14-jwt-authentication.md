# JWT Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Track each
> task with its checkbox.

**Goal:** Replace database-backed opaque sessions with 24-hour stateless JWT
authentication while preserving existing browser, HTTP, WebSocket, persistence,
and player restoration behavior.

**Architecture:** `auth.Service` signs and verifies fixed-algorithm HS256 JWTs
using an injected secret and clock. Request authentication becomes stateless,
while endpoints that return user profile data and the Hub player-loading path
continue reading current database state.

**Tech Stack:** Go 1.26 standard library, HMAC-SHA256, existing bcrypt,
HTTP-only cookies, SQLite/MySQL storage.

**Required context:** Read
`docs/superpowers/specs/2026-06-14-jwt-authentication-design.md`,
`docs/map-walker-handoff.md`, and `AGENTS.md`.

---

### Task 1: Implement Fixed HS256 JWT Signing And Verification

- [ ] Deliver the standalone JWT identity format and validation behavior.

**Task boundary:**

- Replace opaque session-token generation and hashing helpers with standard
  library JWT signing and verification.
- Use a fixed `HS256` header and HMAC-SHA256 signature comparison through
  `hmac.Equal`.
- Include only `sub`, `username`, `iat`, and `exp` claims.
- Set `exp` to exactly 24 hours after `iat`.
- Require all claims with valid types and timestamps.
- Return `ErrUnauthenticated` for missing, malformed, forged, incomplete, or
  non-HS256 tokens.
- Return `ErrSessionExpired` when the injected current time is equal to or
  later than `exp`.
- Preserve the `map_walker_session` cookie name and change its lifetime to
  24 hours.
- Do not add third-party JWT packages, refresh tokens, revocation, clock-skew
  tolerance, or multiple algorithms.

**Behavioral goals:**

- The same non-empty secret verifies tokens issued by any service instance.
- Modified headers, payloads, and signatures are rejected.
- Token verification does not query storage.
- Tests can control issue and expiry time without sleeping.
- Cookie security attributes remain HttpOnly, SameSite=Lax, path `/`, and
  conditionally Secure.

**Affected modules:**

- Replace or rewrite `internal/auth/session.go`
- Modify `internal/auth/cookie.go`
- Create or modify focused JWT tests under `internal/auth/`
- Modify cookie assertions in `internal/auth/service_test.go` or a focused
  cookie test file

**Verification:**

- Run `go test ./internal/auth`.
- Verify a valid token round trip exposes the expected claims.
- Verify signature, payload, algorithm, encoding, JSON, segment-count, missing
  claim, and invalid claim-type failures.
- Verify a token is accepted immediately before expiry and returns
  `ErrSessionExpired` exactly at expiry.
- Verify cookie name and security attributes are unchanged and MaxAge is
  24 hours.

---

### Task 2: Migrate Auth Service From Persistent Sessions To JWT Identity

- [ ] Make registration, login, authentication, and profile loading use the new
  JWT boundary.

**Task boundary:**

- Change `auth.Service` construction to require a non-empty JWT secret and
  support an injectable clock for tests.
- Keep registration validation, bcrypt hashing, duplicate-name handling, user
  creation, and response behavior unchanged.
- Sign a JWT after successful registration and login instead of creating a
  session row.
- Make token authentication verify claims without loading the user record.
- Add an explicit user-profile lookup by authenticated user ID for endpoints
  that need current username and appearance.
- Remove server-side token deletion from logout behavior.
- Keep appearance persistence and password verification unchanged.
- Do not implement Synthetic Client identity issuance in this phase.

**Behavioral goals:**

- Registration and login still return an authenticated identity and token.
- Authentication succeeds from valid signed claims even without a session
  record or user lookup.
- Current profile data remains obtainable from the database after identity
  verification.
- Invalid credentials and duplicate usernames retain their existing errors.
- A non-empty secret is required through service construction; no default or
  minimum length is imposed.

**Affected modules:**

- Modify `internal/auth/service.go`
- Modify `internal/auth/service_test.go`
- Modify `internal/auth/appearance_test.go`
- Use existing user lookup functions from `internal/storage/users.go`

**Verification:**

- Run `go test ./internal/auth`.
- Verify registration creates the user and returns a verifiable JWT without
  creating a session record.
- Verify login still checks bcrypt and returns a verifiable JWT.
- Verify authentication does not require storage access after token
  verification.
- Verify profile lookup returns the latest stored username and appearance.
- Verify missing or empty secret construction fails clearly.

---

### Task 3: Migrate HTTP, WebSocket, Logout, And Startup Configuration

- [ ] Preserve external application behavior while switching all request
  authentication to JWT.

**Task boundary:**

- Keep registration and login response JSON unchanged and store the JWT in the
  existing cookie.
- Make `GET /api/session` verify JWT identity, then load the current database
  user profile by `sub`.
- Return `401` when a valid JWT references a missing user in the session
  bootstrap endpoint.
- Keep appearance endpoints authenticated by JWT and preserve current storage
  behavior.
- Make WebSocket handshake authentication verify JWT without a user-existence
  lookup.
- Keep Hub registration and saved player loading responsible for restoring
  database position, appearance, and stored username.
- Keep logout ordering as disconnect current WebSocket, save final position,
  then clear the cookie.
- Remove logout token-revocation calls; a separately retained JWT remains valid
  until expiry.
- Read `MAP_WALKER_JWT_SECRET` during service startup and fail before serving
  when it is missing or empty.
- Do not disconnect established WebSockets at token expiry or reauthenticate
  individual messages.

**Behavioral goals:**

- Existing frontend login, registration, session bootstrap, appearance, logout,
  and reconnect flows require no protocol changes.
- Valid JWTs authenticate HTTP and WebSocket requests.
- Expired, malformed, forged, and old opaque tokens receive `401`.
- Existing WebSockets remain active after JWT expiry; later reconnects require
  a new login.
- Logout clears the browser cookie without claiming server-side revocation.
- Service startup never generates or logs a signing secret.

**Affected modules:**

- Modify `internal/server/auth.go`
- Modify `internal/server/appearance.go` if profile loading changes its auth
  call pattern
- Modify `internal/server/websocket.go`
- Modify `internal/server/auth_test.go`
- Modify `internal/server/appearance_test.go`
- Modify `internal/server/websocket_test.go`
- Modify `cmd/map-walker/main.go`
- Add or modify startup configuration tests where practical

**Verification:**

- Run `go test ./internal/server ./cmd/map-walker`.
- Verify registration and login set a 24-hour JWT cookie and preserve response
  payloads.
- Verify session bootstrap returns the latest stored appearance.
- Verify a missing user after valid JWT verification returns `401` from
  `/api/session`.
- Verify logout disconnects the WebSocket, persists final position, and clears
  the cookie.
- Verify a retained JWT still authenticates after logout until expiry.
- Verify valid JWT WebSocket admission and `401` responses for expired, forged,
  malformed, and old opaque tokens.
- Verify missing `MAP_WALKER_JWT_SECRET` prevents service startup.

---

### Task 4: Remove Session Storage Runtime Code And Update Documentation

- [ ] Complete the migration by removing unused session persistence APIs while
  preserving forward-only schema history.

**Task boundary:**

- Delete session storage implementation and its focused storage tests.
- Remove obsolete opaque-token hashing, session expiry, creation, lookup, and
  deletion references.
- Keep the historical `sessions` table in `001_initial.sql`.
- Do not add a migration that drops or modifies the table.
- Update project instructions and handoff to describe JWT authentication,
  24-hour expiry, logout limitations, and required secret configuration.
- Add local run examples with a placeholder secret and recommend
  `openssl rand -base64 32`.
- Do not commit or log a real secret.
- Keep Synthetic Client design and implementation out of this phase.

**Behavioral goals:**

- No runtime authentication path reads or writes the `sessions` table.
- Existing databases continue migrating without destructive schema changes.
- Documentation accurately states that logout clears the cookie but cannot
  revoke a copied JWT.
- Local and deployed startup instructions include the required environment
  variable.

**Affected modules:**

- Delete `internal/storage/sessions.go`
- Modify `internal/storage/users_test.go` to remove session CRUD coverage
- Modify other storage tests only where they reference removed session APIs
- Keep `internal/storage/migrations/001_initial.sql` unchanged
- Modify `AGENTS.md`
- Modify `docs/map-walker-handoff.md`
- Modify any existing local run documentation containing bare
  `go run ./cmd/map-walker`

**Verification:**

- Run `rg -n "CreateSession|GetSession|DeleteSession|HashSessionToken|NewSessionToken|SessionExpiresAt" --glob '*.go'` and confirm no obsolete runtime
  references remain.
- Run `go test ./internal/storage ./internal/auth ./internal/server`.
- Run `go test ./...`.
- Run `go vet ./...`.
- Verify migrations still create the historical `sessions` table.
- Verify documentation examples use `MAP_WALKER_JWT_SECRET` without embedding a
  real secret.
