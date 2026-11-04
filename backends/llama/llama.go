// Package llama implements a fully local cognitive backend for NEUROFORGE X that
// runs Meta's own Llama models on the developer's machine via Ollama. It
// requires no cloud credentials and no Meta API key: inference happens locally.
//
// Ollama exposes an OpenAI-compatible chat API at {OLLAMA_HOST}/v1, so the same
// structured-JSON planning and reasoning the Meta Model API path uses runs here
// unchanged, against a real Meta Llama model. When Ollama is not running or the
// model is not pulled, the backend reports Configured()=false and the runtime's
// Fallback routes to the clearly-labeled mock instead.
//
// Configuration (all optional):
//
//	OLLAMA_HOST   base host for Ollama      (default http://localhost:11434)
//	OLLAMA_MODEL  Llama model tag to use    (default llama3.2:3b)
package llama

import (
	"bytes"
	"context"
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

// Backend adapts a local Ollama-hosted Llama model to the NEUROFORGE X backend
// interface for the cognition.plan and cognition.reasoning operators.
type Backend struct {
	host  string
	model string
	http  *http.Client
}

// New builds a local Llama backend from the environment.
func New() *Backend {
	host := strings.TrimRight(os.Getenv("OLLAMA_HOST"), "/")
	if host == "" {
		host = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "llama3.2:3b"
	}
	timeout := 300 * time.Second
	if v := os.Getenv("OLLAMA_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	return &Backend{
		host:  host,
		model: model,
		http:  &http.Client{Timeout: timeout},
	}
}

func (b *Backend) Name() string { return "local.llama" }

func (b *Backend) Capabilities() []backends.Capability {
	return []backends.Capability{
		{OperatorType: "cognition.plan"},
		{OperatorType: "cognition.reasoning"},
	}
}

// Configured reports whether a local Ollama server is reachable and the
// configured Llama model is actually pulled. This is what lets the Fallback
// decide between real local inference and the mock, with no credentials.
func (b *Backend) Configured() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return b.modelPresent(ctx) == nil
}

// Model returns the configured Llama model tag, for diagnostics.
func (b *Backend) Model() string { return b.model }

// Host returns the configured Ollama host, for diagnostics.
func (b *Backend) Host() string { return b.host }

func (b *Backend) Validate(_ context.Context, op *ir.Operation) error {
	switch op.Type {
	case "cognition.plan", "cognition.reasoning":
		return nil
	default:
		return fmt.Errorf("local.llama: unsupported operator %q", op.Type)
	}
}

func (b *Backend) Execute(ctx context.Context, op *ir.Operation, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	switch op.Type {
	case "cognition.plan":
		return b.plan(ctx, inputs)
	case "cognition.reasoning":
		return b.reason(ctx, inputs)
	default:
		return nil, fmt.Errorf("local.llama: unsupported operator %q", op.Type)
	}
}

const planSystem = `You are a multimodal workload planner for an AI compiler.
Given a natural-language instruction, produce a STRICT JSON object with this shape:
{"tasks":[{"id":"string","operation":"string","why":"string"}],"outputs":["string"]}
Use only these operation types: vision.frame_extract, vision.segmentation, vision.features, cognition.reasoning, image.generate, output.structured.
Return ONLY the JSON object.`

func (b *Backend) plan(ctx context.Context, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	instruction := asText(inputs["instruction"])
	var plan map[string]any
	msgs := []chatMessage{
		{Role: "system", Content: planSystem},
		{Role: "user", Content: instruction},
	}
	if err := b.chatJSON(ctx, msgs, &plan); err != nil {
		return nil, err
	}
	return map[string]backends.Value{
		"plan": {Type: ir.TypeJSON, Data: plan, Meta: b.meta()},
	}, nil
}

const reasonSystem = `You are a reasoning model interpreting measured observations from a vision pipeline.
Return a STRICT JSON object: {"interpretation":"string","findings":[{"claim":"string","confidence":number,"basis":"string"}],"caveats":["string"]}
Distinguish measured observations from inferences. Do not assert a safety violation from object proximity alone. Return ONLY the JSON object.`

func (b *Backend) reason(ctx context.Context, inputs map[string]backends.Value) (map[string]backends.Value, error) {
	obs := asJSONString(inputs["observations"])
	var result map[string]any
	msgs := []chatMessage{
		{Role: "system", Content: reasonSystem},
		{Role: "user", Content: "Observations:\n" + obs},
	}
	if err := b.chatJSON(ctx, msgs, &result); err != nil {
		return nil, err
	}
	return map[string]backends.Value{
		"result": {Type: ir.TypeJSON, Data: result, Meta: b.meta()},
	}, nil
}

func (b *Backend) meta() map[string]any {
	return map[string]any{
		"backend":   "local.llama",
		"model":     b.model,
		"host":      b.host,
		"inference": true,
		"local":     true,
	}
}

// --- Ollama OpenAI-compatible chat client ---

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	Stream         bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// chatJSON calls Ollama's OpenAI-compatible /v1/chat/completions in JSON mode and
// unmarshals the assistant message into out.
func (b *Backend) chatJSON(ctx context.Context, msgs []chatMessage, out any) error {
	reqBody := chatRequest{
		Model:          b.model,
		Messages:       msgs,
		Temperature:    0,
		Stream:         false,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("local.llama: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.host+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("local.llama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Ollama ignores the key, but the OpenAI-compatible path expects the header.
	req.Header.Set("Authorization", "Bearer ollama")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("local.llama: Ollama not reachable at %s (is `ollama serve` running?): %w", b.host, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("local.llama: ollama http %d: %s", resp.StatusCode, truncate(string(data), 400))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return fmt.Errorf("local.llama: decode response: %w", err)
	}
	if cr.Error != nil {
		return fmt.Errorf("local.llama: ollama error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return fmt.Errorf("local.llama: empty choices from model %q", b.model)
	}
	content := strings.TrimSpace(cr.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("local.llama: model response was not valid JSON: %w (got: %s)", err, truncate(content, 300))
	}
	return nil
}

// modelPresent checks that Ollama is up and the configured model is pulled.
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
	return fmt.Errorf("ollama model %q not pulled (run: ollama pull %s)", b.model, b.model)
}

// --- input coercion helpers ---

func asText(v backends.Value) string {
	if s, ok := v.Data.(string); ok {
		return s
	}
	if v.Ref != "" {
		return v.Ref
	}
	return asJSONString(v)
}

func asJSONString(v backends.Value) string {
	if s, ok := v.Data.(string); ok {
		return s
	}
	bts, err := json.Marshal(v.Data)
	if err != nil {
		return fmt.Sprintf("%v", v.Data)
	}
	return string(bts)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
