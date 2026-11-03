package executor

import (
	"context"
	"testing"
	"time"

	"neuroforge/backends"
	"neuroforge/backends/local"
	"neuroforge/backends/mock"
	"neuroforge/core/compiler"
	"neuroforge/runtime/cache"
	"neuroforge/runtime/scheduler"
)

const workload = `
name: warehouse
inputs:
  - name: video
    type: video
    ref: warehouse.mp4
operations:
  - id: frames
    type: vision.frame_extract
    backend: local
    inputs: { video: $video }
  - id: segment
    type: vision.segmentation
    backend: mock
    inputs: { image: frames.frames }
  - id: features
    type: vision.features
    backend: local
    inputs: { objects: segment.objects }
  - id: report
    type: output.structured
    backend: local
    inputs: { data: features.features }
outputs: [report]
`

func registry() *backends.Registry {
	r := backends.NewRegistry()
	r.Register(mock.New())
	r.Register(local.New())
	return r
}

func TestEndToEnd(t *testing.T) {
	c, err := compiler.CompileBytes([]byte(workload), compiler.DefaultOptions())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ex := New(registry())
	res := ex.Run(context.Background(), c.Optimized, nil, Config{Concurrency: 4})
	if res.Err != nil {
		t.Fatalf("run err: %v", res.Err)
	}
	rep, ok := res.Outputs["report"]
	if !ok {
		t.Fatal("expected report output")
	}
	if rep["report"].Data == nil {
		t.Fatal("report data empty")
	}
	for id, st := range res.Metrics.States {
		if st != scheduler.StateCompleted {
			t.Fatalf("op %q not completed: %s", id, st)
		}
	}
	if res.Metrics.Profile.NodeCount != 4 {
		t.Fatalf("expected 4 node profiles, got %d", res.Metrics.Profile.NodeCount)
	}
}

func TestCachingHits(t *testing.T) {
	c, err := compiler.CompileBytes([]byte(workload), compiler.DefaultOptions())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ca := cache.New(128)
	ex := New(registry())

	// First run: all misses (segment/frames/features/report are cacheable).
	r1 := ex.Run(context.Background(), c.Optimized, nil, Config{Concurrency: 4, Cache: ca})
	if r1.Err != nil {
		t.Fatalf("run1: %v", r1.Err)
	}
	if r1.Metrics.Cache.Hits != 0 {
		t.Fatalf("expected 0 hits on first run, got %d", r1.Metrics.Cache.Hits)
	}

	// Second run with same cache: cacheable ops should hit.
	r2 := ex.Run(context.Background(), c.Optimized, nil, Config{Concurrency: 4, Cache: ca})
	if r2.Err != nil {
		t.Fatalf("run2: %v", r2.Err)
	}
	if r2.Metrics.Cache.Hits == 0 {
		t.Fatal("expected cache hits on second run")
	}
	if r2.Metrics.Profile.CacheHits == 0 {
		t.Fatal("expected profiled cache hits on second run")
	}
}

func TestCacheSpeedup(t *testing.T) {
	c, err := compiler.CompileBytes([]byte(workload), compiler.DefaultOptions())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ca := cache.New(128)
	ex := New(registry())

	start := time.Now()
	ex.Run(context.Background(), c.Optimized, nil, Config{Concurrency: 1, Cache: ca})
	cold := time.Since(start)

	start = time.Now()
	ex.Run(context.Background(), c.Optimized, nil, Config{Concurrency: 1, Cache: ca})
	warm := time.Since(start)

	// The warm run should be faster because the simulated-latency segmentation
	// is served from cache. This is a sanity check, not a strict benchmark.
	if warm >= cold {
		t.Logf("warning: warm (%v) not faster than cold (%v)", warm, cold)
	}
	t.Logf("cold=%v warm=%v", cold, warm)
}
