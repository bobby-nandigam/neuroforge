// Package mock provides a deterministic, dependency-free backend used for
// tests, benchmarks, and credential-less demos. Its outputs are clearly labeled
// synthetic (every Value carries meta.synthetic=true and meta.backend="mock")
// and must never be presented as real Meta API results.
package mock

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"

	"neuroforge/backends"
	"neuroforge/core/ir"
)

// Backend is a deterministic synthetic executor supporting every operator type.
type Backend struct {
	// Latency simulates per-operator-type execution time so that parallelism
	// and caching produce measurable, honest wall-clock differences. These are
	// SIMULATED durations, labeled as such in all output; they are not real
	// Meta API latencies.
	Latency map[string]time.Duration
}

// New returns a mock backend with default simulated latencies that approximate
// the relative cost of remote model calls vs. local operators.
func New() *Backend {
	return &Backend{Latency: map[string]time.Duration{
		"cognition.plan":       60 * time.Millisecond,
		"cognition.reasoning":  60 * time.Millisecond,
		"vision.segmentation":  40 * time.Millisecond,
		"image.generate":       80 * time.Millisecond,
		"vision.frame_extract": 8 * time.Millisecond,
		"vision.features":      5 * time.Millisecond,
		"output.structured":    4 * time.Millisecond,
	}}
}

func (b *Backend) Name() string { return "mock" }

func (b *Backend) Configured() bool { return true }

func (b *Backend) Capabilities() []backends.Capability {
	var caps []backends.Capability
	for _, s := range ir.Operators() {
		caps = append(caps, backends.Capability{OperatorType: s.Type})
	}
	return caps
}

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	if _, ok := ir.Lookup(op.Type); !ok {
		return fmt.Errorf("mock: unknown operator %q", op.Type)
	}
	return nil
}

// Execute sleeps for the simulated latency (honoring cancellation) and returns
// deterministic synthetic outputs derived from the operation and its inputs.
func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	if d := b.Latency[op.Type]; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	spec, _ := ir.Lookup(op.Type)
	seed := inputSeed(op, inputs)

	out := map[string]backends.Value{}
	for _, port := range spec.Outputs {
		out[port.Name] = backends.Value{
			Type: port.Type,
			Data: synthData(port.Type, op, seed),
			Meta: map[string]any{"synthetic": true, "backend": "mock", "op": op.ID},
		}
	}
	return out, nil
}

// inputSeed produces a stable 64-bit seed from the operation identity and its
// input references, so repeated executions are deterministic.
func inputSeed(op *ir.Operation, inputs map[string]backends.Value) uint64 {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s", op.ID, op.Type, op.Backend)
	for _, b := range op.Bindings {
		if v, ok := inputs[b.Port]; ok {
			fmt.Fprintf(h, "|%s=%v/%s", b.Port, v.Data, v.Ref)
		}
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

// synthData returns plausible, clearly synthetic data for a value type.
func synthData(t ir.ValueType, op *ir.Operation, seed uint64) any {
	switch t {
	case ir.TypeObjectList:
		n := 2 + int(seed%4) // 2..5 objects
		objs := make([]map[string]any, n)
		labels := []string{"forklift", "pallet", "worker", "shelf", "box"}
		for i := 0; i < n; i++ {
			s := seed + uint64(i)*2654435761
			objs[i] = map[string]any{
				"id":         i,
				"label":      labels[int(s%uint64(len(labels)))],
				"confidence": 0.70 + float64(s%25)/100.0,
				"bbox":       []int{int(s % 300), int((s / 7) % 300), 40 + int(s%120), 40 + int((s/13)%120)},
			}
		}
		return objs
	case ir.TypeMask:
		return map[string]any{"encoding": "rle", "count": int(seed%5) + 1, "synthetic": true}
	case ir.TypeImage:
		return map[string]any{"width": 1280, "height": 720, "frames": int(seed%8) + 1, "synthetic": true}
	case ir.TypeJSON:
		return map[string]any{
			"operator":  op.Type,
			"summary":   "synthetic result from mock backend",
			"spatial":   map[string]any{"objects": int(seed%6) + 1, "proximity_pairs": int(seed % 3)},
			"synthetic": true,
		}
	case ir.TypeText:
		return fmt.Sprintf("synthetic text for %s (seed %d)", op.ID, seed%1000)
	default:
		return map[string]any{"synthetic": true, "type": string(t)}
	}
}
