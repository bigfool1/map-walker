# Map Walker

Map Walker is a server-authoritative shared spatial world in which online
players move and observe nearby players.

## Language

**Movement Pattern**:
The geometric category of an active player's movement, classified by whether
the movement remains within or crosses a spatial Cell boundary.
_Avoid_: AOI-threshold movement, visibility movement

**Visibility Relationship**:
A symmetric relationship between two players that currently makes each player
visible to the other.
_Avoid_: one-way subscription, marker ownership

**Visibility Churn**:
The Visibility Relationships entered or left as the result of movement,
measured separately from the movement's geometric pattern.
_Avoid_: movement class, AOI-threshold movement

**Initial Density**:
The number of Visibility Relationships per player immediately after initial
world construction, before any movement occurs.
_Avoid_: steady-state density, retained density

**Steady-state Density**:
The number of Visibility Relationships per player after movement and AOI
hysteresis have affected the initially constructed world.
_Avoid_: initial density, target density

**AOI Update**:
One application of the active movers' authoritative positions to the spatial
index. Core and World-plus-AOI scenarios share the same AOI Update trajectory.
_Avoid_: simulation tick, broadcast tick

**Baseline Matrix**:
The small representative scenario set used for routine local measurement and
comparison.
_Avoid_: full matrix, smoke test

**Full Matrix**:
The exhaustive frozen scenario set used explicitly for formal target-hardware
measurement.
_Avoid_: baseline matrix, default matrix

**Compact Movement Schedule**:
A deterministic sequence of movement instructions that expands into the shared
AOI Update trajectory without storing every target position as a separate
object.
_Avoid_: precomputed move objects, runtime-random movement

**Memory Guard**:
A parent-process safety boundary that stops a benchmark scenario when its RSS
exceeds a configured share or amount of physical memory.
_Avoid_: Go memory limit, GC tuning

**Logical Core Capacity Baseline**:
Evidence about the single-process World and AOI workload boundary without Hub,
encoding, send queues, sockets, or real client connections.
_Avoid_: online-player capacity, production capacity

**Scenario Repeat**:
One process-isolated execution of a scenario. Repeats share workload identity
but never heap, GC history, or profiling instrumentation.
_Avoid_: tick sample, benchmark iteration

**Reproducible Workload**:
A workload regenerated consistently from the same commit, Go version, explicit
seed, dependency versions, and arguments.
_Avoid_: cross-version byte identity, unseeded workload

**Build Order**:
The deterministic seeded shuffle that defines the sequence in which players
enter an initially empty spatial world.
_Avoid_: grid order, map iteration order

**Primary Metric**:
A headline measurement used directly to describe the logical core's latency,
throughput, memory boundary, or relationship churn.
_Avoid_: diagnostic metric

**Diagnostic Metric**:
A supporting measurement used to explain a Primary Metric or profiling result,
not to headline capacity conclusions.
_Avoid_: primary metric, success criterion

**Natural GC Run**:
A Scenario Repeat that leaves Go's configured garbage collector to react to
normal allocation pressure without an explicit pre-measurement collection.
_Avoid_: forced-clean-heap run, GC-disabled run

**Serial Core Baseline**:
A Logical Core Capacity Baseline whose World and AOI work executes serially
inside one process, while the Go runtime and operating system may use other CPU
cores.
_Avoid_: single-core server, multi-core scaling benchmark

## Example Dialogue

Developer: This Movement Pattern crosses a Cell boundary. Should it count as
visibility movement?

Domain expert: No. Record the Cell crossing as the Movement Pattern, then
measure any entered or left Visibility Relationships as Visibility Churn.

Developer: Does the normal scenario still need exactly 50 neighbors after
warm-up?

Domain expert: No. About 50 neighbors defines its Initial Density. Report the
resulting Steady-state Density separately.

Developer: Does one World simulation tick equal one AOI Update?

Domain expert: No. World simulation runs twice for each AOI Update. Both
benchmark layers still follow the same sequence of 120 AOI Updates.

Developer: Should the default command run every scale and density combination?

Domain expert: No. Run the Baseline Matrix by default and request the Full
Matrix explicitly for formal measurement.

Developer: Does a Compact Movement Schedule reduce benchmark load?

Domain expert: No. It expands to the same mover count and positions before each
timed AOI Update; it only avoids retaining duplicate driver data.

Developer: Does the Memory Guard change the Go runtime's GC behavior?

Domain expert: No. It observes the child process externally and terminates only
after the configured RSS boundary is crossed.

Developer: Does one million logical Observers prove one million online
connections?

Domain expert: No. It is a Logical Core Capacity Baseline. End-to-end online
capacity remains unmeasured.

Developer: Are 100 Tick samples enough to show reproducibility?

Domain expert: They show variation within one Scenario Repeat. Compare three
process-isolated Scenario Repeats to show run-to-run variation.

Developer: Must the workload remain byte-identical across future Go versions?

Domain expert: No. It is a Reproducible Workload within the recorded commit and
environment; cross-version byte identity is not promised.

Developer: Should Build insert players in coordinate order?

Domain expert: No. Use the scenario's Build Order for every repeat so insertion
cost reflects the same distributed arrival sequence.

Developer: Should candidate checks appear beside latency on the report
headline?

Domain expert: No. Latency is a Primary Metric. Candidate checks are a
Diagnostic Metric used to explain latency changes.

Developer: Should every repeat call `runtime.GC()` before timing?

Domain expert: No. A Natural GC Run records the configured collector's behavior
without manufacturing a freshly cleaned heap.

Developer: Is the benchmark running on a single-core server?

Domain expert: No. It is a Serial Core Baseline on the available machine; the
runtime may use multiple cores even though World and AOI work is serial.
