// Package optimizer rewrites a type-checked MIR module into an equivalent but
// cheaper module. Every pass must preserve observable semantics: operations
// with side effects or non-deterministic results are never merged or removed.
package optimizer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"neuroforge/core/graph"
	"neuroforge/core/ir"
)

// Options toggles individual passes. The default (all true) is what the CLI and
// runtime use; benchmarks disable passes to measure their isolated effect.
type Options struct {
	DeadNodeElimination bool
	CommonSubexpr       bool
	ParallelismAnalysis bool
}

// DefaultOptions enables every pass.
func DefaultOptions() Options {
	return Options{DeadNodeElimination: true, CommonSubexpr: true, ParallelismAnalysis: true}
}

// Optimize returns a new optimized module and leaves the input untouched. It
// records each applied pass on the returned module's Passes slice.
func Optimize(m *ir.Module, opts Options) (*ir.Module, error) {
	out := m.Clone()
	if opts.CommonSubexpr {
		rec := commonSubexpr(out)
		out.Passes = append(out.Passes, rec)
	}
	if opts.DeadNodeElimination {
		rec, err := deadNodeElimination(out)
		if err != nil {
			return nil, err
		}
		out.Passes = append(out.Passes, rec)
	}
	if opts.ParallelismAnalysis {
		rec, err := parallelismAnalysis(out)
		if err != nil {
			return nil, err
		}
		out.Passes = append(out.Passes, rec)
	}
	return out, nil
}

// commonSubexpr merges operations that are provably identical and safe to
// deduplicate: same type, backend, params, and input sources, and declared
// deterministic + cacheable + side-effect-free. Dependents of a removed
// duplicate are rewritten to reference the surviving canonical operation.
//
// It runs to a fixpoint: merging a producer can make its consumers identical
// (their input sources now point at the same canonical op), so the pass repeats
// until no further duplicates are found.
func commonSubexpr(m *ir.Module) ir.PassRecord {
	rec := ir.PassRecord{Name: "common-subexpression-elimination"}
	var allMerges []string
	for {
		merges := commonSubexprOnce(m)
		if len(merges) == 0 {
			break
		}
		allMerges = append(allMerges, merges...)
	}
	if len(allMerges) > 0 {
		rec.Changed = true
		sort.Strings(allMerges)
		rec.Notes = append(rec.Notes, fmt.Sprintf("merged %d duplicate operation(s): %s",
			len(allMerges), strings.Join(allMerges, ", ")))
	}
	return rec
}

// commonSubexprOnce performs a single detect-and-rewrite iteration and returns
// the "removed -> canonical" descriptions of any merges it made.
func commonSubexprOnce(m *ir.Module) []string {
	firstByFP := map[string]string{} // fingerprint -> canonical op id
	rename := map[string]string{}    // removed op id -> canonical op id

	for _, op := range m.Operations {
		if !mergeable(op) {
			continue
		}
		fp := fingerprint(op)
		if canon, ok := firstByFP[fp]; ok {
			rename[op.ID] = canon
		} else {
			firstByFP[fp] = op.ID
		}
	}
	if len(rename) == 0 {
		return nil
	}

	// Rewrite references across the module.
	kept := m.Operations[:0]
	for _, op := range m.Operations {
		if _, removed := rename[op.ID]; removed {
			continue
		}
		rewriteRefs(op, rename)
		kept = append(kept, op)
	}
	m.Operations = kept
	for i, out := range m.Outputs {
		if canon, ok := rename[out]; ok {
			m.Outputs[i] = canon
		}
	}

	merges := make([]string, 0, len(rename))
	for id, canon := range rename {
		merges = append(merges, fmt.Sprintf("%s -> %s", id, canon))
	}
	return merges
}

// mergeable reports whether an op may be deduplicated without changing results.
func mergeable(op *ir.Operation) bool {
	return op.Deterministic && op.Cacheable && !op.SideEffects
}

// fingerprint is a content hash of everything that makes an op's result unique:
// its operator type, backend, canonical params, and sorted input sources.
func fingerprint(op *ir.Operation) string {
	h := sha256.New()
	fmt.Fprintf(h, "type=%s\x00backend=%s\x00", op.Type, op.Backend)
	// canonical params via JSON (json.Marshal sorts map keys)
	if pb, err := json.Marshal(op.Params); err == nil {
		h.Write(pb)
	}
	srcs := make([]string, 0, len(op.Bindings))
	for _, b := range op.Bindings {
		srcs = append(srcs, b.Port+"="+b.Source)
	}
	sort.Strings(srcs)
	h.Write([]byte("\x00" + strings.Join(srcs, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

func rewriteRefs(op *ir.Operation, rename map[string]string) {
	for i, dep := range op.DependsOn {
		if canon, ok := rename[dep]; ok {
			op.DependsOn[i] = canon
		}
	}
	for i := range op.Bindings {
		op.Bindings[i].Source = rewriteSource(op.Bindings[i].Source, rename)
	}
	// de-dup depends_on after rewrite
	seen := map[string]bool{}
	deduped := op.DependsOn[:0]
	for _, d := range op.DependsOn {
		if d == op.ID || seen[d] {
			continue
		}
		seen[d] = true
		deduped = append(deduped, d)
	}
	op.DependsOn = deduped
}

func rewriteSource(src string, rename map[string]string) string {
	if src == "" || src[0] == '$' {
		return src
	}
	opID, rest := src, ""
	if i := strings.IndexByte(src, '.'); i >= 0 {
		opID, rest = src[:i], src[i:]
	}
	if canon, ok := rename[opID]; ok {
		return canon + rest
	}
	return src
}

// deadNodeElimination removes operations whose results cannot reach any declared
// output, unless they have side effects (which must still run).
func deadNodeElimination(m *ir.Module) (ir.PassRecord, error) {
	rec := ir.PassRecord{Name: "dead-node-elimination"}
	if len(m.Outputs) == 0 {
		rec.Notes = append(rec.Notes, "no declared outputs; skipped")
		return rec, nil
	}
	g, err := graph.Build(m)
	if err != nil {
		return rec, err
	}
	live := map[string]bool{}
	for _, out := range m.Outputs {
		live[out] = true
		for a := range g.Ancestors(out) {
			live[a] = true
		}
	}
	var removed []string
	kept := m.Operations[:0]
	for _, op := range m.Operations {
		if live[op.ID] || op.SideEffects {
			kept = append(kept, op)
		} else {
			removed = append(removed, op.ID)
		}
	}
	m.Operations = kept
	if len(removed) > 0 {
		rec.Changed = true
		sort.Strings(removed)
		rec.Notes = append(rec.Notes, "removed unreachable operation(s): "+strings.Join(removed, ", "))
	}
	return rec, nil
}

// parallelismAnalysis annotates (via notes) the dependency-level structure that
// the runtime scheduler exploits. It does not rewrite operations.
func parallelismAnalysis(m *ir.Module) (ir.PassRecord, error) {
	rec := ir.PassRecord{Name: "parallelism-analysis"}
	g, err := graph.Build(m)
	if err != nil {
		return rec, err
	}
	levels, err := g.Levels()
	if err != nil {
		return rec, err
	}
	maxWidth := 0
	for _, lvl := range levels {
		if len(lvl) > maxWidth {
			maxWidth = len(lvl)
		}
	}
	rec.Notes = append(rec.Notes,
		fmt.Sprintf("critical path depth=%d, max parallel width=%d across %d operation(s)",
			len(levels), maxWidth, len(m.Operations)))
	return rec, nil
}
