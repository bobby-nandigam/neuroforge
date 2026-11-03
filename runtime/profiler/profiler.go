// Package profiler records execution timing and state transitions for a run.
// It implements scheduler.Observer so the scheduler feeds it state changes, and
// the executor annotates it with cache-hit and backend information.
package profiler

import (
	"sort"
	"sync"
	"time"

	"neuroforge/core/ir"
	"neuroforge/runtime/scheduler"
)

// NodeProfile is the recorded timeline of a single operation.
type NodeProfile struct {
	OpID     string          `json:"op"`
	Type     string          `json:"type"`
	Backend  string          `json:"backend"`
	State    scheduler.State `json:"state"`
	Attempts int             `json:"attempts"`
	CacheHit bool            `json:"cache_hit"`
	ReadyAt  time.Time       `json:"-"`
	StartAt  time.Time       `json:"-"`
	EndAt    time.Time       `json:"-"`
	QueueMS  float64         `json:"queue_wait_ms"`
	DurMS    float64         `json:"duration_ms"`
	Err      string          `json:"error,omitempty"`
}

// Profiler collects per-node profiles from scheduler callbacks.
type Profiler struct {
	mu    sync.Mutex
	nodes map[string]*NodeProfile
	order []string // first-seen order

	started time.Time
	ended   time.Time
}

// New returns an empty profiler.
func New() *Profiler {
	return &Profiler{nodes: map[string]*NodeProfile{}}
}

func (p *Profiler) node(op *ir.Operation) *NodeProfile {
	np, ok := p.nodes[op.ID]
	if !ok {
		np = &NodeProfile{OpID: op.ID, Type: op.Type, Backend: op.Backend}
		p.nodes[op.ID] = np
		p.order = append(p.order, op.ID)
	}
	return np
}

// Start marks the overall run start.
func (p *Profiler) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = time.Now()
}

// Finish marks the overall run end.
func (p *Profiler) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = time.Now()
}

// OnStateChange implements scheduler.Observer.
func (p *Profiler) OnStateChange(op *ir.Operation, st scheduler.State, attempt int, err error) {
	if op == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	np := p.node(op)
	np.State = st
	now := time.Now()
	switch st {
	case scheduler.StateReady:
		if np.ReadyAt.IsZero() {
			np.ReadyAt = now
		}
	case scheduler.StateRunning:
		if np.StartAt.IsZero() {
			np.StartAt = now
		}
		if attempt+1 > np.Attempts {
			np.Attempts = attempt + 1
		}
	case scheduler.StateRetrying:
		if attempt+1 > np.Attempts {
			np.Attempts = attempt + 1
		}
	case scheduler.StateCompleted, scheduler.StateFailed, scheduler.StateCancelled, scheduler.StateBlocked:
		np.EndAt = now
		if err != nil {
			np.Err = err.Error()
		}
	}
}

// MarkCacheHit flags an operation as served from cache.
func (p *Profiler) MarkCacheHit(opID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if np, ok := p.nodes[opID]; ok {
		np.CacheHit = true
	}
}

// Report is the finalized profile of a run.
type Report struct {
	TotalMS    float64       `json:"total_ms"`
	Nodes      []NodeProfile `json:"nodes"`
	NodeCount  int           `json:"node_count"`
	Completed  int           `json:"completed"`
	Failed     int           `json:"failed"`
	CacheHits  int           `json:"cache_hits"`
	Retries    int           `json:"retries"`
	SumNodeMS  float64       `json:"sum_node_ms"`      // serial sum of node durations
	CriticalMS float64       `json:"critical_path_ms"` // slowest start->end span
}

// Report computes the finalized report. Call after Finish.
func (p *Profiler) Report() Report {
	p.mu.Lock()
	defer p.mu.Unlock()

	r := Report{}
	if !p.started.IsZero() && !p.ended.IsZero() {
		r.TotalMS = ms(p.ended.Sub(p.started))
	}
	for _, id := range p.order {
		np := p.nodes[id]
		cp := *np
		if !cp.StartAt.IsZero() && !cp.ReadyAt.IsZero() {
			cp.QueueMS = ms(cp.StartAt.Sub(cp.ReadyAt))
		}
		if !cp.EndAt.IsZero() && !cp.StartAt.IsZero() {
			cp.DurMS = ms(cp.EndAt.Sub(cp.StartAt))
		}
		r.Nodes = append(r.Nodes, cp)
		r.NodeCount++
		r.SumNodeMS += cp.DurMS
		if cp.DurMS > r.CriticalMS {
			r.CriticalMS = cp.DurMS
		}
		switch cp.State {
		case scheduler.StateCompleted:
			r.Completed++
		case scheduler.StateFailed:
			r.Failed++
		}
		if cp.CacheHit {
			r.CacheHits++
		}
		if cp.Attempts > 1 {
			r.Retries += cp.Attempts - 1
		}
	}
	sort.SliceStable(r.Nodes, func(i, j int) bool {
		return r.Nodes[i].StartAt.Before(r.Nodes[j].StartAt)
	})
	return r
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }
