// Package local implements operators the NEUROFORGE X runtime executes itself,
// in-process, with no external model API. These are real deterministic
// computations over structured data (not synthetic), which is what lets the
// pipeline distinguish measured observations from model inferences.
package local

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"neuroforge/backends"
	"neuroforge/core/ir"
)

// Backend executes local operators: frame extraction (metadata), spatial
// feature extraction, and structured report assembly.
type Backend struct {
	// ProximityPx is the center-distance threshold (pixels) under which two
	// detected objects are counted as a proximity pair in vision.features.
	ProximityPx float64
}

// New returns a local backend with sensible defaults.
func New() *Backend { return &Backend{ProximityPx: 150} }

func (b *Backend) Name() string { return "local" }

func (b *Backend) Configured() bool { return true }

var supported = map[string]bool{
	"vision.frame_extract": true,
	"vision.features":      true,
	"output.structured":    true,
}

func (b *Backend) Capabilities() []backends.Capability {
	var caps []backends.Capability
	for t := range supported {
		caps = append(caps, backends.Capability{OperatorType: t})
	}
	return caps
}

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	if !supported[op.Type] {
		return fmt.Errorf("local: operator %q is not implemented by the local backend", op.Type)
	}
	return nil
}

func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch op.Type {
	case "vision.frame_extract":
		return b.frameExtract(ctx, op, inputs)
	case "vision.features":
		return b.features(op, inputs)
	case "output.structured":
		return b.structured(op, inputs)
	default:
		return nil, fmt.Errorf("local: operator %q not implemented", op.Type)
	}
}

// frameExtract decodes real frames from a video input using ffmpeg when a
// readable video file is present at the input ref, returning the on-disk frame
// paths plus a representative key frame (the middle one) for downstream vision.
// When no decodable file exists (e.g. only a placeholder ref), it degrades to
// deterministic sampling metadata so the graph still type-checks and runs —
// clearly labeled so a metadata-only run is never mistaken for real decoding.
func (b *Backend) frameExtract(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	v := inputs["video"]
	fps := intParam(op.Params, "fps", 1)
	videoPath := v.Ref
	if videoPath == "" {
		if s, ok := v.Data.(string); ok {
			videoPath = s
		}
	}

	if videoPath != "" {
		if st, err := os.Stat(videoPath); err == nil && !st.IsDir() {
			if _, ffErr := exec.LookPath("ffmpeg"); ffErr == nil {
				frames, dir, err := extractFrames(ctx, videoPath, fps)
				if err != nil {
					return nil, fmt.Errorf("local: ffmpeg frame extraction failed: %w", err)
				}
				if len(frames) > 0 {
					key := frames[len(frames)/2] // representative middle frame
					return map[string]backends.Value{
						"frames": {
							Type: ir.TypeImage,
							Ref:  key,
							Data: map[string]any{
								"source":         videoPath,
								"dir":            dir,
								"sample_fps":     fps,
								"frame_count":    len(frames),
								"frames":         frames,
								"representative": key,
								"note":           "real frames decoded from the video via ffmpeg",
							},
							Meta: map[string]any{"backend": "local", "measured": true, "decoded": true},
						},
					}, nil
				}
			}
		}
	}

	// Fallback: no decodable video file at the ref.
	return map[string]backends.Value{
		"frames": {
			Type: ir.TypeImage,
			Ref:  v.Ref,
			Data: map[string]any{"source": v.Ref, "sample_fps": fps, "note": "no decodable video file at ref; provide a real file to decode frames"},
			Meta: map[string]any{"backend": "local", "measured": true, "decoded": false},
		},
	}, nil
}

// extractFrames samples frames from videoPath at the given fps into a stable,
// content-addressed directory under ./captures, reusing an existing extraction
// when present. It caps the number of frames so a long clip stays bounded.
func extractFrames(ctx context.Context, videoPath string, fps int) ([]string, string, error) {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|fps=%d|v2", videoPath, fps)))
	dir := filepath.Join("captures", hex.EncodeToString(sum[:])[:12])
	if existing := listFrames(dir); len(existing) > 0 {
		return existing, dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, dir, err
	}
	pattern := filepath.Join(dir, "frame_%03d.jpg")
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=%d", fps),
		"-frames:v", "24", // bound the extraction
		"-q:v", "3",
		pattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, dir, fmt.Errorf("%v: %s", err, string(out))
	}
	return listFrames(dir), dir, nil
}

func listFrames(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "frame_*.jpg"))
	sort.Strings(matches)
	return matches
}

type detection struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	BBox       []int   `json:"bbox"` // x,y,w,h
}

// features computes genuine spatial statistics over a detection list: object and
// per-label counts, centroids, average confidence, and proximity pairs. Every
// field here is a measured observation, not an inference.
func (b *Backend) features(op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	var dets []detection
	if err := rejson(inputs["objects"].Data, &dets); err != nil {
		return nil, fmt.Errorf("local: vision.features: decode objects: %w", err)
	}
	type centroid struct{ x, y float64 }
	centroids := make([]centroid, 0, len(dets))
	perLabel := map[string]int{}
	var confSum float64
	for _, d := range dets {
		perLabel[d.Label]++
		confSum += d.Confidence
		if len(d.BBox) == 4 {
			centroids = append(centroids, centroid{
				x: float64(d.BBox[0]) + float64(d.BBox[2])/2,
				y: float64(d.BBox[1]) + float64(d.BBox[3])/2,
			})
		}
	}
	proximityPairs := 0
	minDist := math.Inf(1)
	for i := 0; i < len(centroids); i++ {
		for j := i + 1; j < len(centroids); j++ {
			dx := centroids[i].x - centroids[j].x
			dy := centroids[i].y - centroids[j].y
			dist := math.Hypot(dx, dy)
			if dist < minDist {
				minDist = dist
			}
			if dist < b.ProximityPx {
				proximityPairs++
			}
		}
	}
	if math.IsInf(minDist, 1) {
		minDist = -1
	}
	avgConf := 0.0
	if len(dets) > 0 {
		avgConf = confSum / float64(len(dets))
	}
	// stable per-label list
	labels := make([]string, 0, len(perLabel))
	for l := range perLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	labelCounts := make([]map[string]any, 0, len(labels))
	for _, l := range labels {
		labelCounts = append(labelCounts, map[string]any{"label": l, "count": perLabel[l]})
	}

	features := map[string]any{
		"kind":            "measured_observations",
		"object_count":    len(dets),
		"label_counts":    labelCounts,
		"avg_confidence":  round3(avgConf),
		"proximity_pairs": proximityPairs,
		"proximity_px":    b.ProximityPx,
		"min_pair_dist":   round3(minDist),
	}
	return map[string]backends.Value{
		"features": {Type: ir.TypeJSON, Data: features, Meta: map[string]any{"backend": "local", "measured": true}},
	}, nil
}

// structured assembles upstream JSON into a final report, explicitly separating
// measured observations from model-generated inferences.
func (b *Backend) structured(op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	data := inputs["data"].Data
	report := map[string]any{
		"title":   strParam(op.Params, "title", "NEUROFORGE X structured report"),
		"content": data,
		"provenance": map[string]any{
			"note": "Fields tagged measured=true are computed observations; model outputs are inferences and may be uncertain.",
		},
	}
	return map[string]backends.Value{
		"report": {Type: ir.TypeJSON, Data: report, Meta: map[string]any{"backend": "local", "measured": true}},
	}, nil
}

// --- helpers ---

func rejson(src any, dst any) error {
	bts, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(bts, dst)
}

func intParam(p map[string]any, key string, def int) int {
	if p == nil {
		return def
	}
	switch v := p[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

func strParam(p map[string]any, key, def string) string {
	if p == nil {
		return def
	}
	if s, ok := p[key].(string); ok && s != "" {
		return s
	}
	return def
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
