// Command neuroforge is the NEUROFORGE X command-line interface: a compiler and
// runtime driver for multimodal AI workloads. It validates, compiles, inspects,
// runs, profiles, and benchmarks workloads against local, mock, and Meta model
// backends.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "validate":
		err = cmdValidate(args)
	case "compile":
		err = cmdCompile(args)
	case "inspect":
		err = cmdInspect(args)
	case "run":
		err = cmdRun(args)
	case "profile":
		err = cmdProfile(args)
	case "benchmark":
		err = cmdBenchmark(args)
	case "doctor":
		err = cmdDoctor(args)
	case "operators":
		err = cmdOperators(args)
	case "version", "--version", "-v":
		fmt.Printf("neuroforge %s\n", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "neuroforge: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`NEUROFORGE X — multimodal AI compiler & runtime

Usage:
  neuroforge <command> [flags]

Commands:
  init [dir]              Scaffold a sample workload and .env.example
  validate <workload>    Parse and type-check a workload
  compile <workload>     Compile a workload to MIR (optimized)
  inspect <workload>     Show graph, execution levels, passes, backends
  run <workload>         Execute a workload and report results + metrics
  profile <exec-id>      Show the saved profile of a previous run
  benchmark [workload]   Compare sequential / parallel / cached / optimized
  doctor                 Check toolchain, backends, and API configuration
  operators              List the operator registry
  version                Print version

Common flags:
  --json                 Machine-readable JSON output
  --no-optimize          Disable optimization passes
  --concurrency N        Max concurrent operations (run/benchmark)
  --cache                Enable result caching (run)

Environment (real Meta API; optional — mock is used otherwise):
  META_API_KEY, META_API_BASE_URL, MUSE_SPARK_MODEL, SAM_API_PATH,
  MUSE_IMAGE_API_PATH, MUSE_IMAGE_MODEL
`)
}

// --- shared helpers ---

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// flagValue returns the value following --name, or def.
func flagValue(args []string, name, def string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return def
}

// positional returns the first non-flag argument (skipping flag values).
func positional(args []string) string {
	skip := map[string]bool{"--concurrency": true, "-o": true, "--out": true, "--input": true, "--save-report": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 0 && a[0] == '-' {
			if skip[a] {
				i++ // skip its value
			}
			continue
		}
		return a
	}
	return ""
}

func readWorkload(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("a workload file path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workload %q: %w", path, err)
	}
	return data, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
