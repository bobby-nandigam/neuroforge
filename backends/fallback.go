package backends

import (
	"context"

	"neuroforge/core/ir"
)

// Fallback is a backend that delegates to a primary backend when it is
// configured (e.g. a Meta adapter with credentials) and otherwise to a
// secondary backend (the mock). It preserves the primary's registry name so a
// workload routed to "meta.sam" runs out of the box without credentials, with
// results clearly labeled simulated.
type Fallback struct {
	name      string
	primary   Backend
	secondary Backend
}

// NewFallback wraps primary with a secondary fallback under primary's name.
func NewFallback(name string, primary, secondary Backend) *Fallback {
	return &Fallback{name: name, primary: primary, secondary: secondary}
}

func (f *Fallback) Name() string { return f.name }

// Configured reports the primary's configured state (true only when the real
// backend can run). The CLI doctor command shows this.
func (f *Fallback) Configured() bool { return f.primary.Configured() }

func (f *Fallback) Capabilities() []Capability { return f.primary.Capabilities() }

func (f *Fallback) active() Backend {
	if f.primary.Configured() {
		return f.primary
	}
	return f.secondary
}

func (f *Fallback) Validate(ctx context.Context, op *ir.Operation) error {
	return f.active().Validate(ctx, op)
}

// Execute runs on the active backend. When falling back to the secondary, every
// output value is tagged so the result is never mistaken for a real Meta call.
func (f *Fallback) Execute(ctx context.Context, op *ir.Operation, inputs map[string]Value) (map[string]Value, error) {
	if f.primary.Configured() {
		return f.primary.Execute(ctx, op, inputs)
	}
	out, err := f.secondary.Execute(ctx, op, inputs)
	if err != nil {
		return nil, err
	}
	for port, v := range out {
		if v.Meta == nil {
			v.Meta = map[string]any{}
		}
		v.Meta["simulated"] = true
		v.Meta["intended_backend"] = f.name
		v.Meta["reason"] = "Meta backend not configured; result produced by mock (set META_API_KEY for real calls)"
		out[port] = v
	}
	return out, nil
}
