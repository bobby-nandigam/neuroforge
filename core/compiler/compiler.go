package compiler

import (
	"fmt"

	"neuroforge/core/graph"
	"neuroforge/core/ir"
	"neuroforge/core/optimizer"
	"neuroforge/core/typechecker"
)

// Compiled is the result of compiling a workload: the validated base MIR, the
// optimized MIR, and the type-checker diagnostics.
type Compiled struct {
	Base        *ir.Module               `json:"base"`
	Optimized   *ir.Module               `json:"optimized"`
	Diagnostics []typechecker.Diagnostic `json:"diagnostics"`
}

// Options configures a compile.
type Options struct {
	Optimize    bool
	OptimizerOf optimizer.Options
}

// DefaultOptions returns the standard compile configuration (optimize on).
func DefaultOptions() Options {
	return Options{Optimize: true, OptimizerOf: optimizer.DefaultOptions()}
}

// CompileBytes parses, type-checks and optimizes an authored workload.
func CompileBytes(src []byte, opts Options) (*Compiled, error) {
	m, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return Compile(m, opts)
}

// Compile type-checks and (optionally) optimizes an MIR module. A type error
// aborts compilation; diagnostics are always returned for inspection.
func Compile(m *ir.Module, opts Options) (*Compiled, error) {
	res := typechecker.Check(m)
	c := &Compiled{Base: m, Diagnostics: res.Diagnostics}
	if err := res.Err(); err != nil {
		return c, err
	}
	if !opts.Optimize {
		c.Optimized = m
		return c, nil
	}
	opt, err := optimizer.Optimize(m, opts.OptimizerOf)
	if err != nil {
		return c, fmt.Errorf("compiler: optimize: %w", err)
	}
	// Re-check the optimized module as a safety net: optimization must never
	// produce an invalid graph.
	if err := typechecker.Check(opt).Err(); err != nil {
		return c, fmt.Errorf("compiler: optimized module failed validation (pass bug): %w", err)
	}
	c.Optimized = opt
	return c, nil
}

// Validate parses and type-checks a workload without optimizing. It returns the
// module and the full diagnostic result.
func Validate(src []byte) (*ir.Module, *typechecker.Result, error) {
	m, err := Parse(src)
	if err != nil {
		return nil, nil, err
	}
	res := typechecker.Check(m)
	return m, res, nil
}

// ExecutionLevels returns the scheduler's parallel execution levels for a module
// (dependency layers). Exposed for inspect/CLI without importing graph directly.
func ExecutionLevels(m *ir.Module) ([][]string, error) {
	g, err := graph.Build(m)
	if err != nil {
		return nil, err
	}
	return g.Levels()
}
