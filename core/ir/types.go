// Package ir defines the Multimodal Intermediate Representation (MIR) for
// NEUROFORGE X. MIR is a typed, serializable, versioned representation of a
// multimodal AI workload as a directed acyclic graph of operations.
//
// The compiler frontend lowers a high-level workload (YAML/JSON) into MIR; the
// optimizer rewrites MIR while preserving semantics; the runtime executes MIR
// against pluggable backends.
package ir

import "fmt"

// SchemaVersion is the current MIR schema version. It is embedded in every
// serialized Module so that older artifacts can be detected and migrated.
const SchemaVersion = "0.2.0"

// ValueType is the type of a value flowing between operations. The type system
// is intentionally small and closed; new modalities are added deliberately.
type ValueType string

const (
	TypeText       ValueType = "text"
	TypeImage      ValueType = "image"
	TypeVideo      ValueType = "video"
	TypeAudio      ValueType = "audio"
	TypeTensor     ValueType = "tensor"
	TypeEmbedding  ValueType = "embedding"
	TypeMask       ValueType = "mask"
	TypeObjectList ValueType = "object_list"
	TypeJSON       ValueType = "json"
	// TypeAny is a wildcard used only in operator signatures to accept any
	// concrete type. A value in a compiled graph never has type any.
	TypeAny ValueType = "any"
)

// AllTypes lists every concrete value type (excluding the any wildcard).
var AllTypes = []ValueType{
	TypeText, TypeImage, TypeVideo, TypeAudio, TypeTensor,
	TypeEmbedding, TypeMask, TypeObjectList, TypeJSON,
}

// IsConcrete reports whether t is a real value type (not the any wildcard).
func (t ValueType) IsConcrete() bool { return t != TypeAny && t != "" }

// Assignable reports whether a value of type src can be supplied where a port
// of type dst is expected. any accepts anything; otherwise types must match.
func (dst ValueType) Assignable(src ValueType) bool {
	if dst == TypeAny || src == TypeAny {
		return true
	}
	return dst == src
}

// Port is a typed input or output of an operation.
type Port struct {
	Name string    `json:"name"`
	Type ValueType `json:"type"`
}

// Binding connects one of an operation's input ports to a data source. The
// source is either a workload input ("$name") or another operation's output
// port ("opID.port", or bare "opID" for that operation's primary output).
type Binding struct {
	Port   string `json:"port"`   // the consuming input port name
	Source string `json:"source"` // "$input" | "opID.port" | "opID"
}

// FailurePolicy controls what the scheduler does when an operation ultimately
// fails (after retries are exhausted).
type FailurePolicy string

const (
	// PolicyFail aborts the whole workload (default).
	PolicyFail FailurePolicy = "fail"
	// PolicySkip marks dependents as BLOCKED but lets independent branches run.
	PolicySkip FailurePolicy = "skip"
)

// Operation is a single node in the MIR graph.
type Operation struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`    // operator type, e.g. "vision.segmentation"
	Backend   string         `json:"backend"` // target backend, e.g. "meta.sam", "mock", "local"
	Params    map[string]any `json:"params,omitempty"`
	Bindings  []Binding      `json:"bindings,omitempty"`
	DependsOn []string       `json:"depends_on,omitempty"`

	// Inferred signature (filled by the type checker from the operator registry).
	Inputs  []Port `json:"inputs,omitempty"`
	Outputs []Port `json:"outputs,omitempty"`

	// Execution semantics (defaulted from the operator registry, overridable).
	Deterministic bool `json:"deterministic"`
	SideEffects   bool `json:"side_effects"`
	Cacheable     bool `json:"cacheable"`

	// Policies.
	TimeoutMS     int           `json:"timeout_ms,omitempty"`
	MaxRetries    int           `json:"max_retries,omitempty"`
	FailurePolicy FailurePolicy `json:"failure_policy,omitempty"`

	// Resource hints.
	EstMemoryMB int `json:"est_memory_mb,omitempty"`
}

// PrimaryOutput returns the operation's first declared output port, or false if
// it has none. "opID" with no port refers to this port.
func (o *Operation) PrimaryOutput() (Port, bool) {
	if len(o.Outputs) == 0 {
		return Port{}, false
	}
	return o.Outputs[0], true
}

// OutputPort looks up a declared output port by name.
func (o *Operation) OutputPort(name string) (Port, bool) {
	for _, p := range o.Outputs {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

// InputPort looks up a declared input port by name.
func (o *Operation) InputPort(name string) (Port, bool) {
	for _, p := range o.Inputs {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

// IOSpec declares a named workload-level input or output and its type.
type IOSpec struct {
	Name string    `json:"name"`
	Type ValueType `json:"type"`
	// Ref is an optional default data reference for inputs (e.g. a file path).
	Ref string `json:"ref,omitempty"`
}

// Module is a complete MIR program: a typed DAG of operations plus workload
// level inputs and outputs. It is the unit the compiler produces and the
// runtime consumes.
type Module struct {
	SchemaVersion string       `json:"schema_version"`
	Name          string       `json:"name"`
	Inputs        []IOSpec     `json:"inputs,omitempty"`
	Operations    []*Operation `json:"operations"`
	Outputs       []string     `json:"outputs,omitempty"` // operation IDs whose results are workload outputs

	// Passes records the optimization passes applied to produce this module,
	// in order, for provenance and UI display.
	Passes []PassRecord `json:"passes,omitempty"`
}

// PassRecord is a single applied compiler/optimizer pass and what it changed.
type PassRecord struct {
	Name    string   `json:"name"`
	Changed bool     `json:"changed"`
	Notes   []string `json:"notes,omitempty"`
}

// Op returns the operation with the given id, or nil.
func (m *Module) Op(id string) *Operation {
	for _, o := range m.Operations {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// Input returns the workload input with the given name, or false.
func (m *Module) Input(name string) (IOSpec, bool) {
	for _, in := range m.Inputs {
		if in.Name == name {
			return in, true
		}
	}
	return IOSpec{}, false
}

// Clone returns a deep copy of the module so passes can rewrite without
// mutating the caller's module.
func (m *Module) Clone() *Module {
	cp := *m
	cp.Inputs = append([]IOSpec(nil), m.Inputs...)
	cp.Outputs = append([]string(nil), m.Outputs...)
	cp.Passes = append([]PassRecord(nil), m.Passes...)
	cp.Operations = make([]*Operation, len(m.Operations))
	for i, o := range m.Operations {
		oc := *o
		oc.DependsOn = append([]string(nil), o.DependsOn...)
		oc.Bindings = append([]Binding(nil), o.Bindings...)
		oc.Inputs = append([]Port(nil), o.Inputs...)
		oc.Outputs = append([]Port(nil), o.Outputs...)
		if o.Params != nil {
			oc.Params = make(map[string]any, len(o.Params))
			for k, v := range o.Params {
				oc.Params[k] = v
			}
		}
		cp.Operations[i] = &oc
	}
	return &cp
}

// String gives a short human identity for an operation, used in diagnostics.
func (o *Operation) String() string {
	return fmt.Sprintf("%s(%s@%s)", o.ID, o.Type, o.Backend)
}
