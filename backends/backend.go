// Package backends defines the execution surface of NEUROFORGE X: the typed
// Value that flows between operations, the Backend interface every executor
// implements, and a process-wide backend registry.
//
// A backend is the only thing that actually performs work. The runtime treats
// all backends uniformly through this interface, whether the work is a local
// operator, a deterministic mock, or a delegated call to a Meta model API.
package backends

import (
	"context"
	"fmt"
	"sort"

	"neuroforge/core/ir"
)

// Value is a concrete datum flowing on an MIR edge at runtime. Data holds an
// in-memory representation; Ref optionally points at an external artifact
// (file path, object-store key) for large payloads. Exactly one is typically
// populated.
type Value struct {
	Type ir.ValueType   `json:"type"`
	Data any            `json:"data,omitempty"`
	Ref  string         `json:"ref,omitempty"`
	Meta map[string]any `json:"meta,omitempty"`
}

// Capability advertises an operator type a backend can execute, plus whether
// the backend is currently reachable/configured (e.g. has API credentials).
type Capability struct {
	OperatorType string `json:"operator_type"`
}

// Backend executes operations. Implementations must be safe for concurrent use
// by multiple goroutines: the scheduler calls Execute on many ops at once.
type Backend interface {
	// Name is the registry key, e.g. "mock", "local", "meta.sam".
	Name() string

	// Capabilities lists the operator types this backend can execute.
	Capabilities() []Capability

	// Configured reports whether the backend is ready to run real work. A Meta
	// adapter with no API key returns false; the mock backend always returns
	// true. The CLI `doctor` command surfaces this.
	Configured() bool

	// Validate checks statically that this backend can execute the operation
	// (operator supported, required params present) without running it.
	Validate(ctx context.Context, op *ir.Operation) error

	// Execute runs the operation against its resolved inputs and returns outputs
	// keyed by output port name. It must honor ctx cancellation and deadlines.
	Execute(ctx context.Context, op *ir.Operation, inputs map[string]Value) (map[string]Value, error)
}

// Registry is a set of named backends.
type Registry struct {
	m map[string]Backend
}

// NewRegistry returns an empty backend registry.
func NewRegistry() *Registry { return &Registry{m: map[string]Backend{}} }

// Register adds a backend, replacing any existing one with the same name.
func (r *Registry) Register(b Backend) { r.m[b.Name()] = b }

// Get returns the backend by name.
func (r *Registry) Get(name string) (Backend, bool) {
	b, ok := r.m[name]
	return b, ok
}

// Names returns the registered backend names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns all registered backends, sorted by name.
func (r *Registry) All() []Backend {
	out := make([]Backend, 0, len(r.m))
	for _, n := range r.Names() {
		out = append(out, r.m[n])
	}
	return out
}

// Validate checks that every operation in the module has a registered backend
// that supports it.
func (r *Registry) Validate(ctx context.Context, m *ir.Module) error {
	for _, op := range m.Operations {
		b, ok := r.Get(op.Backend)
		if !ok {
			return fmt.Errorf("backends: no backend registered named %q (needed by op %q)", op.Backend, op.ID)
		}
		if err := b.Validate(ctx, op); err != nil {
			return fmt.Errorf("backends: op %q: %w", op.ID, err)
		}
	}
	return nil
}

// supports is a small helper for implementations listing capabilities from an
// operator-type set.
func capsFromTypes(types ...string) []Capability {
	caps := make([]Capability, 0, len(types))
	for _, t := range types {
		caps = append(caps, Capability{OperatorType: t})
	}
	return caps
}
