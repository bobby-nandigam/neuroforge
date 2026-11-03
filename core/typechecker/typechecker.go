// Package typechecker performs static validation of an MIR module: it resolves
// operator signatures from the registry, binds and type-checks every input,
// verifies backend compatibility, and detects structural errors (unknown
// dependencies, cycles, type mismatches) before any execution happens.
package typechecker

import (
	"fmt"
	"strings"

	"neuroforge/core/graph"
	"neuroforge/core/ir"
)

// Severity classifies a diagnostic.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Diagnostic is a single type-checking result tied to an operation (or empty
// for module-level issues).
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Op       string   `json:"op,omitempty"`
	Message  string   `json:"message"`
}

func (d Diagnostic) String() string {
	loc := "module"
	if d.Op != "" {
		loc = "op " + d.Op
	}
	return fmt.Sprintf("[%s] %s: %s", d.Severity, loc, d.Message)
}

// Result is the outcome of checking a module.
type Result struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// OK reports whether the module has no error-severity diagnostics.
func (r *Result) OK() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == Error {
			return false
		}
	}
	return true
}

// Errors returns only the error-severity diagnostics.
func (r *Result) Errors() []Diagnostic {
	var out []Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == Error {
			out = append(out, d)
		}
	}
	return out
}

// Err returns a combined error if the result has any errors, else nil.
func (r *Result) Err() error {
	errs := r.Errors()
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, d := range errs {
		msgs[i] = d.String()
	}
	return fmt.Errorf("type check failed with %d error(s):\n  %s", len(errs), strings.Join(msgs, "\n  "))
}

// Check validates and annotates the module in place: it fills each operation's
// Inputs/Outputs signature and default semantics from the registry. It returns
// a Result listing all diagnostics. The module is mutated only with inferred,
// non-destructive annotations; callers that need immutability should Clone first.
func Check(m *ir.Module) *Result {
	res := &Result{}
	add := func(sev Severity, op, format string, args ...any) {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{Severity: sev, Op: op, Message: fmt.Sprintf(format, args...)})
	}

	if m.SchemaVersion == "" {
		m.SchemaVersion = ir.SchemaVersion
	}
	if len(m.Operations) == 0 {
		add(Error, "", "workload has no operations")
		return res
	}

	// Pass 1: resolve operator specs, signatures, semantics, backends.
	seen := map[string]bool{}
	for _, op := range m.Operations {
		if op.ID == "" {
			add(Error, "", "an operation is missing an id")
			continue
		}
		if seen[op.ID] {
			add(Error, op.ID, "duplicate operation id")
			continue
		}
		seen[op.ID] = true

		spec, ok := ir.Lookup(op.Type)
		if !ok {
			add(Error, op.ID, "unknown operator type %q", op.Type)
			continue
		}
		// Inferred signature.
		op.Inputs = append([]ir.Port(nil), spec.Inputs...)
		op.Outputs = append([]ir.Port(nil), spec.Outputs...)

		// Semantics default from the spec; a workload may pin deterministic via
		// params ("deterministic": true) to opt a model op into caching.
		op.Deterministic = spec.Deterministic
		op.SideEffects = spec.SideEffects
		op.Cacheable = spec.Cacheable
		if pinned, ok := boolParam(op.Params, "deterministic"); ok {
			op.Deterministic = pinned
			if pinned && !spec.SideEffects {
				op.Cacheable = true
			}
			if !pinned {
				op.Cacheable = false
			}
		}
		if op.FailurePolicy == "" {
			op.FailurePolicy = ir.PolicyFail
		}

		// Backend selection / validation.
		if op.Backend == "" {
			op.Backend = spec.DefaultBackend()
			add(Warning, op.ID, "no backend specified; defaulting to %q", op.Backend)
		}
		if !spec.SupportsBackend(op.Backend) {
			add(Error, op.ID, "backend %q does not support operator %q (supported: %s)",
				op.Backend, op.Type, strings.Join(spec.Backends, ", "))
		}
	}

	// Build dependency graph (also catches unknown deps and duplicate ids).
	g, err := graph.Build(m)
	if err != nil {
		add(Error, "", "%s", err.Error())
		return res
	}
	if _, err := g.TopoSort(); err != nil {
		add(Error, "", "%s", err.Error())
		return res
	}

	// Pass 2: resolve and type-check bindings.
	for _, op := range m.Operations {
		if _, ok := ir.Lookup(op.Type); !ok {
			continue // already reported
		}
		bound := map[string]bool{}
		for _, b := range op.Bindings {
			dstPort, ok := op.InputPort(b.Port)
			if !ok {
				add(Error, op.ID, "binding targets unknown input port %q", b.Port)
				continue
			}
			bound[b.Port] = true
			srcType, ok := resolveSourceType(m, b.Source, add, op.ID)
			if !ok {
				continue
			}
			if !dstPort.Type.Assignable(srcType) {
				add(Error, op.ID, "type mismatch on port %q: expected %s, source %q provides %s",
					b.Port, dstPort.Type, b.Source, srcType)
			}
		}
		// Every declared input port must be bound.
		for _, p := range op.Inputs {
			if !bound[p.Name] {
				add(Error, op.ID, "input port %q (%s) is not bound to any source", p.Name, p.Type)
			}
		}
	}

	// Pass 3: validate declared outputs.
	for _, outID := range m.Outputs {
		if m.Op(outID) == nil {
			add(Error, "", "declared output %q is not an operation id", outID)
		}
	}
	if len(m.Outputs) == 0 {
		add(Warning, "", "workload declares no outputs")
	}

	return res
}

// resolveSourceType returns the value type produced by a binding source.
func resolveSourceType(m *ir.Module, source string, add func(Severity, string, string, ...any), consumer string) (ir.ValueType, bool) {
	if source == "" {
		add(Error, consumer, "empty binding source")
		return "", false
	}
	if source[0] == '$' {
		name := source[1:]
		in, ok := m.Input(name)
		if !ok {
			add(Error, consumer, "binding references unknown workload input %q", name)
			return "", false
		}
		return in.Type, true
	}
	opID, port := source, ""
	if i := strings.IndexByte(source, '.'); i >= 0 {
		opID, port = source[:i], source[i+1:]
	}
	producer := m.Op(opID)
	if producer == nil {
		add(Error, consumer, "binding references unknown operation %q", opID)
		return "", false
	}
	if port == "" {
		p, ok := producer.PrimaryOutput()
		if !ok {
			add(Error, consumer, "operation %q has no outputs to bind", opID)
			return "", false
		}
		return p.Type, true
	}
	p, ok := producer.OutputPort(port)
	if !ok {
		add(Error, consumer, "operation %q has no output port %q", opID, port)
		return "", false
	}
	return p.Type, true
}

func boolParam(params map[string]any, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	v, ok := params[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}
