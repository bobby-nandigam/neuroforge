package main

import (
	"fmt"
	"os"

	"neuroforge/core/ir"
	"neuroforge/runtime/setup"
)

func cmdDoctor(args []string) error {
	env := setup.Default()

	type check struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}
	var checks []check

	// Operator registry
	checks = append(checks, check{"operator registry", "ok", fmt.Sprintf("%d operators registered", len(ir.Operators()))})

	// Backends
	for _, b := range env.Registry.All() {
		status := "mock-fallback"
		detail := fmt.Sprintf("%d capabilities", len(b.Capabilities()))
		if b.Configured() {
			status = "configured"
		}
		checks = append(checks, check{"backend " + b.Name(), status, detail})
	}

	// Meta gateway
	gwStatus := "not configured"
	gwDetail := "set META_API_KEY to enable real Meta Model API calls"
	if env.Gateway.Configured() {
		gwStatus = "configured"
		gwDetail = "base=" + env.Gateway.BaseURL()
	}
	checks = append(checks, check{"meta gateway", gwStatus, gwDetail})

	// Env hints
	for _, k := range []string{"META_API_BASE_URL", "MUSE_SPARK_MODEL", "SAM_API_PATH", "MUSE_IMAGE_API_PATH"} {
		v := os.Getenv(k)
		st := "unset"
		if v != "" {
			st = "set"
		}
		checks = append(checks, check{"env " + k, st, v})
	}

	if hasFlag(args, "--json") {
		return printJSON(checks)
	}

	fmt.Printf("NEUROFORGE X doctor\n%s\n", rule())
	for _, c := range checks {
		mark := "✓"
		if c.Status == "not configured" || c.Status == "unset" {
			mark = "·"
		}
		fmt.Printf("  %s %-26s %-14s %s\n", mark, c.Name, c.Status, c.Detail)
	}
	fmt.Println(rule())
	if !env.Gateway.Configured() {
		fmt.Println("Meta credentials absent — workloads run with the deterministic mock backend,")
		fmt.Println("and all simulated outputs are clearly labeled. Set META_API_KEY for real calls.")
	}
	return nil
}
