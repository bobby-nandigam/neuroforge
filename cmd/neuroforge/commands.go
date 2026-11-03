package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"neuroforge/backends"
	"neuroforge/core/compiler"
	"neuroforge/core/ir"
	"neuroforge/core/optimizer"
	"neuroforge/runtime/cache"
	"neuroforge/runtime/executor"
	"neuroforge/runtime/setup"
)

func compileOptions(args []string) compiler.Options {
	opts := compiler.DefaultOptions()
	if hasFlag(args, "--no-optimize") {
		opts.Optimize = false
		opts.OptimizerOf = optimizer.Options{}
	}
	return opts
}

func cmdInit(args []string) error {
	dir := positional(args)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	wf := filepath.Join(dir, "workload.yaml")
	if err := os.WriteFile(wf, []byte(sampleWorkload), 0o644); err != nil {
		return err
	}
	ef := filepath.Join(dir, ".env.example")
	if err := os.WriteFile(ef, []byte(envExample), 0o644); err != nil {
		return err
	}
	fmt.Printf("scaffolded:\n  %s\n  %s\n\nnext: neuroforge validate %s\n", wf, ef, wf)
	return nil
}

func cmdValidate(args []string) error {
	src, err := readWorkload(positional(args))
	if err != nil {
		return err
	}
	m, res, err := compiler.Validate(src)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return printJSON(map[string]any{"ok": res.OK(), "workload": m.Name, "diagnostics": res.Diagnostics})
	}
	fmt.Printf("workload: %s\n", m.Name)
	if len(res.Diagnostics) == 0 {
		fmt.Println("no diagnostics")
	}
	for _, d := range res.Diagnostics {
		fmt.Printf("  %s\n", d.String())
	}
	if !res.OK() {
		return fmt.Errorf("validation failed")
	}
	fmt.Println("\n✓ workload is valid")
	return nil
}

func cmdCompile(args []string) error {
	src, err := readWorkload(positional(args))
	if err != nil {
		return err
	}
	c, err := compiler.CompileBytes(src, compileOptions(args))
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return printJSON(c)
	}
	renderCompiled(c)
	if out := flagValue(args, "-o", flagValue(args, "--out", "")); out != "" {
		data, err := ir.Marshal(c.Optimized)
		if err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Printf("\nwrote optimized MIR → %s\n", out)
	}
	return nil
}

func cmdInspect(args []string) error {
	src, err := readWorkload(positional(args))
	if err != nil {
		return err
	}
	c, err := compiler.CompileBytes(src, compileOptions(args))
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		levels, _ := compiler.ExecutionLevels(c.Optimized)
		return printJSON(map[string]any{"compiled": c, "levels": levels})
	}
	renderCompiled(c)
	renderGraph(c.Optimized)
	return nil
}

// RunRecord is the persisted record of an execution, used by `profile`.
type RunRecord struct {
	ID        string                               `json:"id"`
	Workload  string                               `json:"workload"`
	Timestamp time.Time                            `json:"timestamp"`
	Optimized bool                                 `json:"optimized"`
	Simulated bool                                 `json:"simulated"`
	Metrics   executor.Metrics                     `json:"metrics"`
	Outputs   map[string]map[string]backends.Value `json:"outputs"`
}

func cmdRun(args []string) error {
	src, err := readWorkload(positional(args))
	if err != nil {
		return err
	}
	opts := compileOptions(args)
	c, err := compiler.CompileBytes(src, opts)
	if err != nil {
		return err
	}
	concurrency := 0
	if v := flagValue(args, "--concurrency", ""); v != "" {
		concurrency, _ = strconv.Atoi(v)
	}
	cfg := executor.Config{Concurrency: concurrency, RetryBackoff: 100 * time.Millisecond}
	if hasFlag(args, "--cache") {
		cfg.Cache = cache.New(256)
	}

	env := setup.Default()
	ex := executor.New(env.Registry)
	simulated := !env.Gateway.Configured()

	ctx := context.Background()
	res := ex.Run(ctx, c.Optimized, nil, cfg)

	rec := RunRecord{
		ID:        newExecID(),
		Workload:  c.Optimized.Name,
		Timestamp: time.Now(),
		Optimized: opts.Optimize,
		Simulated: simulated,
		Metrics:   res.Metrics,
		Outputs:   res.Outputs,
	}
	if err := saveRun(rec); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save run record: %v\n", err)
	}

	if hasFlag(args, "--json") {
		if err := printJSON(map[string]any{"execution_id": rec.ID, "error": errStr(res.Err), "record": rec}); err != nil {
			return err
		}
		return res.Err
	}

	fmt.Printf("execution %s — workload %q\n", rec.ID, rec.Workload)
	fmt.Println("\noutputs:")
	for _, outID := range c.Optimized.Outputs {
		ports := res.Outputs[outID]
		for port, v := range ports {
			fmt.Printf("  %s.%s (%s):\n", outID, port, v.Type)
			fmt.Printf("    %s\n", trunc(fmt.Sprintf("%v", v.Data), 400))
			if sim, _ := v.Meta["simulated"].(bool); sim {
				fmt.Printf("    [simulated — %v]\n", v.Meta["reason"])
			}
		}
	}
	fmt.Println()
	renderMetrics(res.Metrics, simulated)
	fmt.Printf("\nsaved: neuroforge profile %s\n", rec.ID)
	return res.Err
}

func cmdProfile(args []string) error {
	id := positional(args)
	if id == "" {
		return listRuns()
	}
	rec, err := loadRun(id)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return printJSON(rec)
	}
	fmt.Printf("execution %s — workload %q  (%s)\n", rec.ID, rec.Workload, rec.Timestamp.Format(time.RFC3339))
	fmt.Printf("optimized=%v simulated=%v\n\n", rec.Optimized, rec.Simulated)
	renderMetrics(rec.Metrics, rec.Simulated)
	return nil
}

func cmdOperators(args []string) error {
	ops := ir.Operators()
	if hasFlag(args, "--json") {
		return printJSON(ops)
	}
	fmt.Printf("operator registry (%d operators)\n%s\n", len(ops), rule())
	for _, s := range ops {
		det := "non-deterministic"
		if s.Deterministic {
			det = "deterministic"
		}
		fmt.Printf("%-22s %s\n", s.Type, s.Summary)
		fmt.Printf("    in:  %s\n", portList(s.Inputs))
		fmt.Printf("    out: %s\n", portList(s.Outputs))
		fmt.Printf("    backends: %v  [%s%s]\n", s.Backends, det, cacheTag(s.Cacheable))
	}
	return nil
}

func portList(ports []ir.Port) string {
	if len(ports) == 0 {
		return "(none)"
	}
	s := ""
	for i, p := range ports {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s:%s", p.Name, p.Type)
	}
	return s
}

func cacheTag(c bool) string {
	if c {
		return ", cacheable"
	}
	return ""
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
