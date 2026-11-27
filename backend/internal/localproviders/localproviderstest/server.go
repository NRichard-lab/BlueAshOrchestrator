// Package localproviderstest provides fake local model servers for tests. A
// fake records every request it receives, so a test can assert that a code
// path made metadata requests only.
package localproviderstest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

// OllamaModel is one model a fake Ollama server has pulled.
type OllamaModel struct {
	Name string
	// Capabilities is the list /api/show reports. Nil reports none, as an old
	// Ollama does.
	Capabilities []string
	// MaxContext is <family>.context_length in model_info.
	MaxContext int
	// LoadedContext, when positive, lists the model in /api/ps with that
	// context allocated.
	LoadedContext int
	// NumCtx, when positive, is a Modelfile num_ctx parameter.
	NumCtx int
	Family string
	Digest string
}

// Server is a fake model server.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	requests []string
	bodies   map[string][]string
	key      string
	routes   map[string]http.HandlerFunc
}

// Bodies returns the request bodies received on a route ("METHOD /path"), in
// order.
func (s *Server) Bodies(route string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies[route]...)
}

// Requests returns every request received so far as "METHOD /path".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// GenerationRequests returns the received requests that would have run a
// model.
func (s *Server) GenerationRequests() []string {
	var out []string
	for _, request := range s.Requests() {
		_, path, _ := strings.Cut(request, " ")
		if localproviders.IsGenerationRoute(path) {
			out = append(out, request)
		}
	}
	return out
}

// RequireNoGeneration fails the test if any request reached a generation
// route.
func (s *Server) RequireNoGeneration(t testing.TB) {
	t.Helper()
	if sent := s.GenerationRequests(); len(sent) > 0 {
		t.Fatalf("generation routes were requested: %v", sent)
	}
}

// RequireKey makes the server answer 401 to any request without this bearer
// key.
func (s *Server) RequireKey(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = key
}

// Handle serves a route ("METHOD /path") with handler, replacing any earlier
// one.
func (s *Server) Handle(route string, handler http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route] = handler
}

// HandleJSON serves a route with a fixed JSON body.
func (s *Server) HandleJSON(route string, body any) {
	s.Handle(route, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	route := r.Method + " " + r.URL.Path
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.mu.Lock()
	s.requests = append(s.requests, route)
	s.bodies[route] = append(s.bodies[route], string(body))
	key := s.key
	handler := s.routes[route]
	s.mu.Unlock()
	if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler(w, r)
}

// New starts a fake server with no routes. Every unhandled route answers 404.
func New(t testing.TB) *Server {
	t.Helper()
	server := &Server{routes: map[string]http.HandlerFunc{}, bodies: map[string][]string{}}
	server.Server = httptest.NewServer(http.HandlerFunc(server.serve))
	t.Cleanup(server.Close)
	return server
}

// NewOllama starts a fake Ollama serving the metadata routes for models.
func NewOllama(t testing.TB, models ...OllamaModel) *Server {
	t.Helper()
	server := New(t)
	byName := make(map[string]OllamaModel, len(models))
	tags := make([]map[string]any, 0, len(models))
	running := []map[string]any{}
	for _, model := range models {
		byName[model.Name] = model
		tags = append(tags, map[string]any{
			"name": model.Name, "model": model.Name, "size": 1 << 30, "digest": model.Digest,
			"details": map[string]any{"family": model.Family, "parameter_size": "8B", "quantization_level": "Q4_K_M"},
		})
		if model.LoadedContext > 0 {
			running = append(running, map[string]any{
				"name": model.Name, "model": model.Name, "context_length": model.LoadedContext,
			})
		}
	}
	server.HandleJSON("GET /api/version", map[string]string{"version": "0.40.17"})
	server.HandleJSON("GET /api/tags", map[string]any{"models": tags})
	server.HandleJSON("GET /api/ps", map[string]any{"models": running})
	server.Handle("POST /api/show", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, ok := byName[body.Model]
		if !ok {
			http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
			return
		}
		family := model.Family
		if family == "" {
			family = "test"
		}
		answer := map[string]any{
			"details":    map[string]any{"family": family, "parameter_size": "8B", "quantization_level": "Q4_K_M"},
			"model_info": map[string]any{"general.architecture": family},
		}
		if model.MaxContext > 0 {
			answer["model_info"].(map[string]any)[family+".context_length"] = model.MaxContext
		}
		if model.Capabilities != nil {
			answer["capabilities"] = model.Capabilities
		}
		if model.NumCtx > 0 {
			answer["parameters"] = "num_ctx                        " + itoa(model.NumCtx) + "\nstop                           \"<|im_end|>\""
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	})
	return server
}

// OllamaChat makes POST /api/chat stream thinking (when not empty) and then
// each piece of content, ending with a done line that reports token counts.
// It stands in for a model answering a turn a person started.
func (s *Server) OllamaChat(thinking string, pieces ...string) {
	s.Handle("POST /api/chat", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		write := func(line map[string]any) {
			_ = json.NewEncoder(w).Encode(line)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if thinking != "" {
			write(map[string]any{"message": map[string]any{"role": "assistant", "thinking": thinking}, "done": false})
		}
		for _, piece := range pieces {
			write(map[string]any{"message": map[string]any{"role": "assistant", "content": piece}, "done": false})
		}
		write(map[string]any{"message": map[string]any{"role": "assistant", "content": ""}, "done": true,
			"done_reason": "stop", "prompt_eval_count": 21, "eval_count": 7})
	})
}

// OpenAIChat makes POST /v1/chat/completions stream each piece of content as
// server-sent events, ending with a usage chunk and [DONE].
func (s *Server) OpenAIChat(pieces ...string) {
	s.Handle("POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(chunk map[string]any) {
			encoded, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			if flusher != nil {
				flusher.Flush()
			}
		}
		for _, piece := range pieces {
			write(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": piece}}}})
		}
		write(map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "stop"}}})
		write(map[string]any{"choices": []map[string]any{}, "usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 5}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})
}

// Endpoint returns the endpoint a test registers for this server.
func (s *Server) Endpoint(name string, kind localproviders.Kind) localproviders.Endpoint {
	return localproviders.Endpoint{Name: name, BaseURL: s.URL, Kind: kind}
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// OnlyClient returns a client that reaches this server and nothing else. The
// built-in endpoint addresses may have real model servers behind them on a
// developer's machine; with this client they are reported unreachable instead
// of probed, so a test's result does not depend on what the machine runs.
func (s *Server) OnlyClient() *http.Client {
	host := strings.TrimPrefix(strings.TrimPrefix(s.URL, "http://"), "https://")
	return &http.Client{Transport: onlyHost{host: host}}
}

type onlyHost struct{ host string }

func (o onlyHost) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != o.host {
		return nil, fmt.Errorf("dial %s: connection refused", r.URL.Host)
	}
	return http.DefaultTransport.RoundTrip(r)
}
