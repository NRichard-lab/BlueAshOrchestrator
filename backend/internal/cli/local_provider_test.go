package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

// localProviderFixture registers a fake Ollama as the endpoint "lab" in a
// fresh data directory. Commands in these tests always name that endpoint, so
// the built-in addresses, which may have real servers behind them on a
// developer's machine, are never probed.
func localProviderFixture(t *testing.T) (*localproviderstest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	server := localproviderstest.NewOllama(t,
		localproviderstest.OllamaModel{Name: "qwen3.8:27b", Capabilities: []string{"completion", "vision", "tools", "thinking"}, MaxContext: 262144, Family: "qwen3", Digest: "sha256:bbb"},
		localproviderstest.OllamaModel{Name: "gpt-oss:20b", Capabilities: []string{"completion", "tools"}, MaxContext: 131072, LoadedContext: 65536, Family: "gptoss"},
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	if _, err := localproviders.Add(dataDir, server.Endpoint("lab", localproviders.KindOllama)); err != nil {
		t.Fatal(err)
	}
	return server, dataDir
}

func TestLocalProviderModelsListsCapabilitiesAndHidesEmbeddingModels(t *testing.T) {
	server, _ := localProviderFixture(t)

	out, _, err := executeCLI(t, Deps{}, "local-provider", "models", "lab")
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	for _, want := range []string{"lab/qwen3.8:27b", "lab/gpt-oss:20b", "yes (detected)", "262144 (detected)", "yes, 65536 context"} {
		if !strings.Contains(out, want) {
			t.Errorf("models output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nomic-embed-text") {
		t.Errorf("an embedding model was listed as a coding model:\n%s", out)
	}

	all, _, err := executeCLI(t, Deps{}, "local-provider", "models", "lab", "--all")
	if err != nil {
		t.Fatalf("models --all: %v", err)
	}
	if !strings.Contains(all, "lab/nomic-embed-text:latest") || !strings.Contains(all, "not usable for chat") {
		t.Errorf("--all must show the embedding model and say why it is not offered:\n%s", all)
	}

	raw, _, err := executeCLI(t, Deps{}, "local-provider", "models", "lab", "--json")
	if err != nil {
		t.Fatalf("models --json: %v", err)
	}
	var discoveries []localproviders.Discovery
	if err := json.Unmarshal([]byte(raw), &discoveries); err != nil {
		t.Fatalf("decode --json: %v\n%s", err, raw)
	}
	if len(discoveries) != 1 || len(discoveries[0].Models) != 2 || len(discoveries[0].EmbeddingModels) != 1 || len(discoveries[0].Capabilities) != 2 {
		t.Errorf("json = %+v, want two chat models and one embedding model", discoveries)
	}
	server.RequireNoGeneration(t)
}

func TestLocalProviderModelsRejectsAnUnknownEndpoint(t *testing.T) {
	localProviderFixture(t)
	_, _, err := executeCLI(t, Deps{}, "local-provider", "models", "nope")
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %v, want a usage error", err)
	}
}

func TestLocalProviderInspectShowsOneModelWithSources(t *testing.T) {
	server, dataDir := localProviderFixture(t)

	out, _, err := executeCLI(t, Deps{}, "local-provider", "inspect", "lab/qwen3.8:27b")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, want := range []string{"lab/qwen3.8:27b", "Tools", "yes (detected)", "262144", "sha256:bbb", "completion, thinking, tools, vision"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output lacks %q:\n%s", want, out)
		}
	}

	// An override in local-models.json wins and is labelled as one.
	context32k := 32768
	if _, err := localproviders.UpdateSettings(dataDir, func(settings *localproviders.Settings) error {
		settings.Models = map[string]localproviders.ModelOverride{"lab/qwen3.8:27b": {Context: &context32k}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _, err := executeCLI(t, Deps{}, "local-provider", "inspect", "lab/qwen3.8:27b", "--json")
	if err != nil {
		t.Fatalf("inspect --json: %v", err)
	}
	var capabilities localproviders.Capabilities
	if err := json.Unmarshal([]byte(raw), &capabilities); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	if capabilities.Context != (localproviders.IntValue{Value: 32768, Source: localproviders.SourceOverride}) || capabilities.MaxContext != 262144 {
		t.Errorf("capabilities = %+v, want the overridden context", capabilities)
	}

	embedding, _, err := executeCLI(t, Deps{}, "local-provider", "inspect", "lab/nomic-embed-text:latest")
	if err != nil || !strings.Contains(embedding, "embedding model") {
		t.Errorf("inspect of an embedding model = %q, %v", embedding, err)
	}
	server.RequireNoGeneration(t)
}

func TestLocalProviderInspectValidatesItsArgument(t *testing.T) {
	localProviderFixture(t)
	for _, args := range [][]string{
		{"local-provider", "inspect"},
		{"local-provider", "inspect", "no-slash"},
		{"local-provider", "inspect", "nope/model"},
	} {
		_, _, err := executeCLI(t, Deps{}, args...)
		var usage usageError
		if !errors.As(err, &usage) {
			t.Errorf("%v: error = %v, want a usage error", args, err)
		}
	}
	// A model the endpoint does not have is a runtime failure, not misuse.
	_, _, err := executeCLI(t, Deps{}, "local-provider", "inspect", "lab/absent:1b")
	var usage usageError
	if err == nil || errors.As(err, &usage) || !errors.Is(err, localproviders.ErrModelNotFound) {
		t.Errorf("missing model error = %v, want ErrModelNotFound and exit 1", err)
	}
}

func TestLocalProviderKeyReadsStdinAndNeverPrintsTheKey(t *testing.T) {
	server, dataDir := localProviderFixture(t)
	server.RequireKey("k-secret")

	// Without the key the keyed server refuses discovery.
	_, errOut, err := executeCLI(t, Deps{}, "local-provider", "models", "lab")
	if err != nil || !strings.Contains(errOut, "unreachable") {
		t.Fatalf("models without key: stderr = %q, err = %v", errOut, err)
	}

	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader("k-secret\n")}, "local-provider", "key", "lab")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if strings.Contains(out+errOut, "k-secret") {
		t.Fatalf("the key was printed: %q %q", out, errOut)
	}
	stored, err := os.ReadFile(filepath.Join(dataDir, "local-models.json"))
	if err != nil || !strings.Contains(string(stored), "k-secret") {
		t.Fatalf("local-models.json = %s, %v", stored, err)
	}

	// The stored key is now sent on metadata requests.
	listed, _, err := executeCLI(t, Deps{}, "local-provider", "models", "lab")
	if err != nil || !strings.Contains(listed, "lab/qwen3.8:27b") {
		t.Fatalf("models with key = %q, %v", listed, err)
	}

	if _, _, err := executeCLI(t, Deps{}, "local-provider", "key", "lab", "--clear"); err != nil {
		t.Fatalf("key --clear: %v", err)
	}
	if endpoint, _ := localproviders.Lookup(dataDir, "lab"); endpoint.APIKey != "" {
		t.Fatalf("key survived --clear")
	}
	server.RequireNoGeneration(t)
}

func TestLocalProviderKeyValidation(t *testing.T) {
	localProviderFixture(t)
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"no endpoint", "k\n", []string{"local-provider", "key"}},
		{"unknown endpoint", "k\n", []string{"local-provider", "key", "nope"}},
		{"empty stdin", "", []string{"local-provider", "key", "lab"}},
		{"blank line", "   \n", []string{"local-provider", "key", "lab"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(tc.stdin)}, tc.args...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("error = %v, want a usage error", err)
			}
		})
	}
}

// There is no way to make Blue Ash AI send a model a prompt from this command
// group: no test, warm-up or ping subcommand.
func TestLocalProviderHasNoCommandThatPromptsAModel(t *testing.T) {
	cmd := newLocalProviderCommand(nil)
	for _, sub := range cmd.Commands() {
		switch sub.Name() {
		case "test", "ping", "warm", "warmup", "preflight", "chat", "run":
			t.Errorf("local-provider %s would send a model a prompt Blue Ash AI wrote", sub.Name())
		}
	}
}
