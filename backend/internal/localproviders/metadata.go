package localproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// metadataRoutes is every route this package may request, with its method.
// They describe a server and its models; none of them runs a model.
//
// POST /api/show is on the list although it is a POST: Ollama takes the model
// name in a body, and answers from the model's files without loading it.
var metadataRoutes = map[string]string{
	"/api/tags":      http.MethodGet,
	"/api/ps":        http.MethodGet,
	"/api/show":      http.MethodPost,
	"/api/version":   http.MethodGet,
	"/v1/models":     http.MethodGet,
	"/api/v0/models": http.MethodGet,
	"/props":         http.MethodGet,
}

// GenerationRoutes are the routes that make a model produce output. Nothing
// in this package may request them; only a turn a person started may, through
// the Local Models runner. Tests that guard that rule read this list.
var GenerationRoutes = []string{
	"/api/chat",
	"/api/generate",
	"/api/embed",
	"/api/embeddings",
	"/v1/chat/completions",
	"/v1/completions",
	"/v1/responses",
	"/v1/embeddings",
}

// IsMetadataRoute reports whether a request is one discovery and inspection
// are allowed to make.
func IsMetadataRoute(method, path string) bool {
	want, ok := metadataRoutes[path]
	return ok && want == method
}

// IsGenerationRoute reports whether a request path, as a server sees it, ends
// in a route that runs a model.
func IsGenerationRoute(path string) bool {
	for _, route := range GenerationRoutes {
		if strings.HasSuffix(strings.TrimRight(path, "/"), route) {
			return true
		}
	}
	return false
}

// RootURL is the address routes are appended to. An endpoint registered with
// the OpenAI-style "/v1" suffix names the same server as one without it.
func RootURL(endpoint Endpoint) string {
	return strings.TrimSuffix(strings.TrimRight(endpoint.BaseURL, "/"), "/v1")
}

// metadataRequest is the only place this package builds an HTTP request. It
// refuses a route that is not in metadataRoutes, so a change that would send a
// model anything fails here rather than reaching a server. body is sent as
// JSON when not nil.
func metadataRequest(ctx context.Context, client *http.Client, endpoint Endpoint, route string, body any) (*http.Response, error) {
	method, ok := metadataRoutes[route]
	if !ok {
		return nil, fmt.Errorf("localproviders: %s is not a metadata route", route)
	}
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	var payload io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, RootURL(endpoint)+route, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key := strings.TrimSpace(endpoint.APIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	return client.Do(request)
}

// errNoAnswer marks a metadata route the server does not have or would not
// answer. Inspection treats it as "nothing reported", not as a failure.
type errNoAnswer struct {
	route  string
	status string
}

func (e *errNoAnswer) Error() string { return e.route + " answered " + e.status }

// metadataJSON requests a metadata route and decodes a 2xx JSON answer into
// out, reading at most maxModelListBytes.
func metadataJSON(ctx context.Context, client *http.Client, endpoint Endpoint, route string, body, out any) error {
	response, err := metadataRequest(ctx, client, endpoint, route, body)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return &errNoAnswer{route: route, status: response.Status}
	}
	decoder := json.NewDecoder(&limitedReader{reader: response.Body, left: maxModelListBytes})
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", route, err)
	}
	return nil
}
