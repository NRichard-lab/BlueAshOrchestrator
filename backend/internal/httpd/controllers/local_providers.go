package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
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

// Register mounts the local-provider routes on r.
func (c *LocalProvidersController) Register(r chi.Router) {
	r.Get("/local-providers", c.list)
	r.Post("/local-providers", c.add)
	r.Get("/local-providers/harness-link", c.getLink)
	r.Put("/local-providers/harness-link", c.putLink)
	r.Delete("/local-providers/harness-link", c.deleteLink)
	r.Delete("/local-providers/{name}", c.remove)
	r.Get("/local-providers/{name}/models/{model}", c.inspect)
	r.Put("/local-providers/{name}/key", c.putKey)
	r.Delete("/local-providers/{name}/key", c.deleteKey)
}

// LocalProviderDTO is one local model endpoint and what a probe found there.
type LocalProviderDTO struct {
	Name      string `json:"name"`
	BaseURL   string `json:"baseUrl"`
	Kind      string `json:"kind"`
	Builtin   bool   `json:"builtin"`
	Reachable bool   `json:"reachable"`
	// Models are the chat-capable model names. Embedding models are left out,
	// so nothing offers them as coding models.
	Models []string `json:"models,omitempty"`
	// ModelInfo describes each entry of Models, in the same order.
	ModelInfo []localproviders.Capabilities `json:"modelInfo,omitempty"`
	// EmbeddingModels are served by the endpoint but cannot hold a conversation.
	EmbeddingModels []string `json:"embeddingModels,omitempty"`
	Error           string   `json:"error,omitempty"`
	// HarnessEnv suggests which variable a harness typically reads for this kind.
	HarnessEnv string `json:"harnessEnv"`
	// HasAPIKey says a key is stored for this endpoint. The key itself is never
	// returned.
	HasAPIKey bool `json:"hasApiKey"`
}

// LocalProviderPresetDTO is an address the settings form offers to fill in.
type LocalProviderPresetDTO struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	Kind    string `json:"kind"`
}

// LocalProvidersListResponse is the body of GET /local-providers.
type LocalProvidersListResponse struct {
	Endpoints   []LocalProviderDTO       `json:"endpoints"`
	Presets     []LocalProviderPresetDTO `json:"presets"`
	Notes       map[string]string        `json:"notes"`
	HarnessLink LocalHarnessLinkDTO      `json:"harnessLink"`
	ProbedAt    string                   `json:"probedAt"`
}

// LocalProviderErrorResponse is the error body these routes answer with.
type LocalProviderErrorResponse struct {
	Error string `json:"error"`
}

// LocalProviderNameParam is the {name} path parameter.
type LocalProviderNameParam struct {
	Name string `path:"name" description:"Endpoint name, built-in or added"`
}

// LocalProviderModelParam is the path of one model on one endpoint.
type LocalProviderModelParam struct {
	Name  string `path:"name" description:"Endpoint name, built-in or added"`
	Model string `path:"model" description:"Model name as the endpoint lists it, percent-encoded"`
}

// PutLocalProviderKeyRequest stores the key sent to one endpoint.
type PutLocalProviderKeyRequest struct {
	APIKey string `json:"apiKey" description:"Bearer key for the endpoint. Stored locally and never returned."`
}

// LocalProviderKeyResponse reports whether an endpoint has a stored key.
type LocalProviderKeyResponse struct {
	Name      string `json:"name"`
	HasAPIKey bool   `json:"hasApiKey"`
}

func writeLocalProviderError(w http.ResponseWriter, status int, err error) {
	envelope.WriteJSON(w, status, LocalProviderErrorResponse{Error: err.Error()})
}

func discoveryDTO(discovery localproviders.Discovery) LocalProviderDTO {
	return LocalProviderDTO{
		Name: discovery.Name, BaseURL: discovery.BaseURL, Kind: string(discovery.Kind),
		Builtin: discovery.Builtin, Reachable: discovery.Reachable, Models: discovery.Models,
		ModelInfo: discovery.Capabilities, EmbeddingModels: discovery.EmbeddingModels,
		Error: discovery.Error, HarnessEnv: harnessEnvFor(discovery.Kind),
		HasAPIKey: discovery.APIKey != "",
	}
}

// settings reads local-models.json. The routes that only read it carry on
// without overrides when it cannot be parsed; discovery still has servers to
// report.
func (c *LocalProvidersController) settings() localproviders.Settings {
	settings, err := localproviders.LoadSettings(c.DataDir)
	if err != nil {
		return localproviders.Settings{}
	}
	return settings
}

func harnessEnvFor(kind localproviders.Kind) string {
	if kind == localproviders.KindOllama {
		return "OLLAMA_API_BASE"
	}
	return "OPENAI_BASE_URL"
}

// presetDTOs renders the addresses the settings form offers. They come from the
// localproviders package so a preset always matches the address that is probed.
func presetDTOs() []LocalProviderPresetDTO {
	presets := localproviders.Presets()
	out := make([]LocalProviderPresetDTO, 0, len(presets))
	for _, preset := range presets {
		out = append(out, LocalProviderPresetDTO{Name: preset.Name, BaseURL: preset.BaseURL, Kind: string(preset.Kind)})
	}
	return out
}

func (c *LocalProvidersController) list(w http.ResponseWriter, r *http.Request) {
	// Discovery inspects each model over metadata routes, so the list can say
	// what a model can do and leave out the ones that cannot chat. It never
	// sends a model anything.
	discoveries := localproviders.DiscoverAll(r.Context(), c.client(), localproviders.All(c.DataDir), c.settings())
	out := make([]LocalProviderDTO, 0, len(discoveries))
	for _, discovery := range discoveries {
		out = append(out, discoveryDTO(discovery))
	}
	envelope.WriteJSON(w, http.StatusOK, LocalProvidersListResponse{
		Endpoints: out,
		Presets:   presetDTOs(),
		Notes: map[string]string{
			"bionic":       "Bionic (LM Studio agent app) is treated as a generic OpenAI-compatible server on :1234. Its protocol is not specially verified; if /v1/models answers, discovery lists its models.",
			"lan":          "Non-loopback addresses are only probed after you add them. Use a network you trust.",
			"local":        "Link an endpoint and model to the Local worker harness (OpenCode 2) to use it alongside Claude and Cursor in project and session pickers. Local issues real chat/completions to that endpoint; discovery alone does not.",
			"capabilities": "Capabilities are read from each server's metadata routes. Discovery never loads a model and never sends one a prompt. A value marked assumed was not reported by the server.",
		},
		HarnessLink: c.linkDTO(),
		ProbedAt:    time.Now().UTC().Format(time.RFC3339),
	})
}

// LocalHarnessLinkDTO is the Local (OpenCode) harness's endpoint and model.
type LocalHarnessLinkDTO struct {
	Linked        bool   `json:"linked"`
	EndpointName  string `json:"endpointName,omitempty"`
	Model         string `json:"model,omitempty"`
	HasAPIKey     bool   `json:"hasApiKey"`
	OpenCodeModel string `json:"openCodeModel,omitempty"`
	Harness       string `json:"harness"`
	Requires      string `json:"requires"`
}

func (c *LocalProvidersController) linkDTO() LocalHarnessLinkDTO {
	dto := LocalHarnessLinkDTO{
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

// PutLocalHarnessLinkRequest links an endpoint and model to the Local
// (OpenCode) harness.
type PutLocalHarnessLinkRequest struct {
	EndpointName string `json:"endpointName"`
	Model        string `json:"model"`
	APIKey       string `json:"apiKey,omitempty"`
}

var errInvalidJSONBody = errors.New("invalid JSON body")

func (c *LocalProvidersController) putLink(w http.ResponseWriter, r *http.Request) {
	var body PutLocalHarnessLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeLocalProviderError(w, http.StatusBadRequest, errInvalidJSONBody)
		return
	}
	saved, err := localproviders.SaveLink(c.DataDir, localproviders.Link{
		EndpointName: body.EndpointName,
		Model:        body.Model,
		APIKey:       body.APIKey,
	})
	if err != nil {
		writeLocalProviderError(w, http.StatusBadRequest, err)
		return
	}
	// Never echo the API key back.
	envelope.WriteJSON(w, http.StatusOK, LocalHarnessLinkDTO{
		Linked: true, EndpointName: saved.EndpointName, Model: saved.Model,
		HasAPIKey: strings.TrimSpace(saved.APIKey) != "", OpenCodeModel: localproviders.OpenCodeModelID(saved.Model),
		Harness: "local", Requires: "OpenCode 2 plus a linked endpoint and model",
	})
}

func (c *LocalProvidersController) deleteLink(w http.ResponseWriter, _ *http.Request) {
	if err := localproviders.ClearLink(c.DataDir); err != nil {
		writeLocalProviderError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddLocalProviderRequest names a local model endpoint.
type AddLocalProviderRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	Kind    string `json:"kind,omitempty" enum:"openai-compatible,ollama"`
}

func (c *LocalProvidersController) add(w http.ResponseWriter, r *http.Request) {
	var body AddLocalProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeLocalProviderError(w, http.StatusBadRequest, errInvalidJSONBody)
		return
	}
	kind, err := localproviders.ValidateKind(body.Kind)
	if err != nil {
		writeLocalProviderError(w, http.StatusBadRequest, err)
		return
	}
	added, err := localproviders.Add(c.DataDir, localproviders.Endpoint{
		Name: body.Name, BaseURL: body.BaseURL, Kind: kind,
	})
	if err != nil {
		writeLocalProviderError(w, http.StatusBadRequest, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, discoveryDTO(localproviders.Discover(r.Context(), c.client(), added, c.settings())))
}

func (c *LocalProvidersController) remove(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if err := localproviders.Remove(c.DataDir, name); err != nil {
		writeLocalProviderError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pathParam returns a path parameter as the client meant it. chi routes on the
// still-encoded path when a segment carries an escape such as %2F, which is
// how a model name with a slash in it stays one segment.
func pathParam(r *http.Request, name string) string {
	value := chi.URLParam(r, name)
	if r.URL.RawPath == "" {
		return strings.TrimSpace(value)
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return strings.TrimSpace(value)
}

// inspect reports what one model can do. Metadata requests only: the model is
// not loaded and is sent nothing.
func (c *LocalProvidersController) inspect(w http.ResponseWriter, r *http.Request) {
	endpoint, err := localproviders.Lookup(c.DataDir, pathParam(r, "name"))
	if err != nil {
		writeLocalProviderError(w, http.StatusNotFound, err)
		return
	}
	capabilities, err := localproviders.Inspect(r.Context(), c.client(), endpoint, pathParam(r, "model"))
	switch {
	case errors.Is(err, localproviders.ErrModelNotFound):
		writeLocalProviderError(w, http.StatusNotFound, err)
		return
	case err != nil:
		// The endpoint is known but did not answer: the failure is upstream.
		writeLocalProviderError(w, http.StatusBadGateway, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, localproviders.ApplyOverrides(capabilities, c.settings().Override(capabilities.ID)))
}

func (c *LocalProvidersController) putKey(w http.ResponseWriter, r *http.Request) {
	var body PutLocalProviderKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeLocalProviderError(w, http.StatusBadRequest, errInvalidJSONBody)
		return
	}
	if strings.TrimSpace(body.APIKey) == "" {
		writeLocalProviderError(w, http.StatusBadRequest, errors.New("apiKey is required; use DELETE to clear a stored key"))
		return
	}
	c.setKey(w, pathParam(r, "name"), body.APIKey)
}

func (c *LocalProvidersController) deleteKey(w http.ResponseWriter, r *http.Request) {
	c.setKey(w, pathParam(r, "name"), "")
}

func (c *LocalProvidersController) setKey(w http.ResponseWriter, name, key string) {
	endpoint, err := localproviders.Lookup(c.DataDir, name)
	if err != nil {
		writeLocalProviderError(w, http.StatusNotFound, err)
		return
	}
	if err := localproviders.SetEndpointKey(c.DataDir, endpoint.Name, key); err != nil {
		writeLocalProviderError(w, http.StatusInternalServerError, err)
		return
	}
	// Never echo the key back.
	envelope.WriteJSON(w, http.StatusOK, LocalProviderKeyResponse{Name: endpoint.Name, HasAPIKey: strings.TrimSpace(key) != ""})
}
