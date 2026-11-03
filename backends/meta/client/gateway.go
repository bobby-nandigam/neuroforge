// Package client is the NEUROFORGE X Meta API gateway: a single, hardened HTTP
// client shared by every Meta model adapter. It centralizes authentication,
// timeouts, bounded retries, rate-limit (HTTP 429) handling, error
// normalization, request logging, and token/usage accounting.
//
// Credentials and endpoints are read from the environment so nothing is
// hardcoded. The Meta Model API is OpenAI-compatible, so the chat path targets
// the standard /chat/completions interface; model IDs and any non-chat paths
// are configurable and should be confirmed against the official Meta developer
// portal (dev.meta.ai). No endpoint shape is invented and presented as verified.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// Config holds gateway configuration, typically populated from the environment.
type Config struct {
	BaseURL    string        // META_API_BASE_URL (default https://api.meta.ai/v1)
	APIKey     string        // META_API_KEY (required for real calls)
	Timeout    time.Duration // per-request timeout
	MaxRetries int           // bounded retries for transient/429 errors
	Logger     *slog.Logger
}

// FromEnv builds a Config from environment variables with safe defaults.
func FromEnv() Config {
	base := os.Getenv("META_API_BASE_URL")
	if base == "" {
		base = "https://api.meta.ai/v1"
	}
	timeout := 60 * time.Second
	if v := os.Getenv("META_API_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	return Config{
		BaseURL:    base,
		APIKey:     os.Getenv("META_API_KEY"),
		Timeout:    timeout,
		MaxRetries: 3,
		Logger:     slog.Default(),
	}
}

// Usage accumulates token and request counters across the gateway's lifetime.
type Usage struct {
	mu               sync.Mutex
	Requests         int64
	Retries          int64
	PromptTokens     int64
	CompletionTokens int64
}

// Snapshot returns a copy of the current usage counters.
func (u *Usage) Snapshot() map[string]int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return map[string]int64{
		"requests":          u.Requests,
		"retries":           u.Retries,
		"prompt_tokens":     u.PromptTokens,
		"completion_tokens": u.CompletionTokens,
	}
}

func (u *Usage) addRequest() { u.mu.Lock(); u.Requests++; u.mu.Unlock() }
func (u *Usage) addRetry()   { u.mu.Lock(); u.Retries++; u.mu.Unlock() }
func (u *Usage) addTokens(p, c int64) {
	u.mu.Lock()
	u.PromptTokens += p
	u.CompletionTokens += c
	u.mu.Unlock()
}

// Gateway is the shared Meta API HTTP client.
type Gateway struct {
	cfg   Config
	http  *http.Client
	log   *slog.Logger
	Usage Usage
}

// New builds a gateway from a config.
func New(cfg Config) *Gateway {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Gateway{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
		log:  cfg.Logger,
	}
}

// Configured reports whether an API key is present.
func (g *Gateway) Configured() bool { return g.cfg.APIKey != "" }

// BaseURL exposes the configured base URL for diagnostics.
func (g *Gateway) BaseURL() string { return g.cfg.BaseURL }

// ErrNotConfigured is returned when a real Meta call is attempted without an
// API key. Callers (the fallback backend) use this to switch to the mock.
var ErrNotConfigured = errors.New("meta gateway: no API key configured (set META_API_KEY)")

// APIError is a normalized Meta API error.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("meta api: http %d: %s", e.Status, truncate(e.Body, 500))
}

// Retryable reports whether the error is worth retrying.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// PostJSON sends a JSON body to {BaseURL}/{path} and decodes the JSON response
// into out. It applies bounded retries with backoff for 429 and 5xx responses,
// honoring any Retry-After header, and normalizes errors.
func (g *Gateway) PostJSON(ctx context.Context, path string, body any, out any) error {
	if !g.Configured() {
		return ErrNotConfigured
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("meta gateway: marshal request: %w", err)
	}
	url := g.cfg.BaseURL + "/" + path

	var lastErr error
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			g.Usage.addRetry()
			if !sleepCtx(ctx, backoff(attempt)) {
				return ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("meta gateway: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)
		req.Header.Set("User-Agent", "neuroforge-x/0.1")

		g.Usage.addRequest()
		start := time.Now()
		resp, err := g.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("meta gateway: transport: %w", err)
			g.log.Warn("meta request failed", "path", path, "attempt", attempt, "err", err)
			continue // transport errors are retried
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		g.log.Info("meta request", "path", path, "status", resp.StatusCode, "ms", time.Since(start).Milliseconds())

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil {
				if err := json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("meta gateway: decode response: %w", err)
				}
			}
			return nil
		}
		apiErr := &APIError{Status: resp.StatusCode, Body: string(data)}
		lastErr = apiErr
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				if !sleepCtx(ctx, time.Duration(secs)*time.Second) {
					return ctx.Err()
				}
			}
		}
		if !apiErr.Retryable() {
			return apiErr // normalized, non-retryable
		}
	}
	return fmt.Errorf("meta gateway: exhausted retries: %w", lastErr)
}

func backoff(attempt int) time.Duration {
	d := time.Duration(200*(1<<uint(attempt-1))) * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// AccountTokens records token usage reported by a chat response.
func (g *Gateway) AccountTokens(prompt, completion int64) { g.Usage.addTokens(prompt, completion) }
