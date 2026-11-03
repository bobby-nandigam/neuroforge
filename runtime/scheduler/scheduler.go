// Package scheduler executes an MIR dependency graph with bounded concurrency.
// It owns node state, readiness, retries, per-node timeouts, cancellation, and
// failure-policy handling. The actual work of an operation is supplied by the
// caller as a RunFunc, keeping the scheduler independent of backends.
package scheduler

import (
	"context"
	"fmt"
	"time"

	"neuroforge/core/graph"
	"neuroforge/core/ir"
)

// State is an operation's execution state.
type State string

const (
	StatePending   State = "PENDING"
	StateReady     State = "READY"
	StateRunning   State = "RUNNING"
	StateRetrying  State = "RETRYING"
	StateCompleted State = "COMPLETED"
	StateFailed    State = "FAILED"
	StateCancelled State = "CANCELLED"
	StateBlocked   State = "BLOCKED"
)

func (s State) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled, StateBlocked:
		return true
	}
	return false
}

// RunFunc performs one attempt of an operation. attempt is 0-based. Returning a
// nil error means success. The provided ctx carries any per-node timeout.
type RunFunc func(ctx context.Context, op *ir.Operation, attempt int) error

// Observer receives state transitions for live monitoring and profiling. Its
// methods may be called concurrently and must be safe for concurrent use.
type Observer interface {
	OnStateChange(op *ir.Operation, state State, attempt int, err error)
}

// Config tunes a scheduler run.
type Config struct {
	Concurrency  int           // max operations running at once (<=0 => len(nodes))
	RetryBackoff time.Duration // base delay before a retry
	Observer     Observer
}

// Result is the outcome of a scheduler run.
type Result struct {
	States map[string]State
	Err    error
}

// Scheduler runs a single module to completion.
type Scheduler struct {
	m   *ir.Module
	g   *graph.Graph
	cfg Config
}

// New builds a scheduler for a module and its dependency graph.
func New(m *ir.Module, g *graph.Graph, cfg Config) *Scheduler {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = len(m.Operations)
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	return &Scheduler{m: m, g: g, cfg: cfg}
}

type doneEvent struct {
	op  string
	err error
}

// Run executes the graph. It blocks until all reachable nodes reach a terminal
// state or the context is cancelled. A node failure is handled per the node's
// FailurePolicy: PolicyFail aborts the whole run (cancelling in-flight work),
// PolicySkip blocks only the failed node's descendants.
func (s *Scheduler) Run(parent context.Context, run RunFunc) *Result {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	state := make(map[string]State, len(s.g.Nodes))
	indeg := make(map[string]int, len(s.g.Nodes))
	op := func(id string) *ir.Operation { return s.m.Op(id) }

	// notify is safe to call from any goroutine: the Observer is responsible for
	// its own synchronization. setState additionally mutates the shared `state`
	// map and MUST only be called from the main scheduling goroutine.
	notify := func(o *ir.Operation, st State, attempt int, err error) {
		if s.cfg.Observer != nil {
			s.cfg.Observer.OnStateChange(o, st, attempt, err)
		}
	}
	setState := func(id string, st State, attempt int, err error) {
		state[id] = st
		notify(op(id), st, attempt, err)
	}

	for _, n := range s.g.Nodes {
		state[n] = StatePending
		indeg[n] = len(s.g.Pred[n])
		setState(n, StatePending, 0, nil)
	}

	ready := make([]string, 0, len(s.g.Nodes))
	enqueue := func(id string) {
		setState(id, StateReady, 0, nil)
		ready = append(ready, id)
	}
	for _, n := range s.g.Roots() {
		enqueue(n)
	}

	done := make(chan doneEvent, len(s.g.Nodes))
	inflight := 0
	var runErr error
	aborting := false

	countNonTerminal := func() int {
		c := 0
		for _, st := range state {
			if !st.Terminal() {
				c++
			}
		}
		return c
	}

	for countNonTerminal() > 0 || inflight > 0 {
		// Dispatch ready nodes up to the concurrency limit (unless aborting).
		for !aborting && inflight < s.cfg.Concurrency && len(ready) > 0 {
			id := ready[0]
			ready = ready[1:]
			inflight++
			o := op(id)
			go func() {
				// Workers never touch the shared state map; they only emit
				// observer notifications for RUNNING/RETRYING transitions.
				err := s.runWithRetry(ctx, o, run, notify)
				done <- doneEvent{op: id, err: err}
			}()
		}

		if inflight == 0 {
			// Nothing running and nothing dispatchable. If aborting or the
			// context is done, cancel remaining non-terminal nodes.
			if aborting || ctx.Err() != nil {
				s.cancelRemaining(state, setState)
			}
			break
		}

		ev := <-done
		inflight--
		o := op(ev.op)

		if ev.err == nil {
			setState(ev.op, StateCompleted, 0, nil)
			for _, succ := range s.g.Succ[ev.op] {
				indeg[succ]--
				if indeg[succ] == 0 && state[succ] == StatePending {
					enqueue(succ)
				}
			}
			continue
		}

		// Failure.
		setState(ev.op, StateFailed, 0, ev.err)
		if runErr == nil {
			runErr = fmt.Errorf("operation %q failed: %w", ev.op, ev.err)
		}
		if o.FailurePolicy == ir.PolicySkip {
			// Block only descendants; other branches proceed.
			for d := range s.g.Reachable([]string{ev.op}) {
				if d == ev.op {
					continue
				}
				if !state[d].Terminal() {
					setState(d, StateBlocked, 0, nil)
				}
			}
		} else {
			// Abort the whole run: cancel in-flight work and stop dispatching.
			aborting = true
			cancel()
		}
	}

	// Drain any late events after an abort to avoid goroutine leaks.
	for inflight > 0 {
		<-done
		inflight--
	}
	if aborting || ctx.Err() != nil {
		s.cancelRemaining(state, setState)
	}

	return &Result{States: state, Err: runErr}
}

func (s *Scheduler) cancelRemaining(state map[string]State, setState func(string, State, int, error)) {
	for _, n := range s.g.Nodes {
		if !state[n].Terminal() {
			setState(n, StateCancelled, 0, nil)
		}
	}
}

// runWithRetry executes an operation, retrying transient failures up to the
// node's MaxRetries and enforcing its per-attempt timeout.
func (s *Scheduler) runWithRetry(ctx context.Context, o *ir.Operation, run RunFunc, notify func(*ir.Operation, State, int, error)) error {
	var lastErr error
	for attempt := 0; attempt <= o.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == 0 {
			notify(o, StateRunning, attempt, nil)
		} else {
			notify(o, StateRetrying, attempt, lastErr)
			// Backoff, respecting cancellation.
			backoff := s.cfg.RetryBackoff * time.Duration(attempt)
			if backoff > 0 {
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			notify(o, StateRunning, attempt, nil)
		}

		attemptCtx := ctx
		var cancel context.CancelFunc
		if o.TimeoutMS > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(o.TimeoutMS)*time.Millisecond)
		}
		err := run(attemptCtx, o, attempt)
		if cancel != nil {
			cancel()
		}
		if err == nil {
			return nil
		}
		lastErr = err
		// Do not retry if the parent context is gone.
		if ctx.Err() != nil {
			return err
		}
	}
	return lastErr
}
