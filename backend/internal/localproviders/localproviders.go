// Package localproviders discovers model endpoints served on this machine, and
// keeps the ones a user names by hand.
//
// A local engine needs no credential: AO only needs its base URL to ask what it
// can run. Two request shapes cover the engines in use:
//
//   - OpenAI-compatible: GET {base}/v1/models. The de-facto interface for
//     llama.cpp's server, LM Studio, vLLM and most local gateways.
//   - Ollama: GET {base}/api/tags. Ollama answers this alongside its own
//     OpenAI-compatible /v1, and the native route is probed because it is the
//     one Ollama has always had.
//
// Discovery is read-only and local: it lists what an endpoint says it serves
// and never sends a prompt. Reaching a model is still the harness's job, which
// is why an endpoint carries a base URL a harness can be pointed at rather than
// anything AO would call itself.
package localproviders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind is the request shape an endpoint answers.
type Kind string

const (
	// KindOpenAICompatible serves GET /v1/models.
	KindOpenAICompatible Kind = "openai-compatible"
	// KindOllama serves GET /api/tags.
	KindOllama Kind = "ollama"
)

// Kinds are the shapes an endpoint may be registered as.
var Kinds = []Kind{KindOpenAICompatible, KindOllama}

// Endpoint is one local model server.
type Endpoint struct {
	// Name identifies the endpoint to the user and in commands. Unique.
	Name string `json:"name"`
	// BaseURL is the server's root, without a trailing path ("http://127.0.0.1:8080").
	BaseURL string `json:"baseUrl"`
	Kind    Kind   `json:"kind"`
	// Builtin marks an address AO probes without being told about it. Builtin
	// endpoints are never written to the store.
	Builtin bool `json:"-"`
}

// Result is what one probe found.
type Result struct {
	Endpoint
	Reachable bool `json:"reachable"`
	// Models are the ids the endpoint reported, sorted. Empty from a server that
	// is running but has nothing pulled, which is not an error.
	Models []string `json:"models,omitempty"`
	// Error explains an unreachable endpoint in the server's or transport's own
	// words. Empty when Reachable.
	Error string `json:"error,omitempty"`
}

// builtins are the addresses AO probes with no configuration: the defaults
// Ollama and llama.cpp's server listen on.
//
// Loopback only. A local engine usually binds 127.0.0.1, and probing anything
// else by default would have AO reaching across the network unasked.
var builtins = []Endpoint{
	{Name: "ollama", BaseURL: "http://127.0.0.1:11434", Kind: KindOllama, Builtin: true},
	{Name: "llama.cpp", BaseURL: "http://127.0.0.1:8080", Kind: KindOpenAICompatible, Builtin: true},
}

// Builtin returns the endpoints AO probes without being configured.
func Builtin() []Endpoint {
	out := make([]Endpoint, len(builtins))
	copy(out, builtins)
	return out
}

// storeName is the file under the data directory holding user-named endpoints.
const storeName = "local-providers.json"

// ValidateName rejects a name that could not be typed back at a command or
// that would collide with a built-in.
func ValidateName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("endpoint name is required")
	}
	if strings.ContainsAny(trimmed, " \t\n/\\") {
		return fmt.Errorf("endpoint name %q cannot contain spaces or slashes", name)
	}
	for _, builtin := range builtins {
		if strings.EqualFold(trimmed, builtin.Name) {
			return fmt.Errorf("%q is a built-in endpoint and is probed already", trimmed)
		}
	}
	return nil
}

// ValidateBaseURL accepts an absolute http(s) URL with no query or fragment.
// A path is kept: a gateway may serve its API under one.
func ValidateBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("endpoint URL is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("endpoint URL %q: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint URL %q must start with http:// or https://", raw)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("endpoint URL %q names no host", raw)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("endpoint URL %q must not carry a query or fragment", raw)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

// ValidateKind accepts a known shape, defaulting to OpenAI-compatible, which is
// what an unfamiliar local server is most likely to speak.
func ValidateKind(raw string) (Kind, error) {
	switch trimmed := Kind(strings.TrimSpace(raw)); trimmed {
	case "":
		return KindOpenAICompatible, nil
	case KindOpenAICompatible, KindOllama:
		return trimmed, nil
	default:
		return "", fmt.Errorf("unknown endpoint kind %q; use %s or %s", raw, KindOpenAICompatible, KindOllama)
	}
}

// Load reads the user-named endpoints. A missing or unreadable store is no
// endpoints rather than an error: discovery still has its built-ins to report.
func Load(dataDir string) []Endpoint {
	data, err := os.ReadFile(filepath.Join(dataDir, storeName))
	if err != nil {
		return nil
	}
	var stored []Endpoint
	if json.Unmarshal(data, &stored) != nil {
		return nil
	}
	kept := make([]Endpoint, 0, len(stored))
	for _, endpoint := range stored {
		// A hand-edited file must not introduce an endpoint the commands could
		// not address, or a second entry shadowing the first.
		if ValidateName(endpoint.Name) != nil {
			continue
		}
		base, err := ValidateBaseURL(endpoint.BaseURL)
		if err != nil {
			continue
		}
		kind, err := ValidateKind(string(endpoint.Kind))
		if err != nil {
			continue
		}
		endpoint.BaseURL, endpoint.Kind, endpoint.Builtin = base, kind, false
		kept = append(kept, endpoint)
	}
	return dedupeByName(kept)
}

func dedupeByName(endpoints []Endpoint) []Endpoint {
	seen := make(map[string]bool, len(endpoints))
	out := make([]Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		key := strings.ToLower(endpoint.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, endpoint)
	}
	return out
}

func save(dataDir string, endpoints []Endpoint) error {
	data, err := json.MarshalIndent(endpoints, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("local providers: %w", err)
	}
	path := filepath.Join(dataDir, storeName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("local providers: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("local providers: %w", err)
	}
	return nil
}

// Add stores an endpoint, replacing one of the same name. The endpoint is
// validated first, so the store never holds an entry the commands cannot use.
func Add(dataDir string, endpoint Endpoint) (Endpoint, error) {
	if err := ValidateName(endpoint.Name); err != nil {
		return Endpoint{}, err
	}
	base, err := ValidateBaseURL(endpoint.BaseURL)
	if err != nil {
		return Endpoint{}, err
	}
	kind, err := ValidateKind(string(endpoint.Kind))
	if err != nil {
		return Endpoint{}, err
	}
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.BaseURL, endpoint.Kind, endpoint.Builtin = base, kind, false

	kept := make([]Endpoint, 0, len(Load(dataDir))+1)
	for _, existing := range Load(dataDir) {
		if !strings.EqualFold(existing.Name, endpoint.Name) {
			kept = append(kept, existing)
		}
	}
	kept = append(kept, endpoint)
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })
	if err := save(dataDir, kept); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

// Remove forgets a named endpoint. Removing one that is not there is an error:
// a silent success would read as "it is gone" when the name was a typo.
func Remove(dataDir, name string) error {
	trimmed := strings.TrimSpace(name)
	existing := Load(dataDir)
	kept := make([]Endpoint, 0, len(existing))
	found := false
	for _, endpoint := range existing {
		if strings.EqualFold(endpoint.Name, trimmed) {
			found = true
			continue
		}
		kept = append(kept, endpoint)
	}
	if !found {
		return fmt.Errorf("no local provider named %q", name)
	}
	return save(dataDir, kept)
}

// All returns the endpoints to probe: the built-ins first, then the user's,
// with a stored endpoint that repeats a built-in URL dropped.
func All(dataDir string) []Endpoint {
	out := Builtin()
	taken := make(map[string]bool, len(out))
	for _, endpoint := range out {
		taken[endpoint.BaseURL] = true
	}
	for _, endpoint := range Load(dataDir) {
		if taken[endpoint.BaseURL] {
			continue
		}
		taken[endpoint.BaseURL] = true
		out = append(out, endpoint)
	}
	return out
}

// modelsPath is the route each shape answers with its model list.
func modelsPath(kind Kind) string {
	if kind == KindOllama {
		return "/api/tags"
	}
	return "/v1/models"
}

// DefaultTimeout bounds one probe. A local server answers in milliseconds; this
// is long enough for a loaded one and short enough that probing several
// addresses, most of them closed, stays quick.
const DefaultTimeout = 2 * time.Second

// Probe asks one endpoint what it serves. A refused connection, a timeout or a
// non-2xx answer all mean "not reachable", with the reason kept for the user:
// the common case is simply that the engine is not running.
func Probe(ctx context.Context, client *http.Client, endpoint Endpoint) Result {
	result := Result{Endpoint: endpoint}
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.BaseURL+modelsPath(endpoint.Kind), http.NoBody)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	response, err := client.Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		result.Error = fmt.Sprintf("%s answered %s", modelsPath(endpoint.Kind), response.Status)
		return result
	}
	models, err := parseModels(endpoint.Kind, response.Body)
	if err != nil {
		// Answering the route with something else is the signal that a URL
		// points at a server of another shape, so it is reported, not hidden.
		result.Error = err.Error()
		return result
	}
	result.Reachable = true
	result.Models = models
	return result
}

// ProbeAll probes every endpoint, keeping the input order so a list reads the
// same way twice.
func ProbeAll(ctx context.Context, client *http.Client, endpoints []Endpoint) []Result {
	results := make([]Result, 0, len(endpoints))
	for _, endpoint := range endpoints {
		results = append(results, Probe(ctx, client, endpoint))
	}
	return results
}

// maxModelListBytes caps a model list. A local server is trusted no more than
// any other HTTP peer, and an unbounded decode would hand it this process's
// memory.
const maxModelListBytes = 4 << 20

func parseModels(kind Kind, body interface{ Read([]byte) (int, error) }) ([]string, error) {
	decoder := json.NewDecoder(&limitedReader{reader: body, left: maxModelListBytes})
	ids := []string{}
	if kind == KindOllama {
		var payload struct {
			Models []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"models"`
		}
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("decode /api/tags: %w", err)
		}
		for _, model := range payload.Models {
			// Ollama repeats the tag in both fields; `name` is the one its own
			// CLI prints.
			if id := strings.TrimSpace(firstNonEmpty(model.Name, model.Model)); id != "" {
				ids = append(ids, id)
			}
		}
	} else {
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("decode /v1/models: %w", err)
		}
		for _, model := range payload.Data {
			if id := strings.TrimSpace(model.ID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// limitedReader fails instead of truncating, so a body over the cap is reported
// as a bad answer rather than silently decoded as far as it fit.
type limitedReader struct {
	reader interface{ Read([]byte) (int, error) }
	left   int64
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, errors.New("model list is larger than 4MiB")
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	n, err := r.reader.Read(p)
	r.left -= int64(n)
	return n, err
}
