# NEUROFORGE X

**Compile Intelligence. Optimize Execution.**

NEUROFORGE X is a multimodal AI **compiler and execution runtime**. It treats
multimodal AI operations — language reasoning, image segmentation, feature
extraction, image generation — as typed, programmable computational components.
A high-level workload is lowered into a **typed intermediate representation
(MIR)**, validated and optimized by a **graph compiler**, and executed by a
**dependency-aware concurrent runtime** against local, mock, and Meta model
backends — with real performance profiling.

This is infrastructure, not a chatbot. The core is written in Go.

---

## What actually works today

Everything below is implemented, tested, and runnable end-to-end:

- **Typed MIR** — 9 value types, operator registry, typed input/output ports,
  JSON serialization, versioned schema.
- **Graph compiler** — frontend (YAML/JSON → MIR), type inference, static type
  checking, dependency resolution, **cycle detection**, backend-compatibility
  checks.
- **Optimizer** — dead-node elimination, **common-subexpression elimination to a
  fixpoint** (safe: only deterministic, cacheable, side-effect-free ops),
  parallelism (dependency-level) analysis. Every pass preserves semantics and is
  re-validated.
- **Runtime (Go)** — dependency-aware scheduler with bounded concurrency,
  8-state execution model, per-node timeouts, bounded retries with backoff,
  cancellation, and two failure policies (`fail` / `skip`). Race-clean
  (`go test -race`).
- **Backends** — a common `Backend` interface with a deterministic **mock**
  (clearly labeled synthetic), **local operators** (real spatial feature
  computation), and **Meta adapters** (Muse Spark, SAM 3.1, Muse Image) behind a
  fallback that uses the mock when credentials are absent.
- **Cache** — content-addressed LRU keyed by operation identity + input
  fingerprints; hit/miss/eviction stats.
- **Profiler** — total wall time, per-node latency & queue wait, retry counts,
  cache-hit rate, critical path.
- **CLI** `neuroforge` — `init validate compile inspect run profile benchmark
  doctor operators`.
- **Control-plane API** — JSON HTTP service backing the dashboard.
- **Benchmarks** — real, measured sequential vs. parallel vs. cached vs.
  optimized comparisons.

### Honesty notes

- Without `META_API_KEY`, Meta operations run on the **mock** backend and every
  such output is tagged `simulated: true`. These are **not** real Meta API
  results.
- Mock backend latencies are **simulated** to make scheduling/caching effects
  measurable; benchmark output says so explicitly. Real API latency replaces
  them when credentials are set.
- SAM 3.1 and Muse Image request/response schemas are **config-driven**
  (`SAM_API_PATH`, `MUSE_IMAGE_API_PATH`) and must be confirmed against the Meta
  developer portal — no endpoint shape is invented and presented as verified.
  The Meta Model API is OpenAI-compatible, so the Muse Spark adapter targets the
  standard `/chat/completions` interface.

---

## Architecture

```
 workload.yaml ─► Frontend ─► MIR ─► Type Checker ─► Optimizer ─► Scheduler ─► Backends
   (authored)     (parse)   (typed   (cycles,       (DNE, CSE,   (concurrent,  (mock /
                            DAG)     types, deps)    parallelism) retries,      local /
                                                                 cache)        meta.*)
                                                        │                        │
                                                   Profiler ◄────────────────────┘
```

Packages:

| Path | Responsibility |
|------|----------------|
| `core/ir` | MIR types, operator registry, serialization |
| `core/graph` | DAG build, cycle detection, topo sort, level analysis |
| `core/typechecker` | static validation, binding/type resolution |
| `core/optimizer` | semantics-preserving passes |
| `core/compiler` | frontend + compile pipeline |
| `runtime/scheduler` | concurrent, dependency-aware execution engine |
| `runtime/executor` | input resolution, cache, backend dispatch |
| `runtime/cache` | content-addressed LRU |
| `runtime/profiler` | timing & state capture |
| `backends` | `Backend` interface, registry, fallback |
| `backends/mock`, `backends/local`, `backends/meta/*` | executors |
| `api` | control-plane HTTP service |
| `cmd/neuroforge`, `cmd/neuroforge-server` | CLI and server |
| `dashboard` | Visual Compiler Studio (React + React Flow) |

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and
[`docs/MIR_SPEC.md`](docs/MIR_SPEC.md).

---

## Quickstart (reproduce the demo)

Requires Go 1.26+.

```bash
# 1. Build
make build              # or: go build -o bin/neuroforge ./cmd/neuroforge

# 2. Check the environment and backends
./bin/neuroforge doctor

# 3. Validate and inspect the flagship workload
./bin/neuroforge validate examples/warehouse-intelligence/workload.yaml
./bin/neuroforge inspect  examples/warehouse-intelligence/workload.yaml

# 4. Execute it (parallel + caching). Outputs + real metrics are printed.
./bin/neuroforge run examples/warehouse-intelligence/workload.yaml --cache --concurrency 4

# 5. Benchmark: sequential vs parallel vs cached vs optimized (measured)
./bin/neuroforge benchmark

# 6. Run the test suite
make test               # go test -race ./...
```

### Using the real Meta Model API

```bash
cp examples/warehouse-intelligence/.env.example .env   # or: ./bin/neuroforge init
export META_API_KEY=...        # your hackathon credential
export MUSE_SPARK_MODEL=...     # confirm the model id in the Meta docs
./bin/neuroforge doctor         # meta gateway should now show "configured"
./bin/neuroforge run examples/warehouse-intelligence/workload.yaml --cache
```

### Dashboard (Visual Compiler Studio)

```bash
make server             # starts the control plane on :8080
cd dashboard && npm install && npm run dev   # Vite dev server on :5173
```

---

## Example workload

```yaml
name: warehouse-intelligence
inputs:
  - { name: video, type: video, ref: examples/.../warehouse.mp4 }
  - { name: instruction, type: text, ref: "Analyze this warehouse video..." }
operations:
  - { id: plan,     type: cognition.plan,      backend: meta.muse_spark, inputs: { instruction: $instruction } }
  - { id: frames,   type: vision.frame_extract, backend: local,          inputs: { video: $video } }
  - { id: segment,  type: vision.segmentation,  backend: meta.sam,        inputs: { image: frames.frames } }
  - { id: features, type: vision.features,      backend: local,           inputs: { objects: segment.objects } }
  - { id: reason,   type: cognition.reasoning,  backend: meta.muse_spark, inputs: { observations: features.features } }
  - { id: report,   type: output.structured,    backend: local,           inputs: { data: reason.result } }
outputs: [plan, report]
```

`$name` binds a workload input; `op.port` binds an upstream operation's output.
The type checker verifies every binding; the compiler derives the DAG.

---

## Testing

```bash
go test -race ./...
```

Covers: valid compilation, cycle detection, type mismatch, unbound inputs,
backend mismatch, dead-node elimination, common-subexpression elimination,
MIR serialization round-trip; scheduler sequential/parallel/ordering, retries,
timeouts, cancellation, failure policies; LRU cache behavior; and full
executor end-to-end + caching.

---

## License

MIT — see [LICENSE](LICENSE). Subject to the licenses of dependencies and any
Meta model terms.
