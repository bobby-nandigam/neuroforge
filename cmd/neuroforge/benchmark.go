package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"neuroforge/core/compiler"
	"neuroforge/core/optimizer"
	"neuroforge/runtime/cache"
	"neuroforge/runtime/executor"
	"neuroforge/runtime/setup"
)

const defaultBenchWorkload = "examples/benchmark/workload.yaml"

func cmdBenchmark(args []string) error {
	path := positional(args)
	if path == "" {
		path = defaultBenchWorkload
	}
	src, err := readWorkload(path)
	if err != nil {
		return fmt.Errorf("%w (pass a workload path, or run from the repo root)", err)
	}

	noOpt := compiler.Options{Optimize: true, OptimizerOf: optimizer.Options{}} // optimize on, all passes off => base graph
	fullOpt := compiler.DefaultOptions()

	base, err := compiler.CompileBytes(src, noOpt)
	if err != nil {
		return err
	}
	opt, err := compiler.CompileBytes(src, fullOpt)
	if err != nil {
		return err
	}

	env := setup.Default()
	ex := executor.New(env.Registry)
	simulated := !env.Gateway.Configured()
	ctx := context.Background()

	// median-of-3 wall time of a run configuration
	measure := func(run func() *executor.Result) (time.Duration, *executor.Result) {
		var times []time.Duration
		var last *executor.Result
		for i := 0; i < 3; i++ {
			start := time.Now()
			last = run()
			times = append(times, time.Since(start))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		return times[1], last
	}

	type row struct {
		Name  string
		Dur   time.Duration
		Cache string
	}
	var rows []row

	// 1) sequential baseline (unoptimized graph, concurrency 1, no cache)
	seqDur, _ := measure(func() *executor.Result {
		return ex.Run(ctx, base.Optimized, nil, executor.Config{Concurrency: 1})
	})
	rows = append(rows, row{"sequential (c=1)", seqDur, "-"})

	// 2) parallel DAG (unoptimized graph, full width, no cache)
	parDur, _ := measure(func() *executor.Result {
		return ex.Run(ctx, base.Optimized, nil, executor.Config{Concurrency: 0})
	})
	rows = append(rows, row{"parallel DAG", parDur, "-"})

	// 3) parallel + warm cache (unoptimized graph)
	ca := cache.New(1024)
	ex.Run(ctx, base.Optimized, nil, executor.Config{Concurrency: 0, Cache: ca}) // warm
	cacheDur, cacheRes := measure(func() *executor.Result {
		return ex.Run(ctx, base.Optimized, nil, executor.Config{Concurrency: 0, Cache: ca})
	})
	rows = append(rows, row{"parallel + cache (warm)", cacheDur, hitRate(cacheRes)})

	// 4) optimized graph + parallel + warm cache
	ca2 := cache.New(1024)
	ex.Run(ctx, opt.Optimized, nil, executor.Config{Concurrency: 0, Cache: ca2}) // warm
	optDur, optRes := measure(func() *executor.Result {
		return ex.Run(ctx, opt.Optimized, nil, executor.Config{Concurrency: 0, Cache: ca2})
	})
	rows = append(rows, row{"optimized + parallel + cache", optDur, hitRate(optRes)})

	if hasFlag(args, "--json") {
		out := []map[string]any{}
		for _, r := range rows {
			out = append(out, map[string]any{"config": r.Name, "ms": msOf(r.Dur), "speedup": speedup(seqDur, r.Dur), "cache": r.Cache})
		}
		return printJSON(map[string]any{
			"workload": opt.Optimized.Name, "simulated": simulated,
			"base_ops": len(base.Optimized.Operations), "optimized_ops": len(opt.Optimized.Operations),
			"results": out,
		})
	}

	fmt.Printf("benchmark: %s\n", opt.Optimized.Name)
	fmt.Printf("operations: %d (base) → %d (optimized)\n", len(base.Optimized.Operations), len(opt.Optimized.Operations))
	if simulated {
		fmt.Println("note: backend latencies are SIMULATED by the mock (no Meta credentials set)")
	}
	fmt.Println(rule())
	fmt.Printf("  %-32s %10s %10s %10s\n", "configuration", "time", "speedup", "cache")
	for _, r := range rows {
		fmt.Printf("  %-32s %8.1fms %9.2fx %10s\n", r.Name, msOf(r.Dur), speedup(seqDur, r.Dur), r.Cache)
	}
	fmt.Println(rule())
	fmt.Println("measured wall time, median of 3 runs; speedup relative to sequential baseline.")
	return nil
}

func msOf(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

func speedup(base, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(base) / float64(d)
}

func hitRate(r *executor.Result) string {
	if r == nil || r.Metrics.Cache == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", r.Metrics.Cache.HitRate*100)
}
