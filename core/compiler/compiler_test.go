package compiler

import (
	"strings"
	"testing"

	"neuroforge/core/ir"
	"neuroforge/core/optimizer"
)

const validWorkload = `
name: warehouse-analysis
inputs:
  - name: video
    type: video
operations:
  - id: frames
    type: vision.frame_extract
    backend: local
    inputs: { video: $video }
  - id: segment
    type: vision.segmentation
    backend: meta.sam
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

func TestCompileValid(t *testing.T) {
	c, err := CompileBytes([]byte(validWorkload), DefaultOptions())
	if err != nil {
		t.Fatalf("expected valid workload to compile, got: %v", err)
	}
	if c.Optimized == nil {
		t.Fatal("expected optimized module")
	}
	if got := len(c.Base.Operations); got != 4 {
		t.Fatalf("expected 4 operations, got %d", got)
	}
	// segment must have inferred its typed signature from the registry.
	seg := c.Base.Op("segment")
	if seg == nil || len(seg.Outputs) == 0 || seg.Outputs[0].Type != ir.TypeObjectList {
		t.Fatalf("segment signature not inferred correctly: %+v", seg)
	}
}

func TestCycleDetection(t *testing.T) {
	// a depends on b, b depends on a via bindings.
	wl := `
name: cyclic
operations:
  - id: a
    type: output.structured
    backend: local
    inputs: { data: b.report }
  - id: b
    type: output.structured
    backend: local
    inputs: { data: a.report }
outputs: [a]
`
	_, err := CompileBytes([]byte(wl), DefaultOptions())
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

func TestTypeMismatch(t *testing.T) {
	// feed a video directly into segmentation's image port.
	wl := `
name: mismatch
inputs:
  - name: clip
    type: video
operations:
  - id: segment
    type: vision.segmentation
    backend: meta.sam
    inputs: { image: $clip }
outputs: [segment]
`
	_, err := CompileBytes([]byte(wl), DefaultOptions())
	if err == nil {
		t.Fatal("expected type mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "type mismatch") {
		t.Fatalf("expected type mismatch, got: %v", err)
	}
}

func TestUnknownOperator(t *testing.T) {
	wl := `
name: unknown
operations:
  - id: x
    type: does.not.exist
    backend: mock
outputs: [x]
`
	_, err := CompileBytes([]byte(wl), DefaultOptions())
	if err == nil || !strings.Contains(err.Error(), "unknown operator") {
		t.Fatalf("expected unknown operator error, got: %v", err)
	}
}

func TestUnboundInput(t *testing.T) {
	wl := `
name: unbound
inputs:
  - name: video
    type: video
operations:
  - id: frames
    type: vision.frame_extract
    backend: local
outputs: [frames]
`
	_, err := CompileBytes([]byte(wl), DefaultOptions())
	if err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("expected unbound input error, got: %v", err)
	}
}

func TestBackendMismatch(t *testing.T) {
	// segmentation routed to a reasoning-only backend.
	wl := `
name: badbackend
inputs:
  - name: img
    type: image
operations:
  - id: seg
    type: vision.segmentation
    backend: meta.muse_spark
    inputs: { image: $img }
outputs: [seg]
`
	_, err := CompileBytes([]byte(wl), DefaultOptions())
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("expected backend mismatch error, got: %v", err)
	}
}

func TestDeadNodeElimination(t *testing.T) {
	// 'orphan' feeds nothing that reaches an output and has no side effects.
	wl := `
name: dead
inputs:
  - name: video
    type: video
operations:
  - id: frames
    type: vision.frame_extract
    backend: local
    inputs: { video: $video }
  - id: report
    type: output.structured
    backend: local
    inputs: { data: frames.frames }
  - id: orphan
    type: vision.frame_extract
    backend: local
    inputs: { video: $video }
outputs: [report]
`
	// Note: output.structured expects json on 'data' but frames produces image;
	// relax by routing through features would be needed in real life. Here we
	// only assert dead-node behavior on a type-valid variant.
	_ = wl

	// type-valid dead-node case
	wl2 := `
name: dead2
inputs:
  - name: video
    type: video
operations:
  - id: frames
    type: vision.frame_extract
    backend: local
    inputs: { video: $video }
  - id: features
    type: vision.features
    backend: local
    inputs: { objects: seg.objects }
  - id: seg
    type: vision.segmentation
    backend: meta.sam
    inputs: { image: frames.frames }
  - id: report
    type: output.structured
    backend: local
    inputs: { data: features.features }
  - id: orphan
    type: vision.features
    backend: local
    inputs: { objects: seg.objects }
outputs: [report]
`
	c, err := CompileBytes([]byte(wl2), DefaultOptions())
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if c.Optimized.Op("orphan") != nil {
		t.Fatal("expected orphan to be eliminated")
	}
	if c.Base.Op("orphan") == nil {
		t.Fatal("base module should retain orphan")
	}
}

func TestCommonSubexpressionMerge(t *testing.T) {
	// Two identical, deterministic+cacheable segmentations should merge.
	wl := `
name: cse
inputs:
  - name: video
    type: video
operations:
  - id: frames
    type: vision.frame_extract
    backend: local
    inputs: { video: $video }
  - id: segA
    type: vision.segmentation
    backend: meta.sam
    inputs: { image: frames.frames }
  - id: segB
    type: vision.segmentation
    backend: meta.sam
    inputs: { image: frames.frames }
  - id: fA
    type: vision.features
    backend: local
    inputs: { objects: segA.objects }
  - id: fB
    type: vision.features
    backend: local
    inputs: { objects: segB.objects }
  - id: report
    type: output.structured
    backend: local
    inputs: { data: fA.features }
outputs: [report]
`
	c, err := CompileBytes([]byte(wl), DefaultOptions())
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	// segA and segB are identical; one should be merged away. fB referenced
	// segB and should be dead-node eliminated (fA is the only one reaching output).
	nseg := 0
	for _, op := range c.Optimized.Operations {
		if op.Type == "vision.segmentation" {
			nseg++
		}
	}
	if nseg != 1 {
		t.Fatalf("expected 1 segmentation after CSE, got %d", nseg)
	}
}

func TestSerializationRoundTrip(t *testing.T) {
	c, err := CompileBytes([]byte(validWorkload), DefaultOptions())
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	data, err := ir.Marshal(c.Optimized)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := ir.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Name != c.Optimized.Name || len(back.Operations) != len(c.Optimized.Operations) {
		t.Fatal("round-trip mismatch")
	}
}

func TestOptimizePreservesWhenDisabled(t *testing.T) {
	opts := Options{Optimize: true, OptimizerOf: optimizer.Options{}}
	c, err := CompileBytes([]byte(validWorkload), opts)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(c.Optimized.Operations) != len(c.Base.Operations) {
		t.Fatal("no-op optimizer changed operation count")
	}
}
