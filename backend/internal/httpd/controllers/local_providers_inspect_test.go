package controllers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

// onlyServer returns a client that reaches the fake server and nothing else,
// so the built-in addresses, which may have real model servers behind them on
// a developer's machine, are reported unreachable instead of probed.
func onlyServer(t *testing.T, server *localproviderstest.Server) *http.Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != target.Host {
			return nil, errors.New("connection refused")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
}

func localProvidersRouter(t *testing.T) (chi.Router, *localproviderstest.Server, string) {
	t.Helper()
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	dir := t.TempDir()
	server := localproviderstest.NewOllama(t,
		localproviderstest.OllamaModel{Name: "qwen3.8:27b", Capabilities: []string{"completion", "vision", "tools", "thinking"}, MaxContext: 262144, Digest: "sha256:bbb"},
		localproviderstest.OllamaModel{Name: "hf.co/org/coder:Q4", Capabilities: []string{"completion"}, MaxContext: 32768},
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	if _, err := localproviders.Add(dir, server.Endpoint("lab", localproviders.KindOllama)); err != nil {
		t.Fatal(err)
	}
	ctrl := &controllers.LocalProvidersController{DataDir: dir, Client: onlyServer(t, server)}
	r := chi.NewRouter()
	ctrl.Register(r)
	return r, server, dir
}

func doJSON(t *testing.T, r chi.Router, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestLocalProvidersListCarriesCapabilitiesAndHidesEmbeddingModels(t *testing.T) {
	r, server, _ := localProvidersRouter(t)
	rec := doJSON(t, r, http.MethodGet, "/local-providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body controllers.LocalProvidersListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var lab *controllers.LocalProviderDTO
	for i := range body.Endpoints {
		if body.Endpoints[i].Name == "lab" {
			lab = &body.Endpoints[i]
		}
	}
	if lab == nil || !lab.Reachable {
		t.Fatalf("endpoints = %+v, want lab reachable", body.Endpoints)
	}
	if len(lab.Models) != 2 || strings.Contains(strings.Join(lab.Models, ","), "nomic") {
		t.Errorf("models = %v, want the two chat models and no embedding model", lab.Models)
	}
	if len(lab.EmbeddingModels) != 1 || lab.EmbeddingModels[0] != "nomic-embed-text:latest" {
		t.Errorf("embeddingModels = %v", lab.EmbeddingModels)
	}
	if len(lab.ModelInfo) != 2 {
		t.Fatalf("modelInfo = %+v, want one entry per listed model", lab.ModelInfo)
	}
	for i, info := range lab.ModelInfo {
		if info.Model != lab.Models[i] || info.Context.Source != localproviders.SourceDetected {
			t.Errorf("modelInfo[%d] = %+v, want %s with a detected context", i, info, lab.Models[i])
		}
	}
	if lab.HasAPIKey {
		t.Error("hasApiKey = true with no key stored")
	}
	if body.Notes["capabilities"] == "" {
		t.Error("expected a note saying detection never prompts a model")
	}
	server.RequireNoGeneration(t)
}

func TestLocalProvidersInspectOneModel(t *testing.T) {
	r, server, dir := localProvidersRouter(t)

	rec := doJSON(t, r, http.MethodGet, "/local-providers/lab/models/qwen3.8:27b", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var capabilities localproviders.Capabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.ID != "lab/qwen3.8:27b" || !capabilities.Tools.Value || capabilities.Tools.Source != localproviders.SourceDetected || capabilities.Context.Value != 262144 {
		t.Errorf("capabilities = %+v", capabilities)
	}

	// A model name with slashes in it travels as one percent-encoded segment.
	rec = doJSON(t, r, http.MethodGet, "/local-providers/lab/models/"+url.PathEscape("hf.co/org/coder:Q4"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("slashed model status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.Model != "hf.co/org/coder:Q4" || capabilities.Tools.Value {
		t.Errorf("slashed model = %+v, want it found with a detected no for tools", capabilities)
	}

	// The user's override wins and says so.
	noTools := false
	if _, err := localproviders.UpdateSettings(dir, func(settings *localproviders.Settings) error {
		settings.Models = map[string]localproviders.ModelOverride{"lab/qwen3.8:27b": {Tools: &noTools}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, http.MethodGet, "/local-providers/lab/models/qwen3.8:27b", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.Tools != (localproviders.BoolValue{Value: false, Source: localproviders.SourceOverride}) {
		t.Errorf("tools = %+v, want the override", capabilities.Tools)
	}
	server.RequireNoGeneration(t)
}

func TestLocalProvidersInspectErrors(t *testing.T) {
	r, _, _ := localProvidersRouter(t)
	cases := map[string]int{
		"/local-providers/nope/models/x":         http.StatusNotFound,
		"/local-providers/lab/models/absent:1b":  http.StatusNotFound,
		"/local-providers/ollama/models/any:tag": http.StatusBadGateway, // known endpoint, not answering
	}
	for target, want := range cases {
		rec := doJSON(t, r, http.MethodGet, target, "")
		if rec.Code != want {
			t.Errorf("%s status = %d, want %d (body=%s)", target, rec.Code, want, rec.Body.String())
		}
		var body controllers.LocalProviderErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
			t.Errorf("%s error body = %s", target, rec.Body.String())
		}
	}
}

func TestLocalProvidersKeyRoundTripNeverReturnsTheKey(t *testing.T) {
	r, server, dir := localProvidersRouter(t)
	server.RequireKey("k-secret")

	// The keyed server refuses discovery until the key is stored.
	rec := doJSON(t, r, http.MethodGet, "/local-providers/lab/models/qwen3.8:27b", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("inspect without key status = %d", rec.Code)
	}

	rec = doJSON(t, r, http.MethodPut, "/local-providers/lab/key", `{"apiKey":"k-secret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put key status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "k-secret") {
		t.Fatal("the key was echoed back")
	}
	var saved controllers.LocalProviderKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || saved.Name != "lab" || !saved.HasAPIKey {
		t.Fatalf("put key body = %s", rec.Body.String())
	}
	stored, err := os.ReadFile(filepath.Join(dir, "local-models.json"))
	if err != nil || !strings.Contains(string(stored), "k-secret") {
		t.Fatalf("local-models.json = %s, %v", stored, err)
	}

	// The key is now sent on metadata requests, and the list says a key is
	// stored without saying what it is.
	rec = doJSON(t, r, http.MethodGet, "/local-providers/lab/models/qwen3.8:27b", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect with key status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/local-providers", "")
	if strings.Contains(rec.Body.String(), "k-secret") {
		t.Fatal("the key appears in the list response")
	}
	if !strings.Contains(rec.Body.String(), `"hasApiKey":true`) {
		t.Errorf("list does not report the stored key: %s", rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodDelete, "/local-providers/lab/key", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"hasApiKey":true`) {
		t.Fatalf("delete key status = %d body=%s", rec.Code, rec.Body.String())
	}
	if endpoint, _ := localproviders.Lookup(dir, "lab"); endpoint.APIKey != "" {
		t.Fatal("key survived DELETE")
	}
	server.RequireNoGeneration(t)
}

func TestLocalProvidersKeyValidation(t *testing.T) {
	r, _, _ := localProvidersRouter(t)
	cases := []struct {
		method, target, body string
		want                 int
	}{
		{http.MethodPut, "/local-providers/nope/key", `{"apiKey":"k"}`, http.StatusNotFound},
		{http.MethodDelete, "/local-providers/nope/key", "", http.StatusNotFound},
		{http.MethodPut, "/local-providers/lab/key", `{"apiKey":"  "}`, http.StatusBadRequest},
		{http.MethodPut, "/local-providers/lab/key", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := doJSON(t, r, tc.method, tc.target, tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s %s status = %d, want %d (body=%s)", tc.method, tc.target, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestLocalProvidersRemoveForgetsTheKey(t *testing.T) {
	r, _, dir := localProvidersRouter(t)
	if rec := doJSON(t, r, http.MethodPut, "/local-providers/lab/key", `{"apiKey":"k-secret"}`); rec.Code != http.StatusOK {
		t.Fatalf("put key status = %d", rec.Code)
	}
	if rec := doJSON(t, r, http.MethodDelete, "/local-providers/lab", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d", rec.Code)
	}
	stored, _ := os.ReadFile(filepath.Join(dir, "local-models.json"))
	if strings.Contains(string(stored), "k-secret") {
		t.Fatalf("the key outlived its endpoint: %s", stored)
	}
}
