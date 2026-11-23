package controllers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/go-chi/chi/v5"
)

func TestLocalProvidersListIncludesBuiltinsAndPresets(t *testing.T) {
	dir := t.TempDir()
	ctrl := &controllers.LocalProvidersController{DataDir: dir, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, http.ErrHandlerTimeout
	})}}
	r := chi.NewRouter()
	ctrl.Register(r)

	req := httptest.NewRequest(http.MethodGet, "/local-providers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Endpoints []map[string]any `json:"endpoints"`
		Presets   []map[string]string `json:"presets"`
		Notes     map[string]string `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Endpoints) < 2 {
		t.Fatalf("endpoints = %d, want builtins present", len(body.Endpoints))
	}
	foundBionic := false
	for _, preset := range body.Presets {
		if preset["name"] == "bionic" && strings.Contains(preset["baseUrl"], "1234") {
			foundBionic = true
		}
	}
	if !foundBionic {
		t.Fatalf("presets = %#v, want bionic on 1234", body.Presets)
	}
	if body.Notes["bionic"] == "" {
		t.Fatal("expected bionic protocol note")
	}
}

func TestLocalProvidersAddAndRemove(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "local-model"}},
		})
	}))
	t.Cleanup(server.Close)

	ctrl := &controllers.LocalProvidersController{DataDir: dir, Client: server.Client()}
	r := chi.NewRouter()
	ctrl.Register(r)

	req := httptest.NewRequest(http.MethodPost, "/local-providers", strings.NewReader(`{"name":"lan-box","baseUrl":"`+server.URL+`","kind":"openai-compatible"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d body=%s", rec.Code, rec.Body.String())
	}
	var added map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode add: %v", err)
	}
	if added["reachable"] != true {
		t.Fatalf("added = %#v, want reachable", added)
	}
	if filepath.Base(dir) == "" {
		t.Fatal("data dir vanished")
	}

	del := httptest.NewRequest(http.MethodDelete, "/local-providers/lan-box", nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, del)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", delRec.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
