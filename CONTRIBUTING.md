# Contributing to NEUROFORGE X

Thanks for your interest. This is an early-stage systems project; contributions
that strengthen the compiler, runtime, or backends are very welcome.

## Development

```bash
make build     # build CLI + server
make test      # go test -race ./...
make vet fmt   # go vet + gofmt
```

Go 1.26+ is required. The only runtime dependency is `gopkg.in/yaml.v3`.

## Ground rules

- **Correctness first.** Every optimizer pass must preserve observable
  semantics. If a rewrite could change results for non-deterministic or
  side-effecting operations, it must not apply to them.
- **Write tests alongside code.** New compiler passes need a test that proves
  both that they fire and that they never fire unsafely. New scheduler behavior
  needs a `-race`-clean test.
- **No fabricated results or benchmarks.** Simulated/mock output must be labeled
  as such. Benchmark numbers must be measured.
- **Keep packages narrow.** The compiler must not depend on backends; the
  scheduler must not depend on backends (work is injected via `RunFunc`).
- **Never hardcode credentials or invent API endpoints.** Meta endpoints and
  model ids come from environment variables.

## Adding an operator

1. `ir.Register` a spec in `core/ir/registry.go` (signature, semantics,
   backends).
2. Implement it in a backend (`backends/local`, a Meta adapter, and/or the
   mock already covers it generically).
3. Add a compiler test exercising its type checking.

## Adding a backend

Implement the `backends.Backend` interface and register it in
`runtime/setup`. If it is a remote model, route it through the
`backends/meta/client` gateway and give it a mock `Fallback`.
