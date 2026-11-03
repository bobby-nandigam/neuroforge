// Package muse_spark implements the Meta Muse Spark cognitive backend: the
// compiler frontend's reasoning and planning model. It uses the OpenAI-compatible
// Meta Model API chat interface with JSON structured output.
//
// The model ID is read from MUSE_SPARK_MODEL (default "muse-spark"); confirm the
// exact identifier against the Meta developer portal. When no API key is present
// the backend reports Configured()=false and the runtime's Fallback routes to
// the mock instead.
package muse_spark

import (
	"context"
	"fmt"
	"os"

	"neuroforge/backends"
	"neuroforge/backends/meta/client"
	"neuroforge/core/ir"
)

// Backend adapts Muse Spark to the NEUROFORGE X backend interface.
type Backend struct {
	gw    *client.Gateway
	model string
}

// New builds a Muse Spark backend from a gateway.
func New(gw *client.Gateway) *Backend {
	model := os.Getenv("MUSE_SPARK_MODEL")
	if model == "" {
		model = "muse-spark"
	}
	return &Backend{gw: gw, model: model}
}

func (b *Backend) Name() string { return "meta.muse_spark" }

func (b *Backend) Configured() bool { return b.gw.Configured() }

func (b *Backend) Capabilities() []backends.Capability {
	return []backends.Capability{{OperatorType: "cognition.plan"}, {OperatorType: "cognition.reasoning"}}
}

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	switch op.Type {
	case "cognition.plan", "cognition.reasoning":
		return nil
	default:
		return fmt.Errorf("muse_spark: unsupported operator %q", op.Type)
	}
}

func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	switch op.Type {
	case "cognition.plan":
		return b.plan(ctx, op, inputs)
	case "cognition.reasoning":
		return b.reason(ctx, op, inputs)
	default:
		return nil, fmt.Errorf("muse_spark: unsupported operator %q", op.Type)
	}
}

const planSystem = `You are a multimodal workload planner for an AI compiler.
Given a natural-language instruction, produce a STRICT JSON object with this shape:
{"tasks":[{"id":"string","operation":"string","why":"string"}],"outputs":["string"]}
Use only these operation types: vision.frame_extract, vision.segmentation, vision.features, cognition.reasoning, image.generate, output.structured.
Return ONLY the JSON object.`

func (b *Backend) plan(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	instruction := asText(inputs["instruction"])
	var plan map[string]any
	msgs := []client.ChatMessage{
		{Role: "system", Content: planSystem},
		{Role: "user", Content: instruction},
	}
	if err := b.gw.ChatStructured(ctx, b.model, msgs, &plan); err != nil {
		return nil, err
	}
	return map[string]backends.Value{
		"plan": {Type: ir.TypeJSON, Data: plan, Meta: meta(b.model)},
	}, nil
}

const reasonSystem = `You are a reasoning model interpreting measured observations from a vision pipeline.
Return a STRICT JSON object: {"interpretation":"string","findings":[{"claim":"string","confidence":number,"basis":"string"}],"caveats":["string"]}
Distinguish measured observations from inferences. Do not assert a safety violation from object proximity alone. Return ONLY the JSON object.`

func (b *Backend) reason(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	obs := asJSONString(inputs["observations"])
	var result map[string]any
	msgs := []client.ChatMessage{
		{Role: "system", Content: reasonSystem},
		{Role: "user", Content: "Observations:\n" + obs},
	}
	if err := b.gw.ChatStructured(ctx, b.model, msgs, &result); err != nil {
		return nil, err
	}
	return map[string]backends.Value{
		"result": {Type: ir.TypeJSON, Data: result, Meta: meta(b.model)},
	}, nil
}

func meta(model string) map[string]any {
	return map[string]any{"backend": "meta.muse_spark", "model": model, "inference": true}
}
