# Replication Builder — Benchmark Evidence

2026-06-21

## Commands

```bash
go test ./internal/realtime -run '^$' -bench 'Benchmark(HubReplication|ReplicationBuilder|ReplicationDispatcher)' -benchmem -count=1
```

## Headline Results (Apple M5, Go 1.26)

### Hub Replication (end-to-end: world.Step + broadcastReplication + WaitIdle + drain)

| Clients | ops/s | ns/op | msgs/op | bytes/op | B/op | allocs/op |
|---------|-------|-------|---------|----------|------|-----------|
| 2000 | 98 | 13,744,545 | 1994 | 2,987,282 | 17.9M | 87,059 |
| 3000 | 44 | 27,645,081 | 2578 | 5,930,983 | 40.1M | 137,355 |

### Builder (fanout-only, no AOI mutation / encode / send)

| Variant | Recipients | ns/op | jobs/op | accum µs | copy µs | total µs | B/op | allocs/op |
|----------|------------|-------|---------|----------|---------|----------|------|-----------|
| iface-1000 | 1000 | 475,510 | 1000 | 373 | 66 | 439 | 1.1M | 8,254 |
| concrete-1000 | 1000 | 468,292 | 1000 | 372 | 68 | 441 | 1.1M | 8,140 |
| iface-2000 | 2000 | 1,547,223 | 2000 | 1388 | 184 | 1573 | 3.4M | 18,741 |
| concrete-2000 | 2000 | 1,544,901 | 2000 | 1291 | 161 | 1452 | 3.4M | 18,730 |

### Dispatcher (encode/send only, 1000 clients)

| Workers | ns/op | jobs/op | msgs/op | B/op | allocs/op |
|---------|-------|---------|---------|------|-----------|
| 2 | 726,983 | 1000 | 1000 | 1.2M | 23,000 |
| 4 | 616,390 | 1000 | 1000 | 1.2M | 23,000 |
| 8 | 517,167 | 1000 | 1000 | 1.2M | 23,000 |

## Interface vs Concrete Reader

Interface dispatch overhead is within noise at both 1000 and 2000 recipients.
**Decision: keep `ReplicationBuildReader` interface in production.**

## Bottleneck Classification

At 2000 clients, end-to-end broadcast tick is ~13.7ms:

| Component | Approx cost |
|-----------|-------------|
| `world.Step` + AOI mutation | ~11ms (estimated) |
| Builder fanout | ~1.5ms |
| Dispatcher encode/send | ~0.5ms (8 workers) |
| Drain/other | ~0.5ms |

**Next bottleneck: AOI mutation + World state updates** (`snapshotMoverVisibility`,
`applyMovementAOIChanges`, `recalcCollectibleVisibility`,
`advanceCollectibleReplacements`). These are still synchronous in the Hub actor.

Builder fanout (~11% of tick) is not yet the dominant cost. Immutable job copying
(~0.2ms at 2000) is negligible.

**Recommended next step: Candidate A — AOI/fanout algorithm optimization**
(per `docs/superpowers/specs/2026-06-21-replication-builder-design.md`).

## Counter Semantics

| Counter | Meaning |
|---------|---------|
| `ReplicationRecipients` | Builder-produced jobs submitted by Hub to dispatcher |
| `ReplicationMessages` | Dispatcher-encoded non-empty messages |
| `ReplicationBytes` | Dispatcher-encoded bytes |
| `Builder.Jobs` | Jobs produced by `ReplicationBuilder.Build` |
| `Builder.Recipients` | Distinct recipients touched in fanout |

`ReplicationRecipients` ≠ "messages sent" — dispatcher may drop or skip jobs.
