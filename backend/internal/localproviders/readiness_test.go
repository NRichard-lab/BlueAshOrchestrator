package localproviders_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

func readinessFixture(t *testing.T, models ...localproviderstest.OllamaModel) (*localproviderstest.Server, string) {
	t.Helper()
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	dir := t.TempDir()
	server := localproviderstest.NewOllama(t, models...)
	if _, err := localproviders.Add(dir, server.Endpoint("lab", localproviders.KindOllama)); err != nil {
		t.Fatal(err)
	}
	return server, dir
}

func TestReadinessIsConfiguredWhenAChatModelIsListed(t *testing.T) {
	server, dir := readinessFixture(t,
		localproviderstest.OllamaModel{Name: "qwen3:8b", Capabilities: []string{"completion", "tools"}, MaxContext: 32768},
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	got := localproviders.CheckReadiness(context.Background(), server.OnlyClient(), dir)
	if got.State != localproviders.ReadinessConfigured || got.Reachable != 1 || got.ChatModels != 1 || got.Reason != "" {
		t.Fatalf("readiness = %+v, want configured with one endpoint and one chat model", got)
	}
	server.RequireNoGeneration(t)
}

// An endpoint that serves only an embedding model is not ready: there is
// nothing on it that can hold a conversation.
func TestReadinessNeedsSetupWithOnlyAnEmbeddingModel(t *testing.T) {
	server, dir := readinessFixture(t,
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	got := localproviders.CheckReadiness(context.Background(), server.OnlyClient(), dir)
	if got.State != localproviders.ReadinessNeedsSetup || got.Reachable != 1 || got.ChatModels != 0 || got.Reason == "" {
		t.Fatalf("readiness = %+v, want needs-setup with a reason", got)
	}
	server.RequireNoGeneration(t)
}

func TestReadinessNeedsSetupWhenNothingAnswers(t *testing.T) {
	server, dir := readinessFixture(t)
	client := server.OnlyClient()
	server.Close()
	got := localproviders.CheckReadiness(context.Background(), client, dir)
	if got.State != localproviders.ReadinessNeedsSetup || got.Reachable != 0 || got.Reason == "" {
		t.Fatalf("readiness = %+v, want needs-setup with a reason", got)
	}
}

// TestNothingOutsideATurnReachesAGenerationRoute is the rule's second
// enforcement test. It runs every read path this package offers (the ones
// behind the picker, Settings, readiness and the CLI) against one server and
// requires that none of them made a generation request. The first enforcement
// test, in internal/localagent, pins that only the runner can import the
// generation client at all.
func TestNothingOutsideATurnReachesAGenerationRoute(t *testing.T) {
	server, dir := readinessFixture(t,
		localproviderstest.OllamaModel{Name: "qwen3:8b", Capabilities: []string{"completion", "tools", "thinking"}, MaxContext: 32768, Digest: "sha256:aaa"},
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	ctx := context.Background()
	client := server.OnlyClient()
	endpoint, err := localproviders.Lookup(dir, "lab")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := localproviders.LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	localproviders.Probe(ctx, client, endpoint)
	localproviders.ProbeAll(ctx, client, localproviders.All(dir))
	localproviders.Discover(ctx, client, endpoint, settings)
	localproviders.DiscoverAll(ctx, client, localproviders.All(dir), settings)
	for _, model := range []string{"qwen3:8b", "nomic-embed-text:latest", "not-pulled"} {
		_, _ = localproviders.Inspect(ctx, client, endpoint, model)
	}
	localproviders.InspectAll(ctx, client, endpoint, []string{"qwen3:8b", "nomic-embed-text:latest"})
	localproviders.CheckReadiness(ctx, client, dir)

	if len(server.Requests()) == 0 {
		t.Fatal("the fake server saw no request at all; the test proves nothing")
	}
	server.RequireNoGeneration(t)
	for _, request := range server.Requests() {
		method, path, _ := cutRequest(request)
		if !localproviders.IsMetadataRoute(method, path) {
			t.Errorf("%s is not on the metadata allowlist", request)
		}
	}
}

func cutRequest(request string) (method, path string, ok bool) {
	for i := 0; i < len(request); i++ {
		if request[i] == ' ' {
			return request[:i], request[i+1:], true
		}
	}
	return request, "", false
}
