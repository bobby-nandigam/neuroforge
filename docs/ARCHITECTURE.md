# NEUROFORGE X — Architecture

NEUROFORGE X is organized as a classic compiler + runtime split. A workload
travels through a frontend, a typed IR, validation, optimization, and a
concurrent execution engine. Each stage is a separate Go package with a narrow
interface.

## Pipeline

```
author ──► Frontend ──► MIR Module ──► Type Checker ──► Optimizer ──► Executor ──► Backends
          (YAML/JSON)   (typed DAG)    (annotate +      (rewrite)     (schedule)   (run)
                                        validate)
```

### 1. Frontend (`core/compiler`)
Lowers an authored workload (loose, human-friendly) into a strict MIR `Module`.
Inputs are written as a `port: source` map and lowered into ordered `Binding`s.
Structural only — no semantic judgement here.

### 2. MIR (`core/ir`)
The intermediate representation: `Module` → `Operation`s with typed input/output
`Port`s, `Binding`s (dataflow), `DependsOn` edges, execution semantics
(`Deterministic`, `SideEffects`, `Cacheable`), policies (timeout, retries,
failure policy), and resource hints. An **operator registry** declares each
operator type's signature, default semantics, and supported backends — the one
source of truth the type checker and backend selector consult. MIR is JSON-
serializable with a versioned schema.

### 3. Graph (`core/graph`)
Builds the dependency DAG from `DependsOn` **and** binding sources (so dataflow
is captured even when `depends_on` is omitted). Provides cycle detection
(colored DFS that returns the actual cycle path), topological sort, dependency-
**level** analysis (nodes in the same level run concurrently), and ancestor/
reachability queries used by the optimizer.

### 4. Type checker (`core/typechecker`)
Resolves each operation's signature from the registry, binds and type-checks
every input (`$input`, `op.port`, or bare `op`), verifies backend compatibility,
requires all input ports to be bound, and detects cycles. Emits structured
diagnostics (error/warning) rather than failing on the first problem.

### 5. Optimizer (`core/optimizer`)
Semantics-preserving rewrites, each recorded as a `PassRecord`:
- **Common-subexpression elimination** — merges provably identical operations,
  but only when `deterministic ∧ cacheable ∧ ¬side_effects`. Runs to a fixpoint
  (merging a producer can expose identical consumers). Uses a content
  fingerprint of type + backend + params + sorted input sources.
- **Dead-node elimination** — removes operations that cannot reach any declared
  output, unless they have side effects.
- **Parallelism analysis** — records critical-path depth and max parallel width.

The optimizer clones the module and the result is re-validated — an optimization
that produced an invalid graph is a bug and is rejected.

### 6. Scheduler (`runtime/scheduler`)
A single-coordinator engine: the main goroutine owns all mutable scheduling state
(readiness, in-degree, node state); workers only execute and report completion
over a channel, so there are no data races on scheduling state (verified with
`-race`). Features: bounded concurrency, the 8-state model
(`PENDING→READY→RUNNING→COMPLETED|FAILED`, plus `RETRYING`, `CANCELLED`,
`BLOCKED`), per-node timeouts, bounded retries with linear backoff, context
cancellation, and failure policies (`fail` aborts and cancels in-flight work;
`skip` blocks only the failed node's descendants). Work is injected via a
`RunFunc`, keeping the scheduler independent of backends.

### 7. Executor (`runtime/executor`)
Wires everything: resolves each operation's inputs from workload inputs and
upstream results, consults the cache for eligible ops, dispatches to the backend
registry through the scheduler, and assembles declared outputs + `Metrics`.

### 8. Cache (`runtime/cache`)
Thread-safe content-addressed LRU. Keys hash operation identity + input value
fingerprints, so a hit is only ever returned for an identical computation.
Eligibility (`deterministic ∧ cacheable ∧ ¬side_effects`) is enforced by the
executor.

### 9. Profiler (`runtime/profiler`)
Implements `scheduler.Observer`. Captures per-node ready/start/end timestamps,
attempts, cache hits, and final state; computes queue wait, duration, critical
path, and aggregate counters.

## Backends (`backends`)
`Backend` is `{Name, Capabilities, Configured, Validate, Execute}`. A `Registry`
resolves operations to backends. `Fallback` wraps a primary (a Meta adapter)
with a secondary (the mock) under the primary's name, so workloads routed to
`meta.*` run without credentials and automatically use the real API when
configured. Meta calls go through a single hardened gateway
(`backends/meta/client`): auth, timeouts, bounded retries, 429/Retry-After
handling, error normalization, request logging, token/usage accounting.

## Control plane (`api`) & CLI (`cmd`)
The CLI drives the pipeline directly. The control-plane HTTP service exposes
`compile`, `run`, `operators`, `backends`, and `health` as JSON for the
dashboard, which therefore renders **actual** backend state and metrics.
