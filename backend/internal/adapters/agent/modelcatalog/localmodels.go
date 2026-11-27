package modelcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// localModelsAgentID is the Local Models harness.
const localModelsAgentID = "local-models"

// localModelsClient is the HTTP client the Local Models catalog probes with.
// Nil uses the localproviders default. Tests replace it.
var localModelsClient *http.Client

// localModelsFingerprintTimeout bounds the list request the fingerprint makes
// per endpoint. The fingerprint is computed on cache-first reads, so an
// endpoint that is down must not hold a picker open.
const localModelsFingerprintTimeout = 750 * time.Millisecond

// discoverLocalModelsCatalog lists every chat-capable model on every known
// local endpoint as <endpoint>/<model>, with what was detected about it.
//
// It reads metadata routes only (see localproviders): opening a model picker
// never loads a model and never sends one a prompt. Embedding models are left
// out. A model id can also be typed, for a server that does not list what it
// can load.
func discoverLocalModelsCatalog(ctx context.Context, env map[string]string) (ports.AgentModelCatalog, error) {
	base := Base(localModelsAgentID)
	base.Source = "local-endpoints"
	base.FetchedAt = time.Now().UTC()
	dataDir := localproviders.DataDir(env)
	settings, err := localproviders.LoadSettings(dataDir)
	if err != nil {
		settings = localproviders.Settings{}
	}
	reachable := 0
	models := []ports.AgentModelInfo{}
	for _, discovery := range localproviders.DiscoverAll(ctx, localModelsClient, localproviders.All(dataDir), settings) {
		if !discovery.Reachable {
			continue
		}
		reachable++
		base.Providers = append(base.Providers, discovery.Name)
		for _, capabilities := range discovery.Capabilities {
			info := ports.AgentModelInfo{
				ID: capabilities.ID, Label: capabilities.Model, Provider: capabilities.Endpoint,
				ContextWindow: int64(capabilities.Context.Value),
				Inputs:        []string{"text"},
				IsDefault:     capabilities.ID == settings.Default,
			}
			if capabilities.Vision.Value {
				info.Inputs = append(info.Inputs, "image")
			}
			models = append(models, info)
		}
	}
	if reachable == 0 {
		// An error, not an empty success, so the service keeps any cached
		// models and retries instead of parking an empty catalog for the day.
		return base, errors.New("no local model server is answering; start one (Ollama, llama.cpp, LM Studio) or add its address in Settings")
	}
	if settings.Default != "" {
		listed := false
		for _, item := range models {
			listed = listed || item.ID == settings.Default
		}
		if !listed {
			// Keep the user's chosen default visible when its server is down
			// or does not list models it can load on demand.
			models = append(models, ports.AgentModelInfo{ID: settings.Default, Label: settings.Default, IsDefault: true})
		}
	}
	base.Models = normalize(models)
	return base, nil
}

// localModelsDiscoveryFingerprint covers what the Local Models catalog is
// built from: the endpoints and settings files, and the model list each
// endpoint reports now. The catalog is otherwise revalidated once a day, which
// is too slow for "I just started Ollama" or "I just pulled a model", so the
// lists are part of the fingerprint. Reading them is a metadata request.
func localModelsDiscoveryFingerprint(ctx context.Context, env map[string]string) string {
	dataDir := localproviders.DataDir(env)
	hash := sha256.New()
	_, _ = hash.Write([]byte(fingerprintConfigPaths([]string{
		filepath.Join(dataDir, "local-providers.json"),
		filepath.Join(dataDir, "local-models.json"),
	})))
	client := localModelsClient
	if client == nil {
		client = &http.Client{Timeout: localModelsFingerprintTimeout}
	}
	for _, result := range localproviders.ProbeAll(ctx, client, localproviders.All(dataDir)) {
		_, _ = hash.Write([]byte(result.Name + "\x00" + result.BaseURL + "\x00"))
		if result.Reachable {
			_, _ = hash.Write([]byte("up\x00" + strings.Join(result.Models, "\x00")))
		}
		_, _ = hash.Write([]byte{1})
	}
	return hex.EncodeToString(hash.Sum(nil)[:8])
}
