package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// ChatMessage is an OpenAI-compatible chat message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"` // "json_object" for structured output
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

// Chat calls the OpenAI-compatible /chat/completions endpoint and returns the
// assistant message content. When jsonMode is true it requests structured JSON
// output (response_format=json_object), which the Muse Spark adapters rely on.
func (g *Gateway) Chat(ctx context.Context, model string, messages []ChatMessage, temperature float64, jsonMode bool) (string, error) {
	req := chatRequest{Model: model, Messages: messages, Temperature: temperature}
	if jsonMode {
		req.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	var resp chatResponse
	if err := g.PostJSON(ctx, "chat/completions", req, &resp); err != nil {
		return "", err
	}
	g.AccountTokens(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("meta chat: empty choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// ChatStructured calls Chat in JSON mode and unmarshals the response into out.
func (g *Gateway) ChatStructured(ctx context.Context, model string, messages []ChatMessage, out any) error {
	content, err := g.Chat(ctx, model, messages, 0, true)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("meta chat: response was not valid JSON: %w", err)
	}
	return nil
}
