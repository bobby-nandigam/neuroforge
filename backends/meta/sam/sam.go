// Package sam implements the Meta SAM 3.1 visual-perception backend. SAM is not
// an OpenAI-compatible chat model, so its request path and payload shape are
// configuration-driven (SAM_API_PATH) and must be confirmed against the Meta
// developer portal before use. This adapter posts an image reference plus any
// text prompts and normalizes the response into MIR object_list and mask values.
//
// Because the exact SAM payload is not publicly documented here, the backend
// reports Configured()=true only when BOTH an API key and SAM_API_PATH are set;
// otherwise the runtime's Fallback routes vision.segmentation to the mock.
package sam

import (
	"context"
	"fmt"
	"os"

	"neuroforge/backends"
	"neuroforge/backends/meta/client"
	"neuroforge/core/ir"
)

// Backend adapts SAM 3.1 to the NEUROFORGE X backend interface.
type Backend struct {
	gw   *client.Gateway
	path string // configurable endpoint path, e.g. "sam/segment"
}

// New builds a SAM backend from a gateway.
func New(gw *client.Gateway) *Backend {
	return &Backend{gw: gw, path: os.Getenv("SAM_API_PATH")}
}

func (b *Backend) Name() string { return "meta.sam" }

// Configured requires both credentials and an explicitly configured endpoint,
// so we never POST to an invented path.
func (b *Backend) Configured() bool { return b.gw.Configured() && b.path != "" }

func (b *Backend) Capabilities() []backends.Capability {
	return []backends.Capability{{OperatorType: "vision.segmentation"}}
}

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	if op.Type != "vision.segmentation" {
		return fmt.Errorf("sam: unsupported operator %q", op.Type)
	}
	return nil
}

// segRequest is the request body posted to the configured SAM endpoint. The
// field names follow common segmentation API conventions; confirm/adjust to the
// official schema via SAM_API_PATH and this struct.
type segRequest struct {
	Image   string   `json:"image"`             // image URL or reference
	Prompts []string `json:"prompts,omitempty"` // optional text prompts
}

// segResponse is the expected response shape. Unknown fields are ignored.
type segResponse struct {
	Objects []struct {
		Label      string  `json:"label"`
		Confidence float64 `json:"confidence"`
		BBox       []int   `json:"bbox"`
	} `json:"objects"`
	Masks []any `json:"masks"`
}

func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	img := inputs["image"]
	ref := img.Ref
	if ref == "" {
		if s, ok := img.Data.(string); ok {
			ref = s
		}
	}
	req := segRequest{Image: ref}
	if ps, ok := op.Params["prompts"].([]string); ok {
		req.Prompts = ps
	}
	var resp segResponse
	if err := b.gw.PostJSON(ctx, b.path, req, &resp); err != nil {
		return nil, err
	}
	objs := make([]map[string]any, 0, len(resp.Objects))
	for i, o := range resp.Objects {
		objs = append(objs, map[string]any{
			"id": i, "label": o.Label, "confidence": o.Confidence, "bbox": o.BBox,
		})
	}
	m := map[string]any{"backend": "meta.sam", "observation": true}
	return map[string]backends.Value{
		"objects": {Type: ir.TypeObjectList, Data: objs, Meta: m},
		"mask":    {Type: ir.TypeMask, Data: map[string]any{"count": len(resp.Masks)}, Meta: m},
	}, nil
}
