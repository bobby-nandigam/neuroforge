package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"neuroforge/core/graph"
	"neuroforge/core/ir"
)

// buildModule creates a module from (id -> deps) with a trivial operator type.
func buildModule(deps map[string][]string, order []string) (*ir.Module, *graph.Graph) {
	m := &ir.Module{Name: "t", SchemaVersion: ir.SchemaVersion}
	for _, id := range order {
		m.Operations = append(m.Operations, &ir.Operation{ID: id, Type: "output.structured", Backend: "mock", DependsOn: deps[id]})
	}
	g, err := graph.Build(m)
	if err != nil {
		panic(err)
	}
	return m, g
}

func TestSequentialVsParallel(t *testing.T) {
	// three independent nodes, each sleeping 50ms
	deps := map[string][]string{"a": nil, "b": nil, "c": nil}
	order := []string{"a", "b", "c"}
	sleep := func(ctx context.Context, op *ir.Operation, attempt int) error {
		select {
		case <-time.After(50 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	m, g := buildModule(deps, order)
	start := time.Now()
	r1 := New(m, g, Config{Concurrency: 1}).Run(context.Background(), sleep)
	serial := time.Since(start)
	if r1.Err != nil {
		t.Fatalf("serial run err: %v", r1.Err)
	}

	m, g = buildModule(deps, order)
	start = time.Now()
	r3 := New(m, g, Config{Concurrency: 3}).Run(context.Background(), sleep)
	par := time.Since(start)
	if r3.Err != nil {
		t.Fatalf("parallel run err: %v", r3.Err)
	}

	if serial < 140*time.Millisecond {
		t.Fatalf("expected serial ~150ms, got %v", serial)
	}
	if par > 110*time.Millisecond {
		t.Fatalf("expected parallel ~50ms, got %v", par)
	}
	t.Logf("serial=%v parallel=%v speedup=%.2fx", serial, par, float64(serial)/float64(par))
}

func TestDependencyOrdering(t *testing.T) {
	// a -> b -> c chain; record completion order.
	deps := map[string][]string{"a": nil, "b": {"a"}, "c": {"b"}}
	m, g := buildModule(deps, []string{"a", "b", "c"})
	var seq []string
	var mu atomic.Value
	_ = mu
	ch := make(chan string, 3)
	run := func(ctx context.Context, op *ir.Operation, attempt int) error {
		ch <- op.ID
		return nil
	}
	r := New(m, g, Config{Concurrency: 3}).Run(context.Background(), run)
	if r.Err != nil {
		t.Fatalf("run err: %v", r.Err)
	}
	close(ch)
	for id := range ch {
		seq = append(seq, id)
	}
	if len(seq) != 3 || seq[0] != "a" || seq[2] != "c" {
		t.Fatalf("expected a..c ordering, got %v", seq)
	}
}

func TestRetrySucceeds(t *testing.T) {
	m, g := buildModule(map[string][]string{"a": nil}, []string{"a"})
	m.Op("a").MaxRetries = 3
	var calls int32
	run := func(ctx context.Context, op *ir.Operation, attempt int) error {
		if atomic.AddInt32(&calls, 1) < 3 {
			return fmt.Errorf("transient")
		}
		return nil
	}
	r := New(m, g, Config{Concurrency: 1, RetryBackoff: time.Millisecond}).Run(context.Background(), run)
	if r.Err != nil {
		t.Fatalf("expected success after retries, got %v", r.Err)
	}
	if r.States["a"] != StateCompleted {
		t.Fatalf("expected COMPLETED, got %s", r.States["a"])
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestTimeout(t *testing.T) {
	m, g := buildModule(map[string][]string{"a": nil}, []string{"a"})
	m.Op("a").TimeoutMS = 20
	run := func(ctx context.Context, op *ir.Operation, attempt int) error {
		select {
		case <-time.After(200 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r := New(m, g, Config{Concurrency: 1}).Run(context.Background(), run)
	if r.Err == nil {
		t.Fatal("expected timeout failure")
	}
	if r.States["a"] != StateFailed {
		t.Fatalf("expected FAILED, got %s", r.States["a"])
	}
}

func TestFailurePolicyFailAborts(t *testing.T) {
	// a fails (policy fail); b depends on a and must not complete.
	deps := map[string][]string{"a": nil, "b": {"a"}}
	m, g := buildModule(deps, []string{"a", "b"})
	m.Op("a").FailurePolicy = ir.PolicyFail
	run := func(ctx context.Context, op *ir.Operation, attempt int) error {
		if op.ID == "a" {
			return fmt.Errorf("boom")
		}
		return nil
	}
	r := New(m, g, Config{Concurrency: 2}).Run(context.Background(), run)
	if r.Err == nil {
		t.Fatal("expected run error")
	}
	if r.States["a"] != StateFailed {
		t.Fatalf("a should be FAILED, got %s", r.States["a"])
	}
	if r.States["b"] == StateCompleted {
		t.Fatal("b should not complete when a fails")
	}
}

func TestFailurePolicySkipBlocksDescendants(t *testing.T) {
	// a fails (policy skip); b depends on a -> BLOCKED; c independent -> COMPLETED.
	deps := map[string][]string{"a": nil, "b": {"a"}, "c": nil}
	m, g := buildModule(deps, []string{"a", "b", "c"})
	m.Op("a").FailurePolicy = ir.PolicySkip
	run := func(ctx context.Context, op *ir.Operation, attempt int) error {
		if op.ID == "a" {
			return fmt.Errorf("boom")
		}
		return nil
	}
	r := New(m, g, Config{Concurrency: 3}).Run(context.Background(), run)
	if r.States["a"] != StateFailed {
		t.Fatalf("a should be FAILED, got %s", r.States["a"])
	}
	if r.States["b"] != StateBlocked {
		t.Fatalf("b should be BLOCKED, got %s", r.States["b"])
	}
	if r.States["c"] != StateCompleted {
		t.Fatalf("c should be COMPLETED, got %s", r.States["c"])
	}
}

func TestCancellation(t *testing.T) {
	deps := map[string][]string{"a": nil, "b": nil}
	m, g := buildModule(deps, []string{"a", "b"})
	ctx, cancel := context.WithCancel(context.Background())
	run := func(c context.Context, op *ir.Operation, attempt int) error {
		cancel()
		<-c.Done()
		return c.Err()
	}
	r := New(m, g, Config{Concurrency: 2}).Run(ctx, run)
	if r.Err == nil {
		t.Fatal("expected cancellation error")
	}
}
