package controllers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

func TestLocalProvidersListIncludesBuiltinsAndPresets(t *testing.T) {
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "9090")
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
		Endpoints []map[string]any    `json:"endpoints"`
		Presets   []map[string]string `json:"presets"`
		Notes     map[string]string   `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Endpoints) < 2 {
		t.Fatalf("endpoints = %d, want builtins present", len(body.Endpoints))
	}
	// The preset the form offers for llama.cpp is the address actually probed,
	// including when the environment moves it.
	llamaPreset, llamaEndpoint := "", ""
	for _, preset := range body.Presets {
		if preset["name"] == "llama.cpp" {
			llamaPreset = preset["baseUrl"]
		}
	}
	for _, endpoint := range body.Endpoints {
		if endpoint["name"] == "llama.cpp" {
			llamaEndpoint, _ = endpoint["baseUrl"].(string)
		}
	}
	if llamaPreset != "http://127.0.0.1:9090" || llamaEndpoint != llamaPreset {
		t.Fatalf("llama.cpp preset = %q, endpoint = %q, want both on the overridden port 9090", llamaPreset, llamaEndpoint)
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

func TestLocalProvidersHarnessLinkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "coder-v1"}},
		})
	}))
	t.Cleanup(server.Close)

	ctrl := &controllers.LocalProvidersController{DataDir: dir, Client: server.Client()}
	r := chi.NewRouter()
	ctrl.Register(r)

	add := httptest.NewRequest(http.MethodPost, "/local-providers", strings.NewReader(`{"name":"lab","baseUrl":"`+server.URL+`","kind":"openai-compatible"}`))
	add.Header.Set("Content-Type", "application/json")
	addRec := httptest.NewRecorder()
	r.ServeHTTP(addRec, add)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status = %d body=%s", addRec.Code, addRec.Body.String())
	}

	put := httptest.NewRequest(http.MethodPut, "/local-providers/harness-link", strings.NewReader(`{"endpointName":"lab","model":"coder-v1","apiKey":"secret-token"}`))
	put.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	r.ServeHTTP(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put link status = %d body=%s", putRec.Code, putRec.Body.String())
	}
	if strings.Contains(putRec.Body.String(), "secret-token") {
		t.Fatal("API key must not appear in link response")
	}
	var link map[string]any
	if err := json.Unmarshal(putRec.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link["linked"] != true || link["model"] != "coder-v1" || link["hasApiKey"] != true {
		t.Fatalf("link = %#v", link)
	}

	list := httptest.NewRequest(http.MethodGet, "/local-providers", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRec.Code)
	}
	if strings.Contains(listRec.Body.String(), "secret-token") {
		t.Fatal("API key must not appear in list response")
	}
	var listBody struct {
		HarnessLink map[string]any    `json:"harnessLink"`
		Notes       map[string]string `json:"notes"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if listBody.HarnessLink["linked"] != true || listBody.Notes["local"] == "" {
		t.Fatalf("list body harnessLink=%#v notes=%#v", listBody.HarnessLink, listBody.Notes)
	}

	del := httptest.NewRequest(http.MethodDelete, "/local-providers/harness-link", nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, del)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("unlink status = %d", delRec.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
