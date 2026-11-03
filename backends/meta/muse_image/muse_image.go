// Package muse_image implements the Meta Muse Image generation backend. Like
// SAM, the image-generation endpoint path is configuration-driven
// (MUSE_IMAGE_API_PATH) and must be confirmed against the Meta developer portal.
// The adapter posts a prompt and normalizes the response into an MIR image value
// (a URL or base64 reference). Without credentials AND a configured path the
// runtime's Fallback routes image.generate to the mock.
package muse_image

import (
	"context"
	"fmt"
	"os"

	"neuroforge/backends"
	"neuroforge/backends/meta/client"
	"neuroforge/core/ir"
)

// Backend adapts Muse Image to the NEUROFORGE X backend interface.
type Backend struct {
	gw    *client.Gateway
	path  string
	model string
}

// New builds a Muse Image backend from a gateway.
func New(gw *client.Gateway) *Backend {
	model := os.Getenv("MUSE_IMAGE_MODEL")
	if model == "" {
		model = "muse-image"
	}
	return &Backend{gw: gw, path: os.Getenv("MUSE_IMAGE_API_PATH"), model: model}
}

func (b *Backend) Name() string { return "meta.muse_image" }

func (b *Backend) Configured() bool { return b.gw.Configured() && b.path != "" }

func (b *Backend) Capabilities() []backends.Capability {
	return []backends.Capability{{OperatorType: "image.generate"}}
}

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	if op.Type != "image.generate" {
		return fmt.Errorf("muse_image: unsupported operator %q", op.Type)
	}
	return nil
}

type genRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type genResponse struct {
	Data []struct {
		URL     string `json:"url"`
		B64JSON string `json:"b64_json"`
	} `json:"data"`
}

func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	prompt := ""
	if s, ok := inputs["prompt"].Data.(string); ok {
		prompt = s
	} else if inputs["prompt"].Ref != "" {
		prompt = inputs["prompt"].Ref
	}
	req := genRequest{Model: b.model, Prompt: prompt}
	var resp genResponse
	if err := b.gw.PostJSON(ctx, b.path, req, &resp); err != nil {
		return nil, err
	}
	ref := ""
	if len(resp.Data) > 0 {
		ref = resp.Data[0].URL
	}
	return map[string]backends.Value{
		"image": {Type: ir.TypeImage, Ref: ref, Meta: map[string]any{"backend": "meta.muse_image", "model": b.model, "inference": true}},
	}, nil
}
