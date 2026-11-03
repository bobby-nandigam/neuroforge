// Package graph provides DAG analysis over an MIR module: dependency edges,
// cycle detection, topological ordering, and parallelism (level) analysis.
// It is pure and backend-agnostic so the compiler and runtime share one view
// of structure.
package graph

import (
	"fmt"
	"sort"

	"neuroforge/core/ir"
)

// Graph is the dependency view of a module. Nodes are operation IDs; an edge
// u -> v means v depends on u (u must complete before v).
type Graph struct {
	Nodes   []string            // operation IDs, in module order
	Succ    map[string][]string // u -> nodes that depend on u
	Pred    map[string][]string // v -> nodes v depends on
	indexOf map[string]int
}

// Build constructs the dependency graph from a module. Dependencies come from
// explicit depends_on plus any producer referenced by an input binding, so the
// graph reflects true dataflow even if depends_on is omitted. It errors if a
// dependency references an unknown operation.
func Build(m *ir.Module) (*Graph, error) {
	g := &Graph{
		Succ:    map[string][]string{},
		Pred:    map[string][]string{},
		indexOf: map[string]int{},
	}
	for i, op := range m.Operations {
		if op.ID == "" {
			return nil, fmt.Errorf("graph: operation %d has empty id", i)
		}
		if _, dup := g.indexOf[op.ID]; dup {
			return nil, fmt.Errorf("graph: duplicate operation id %q", op.ID)
		}
		g.Nodes = append(g.Nodes, op.ID)
		g.indexOf[op.ID] = i
		g.Succ[op.ID] = nil
		g.Pred[op.ID] = nil
	}

	addEdge := func(from, to string) error {
		if _, ok := g.indexOf[from]; !ok {
			return fmt.Errorf("graph: operation %q depends on unknown operation %q", to, from)
		}
		// de-dup
		for _, s := range g.Succ[from] {
			if s == to {
				return nil
			}
		}
		g.Succ[from] = append(g.Succ[from], to)
		g.Pred[to] = append(g.Pred[to], from)
		return nil
	}

	for _, op := range m.Operations {
		for _, dep := range op.DependsOn {
			if err := addEdge(dep, op.ID); err != nil {
				return nil, err
			}
		}
		for _, b := range op.Bindings {
			if src, ok := parseOpSource(b.Source); ok {
				if err := addEdge(src, op.ID); err != nil {
					return nil, err
				}
			}
		}
	}
	// Stable ordering for deterministic output.
	for k := range g.Succ {
		sort.Slice(g.Succ[k], func(i, j int) bool { return g.indexOf[g.Succ[k][i]] < g.indexOf[g.Succ[k][j]] })
	}
	for k := range g.Pred {
		sort.Slice(g.Pred[k], func(i, j int) bool { return g.indexOf[g.Pred[k][i]] < g.indexOf[g.Pred[k][j]] })
	}
	return g, nil
}

// parseOpSource extracts the producing operation id from a binding source.
// "$input" (workload input) yields ("", false); "op.port" or "op" yields the
// op id. It is duplicated minimally here to keep graph independent of binding
// resolution in the type checker.
func parseOpSource(source string) (string, bool) {
	if source == "" || source[0] == '$' {
		return "", false
	}
	for i := 0; i < len(source); i++ {
		if source[i] == '.' {
			return source[:i], true
		}
	}
	return source, true
}

// Cycle holds a detected dependency cycle as an ordered list of operation IDs.
type Cycle struct{ Path []string }

func (c Cycle) Error() string {
	s := "graph: dependency cycle: "
	for i, n := range c.Path {
		if i > 0 {
			s += " -> "
		}
		s += n
	}
	return s
}

// TopoSort returns a topological ordering of the nodes (dependencies first).
// If the graph contains a cycle it returns a *Cycle error naming the cycle.
func (g *Graph) TopoSort() ([]string, error) {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(g.Nodes))
	var order []string
	var stack []string

	var visit func(n string) *Cycle
	visit = func(n string) *Cycle {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range g.Succ[n] {
			switch color[m] {
			case white:
				if c := visit(m); c != nil {
					return c
				}
			case gray:
				// Found a back edge; extract the cycle from the stack.
				cyc := extractCycle(stack, m)
				return &Cycle{Path: cyc}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		order = append(order, n)
		return nil
	}

	for _, n := range g.Nodes {
		if color[n] == white {
			if c := visit(n); c != nil {
				return nil, *c
			}
		}
	}
	// order is in post-order (dependencies last-finished first); reverse it.
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}

func extractCycle(stack []string, start string) []string {
	for i, n := range stack {
		if n == start {
			cyc := append([]string(nil), stack[i:]...)
			return append(cyc, start)
		}
	}
	return append([]string(nil), start)
}

// Levels partitions nodes into dependency levels. All nodes in level k depend
// only on nodes in levels < k, so every node within a level can execute
// concurrently. Returns an error if the graph is cyclic.
func (g *Graph) Levels() ([][]string, error) {
	if _, err := g.TopoSort(); err != nil {
		return nil, err
	}
	level := make(map[string]int, len(g.Nodes))
	order, _ := g.TopoSort()
	maxLevel := 0
	for _, n := range order {
		l := 0
		for _, p := range g.Pred[n] {
			if level[p]+1 > l {
				l = level[p] + 1
			}
		}
		level[n] = l
		if l > maxLevel {
			maxLevel = l
		}
	}
	out := make([][]string, maxLevel+1)
	for _, n := range g.Nodes { // module order for stability
		out[level[n]] = append(out[level[n]], n)
	}
	return out, nil
}

// Roots returns nodes with no predecessors (ready at start).
func (g *Graph) Roots() []string {
	var r []string
	for _, n := range g.Nodes {
		if len(g.Pred[n]) == 0 {
			r = append(r, n)
		}
	}
	return r
}

// Reachable returns the set of nodes reachable from the given roots following
// successor edges, including the roots themselves.
func (g *Graph) Reachable(roots []string) map[string]bool {
	seen := map[string]bool{}
	var dfs func(string)
	dfs = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		for _, s := range g.Succ[n] {
			dfs(s)
		}
	}
	for _, r := range roots {
		dfs(r)
	}
	return seen
}

// Ancestors returns all nodes that the given node transitively depends on.
func (g *Graph) Ancestors(n string) map[string]bool {
	seen := map[string]bool{}
	var dfs func(string)
	dfs = func(x string) {
		for _, p := range g.Pred[x] {
			if !seen[p] {
				seen[p] = true
				dfs(p)
			}
		}
	}
	dfs(n)
	return seen
}
