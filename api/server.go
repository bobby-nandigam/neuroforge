// Package api is the NEUROFORGE X control plane: a small HTTP service exposing
// the compiler and runtime as JSON endpoints so the Visual Compiler Studio
// (and any client) can drive real compilation and execution and render actual
// backend state — never fabricated metrics.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"neuroforge/core/compiler"
	"neuroforge/core/ir"
	"neuroforge/core/optimizer"
	"neuroforge/runtime/cache"
	"neuroforge/runtime/executor"
	"neuroforge/runtime/setup"
)

// Server holds the shared runtime environment.
type Server struct {
	env *setup.Env
}

// NewServer builds a control-plane server with the default backend registry.
func NewServer() *Server { return &Server{env: setup.Default()} }

// Handler returns the HTTP mux with all routes and permissive CORS (dev).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/operators", s.operators)
	mux.HandleFunc("/api/backends", s.backendsList)
	mux.HandleFunc("/api/compile", s.compile)
	mux.HandleFunc("/api/run", s.run)
	return cors(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"meta_configured": s.env.Gateway.Configured(),
		"base_url":        s.env.Gateway.BaseURL(),
		"time":            time.Now().UTC(),
	})
}

func (s *Server) operators(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ir.Operators())
}

type backendInfo struct {
	Name         string   `json:"name"`
	Configured   bool     `json:"configured"`
	Capabilities []string `json:"capabilities"`
}

func (s *Server) backendsList(w http.ResponseWriter, r *http.Request) {
	var out []backendInfo
	for _, b := range s.env.Registry.All() {
		caps := make([]string, 0)
		for _, c := range b.Capabilities() {
			caps = append(caps, c.OperatorType)
		}
		out = append(out, backendInfo{Name: b.Name(), Configured: b.Configured(), Capabilities: caps})
	}
	writeJSON(w, http.StatusOK, out)
}

type compileReq struct {
	Workload string `json:"workload"`
	Optimize *bool  `json:"optimize,omitempty"`
}

func (s *Server) compile(w http.ResponseWriter, r *http.Request) {
	var req compileReq
	if !decode(w, r, &req) {
		return
	}
	opts := compiler.DefaultOptions()
	if req.Optimize != nil && !*req.Optimize {
		opts.Optimize = false
		opts.OptimizerOf = optimizer.Options{}
	}
	c, err := compiler.CompileBytes([]byte(req.Workload), opts)
	resp := map[string]any{"compiled": c}
	if c != nil && c.Optimized != nil {
		levels, _ := compiler.ExecutionLevels(c.Optimized)
		resp["levels"] = levels
	}
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp) // diagnostics carried in compiled
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type runReq struct {
	Workload    string `json:"workload"`
	Optimize    *bool  `json:"optimize,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	Cache       bool   `json:"cache,omitempty"`
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var req runReq
	if !decode(w, r, &req) {
		return
	}
	opts := compiler.DefaultOptions()
	if req.Optimize != nil && !*req.Optimize {
		opts.Optimize = false
		opts.OptimizerOf = optimizer.Options{}
	}
	c, err := compiler.CompileBytes([]byte(req.Workload), opts)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "compiled": c})
		return
	}
	cfg := executor.Config{Concurrency: req.Concurrency, RetryBackoff: 100 * time.Millisecond}
	if req.Cache {
		cfg.Cache = cache.New(256)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res := executor.New(s.env.Registry).Run(ctx, c.Optimized, nil, cfg)

	writeJSON(w, http.StatusOK, map[string]any{
		"compiled":  c,
		"simulated": !s.env.Gateway.Configured(),
		"outputs":   res.Outputs,
		"metrics":   res.Metrics,
		"error":     errString(res.Err),
	})
}

// --- helpers ---

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	return true
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
