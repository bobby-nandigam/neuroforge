// Package executor runs a compiled MIR module end-to-end. It resolves each
// operation's inputs from workload inputs and upstream results, consults the
// content-addressed cache for eligible operations, dispatches work to backends
// through the dependency-aware scheduler, and records a performance profile.
package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"neuroforge/backends"
	"neuroforge/core/graph"
	"neuroforge/core/ir"
	"neuroforge/runtime/cache"
	"neuroforge/runtime/profiler"
	"neuroforge/runtime/scheduler"
)

// Config controls a run.
type Config struct {
	Concurrency  int           // max concurrent operations (<=0 => unlimited within graph width)
	Cache        *cache.Cache  // optional; enables result caching for eligible ops
	RetryBackoff time.Duration // base retry backoff
}

// Metrics is the combined performance report for a run.
type Metrics struct {
	Profile     profiler.Report            `json:"profile"`
	Cache       *cache.Stats               `json:"cache,omitempty"`
	Concurrency int                        `json:"concurrency"`
	States      map[string]scheduler.State `json:"states"`
}

// Result is the outcome of executing a module.
type Result struct {
	Outputs map[string]map[string]backends.Value `json:"outputs"` // opID -> port -> value (declared outputs only)
	Metrics Metrics                              `json:"metrics"`
	Err     error                                `json:"-"`
}

// Executor runs modules against a backend registry.
type Executor struct {
	reg *backends.Registry
}

// New returns an executor bound to a backend registry.
func New(reg *backends.Registry) *Executor { return &Executor{reg: reg} }

// Run executes a compiled module. workloadInputs supplies values for the
// module's declared inputs; any input not supplied falls back to a Value built
// from the IOSpec's Ref. It returns results even on failure (partial).
func (e *Executor) Run(ctx context.Context, m *ir.Module, workloadInputs map[string]backends.Value, cfg Config) *Result {
	g, err := graph.Build(m)
	if err != nil {
		return &Result{Err: err}
	}

	// Seed workload inputs.
	inputs := map[string]backends.Value{}
	for _, in := range m.Inputs {
		inputs[in.Name] = backends.Value{Type: in.Type, Ref: in.Ref}
	}
	for k, v := range workloadInputs {
		inputs[k] = v
	}

	prof := profiler.New()
	results := map[string]map[string]backends.Value{}
	var mu sync.Mutex

	run := func(runCtx context.Context, op *ir.Operation, attempt int) error {
		resolved, err := e.resolveInputs(m, op, inputs, results, &mu)
		if err != nil {
			return err
		}
		// Cache lookup for eligible operations.
		var key string
		eligible := cfg.Cache != nil && op.Cacheable && op.Deterministic && !op.SideEffects
		if eligible {
			key = cache.Key(op, resolved)
			if out, hit := cfg.Cache.Get(key); hit {
				mu.Lock()
				results[op.ID] = out
				mu.Unlock()
				prof.MarkCacheHit(op.ID)
				return nil
			}
		}
		b, ok := e.reg.Get(op.Backend)
		if !ok {
			return fmt.Errorf("no backend %q registered", op.Backend)
		}
		out, err := b.Execute(runCtx, op, resolved)
		if err != nil {
			return err
		}
		mu.Lock()
		results[op.ID] = out
		mu.Unlock()
		if eligible {
			cfg.Cache.Put(key, out)
		}
		return nil
	}

	sch := scheduler.New(m, g, scheduler.Config{
		Concurrency:  cfg.Concurrency,
		RetryBackoff: cfg.RetryBackoff,
		Observer:     prof,
	})

	prof.Start()
	sres := sch.Run(ctx, run)
	prof.Finish()

	// Collect declared outputs.
	outputs := map[string]map[string]backends.Value{}
	for _, id := range m.Outputs {
		mu.Lock()
		if v, ok := results[id]; ok {
			outputs[id] = v
		}
		mu.Unlock()
	}

	metrics := Metrics{
		Profile:     prof.Report(),
		Concurrency: effectiveConcurrency(cfg.Concurrency, len(m.Operations)),
		States:      sres.States,
	}
	if cfg.Cache != nil {
		cs := cfg.Cache.Stats()
		metrics.Cache = &cs
	}
	return &Result{Outputs: outputs, Metrics: metrics, Err: sres.Err}
}

// resolveInputs gathers an operation's input values from workload inputs and
// upstream operation results, following each binding's source.
func (e *Executor) resolveInputs(m *ir.Module, op *ir.Operation, inputs map[string]backends.Value, results map[string]map[string]backends.Value, mu *sync.Mutex) (map[string]backends.Value, error) {
	resolved := map[string]backends.Value{}
	for _, b := range op.Bindings {
		src := b.Source
		if src == "" {
			return nil, fmt.Errorf("op %q: empty binding source for port %q", op.ID, b.Port)
		}
		if src[0] == '$' {
			name := src[1:]
			v, ok := inputs[name]
			if !ok {
				return nil, fmt.Errorf("op %q: workload input %q not provided", op.ID, name)
			}
			resolved[b.Port] = v
			continue
		}
		opID, port := src, ""
		for i := 0; i < len(src); i++ {
			if src[i] == '.' {
				opID, port = src[:i], src[i+1:]
				break
			}
		}
		mu.Lock()
		outs, ok := results[opID]
		mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("op %q: upstream %q has no result", op.ID, opID)
		}
		if port == "" {
			producer := m.Op(opID)
			p, has := producer.PrimaryOutput()
			if !has {
				return nil, fmt.Errorf("op %q: upstream %q has no primary output", op.ID, opID)
			}
			port = p.Name
		}
		v, ok := outs[port]
		if !ok {
			return nil, fmt.Errorf("op %q: upstream %q has no output port %q", op.ID, opID, port)
		}
		resolved[b.Port] = v
	}
	return resolved, nil
}

func effectiveConcurrency(c, n int) int {
	if c <= 0 {
		return n
	}
	return c
}
