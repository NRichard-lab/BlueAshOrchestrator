// Package localproviderstest provides fake local model servers for tests. A
// fake records every request it receives, so a test can assert that a code
// path made metadata requests only.
package localproviderstest

import (
	"encoding/json"
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
	key      string
	routes   map[string]http.HandlerFunc
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
	s.mu.Lock()
	s.requests = append(s.requests, route)
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
	server := &Server{routes: map[string]http.HandlerFunc{}}
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

// Endpoint returns the endpoint a test registers for this server.
func (s *Server) Endpoint(name string, kind localproviders.Kind) localproviders.Endpoint {
	return localproviders.Endpoint{Name: name, BaseURL: s.URL, Kind: kind}
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
