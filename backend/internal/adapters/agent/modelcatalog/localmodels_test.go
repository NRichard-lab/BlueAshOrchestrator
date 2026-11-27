package modelcatalog

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

func localModelsFixture(t *testing.T) (*localproviderstest.Server, map[string]string, string) {
	t.Helper()
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	dir := t.TempDir()
	server := localproviderstest.NewOllama(t,
		localproviderstest.OllamaModel{Name: "qwen3:8b", Capabilities: []string{"completion", "vision", "tools"}, MaxContext: 32768},
		localproviderstest.OllamaModel{Name: "gpt-oss:20b", Capabilities: []string{"completion"}, MaxContext: 131072, LoadedContext: 65536},
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	if _, err := localproviders.Add(dir, server.Endpoint("lab", localproviders.KindOllama)); err != nil {
		t.Fatal(err)
	}
	previous := localModelsClient
	localModelsClient = server.OnlyClient()
	t.Cleanup(func() { localModelsClient = previous })
	return server, map[string]string{localproviders.DataDirEnv: dir}, dir
}

func setLocalModelsDefault(t *testing.T, dir, id string) {
	t.Helper()
	if _, err := localproviders.UpdateSettings(dir, func(settings *localproviders.Settings) error {
		settings.Default = id
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalModelsCatalogListsChatModelsOnly(t *testing.T) {
	server, env, dir := localModelsFixture(t)
	setLocalModelsDefault(t, dir, "lab/gpt-oss:20b")
	catalog, err := discoverLocalModelsCatalog(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("models = %+v, want the two chat models", catalog.Models)
	}
	byID := map[string]int{}
	for i, model := range catalog.Models {
		byID[model.ID] = i
	}
	if _, listed := byID["lab/nomic-embed-text:latest"]; listed {
		t.Error("an embedding model is offered in the model picker")
	}
	qwen := catalog.Models[byID["lab/qwen3:8b"]]
	if qwen.Provider != "lab" || qwen.ContextWindow != 32768 || len(qwen.Inputs) != 2 || qwen.IsDefault {
		t.Errorf("qwen = %+v, want provider lab, 32768 context, text and image input, not default", qwen)
	}
	oss := catalog.Models[byID["lab/gpt-oss:20b"]]
	if oss.ContextWindow != 65536 || !oss.IsDefault {
		t.Errorf("gpt-oss = %+v, want the loaded context 65536 and the default mark", oss)
	}
	server.RequireNoGeneration(t)
}

// A chosen default stays visible when its server does not list it, so the
// picker never silently drops what the user set.
func TestLocalModelsCatalogKeepsAnUnlistedDefault(t *testing.T) {
	_, env, dir := localModelsFixture(t)
	setLocalModelsDefault(t, dir, "lab/loads-on-demand")
	catalog, err := discoverLocalModelsCatalog(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, model := range catalog.Models {
		found = found || (model.ID == "lab/loads-on-demand" && model.IsDefault)
	}
	if !found {
		t.Fatalf("models = %+v, want the unlisted default kept", catalog.Models)
	}
}

// With no server answering the catalog is an error, not an empty success, so
// the service keeps cached models and retries.
func TestLocalModelsCatalogWithNoServerIsAnError(t *testing.T) {
	server, env, _ := localModelsFixture(t)
	server.Close()
	if _, err := discoverLocalModelsCatalog(context.Background(), env); err == nil {
		t.Fatal("no error with every endpoint down")
	}
}

func TestLocalModelsFingerprintFollowsTheServedModels(t *testing.T) {
	server, env, dir := localModelsFixture(t)
	ctx := context.Background()
	first := localModelsDiscoveryFingerprint(ctx, env)
	if again := localModelsDiscoveryFingerprint(ctx, env); again != first {
		t.Fatalf("fingerprint is not stable: %s then %s", first, again)
	}
	setLocalModelsDefault(t, dir, "lab/qwen3:8b")
	afterSettings := localModelsDiscoveryFingerprint(ctx, env)
	if afterSettings == first {
		t.Error("changing the settings file did not change the fingerprint")
	}
	server.RequireNoGeneration(t)
	server.Close()
	if down := localModelsDiscoveryFingerprint(ctx, env); down == afterSettings {
		t.Error("the server going away did not change the fingerprint")
	}
}
