package localproviders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Link binds a saved local endpoint and model to the Local coding harness so
// project/session pickers can launch workers that call that endpoint.
type Link struct {
	// EndpointName is the builtin or stored endpoint name (for example ollama).
	EndpointName string `json:"endpointName"`
	// Model is the concrete model id the endpoint serves (or a supported manual id).
	Model string `json:"model"`
	// APIKey is optional. Keyless local servers leave it empty; the harness then
	// sends a non-secret placeholder the OpenAI-compatible client requires.
	// Never log this field.
	APIKey string `json:"apiKey,omitempty"`
}

const linkStoreName = "local-harness.json"

// ProviderID is the OpenCode provider slug injected for the Local harness.
const ProviderID = "ao-local"

// ErrNotLinked means no Local harness endpoint/model is saved.
var ErrNotLinked = errors.New("local harness is not linked to an endpoint and model")

// LoadLink reads the Local harness binding. Missing file means not linked.
func LoadLink(dataDir string) (Link, bool, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, linkStoreName))
	if err != nil {
		if os.IsNotExist(err) {
			return Link{}, false, nil
		}
		return Link{}, false, fmt.Errorf("local harness link: %w", err)
	}
	var link Link
	if err := json.Unmarshal(data, &link); err != nil {
		return Link{}, false, fmt.Errorf("local harness link: %w", err)
	}
	link.EndpointName = strings.TrimSpace(link.EndpointName)
	link.Model = strings.TrimSpace(link.Model)
	if link.EndpointName == "" || link.Model == "" {
		return Link{}, false, nil
	}
	return link, true, nil
}

// SaveLink stores the Local harness binding. API keys are written only to this
// 0600 file under the data directory and must not appear in logs or list DTOs.
func SaveLink(dataDir string, link Link) (Link, error) {
	link.EndpointName = strings.TrimSpace(link.EndpointName)
	link.Model = strings.TrimSpace(link.Model)
	link.APIKey = strings.TrimSpace(link.APIKey)
	if link.EndpointName == "" {
		return Link{}, errors.New("endpoint name is required")
	}
	if link.Model == "" {
		return Link{}, errors.New("model is required")
	}
	endpoint, ok := lookupEndpoint(dataDir, link.EndpointName)
	if !ok {
		return Link{}, fmt.Errorf("unknown endpoint %q", link.EndpointName)
	}
	_ = endpoint
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Link{}, fmt.Errorf("local harness link: %w", err)
	}
	data, err := json.MarshalIndent(link, "", "  ")
	if err != nil {
		return Link{}, err
	}
	path := filepath.Join(dataDir, linkStoreName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return Link{}, fmt.Errorf("local harness link: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return Link{}, fmt.Errorf("local harness link: %w", err)
	}
	return link, nil
}

// ClearLink removes the Local harness binding.
func ClearLink(dataDir string) error {
	path := filepath.Join(dataDir, linkStoreName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local harness link: %w", err)
	}
	return nil
}

func lookupEndpoint(dataDir, name string) (Endpoint, bool) {
	for _, endpoint := range All(dataDir) {
		if strings.EqualFold(endpoint.Name, name) {
			return endpoint, true
		}
	}
	return Endpoint{}, false
}

// ResolveLink returns the linked endpoint, concrete model, and OpenAI-compatible
// base URL the Local harness should call. Model may be the linked value even when
// the probe list is empty (manual id), as long as the endpoint exists.
func ResolveLink(dataDir string) (Endpoint, Link, string, error) {
	link, ok, err := LoadLink(dataDir)
	if err != nil {
		return Endpoint{}, Link{}, "", err
	}
	if !ok {
		return Endpoint{}, Link{}, "", ErrNotLinked
	}
	endpoint, found := lookupEndpoint(dataDir, link.EndpointName)
	if !found {
		return Endpoint{}, Link{}, "", fmt.Errorf("linked endpoint %q is no longer known", link.EndpointName)
	}
	return endpoint, link, OpenAICompatibleBaseURL(endpoint), nil
}

// OpenAICompatibleBaseURL returns the /v1 root OpenCode's openai-compatible
// package expects for both openai-compatible and Ollama servers.
func OpenAICompatibleBaseURL(endpoint Endpoint) string {
	base := strings.TrimRight(endpoint.BaseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// OpenCodeModelID is the provider/model string OpenCode selects for Local.
func OpenCodeModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if before, after, ok := strings.Cut(model, "/"); ok && before == ProviderID && after != "" {
		return model
	}
	return ProviderID + "/" + model
}

// ModelIDFromOpenCode strips the ao-local/ prefix when present.
func ModelIDFromOpenCode(model string) string {
	model = strings.TrimSpace(model)
	if before, after, ok := strings.Cut(model, "/"); ok && before == ProviderID {
		return after
	}
	return model
}

// EffectiveAPIKey returns the key OpenCode should send. Empty stored keys use a
// non-secret placeholder so keyless local servers still satisfy the client.
func EffectiveAPIKey(link Link) string {
	if key := strings.TrimSpace(link.APIKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("AO_LOCAL_API_KEY")); key != "" {
		return key
	}
	return "local"
}

// VerifyLinkedModel probes the linked endpoint and confirms the model is listed,
// or accepts a manual model when the probe succeeds but the catalog is empty or
// the model was named explicitly. Returns unauthorized-style errors as plain text.
func VerifyLinkedModel(ctx context.Context, client *http.Client, dataDir string) (Endpoint, Link, error) {
	endpoint, link, _, err := ResolveLink(dataDir)
	if err != nil {
		return Endpoint{}, Link{}, err
	}
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	result := Probe(ctx, client, endpoint)
	if !result.Reachable {
		if result.Error != "" {
			return endpoint, link, fmt.Errorf("linked endpoint %q is unreachable: %s", endpoint.Name, result.Error)
		}
		return endpoint, link, fmt.Errorf("linked endpoint %q is unreachable", endpoint.Name)
	}
	want := ModelIDFromOpenCode(link.Model)
	for _, model := range result.Models {
		if model == want || model == link.Model {
			return endpoint, link, nil
		}
	}
	// Manual model ids are allowed when the user linked one deliberately; the
	// endpoint answered, so generation may still work for servers that omit
	// unloaded models from /v1/models.
	if want != "" {
		return endpoint, link, nil
	}
	return endpoint, link, errors.New("linked model is empty")
}

// ProviderConfigFragment builds the OpenCode providers block for the linked
// endpoint. Callers merge it into OPENCODE_CONFIG_CONTENT. The apiKey value is
// intended for the child process only — never log the returned map.
func ProviderConfigFragment(endpoint Endpoint, link Link) map[string]any {
	model := ModelIDFromOpenCode(link.Model)
	return map[string]any{
		ProviderID: map[string]any{
			"name":    "Local",
			"package": "aisdk:@ai-sdk/openai-compatible",
			"settings": map[string]string{
				"apiKey":  EffectiveAPIKey(link),
				"baseURL": OpenAICompatibleBaseURL(endpoint),
			},
			"models": map[string]any{
				model: map[string]any{
					"name": model,
					"capabilities": map[string]any{
						"tools":  true,
						"input":  []string{"text"},
						"output": []string{"text"},
					},
					"cost":  map[string]int{"input": 0, "output": 0},
					"limit": map[string]int{"context": 128000, "output": 8192},
				},
			},
		},
	}
}
