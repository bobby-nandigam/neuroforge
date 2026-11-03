// Package compiler is the NEUROFORGE X frontend and pass pipeline. It lowers a
// high-level workload definition (YAML/JSON) into a typed MIR module, runs the
// type checker, and applies semantics-preserving optimization passes.
package compiler

import (
	"fmt"

	"neuroforge/core/ir"

	"gopkg.in/yaml.v3"
)

// surfaceWorkload is the authored, human-facing workload format. It is
// deliberately looser than MIR (inputs as a port->source map, optional
// depends_on) and is lowered into a strict MIR module.
type surfaceWorkload struct {
	Name          string      `yaml:"name"`
	SchemaVersion string      `yaml:"schema_version"`
	Inputs        []surfaceIO `yaml:"inputs"`
	Operations    []surfaceOp `yaml:"operations"`
	Outputs       []string    `yaml:"outputs"`
}

type surfaceIO struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	Ref  string `yaml:"ref"`
}

type surfaceOp struct {
	ID            string            `yaml:"id"`
	Type          string            `yaml:"type"`
	Backend       string            `yaml:"backend"`
	Params        map[string]any    `yaml:"params"`
	Inputs        map[string]string `yaml:"inputs"`
	DependsOn     []string          `yaml:"depends_on"`
	TimeoutMS     int               `yaml:"timeout_ms"`
	MaxRetries    int               `yaml:"max_retries"`
	FailurePolicy string            `yaml:"failure_policy"`
}

// Parse lowers an authored workload (YAML or JSON bytes — YAML is a superset of
// JSON so one decoder handles both) into an MIR module. It performs structural
// lowering only; semantic validation is the type checker's job.
func Parse(src []byte) (*ir.Module, error) {
	var sw surfaceWorkload
	if err := yaml.Unmarshal(src, &sw); err != nil {
		return nil, fmt.Errorf("compiler: parse workload: %w", err)
	}
	if sw.Name == "" {
		return nil, fmt.Errorf("compiler: workload is missing a name")
	}

	m := &ir.Module{
		SchemaVersion: ir.SchemaVersion,
		Name:          sw.Name,
		Outputs:       sw.Outputs,
	}
	for _, in := range sw.Inputs {
		m.Inputs = append(m.Inputs, ir.IOSpec{
			Name: in.Name,
			Type: ir.ValueType(in.Type),
			Ref:  in.Ref,
		})
	}
	for i, so := range sw.Operations {
		if so.ID == "" {
			return nil, fmt.Errorf("compiler: operation %d is missing an id", i)
		}
		op := &ir.Operation{
			ID:            so.ID,
			Type:          so.Type,
			Backend:       so.Backend,
			Params:        so.Params,
			DependsOn:     so.DependsOn,
			TimeoutMS:     so.TimeoutMS,
			MaxRetries:    so.MaxRetries,
			FailurePolicy: ir.FailurePolicy(so.FailurePolicy),
		}
		// Lower the inputs map into ordered bindings. Order is stabilized so
		// MIR output is deterministic across runs.
		for _, port := range sortedKeys(so.Inputs) {
			op.Bindings = append(op.Bindings, ir.Binding{Port: port, Source: so.Inputs[port]})
		}
		m.Operations = append(m.Operations, op)
	}
	return m, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid importing sort for a tiny map
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
