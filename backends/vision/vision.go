// Package vision implements a fully local visual-perception backend for
// NEUROFORGE X that runs Meta's own Llama 3.2 Vision model on real image frames
// via Ollama. It requires no cloud credentials and no PyTorch/GPU stack: the
// model reads an actual frame and returns the objects it observes.
//
// This is genuine multimodal inference on real pixels — not the mock. It is,
// however, a vision-language model performing open-vocabulary detection, not
// SAM: bounding boxes are the model's estimates, and there is no pixel-accurate
// segmentation mask. Outputs are labeled accordingly so nothing is mistaken for
// SAM-grade masks.
//
// Configuration (all optional):
//
//	OLLAMA_HOST          base host for Ollama   (default http://localhost:11434)
//	OLLAMA_VISION_MODEL  vision model tag       (default llama3.2-vision)
package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"neuroforge/backends"
	"neuroforge/core/ir"
)

// Backend adapts a local Ollama-hosted Llama Vision model to the NEUROFORGE X
// backend interface for the vision.segmentation operator.
type Backend struct {
	host  string
	model string
	http  *http.Client
}

// New builds a local vision backend from the environment.
func New() *Backend {
	host := strings.TrimRight(os.Getenv("OLLAMA_HOST"), "/")
	if host == "" {
		host = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_VISION_MODEL")
	if model == "" {
		model = "llama3.2-vision"
	}
	// Vision models are large; the first (cold) load can take minutes. Allow a
	// generous, configurable timeout so a cold start does not fail the run.
	timeout := 600 * time.Second
	if v := os.Getenv("OLLAMA_VISION_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	return &Backend{host: host, model: model, http: &http.Client{Timeout: timeout}}
}

func (b *Backend) Name() string { return "local.vision" }

func (b *Backend) Model() string { return b.model }

func (b *Backend) Capabilities() []backends.Capability {
	return []backends.Capability{{OperatorType: "vision.segmentation"}}
}

// Configured reports whether Ollama is reachable and the vision model is pulled.
func (b *Backend) Configured() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return b.modelPresent(ctx) == nil
}

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	if op.Type != "vision.segmentation" {
		return fmt.Errorf("local.vision: unsupported operator %q", op.Type)
	}
	return nil
}

type detection struct {
	Label      string  `json:"label"`
	Count      int     `json:"count"`
	Confidence float64 `json:"confidence"`
}

func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	img := inputs["image"]
	path := img.Ref
	if path == "" {
		if s, ok := img.Data.(string); ok {
			path = s
		}
	}
	if path == "" {
		return nil, fmt.Errorf("local.vision: no image path on input (expected a frame file ref)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("local.vision: read frame %q: %w", path, err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)

	var parsed struct {
		Objects []detection `json:"objects"`
		Scene   string      `json:"scene"`
	}
	if err := b.detect(ctx, b64, &parsed); err != nil {
		return nil, err
	}

	// Expand each detected object TYPE into that many instances for the local
	// vision.features operator (which tallies per-instance). Boxes are left
	// empty on purpose: a vision-language model does not localize reliably, so
	// we report what it does know (which objects are present and how many)
	// rather than fabricate pixel coordinates. Counts are capped so a wild
	// estimate cannot explode the graph.
	const perTypeCap, totalCap = 30, 150
	objects := make([]map[string]any, 0)
	detectedTypes := make([]map[string]any, 0, len(parsed.Objects))
	for _, d := range parsed.Objects {
		if d.Label == "" {
			continue
		}
		n := d.Count
		if n <= 0 {
			n = 1
		}
		detectedTypes = append(detectedTypes, map[string]any{"label": d.Label, "count": n, "confidence": d.Confidence})
		if n > perTypeCap {
			n = perTypeCap
		}
		for i := 0; i < n && len(objects) < totalCap; i++ {
			objects = append(objects, map[string]any{
				"label":      d.Label,
				"confidence": d.Confidence,
				"bbox":       []int{}, // no reliable localization from a VLM
			})
		}
	}

	meta := map[string]any{
		"backend":        "local.vision",
		"model":          b.model,
		"local":          true,
		"inference":      true,
		"detection":      "vision-language model (open-vocabulary); real inference on actual pixels",
		"localization":   "none — counts/labels are real detections; pixel boxes/masks require SAM",
		"source":         path,
		"scene":          parsed.Scene,
		"detected_types": detectedTypes,
		"num_instances":  len(objects),
	}

	return map[string]backends.Value{
		"objects": {Type: ir.TypeObjectList, Data: objects, Meta: meta},
		"mask": {Type: ir.TypeMask, Ref: path, Data: map[string]any{
			"kind": "none",
			"note": "vision-language detection provides no pixel mask (SAM required for masks)",
		}, Meta: meta},
	}, nil
}

const visionPrompt = `You are a visual perception module analyzing a single video frame.
Report the distinct TYPES of physical objects you can see. Return STRICT JSON only, exactly this shape:
{"objects":[{"label":"string","count":0,"confidence":0.0}],"scene":"one short sentence describing the scene"}
- label: a short noun for the object type (e.g. "cardboard box", "pallet", "forklift", "shelving rack", "person").
- count: how many of that object are visible (your best estimate).
- confidence: your confidence from 0 to 1.
List up to 8 distinct object TYPES. Do not repeat a type and do not list more than 8. Return ONLY the JSON object, no prose.`

// detect runs the vision model on a base64 JPEG via Ollama's native generate
// endpoint (/api/generate with an images array) in JSON mode. This is the path
// Ollama supports reliably for vision models; the OpenAI-compatible chat path
// with image_url data URIs is not dependable for multimodal input.
func (b *Backend) detect(ctx context.Context, b64 string, out any) error {
	type request struct {
		Model   string         `json:"model"`
		Prompt  string         `json:"prompt"`
		Images  []string       `json:"images"`
		Stream  bool           `json:"stream"`
		Format  string         `json:"format"`
		Options map[string]any `json:"options,omitempty"`
	}
	reqBody := request{
		Model:   b.model,
		Prompt:  visionPrompt,
		Images:  []string{b64},
		Stream:  false,
		Format:  "json",
		Options: map[string]any{"temperature": 0, "num_predict": 700},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("local.vision: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.host+"/api/generate", bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("local.vision: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("local.vision: Ollama not reachable at %s: %w", b.host, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("local.vision: ollama http %d: %s", resp.StatusCode, truncate(string(data), 400))
	}
	var gr struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(data, &gr); err != nil {
		return fmt.Errorf("local.vision: decode response: %w", err)
	}
	if gr.Error != "" {
		return fmt.Errorf("local.vision: ollama error: %s", gr.Error)
	}
	text := strings.TrimSpace(gr.Response)
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("local.vision: model response was not valid JSON: %w (got: %s)", err, truncate(text, 300))
	}
	return nil
}

func (b *Backend) modelPresent(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.host+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama /api/tags: http %d", resp.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return err
	}
	for _, m := range tags.Models {
		if m.Name == b.model || m.Model == b.model || strings.HasPrefix(m.Name, b.model+":") {
			return nil
		}
	}
	return fmt.Errorf("ollama vision model %q not pulled (run: ollama pull %s)", b.model, b.model)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
