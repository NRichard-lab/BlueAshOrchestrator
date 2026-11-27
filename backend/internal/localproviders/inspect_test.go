package localproviders_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

// machineModels is the Ollama server the design was checked against.
func machineModels() []localproviderstest.OllamaModel {
	return []localproviderstest.OllamaModel{
		{Name: "gpt-oss-20b-mxfp4:latest", Capabilities: []string{"tools", "completion", "thinking"}, MaxContext: 131072, LoadedContext: 131072, Family: "gptoss", Digest: "sha256:aaa"},
		{Name: "qwen3.8:27b", Capabilities: []string{"completion", "vision", "tools", "thinking"}, MaxContext: 262144, Family: "qwen3", Digest: "sha256:bbb"},
		{Name: "gemma4:e2b", Capabilities: []string{"completion", "vision", "audio", "tools", "thinking"}, MaxContext: 131072, NumCtx: 32768, Family: "gemma4", Digest: "sha256:ccc"},
		{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048, Family: "nomic-bert", Digest: "sha256:ddd"},
	}
}

func TestInspectReadsOllamaCapabilities(t *testing.T) {
	server := localproviderstest.NewOllama(t, machineModels()...)
	endpoint := server.Endpoint("ollama", localproviders.KindOllama)

	loaded, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "gpt-oss-20b-mxfp4:latest")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if loaded.ID != "ollama/gpt-oss-20b-mxfp4:latest" || !loaded.Chat || !loaded.Loaded {
		t.Errorf("loaded model = %+v, want a loaded chat model with its endpoint-qualified id", loaded)
	}
	if loaded.Tools != (localproviders.BoolValue{Value: true, Source: localproviders.SourceDetected}) {
		t.Errorf("tools = %+v, want detected true", loaded.Tools)
	}
	if loaded.Thinking.Value != true || loaded.Vision != (localproviders.BoolValue{Value: false, Source: localproviders.SourceDetected}) {
		t.Errorf("thinking = %+v vision = %+v, want thinking on and a detected no for vision", loaded.Thinking, loaded.Vision)
	}
	if loaded.Context != (localproviders.IntValue{Value: 131072, Source: localproviders.SourceDetected}) || loaded.LoadedContext != 131072 {
		t.Errorf("context = %+v loaded = %d, want the allocated 131072", loaded.Context, loaded.LoadedContext)
	}
	if loaded.Digest != "sha256:aaa" || loaded.Family != "gptoss" {
		t.Errorf("digest = %q family = %q", loaded.Digest, loaded.Family)
	}

	// Not loaded: the model's maximum is all the server can say.
	unloaded, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "qwen3.8:27b")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if unloaded.Loaded || unloaded.MaxContext != 262144 || unloaded.Context.Value != 262144 || !unloaded.Vision.Value {
		t.Errorf("unloaded model = %+v, want its 262144 maximum and vision", unloaded)
	}

	// A Modelfile that pins num_ctx beats the model's maximum.
	pinned, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "gemma4:e2b")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if pinned.Context != (localproviders.IntValue{Value: 32768, Source: localproviders.SourceDetected}) || pinned.MaxContext != 131072 {
		t.Errorf("pinned context = %+v max = %d, want the Modelfile's 32768 under a 131072 maximum", pinned.Context, pinned.MaxContext)
	}
	server.RequireNoGeneration(t)
}

func TestInspectClampsAPinnedContextToTheModelMaximum(t *testing.T) {
	// Seen on a real server: a Modelfile pinning num_ctx 8192 on a model whose
	// maximum is 2048.
	server := localproviderstest.NewOllama(t, localproviderstest.OllamaModel{
		Name: "small:latest", Capabilities: []string{"completion"}, MaxContext: 2048, NumCtx: 8192,
	})
	capabilities, err := localproviders.Inspect(context.Background(), server.Client(), server.Endpoint("ollama", localproviders.KindOllama), "small:latest")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if capabilities.Context != (localproviders.IntValue{Value: 2048, Source: localproviders.SourceDetected}) {
		t.Errorf("context = %+v, want it clamped to the 2048 maximum", capabilities.Context)
	}
}

func TestInspectMarksAnEmbeddingModelAsNotChat(t *testing.T) {
	server := localproviderstest.NewOllama(t, machineModels()...)
	endpoint := server.Endpoint("ollama", localproviders.KindOllama)
	embedding, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "nomic-embed-text:latest")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if embedding.Chat || embedding.Tools.Value {
		t.Errorf("embedding model = %+v, want chat false and no tools", embedding)
	}
	if !reflect.DeepEqual(embedding.Reported, []string{"embedding"}) {
		t.Errorf("reported = %v", embedding.Reported)
	}
}

func TestDiscoverHidesEmbeddingModelsFromTheModelList(t *testing.T) {
	server := localproviderstest.NewOllama(t, machineModels()...)
	endpoint := server.Endpoint("ollama", localproviders.KindOllama)
	discovery := localproviders.Discover(context.Background(), server.Client(), endpoint, localproviders.Settings{})
	if !discovery.Reachable {
		t.Fatalf("discovery = %+v, want reachable", discovery)
	}
	want := []string{"gemma4:e2b", "gpt-oss-20b-mxfp4:latest", "qwen3.8:27b"}
	if !reflect.DeepEqual(discovery.Models, want) {
		t.Errorf("models = %v, want %v with the embedding model left out", discovery.Models, want)
	}
	if !reflect.DeepEqual(discovery.EmbeddingModels, []string{"nomic-embed-text:latest"}) {
		t.Errorf("embedding models = %v", discovery.EmbeddingModels)
	}
	if len(discovery.Capabilities) != len(want) {
		t.Fatalf("capabilities = %d entries, want one per listed model", len(discovery.Capabilities))
	}
	for i, capabilities := range discovery.Capabilities {
		if capabilities.Model != want[i] || !capabilities.Tools.Value {
			t.Errorf("capabilities[%d] = %+v, want %s with tools", i, capabilities, want[i])
		}
	}
	server.RequireNoGeneration(t)
}

func TestDiscoverAppliesUserOverrides(t *testing.T) {
	server := localproviderstest.NewOllama(t, machineModels()...)
	endpoint := server.Endpoint("ollama", localproviders.KindOllama)
	context32k, noTools := 32768, false
	settings := localproviders.Settings{Models: map[string]localproviders.ModelOverride{
		"ollama/qwen3.8:27b": {Context: &context32k, Tools: &noTools},
	}}
	discovery := localproviders.Discover(context.Background(), server.Client(), endpoint, settings)
	for _, capabilities := range discovery.Capabilities {
		if capabilities.Model != "qwen3.8:27b" {
			continue
		}
		if capabilities.Context != (localproviders.IntValue{Value: 32768, Source: localproviders.SourceOverride}) {
			t.Errorf("context = %+v, want the override", capabilities.Context)
		}
		if capabilities.Tools != (localproviders.BoolValue{Value: false, Source: localproviders.SourceOverride}) {
			t.Errorf("tools = %+v, want the override", capabilities.Tools)
		}
		if capabilities.Thinking.Source != localproviders.SourceDetected {
			t.Errorf("thinking = %+v, want it left as detected", capabilities.Thinking)
		}
		return
	}
	t.Fatal("qwen3.8:27b missing from discovery")
}

func TestInspectAssumesSmallDefaultsWhenNothingIsReported(t *testing.T) {
	// An Ollama too old to send a capabilities list, and a model with no
	// context length in model_info.
	server := localproviderstest.NewOllama(t, localproviderstest.OllamaModel{Name: "old:latest"})
	endpoint := server.Endpoint("ollama", localproviders.KindOllama)
	capabilities, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "old:latest")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !capabilities.Chat {
		t.Error("a model that reports nothing must still be offered for chat")
	}
	if capabilities.Tools != (localproviders.BoolValue{Value: false, Source: localproviders.SourceAssumed}) {
		t.Errorf("tools = %+v, want assumed off", capabilities.Tools)
	}
	if capabilities.Context != (localproviders.IntValue{Value: localproviders.AssumedContext, Source: localproviders.SourceAssumed}) || localproviders.AssumedContext != 8192 {
		t.Errorf("context = %+v, want assumed 8192", capabilities.Context)
	}
}

func TestInspectReportsAMissingModelAndAnUnreachableEndpoint(t *testing.T) {
	server := localproviderstest.NewOllama(t, machineModels()...)
	endpoint := server.Endpoint("ollama", localproviders.KindOllama)
	_, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "nope:1b")
	if !errors.Is(err, localproviders.ErrModelNotFound) {
		t.Errorf("missing model error = %v, want ErrModelNotFound", err)
	}

	bare := localproviderstest.New(t)
	bare.HandleJSON("GET /v1/models", map[string]any{"data": []map[string]string{{"id": "only"}}})
	_, err = localproviders.Inspect(context.Background(), bare.Client(), bare.Endpoint("lab", localproviders.KindOpenAICompatible), "other")
	if !errors.Is(err, localproviders.ErrModelNotFound) {
		t.Errorf("unlisted model error = %v, want ErrModelNotFound", err)
	}

	closed := localproviderstest.New(t)
	closedEndpoint := closed.Endpoint("gone", localproviders.KindOllama)
	closed.Close()
	_, err = localproviders.Inspect(context.Background(), &http.Client{}, closedEndpoint, "any")
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("unreachable error = %v", err)
	}
}

func TestInspectReadsOpenAICompatibleServers(t *testing.T) {
	t.Run("ids only", func(t *testing.T) {
		server := localproviderstest.New(t)
		server.HandleJSON("GET /v1/models", map[string]any{"data": []map[string]any{{"id": "a"}, {"id": "b"}}})
		capabilities, err := localproviders.Inspect(context.Background(), server.Client(), server.Endpoint("gw", localproviders.KindOpenAICompatible), "a")
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		if !capabilities.Chat || capabilities.Tools.Source != localproviders.SourceAssumed || capabilities.Context.Value != 8192 {
			t.Errorf("capabilities = %+v, want everything assumed", capabilities)
		}
		server.RequireNoGeneration(t)
	})
	t.Run("vLLM max_model_len", func(t *testing.T) {
		server := localproviderstest.New(t)
		server.HandleJSON("GET /v1/models", map[string]any{"data": []map[string]any{{"id": "org/coder", "max_model_len": 65536}}})
		capabilities, err := localproviders.Inspect(context.Background(), server.Client(), server.Endpoint("vllm", localproviders.KindOpenAICompatible), "org/coder")
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		if capabilities.ID != "vllm/org/coder" || capabilities.Context != (localproviders.IntValue{Value: 65536, Source: localproviders.SourceDetected}) {
			t.Errorf("capabilities = %+v, want a detected 65536 context", capabilities)
		}
	})
	t.Run("llama.cpp props", func(t *testing.T) {
		server := localproviderstest.New(t)
		server.HandleJSON("GET /v1/models", map[string]any{"data": []map[string]any{{"id": "coder.gguf", "meta": map[string]any{"n_ctx_train": 131072}}}})
		server.HandleJSON("GET /props", map[string]any{
			"default_generation_settings": map[string]any{"n_ctx": 16384},
			"model_path":                  "/models/coder.gguf",
			"modalities":                  map[string]any{"vision": false},
			"chat_template_caps":          map[string]any{"supports_tools": true},
		})
		capabilities, err := localproviders.Inspect(context.Background(), server.Client(), server.Endpoint("llama.cpp", localproviders.KindOpenAICompatible), "coder.gguf")
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		if capabilities.Context.Value != 16384 || capabilities.MaxContext != 131072 || !capabilities.Loaded {
			t.Errorf("capabilities = %+v, want the loaded 16384 under a 131072 maximum", capabilities)
		}
		if capabilities.Tools != (localproviders.BoolValue{Value: true, Source: localproviders.SourceDetected}) {
			t.Errorf("tools = %+v, want detected from the chat template", capabilities.Tools)
		}
	})
	t.Run("LM Studio", func(t *testing.T) {
		server := localproviderstest.New(t)
		server.HandleJSON("GET /v1/models", map[string]any{"data": []map[string]any{{"id": "qwen-coder"}, {"id": "text-embedding-nomic"}}})
		server.HandleJSON("GET /api/v0/models", map[string]any{"data": []map[string]any{
			{"id": "qwen-coder", "type": "llm", "state": "loaded", "max_context_length": 32768, "loaded_context_length": 8192, "capabilities": []string{"tool_use"}, "quantization": "Q4_K_M", "arch": "qwen2"},
			{"id": "text-embedding-nomic", "type": "embeddings", "state": "not-loaded", "max_context_length": 2048},
		}})
		endpoint := server.Endpoint("studio", localproviders.KindOpenAICompatible)
		discovery := localproviders.Discover(context.Background(), server.Client(), endpoint, localproviders.Settings{})
		if !reflect.DeepEqual(discovery.Models, []string{"qwen-coder"}) || !reflect.DeepEqual(discovery.EmbeddingModels, []string{"text-embedding-nomic"}) {
			t.Fatalf("models = %v embedding = %v, want the embedding model hidden", discovery.Models, discovery.EmbeddingModels)
		}
		coder := discovery.Capabilities[0]
		if coder.Context.Value != 8192 || coder.MaxContext != 32768 || !coder.Tools.Value || coder.Vision.Source != localproviders.SourceDetected {
			t.Errorf("capabilities = %+v", coder)
		}
		server.RequireNoGeneration(t)
	})
}

func TestMetadataRequestsCarryTheEndpointKey(t *testing.T) {
	server := localproviderstest.NewOllama(t, machineModels()...)
	server.RequireKey("s3cret")
	endpoint := server.Endpoint("locked", localproviders.KindOllama)

	if result := localproviders.Probe(context.Background(), server.Client(), endpoint); result.Reachable {
		t.Fatal("a keyed server answered a probe that sent no key")
	}
	endpoint.APIKey = "s3cret"
	result := localproviders.Probe(context.Background(), server.Client(), endpoint)
	if !result.Reachable || len(result.Models) != 4 {
		t.Fatalf("probe with key = %+v, want reachable with 4 models", result)
	}
	capabilities, err := localproviders.Inspect(context.Background(), server.Client(), endpoint, "qwen3.8:27b")
	if err != nil || !capabilities.Tools.Value {
		t.Fatalf("inspect with key = %+v, %v", capabilities, err)
	}
}

func TestOnlyMetadataRoutesAreAllowed(t *testing.T) {
	for _, route := range localproviders.GenerationRoutes {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if localproviders.IsMetadataRoute(method, route) {
				t.Errorf("%s %s is a generation route and must not be a metadata route", method, route)
			}
		}
		if !localproviders.IsGenerationRoute(route) || !localproviders.IsGenerationRoute("/gateway"+route) {
			t.Errorf("IsGenerationRoute(%q) = false", route)
		}
	}
	for _, route := range []string{"/api/tags", "/api/ps", "/api/show", "/v1/models", "/props", "/api/v0/models"} {
		if localproviders.IsGenerationRoute(route) {
			t.Errorf("%s is a metadata route and must not count as generation", route)
		}
	}
}

func TestRootURLTreatsATrailingV1AsTheSameServer(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:1234":       "http://127.0.0.1:1234",
		"http://127.0.0.1:1234/v1":    "http://127.0.0.1:1234",
		"http://127.0.0.1:1234/v1/":   "http://127.0.0.1:1234",
		"http://gw.local/models/team": "http://gw.local/models/team",
	} {
		if got := localproviders.RootURL(localproviders.Endpoint{BaseURL: raw}); got != want {
			t.Errorf("RootURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
