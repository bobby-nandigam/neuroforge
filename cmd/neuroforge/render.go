package main

import (
	"fmt"
	"sort"
	"strings"

	"neuroforge/core/compiler"
	"neuroforge/core/ir"
	"neuroforge/runtime/executor"
)

func rule() string { return strings.Repeat("─", 64) }

func renderCompiled(c *compiler.Compiled) {
	fmt.Printf("workload: %s\n", c.Base.Name)
	fmt.Printf("MIR schema: %s\n", c.Base.SchemaVersion)
	fmt.Printf("operations: %d (base) → %d (optimized)\n", len(c.Base.Operations), len(c.Optimized.Operations))
	if len(c.Optimized.Passes) > 0 {
		fmt.Println("\noptimization passes:")
		for _, p := range c.Optimized.Passes {
			mark := "·"
			if p.Changed {
				mark = "✓"
			}
			fmt.Printf("  %s %s\n", mark, p.Name)
			for _, n := range p.Notes {
				fmt.Printf("      %s\n", n)
			}
		}
	}
}

func renderGraph(m *ir.Module) {
	levels, err := compiler.ExecutionLevels(m)
	if err != nil {
		fmt.Printf("  (graph error: %v)\n", err)
		return
	}
	fmt.Printf("\nexecution plan (%d parallel levels):\n", len(levels))
	for i, lvl := range levels {
		sort.Strings(lvl)
		fmt.Printf("  level %d: %s\n", i, strings.Join(lvl, ", "))
	}
	fmt.Println("\noperations:")
	for _, op := range m.Operations {
		cache := "no-cache"
		if op.Cacheable && op.Deterministic && !op.SideEffects {
			cache = "cacheable"
		}
		deps := ""
		if len(op.DependsOn) > 0 {
			deps = "  <- " + strings.Join(op.DependsOn, ", ")
		}
		fmt.Printf("  %-10s %-22s @ %-16s [%s]%s\n", op.ID, op.Type, op.Backend, cache, deps)
	}
}

func renderMetrics(m executor.Metrics, simulated bool) {
	fmt.Println(rule())
	fmt.Printf("execution metrics  (concurrency=%d)\n", m.Concurrency)
	if simulated {
		fmt.Println("note: one or more nodes ran on the mock — those latencies are SIMULATED;")
		fmt.Println("      local and configured model backends (e.g. local.llama) are measured.")
	}
	fmt.Println(rule())
	p := m.Profile
	fmt.Printf("total wall time   : %.2f ms\n", p.TotalMS)
	fmt.Printf("sum of node time  : %.2f ms  (serial-equivalent)\n", p.SumNodeMS)
	fmt.Printf("critical path node: %.2f ms\n", p.CriticalMS)
	fmt.Printf("nodes             : %d completed, %d failed\n", p.Completed, p.Failed)
	fmt.Printf("cache hits        : %d\n", p.CacheHits)
	fmt.Printf("retries           : %d\n", p.Retries)
	if m.Cache != nil {
		fmt.Printf("cache             : %d hit / %d miss (%.0f%% hit rate), %d evictions, %d/%d entries\n",
			m.Cache.Hits, m.Cache.Misses, m.Cache.HitRate*100, m.Cache.Evicts, m.Cache.Size, m.Cache.Capacity)
	}
	fmt.Println("\nper-node timeline:")
	fmt.Printf("  %-10s %-20s %-14s %8s %8s %6s %s\n", "op", "type", "backend", "queue", "dur", "tries", "state")
	for _, n := range p.Nodes {
		hit := ""
		if n.CacheHit {
			hit = " (cache)"
		}
		fmt.Printf("  %-10s %-20s %-14s %6.1fms %6.1fms %6d %s%s\n",
			n.OpID, trunc(n.Type, 20), trunc(n.Backend, 14), n.QueueMS, n.DurMS, n.Attempts, n.State, hit)
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
