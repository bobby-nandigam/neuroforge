package cache

import (
	"testing"

	"neuroforge/backends"
	"neuroforge/core/ir"
)

func mkOp(id string, params map[string]any) *ir.Operation {
	return &ir.Operation{ID: id, Type: "vision.segmentation", Backend: "meta.sam", Params: params,
		Bindings: []ir.Binding{{Port: "image", Source: "$img"}}}
}

func TestKeyStability(t *testing.T) {
	op := mkOp("a", map[string]any{"threshold": 0.5})
	in := map[string]backends.Value{"image": {Type: ir.TypeImage, Ref: "x.png"}}
	k1 := Key(op, in)
	k2 := Key(op, in)
	if k1 != k2 {
		t.Fatal("key should be stable for identical op+inputs")
	}
	// different input -> different key
	in2 := map[string]backends.Value{"image": {Type: ir.TypeImage, Ref: "y.png"}}
	if Key(op, in2) == k1 {
		t.Fatal("different inputs should produce different keys")
	}
}

func TestLRUEviction(t *testing.T) {
	c := New(2)
	put := func(k string) { c.Put(k, map[string]backends.Value{"o": {Ref: k}}) }
	put("a")
	put("b")
	_, _ = c.Get("a") // touch a -> b is now LRU
	put("c")          // evicts b
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should still be present")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should be present")
	}
	st := c.Stats()
	if st.Evicts != 1 {
		t.Fatalf("expected 1 eviction, got %d", st.Evicts)
	}
}

func TestHitRate(t *testing.T) {
	c := New(10)
	c.Put("k", map[string]backends.Value{"o": {Ref: "v"}})
	c.Get("k") // hit
	c.Get("k") // hit
	c.Get("z") // miss
	st := c.Stats()
	if st.Hits != 2 || st.Misses != 1 {
		t.Fatalf("expected 2 hits/1 miss, got %d/%d", st.Hits, st.Misses)
	}
	if st.HitRate < 0.66 || st.HitRate > 0.67 {
		t.Fatalf("unexpected hit rate %.3f", st.HitRate)
	}
}
