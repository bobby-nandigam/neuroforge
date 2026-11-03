# MIR Specification (v0.2.0)

The Multimodal Intermediate Representation (MIR) is the typed, serializable DAG
that NEUROFORGE X compiles to and executes. This document specifies its types
and invariants.

## Value types

```
text · image · video · audio · tensor · embedding · mask · object_list · json
```

Plus `any`, a wildcard used **only** in operator signatures (a compiled value is
never `any`). Assignability: `dst.Assignable(src)` is true iff `dst == src` or
either side is `any`.

## Module

```jsonc
{
  "schema_version": "0.2.0",
  "name": "warehouse-intelligence",
  "inputs":  [ { "name": "video", "type": "video", "ref": "..." } ],
  "operations": [ /* Operation[] */ ],
  "outputs": [ "report" ],          // operation ids whose results are outputs
  "passes":  [ /* PassRecord[] */ ] // optimizer provenance
}
```

Schema compatibility: a serialized module loads iff its major version equals the
build's and its minor version is ≤ the build's.

## Operation

```jsonc
{
  "id": "segment",
  "type": "vision.segmentation",    // must exist in the operator registry
  "backend": "meta.sam",            // must support the operator type
  "params": { "region": 1 },
  "bindings": [ { "port": "image", "source": "frames.frames" } ],
  "depends_on": [ "frames" ],       // optional; also derived from bindings

  // inferred from the registry by the type checker:
  "inputs":  [ { "name": "image", "type": "image" } ],
  "outputs": [ { "name": "objects", "type": "object_list" },
               { "name": "mask", "type": "mask" } ],

  // semantics (defaulted from the registry; `params.deterministic` may pin):
  "deterministic": true, "side_effects": false, "cacheable": true,

  // policies:
  "timeout_ms": 0, "max_retries": 0, "failure_policy": "fail",  // or "skip"
  "est_memory_mb": 0
}
```

### Binding sources
- `"$name"` — the workload input `name`.
- `"opID.port"` — a specific output port of another operation.
- `"opID"` — that operation's **primary** (first) output port.

## Operator registry

Each operator type declares its signature, default semantics, and supported
backends (first = default). Standard catalog:

| Type | In → Out | Backends | Semantics |
|------|----------|----------|-----------|
| `cognition.plan` | instruction:text → plan:json | meta.muse_spark, mock | non-det |
| `cognition.reasoning` | observations:json → result:json | meta.muse_spark, mock | non-det |
| `vision.segmentation` | image:image → objects:object_list, mask:mask | meta.sam, mock | det, cacheable |
| `vision.frame_extract` | video:video → frames:image | local, mock | det, cacheable |
| `vision.features` | objects:object_list → features:json | local, mock | det, cacheable |
| `image.generate` | prompt:text → image:image | meta.muse_image, mock | non-det |
| `output.structured` | data:json → report:json | local, mock | det, cacheable |

## Invariants (enforced by the type checker)

1. Every operation id is unique and non-empty.
2. Every `type` exists in the registry.
3. Every `backend` supports the operation's type.
4. Every declared input port is bound exactly once; sources resolve and are
   type-assignable.
5. The dependency graph is acyclic.
6. Every declared output names a real operation.

## Caching eligibility

An operation's result may be cached iff `deterministic ∧ cacheable ∧
¬side_effects`. The cache key hashes `id + type + backend + params +
fingerprints(resolved inputs)`. Non-deterministic model operations
(reasoning, planning, generation) are never cached unless explicitly pinned
`params.deterministic: true`.
