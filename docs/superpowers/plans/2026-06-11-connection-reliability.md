# Connection Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect stale WebSocket clients and let browsers reconnect continuously after temporary connection failures without changing the authoritative movement model.

**Architecture:** Each realtime client owns protocol-level heartbeat and ends its existing connection lifecycle when liveness, reading, or writing fails. The Hub remains the only actor that removes connections and players. The browser owns one current WebSocket and one retry schedule, retains the existing map while disconnected, and uses a full snapshot to restore authoritative rendering after reconnecting.

**Tech Stack:** Go 1.26, `github.com/coder/websocket`, Go tests, plain JavaScript, Leaflet/Amap.

---

## File Map

- Modify: `internal/realtime/client.go`
  - Own protocol-level heartbeat and unify connection termination.
- Create: `internal/realtime/client_test.go`
  - Verify heartbeat-driven connection cleanup and lifecycle completion.
- Modify: `internal/realtime/hub_test.go`
  - Preserve duplicate-ID replacement guarantees during old/new connection overlap.
- Modify: `web/app.js`
  - Own retry state, exponential backoff, obsolete-socket protection, and reconnect status.
- Modify: `web/index.html`
  - Provide the initial Chinese connection status.
- Modify: `web/styles.css`
  - Keep the existing three status presentations aligned with the new text states.
- Modify: `README.md`
  - Document heartbeat, automatic reconnection, and reconnect identity semantics.
- Modify: `docs/map-walker-handoff.md`
  - Record completion and remaining reliability limitations.

No application-level heartbeat message is added to
`internal/realtime/messages.go`. The Hub remains lock-free and does not take
ownership of heartbeat scheduling.

---

### Task 1: Add Server-Side Connection Liveness

**Files:**
- Modify: `internal/realtime/client.go`
- Create: `internal/realtime/client_test.go`

- [ ] Add protocol-level heartbeat ownership to each realtime client using internal timing policy.
- [ ] Ensure heartbeat, read, and write failures all finish the same client lifecycle.
- [ ] Preserve the existing deferred Hub unregister path as the only route for removing the connection and player.
- [ ] Keep outbound write serialization and slow-client disconnection behavior intact.
- [ ] Add deterministic coverage showing that an unresponsive peer is disconnected and the client lifecycle completes.
- [ ] Confirm responsive peers remain connected across heartbeat cycles.

**Acceptance criteria:**

- A silently dead peer is detected without waiting for a game message.
- Heartbeat does not add state mutation or locking outside `Hub.Run`.
- Stopping or replacing a client cannot leave a heartbeat worker running.
- Existing input and outbound message behavior remains unchanged while the peer is healthy.

**Verification:**

- Run the focused realtime client tests.
- Run `go test ./internal/realtime`.

---

### Task 2: Preserve Replacement-Connection Safety

**Files:**
- Modify: `internal/realtime/hub_test.go`
- Modify: `internal/realtime/client_test.go`

- [ ] Cover the overlap where a new connection registers with an existing `playerId` before the old connection finishes shutting down.
- [ ] Verify the old connection's later heartbeat or unregister event cannot remove the replacement connection.
- [ ] Verify the replacement connection can restart its input sequence and receives the expected world snapshot.
- [ ] Keep player removal routed through the Hub actor loop.

**Acceptance criteria:**

- Exactly one current client remains associated with the shared `playerId`.
- Shutdown events from the obsolete client do not remove or reset the replacement.
- No lock is added to Hub or World.

**Verification:**

- Run the focused duplicate-ID Hub tests.
- Run `go test ./internal/realtime`.

---

### Task 3: Add Continuous Frontend Reconnection

**Files:**
- Modify: `web/app.js`

- [ ] Manage one current WebSocket and at most one pending retry timer.
- [ ] Reconnect continuously with delays of 1, 2, 4, and 8 seconds, followed by a 10-second ceiling.
- [ ] Use `close` as the sole reconnect trigger so `error` and `close` cannot schedule parallel attempts.
- [ ] Ignore messages and lifecycle events emitted by obsolete WebSocket instances.
- [ ] Reset the retry count and backoff after a successful connection.
- [ ] Reuse the existing `sessionStorage` player ID for every attempt.
- [ ] Keep markers visible while disconnected and let the next `world_snapshot` reconcile them.
- [ ] Do not queue disconnected input history; send only the latest input state when the socket opens.

**Acceptance criteria:**

- Server loss starts exactly one retry sequence.
- Retry attempts continue indefinitely and never exceed a 10-second interval.
- Server recovery reconnects the page without refresh.
- A successful reconnect sends the current controls and receives a fresh spawn snapshot.
- Late events from an older socket cannot change status or rendering owned by the current socket.

**Verification:**

- Use browser developer tools or controlled server restarts to observe retry timing.
- Exercise rapid stop/start cycles to check for duplicate attempts.
- Confirm marker retention and later snapshot reconciliation.

---

### Task 4: Update Connection Status Presentation

**Files:**
- Modify: `web/index.html`
- Modify: `web/app.js`
- Modify: `web/styles.css`

- [ ] Show `连接中` during the initial attempt.
- [ ] Show `已连接` after a successful open.
- [ ] Show `连接已断开，正在重连（第 N 次）` while waiting for or making retries.
- [ ] Keep the existing connected, connecting, and disconnected visual styles without adding controls, dialogs, or panels.

**Acceptance criteria:**

- Status text always reflects the current WebSocket lifecycle.
- Retry numbering advances once per scheduled attempt and resets after success.
- The status remains readable on desktop and mobile layouts.

**Verification:**

- Check initial load, active connection, repeated failure, and successful recovery states in the browser.
- Confirm no stale socket can overwrite the current status.

---

### Task 5: Document The Reliability Contract

**Files:**
- Modify: `README.md`
- Modify: `docs/map-walker-handoff.md`

- [ ] Document that the server detects stale clients with protocol-level heartbeat.
- [ ] Document continuous automatic reconnection and its capped exponential backoff.
- [ ] Clarify that reconnecting reuses `playerId` but creates the player at the server spawn position.
- [ ] Clarify that disconnected input history and previous coordinates are not restored.
- [ ] Update known limitations without claiming session persistence or seamless state restoration.

**Acceptance criteria:**

- README behavior matches the implemented user experience.
- The handoff clearly distinguishes completed reliability work from deferred session restoration.

**Verification:**

- Review both documents against the approved design and observed behavior.

---

### Task 6: Complete End-to-End Verification

**Files:**
- Modify only files implicated by failures found during verification.

- [ ] Run all Go tests.
- [ ] Run Go static analysis.
- [ ] Start the service and confirm the initial connection reaches `已连接`.
- [ ] Stop the service and confirm markers remain while retry delays increase and cap at 10 seconds.
- [ ] Restart the service and confirm reconnection succeeds without refreshing the page.
- [ ] Confirm the same `playerId` reconnects at the server spawn position and the full snapshot corrects stale markers.
- [ ] Repeat with two windows and confirm an obsolete connection cannot remove its replacement.
- [ ] Confirm keyboard, joystick, neutral-input safety, simulation frequency, and delta broadcasts still behave as before.

**Acceptance criteria:**

- `go test ./...` passes.
- `go vet ./...` passes.
- Manual reconnect scenarios pass on desktop-sized and mobile-sized browser views.
- No regression is observed in authoritative movement or duplicate-ID handling.

---

## Commit Boundaries

Keep commits aligned with independently reviewable behavior:

1. Server heartbeat and lifecycle tests.
2. Frontend reconnection and status behavior.
3. Documentation and final verification adjustments.

Do not mix unrelated refactoring, protocol changes, persistence, client-side
prediction, or session restoration into this phase.
