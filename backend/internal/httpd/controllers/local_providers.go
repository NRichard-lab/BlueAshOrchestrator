package controllers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/go-chi/chi/v5"
)

// LocalProvidersController exposes discovery of local model endpoints to the
// desktop settings UI. It wraps the same store and probes as `ao local-provider`.
type LocalProvidersController struct {
	DataDir string
	Client  *http.Client
}

func (c *LocalProvidersController) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: localproviders.DefaultTimeout}
}

func (c *LocalProvidersController) Register(r chi.Router) {
	r.Get("/local-providers", c.list)
	r.Post("/local-providers", c.add)
	r.Get("/local-providers/harness-link", c.getLink)
	r.Put("/local-providers/harness-link", c.putLink)
	r.Delete("/local-providers/harness-link", c.deleteLink)
	r.Delete("/local-providers/{name}", c.remove)
}

type localProviderDTO struct {
	Name      string   `json:"name"`
	BaseURL   string   `json:"baseUrl"`
	Kind      string   `json:"kind"`
	Builtin   bool     `json:"builtin"`
	Reachable bool     `json:"reachable"`
	Models    []string `json:"models,omitempty"`
	Error     string   `json:"error,omitempty"`
	// HarnessEnv suggests which variable a harness typically reads for this kind.
	HarnessEnv string `json:"harnessEnv"`
}

func harnessEnvFor(kind localproviders.Kind) string {
	if kind == localproviders.KindOllama {
		return "OLLAMA_API_BASE"
	}
	return "OPENAI_BASE_URL"
}

func (c *LocalProvidersController) list(w http.ResponseWriter, r *http.Request) {
	results := localproviders.ProbeAll(r.Context(), c.client(), localproviders.All(c.DataDir))
	out := make([]localProviderDTO, 0, len(results))
	for _, result := range results {
		out = append(out, localProviderDTO{
			Name: result.Name, BaseURL: result.BaseURL, Kind: string(result.Kind),
			Builtin: result.Builtin, Reachable: result.Reachable, Models: result.Models,
			Error: result.Error, HarnessEnv: harnessEnvFor(result.Kind),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{
		"endpoints": out,
		"presets": []map[string]string{
			{"name": "ollama", "baseUrl": "http://127.0.0.1:11434", "kind": "ollama"},
			{"name": "llama.cpp", "baseUrl": "http://127.0.0.1:8080", "kind": "openai-compatible"},
			{"name": "bionic", "baseUrl": "http://127.0.0.1:1234", "kind": "openai-compatible"},
		},
		"notes": map[string]string{
			"bionic": "Bionic (LM Studio agent app) is treated as a generic OpenAI-compatible server on :1234. Its protocol is not specially verified; if /v1/models answers, discovery lists its models.",
			"lan":    "Non-loopback addresses are only probed after you add them. Use a network you trust.",
			"local":  "Link an endpoint and model to the Local worker harness (OpenCode 2) to use it alongside Claude and Cursor in project and session pickers. Local issues real chat/completions to that endpoint; discovery alone does not.",
		},
		"harnessLink": c.linkDTO(),
		"probedAt":    time.Now().UTC().Format(time.RFC3339),
	})
}

type localHarnessLinkDTO struct {
	Linked        bool   `json:"linked"`
	EndpointName  string `json:"endpointName,omitempty"`
	Model         string `json:"model,omitempty"`
	HasAPIKey     bool   `json:"hasApiKey"`
	OpenCodeModel string `json:"openCodeModel,omitempty"`
	Harness       string `json:"harness"`
	Requires      string `json:"requires"`
}

func (c *LocalProvidersController) linkDTO() localHarnessLinkDTO {
	dto := localHarnessLinkDTO{
		Harness:  "local",
		Requires: "OpenCode 2 plus a linked endpoint and model",
	}
	link, ok, err := localproviders.LoadLink(c.DataDir)
	if err != nil || !ok {
		return dto
	}
	dto.Linked = true
	dto.EndpointName = link.EndpointName
	dto.Model = link.Model
	dto.HasAPIKey = strings.TrimSpace(link.APIKey) != ""
	dto.OpenCodeModel = localproviders.OpenCodeModelID(link.Model)
	return dto
}

func (c *LocalProvidersController) getLink(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, c.linkDTO())
}

type putLocalHarnessLinkRequest struct {
	EndpointName string `json:"endpointName"`
	Model        string `json:"model"`
	APIKey       string `json:"apiKey"`
}

func (c *LocalProvidersController) putLink(w http.ResponseWriter, r *http.Request) {
	var body putLocalHarnessLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	saved, err := localproviders.SaveLink(c.DataDir, localproviders.Link{
		EndpointName: body.EndpointName,
		Model:        body.Model,
		APIKey:       body.APIKey,
	})
	if err != nil {
		envelope.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Never echo the API key back.
	envelope.WriteJSON(w, http.StatusOK, localHarnessLinkDTO{
		Linked: true, EndpointName: saved.EndpointName, Model: saved.Model,
		HasAPIKey: strings.TrimSpace(saved.APIKey) != "", OpenCodeModel: localproviders.OpenCodeModelID(saved.Model),
		Harness: "local", Requires: "OpenCode 2 plus a linked endpoint and model",
	})
}

func (c *LocalProvidersController) deleteLink(w http.ResponseWriter, _ *http.Request) {
	if err := localproviders.ClearLink(c.DataDir); err != nil {
		envelope.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type addLocalProviderRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	Kind    string `json:"kind"`
}

func (c *LocalProvidersController) add(w http.ResponseWriter, r *http.Request) {
	var body addLocalProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	kind, err := localproviders.ValidateKind(body.Kind)
	if err != nil {
		envelope.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	added, err := localproviders.Add(c.DataDir, localproviders.Endpoint{
		Name: body.Name, BaseURL: body.BaseURL, Kind: kind,
	})
	if err != nil {
		envelope.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	result := localproviders.Probe(r.Context(), c.client(), added)
	envelope.WriteJSON(w, http.StatusOK, localProviderDTO{
		Name: result.Name, BaseURL: result.BaseURL, Kind: string(result.Kind),
		Builtin: false, Reachable: result.Reachable, Models: result.Models,
		Error: result.Error, HarnessEnv: harnessEnvFor(result.Kind),
	})
}

func (c *LocalProvidersController) remove(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if err := localproviders.Remove(c.DataDir, name); err != nil {
		envelope.WriteJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
