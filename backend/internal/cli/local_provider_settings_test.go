package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

func TestLocalProviderUseSetsAndClearsTheDefault(t *testing.T) {
	server, dataDir := localProviderFixture(t)

	out, _, err := executeCLI(t, Deps{}, "local-provider", "use", "lab/qwen3.8:27b")
	if err != nil || !strings.Contains(out, "lab/qwen3.8:27b") {
		t.Fatalf("use: %v\n%s", err, out)
	}
	settings, err := localproviders.LoadSettings(dataDir)
	if err != nil || settings.Default != "lab/qwen3.8:27b" {
		t.Fatalf("settings = %+v, %v", settings, err)
	}

	if _, _, err := executeCLI(t, Deps{}, "local-provider", "use", "--clear"); err != nil {
		t.Fatalf("use --clear: %v", err)
	}
	if settings, _ = localproviders.LoadSettings(dataDir); settings.Default != "" {
		t.Errorf("default after --clear = %q", settings.Default)
	}
	// Choosing a default loads no model and sends it nothing.
	server.RequireNoGeneration(t)
}

func TestLocalProviderUseRefusesWhatCannotHoldAConversation(t *testing.T) {
	server, dataDir := localProviderFixture(t)
	for name, args := range map[string][]string{
		"embedding model":  {"lab/nomic-embed-text:latest"},
		"model not served": {"lab/not-pulled"},
		"unknown endpoint": {"nowhere/qwen3.8:27b"},
		"no endpoint":      {"qwen3.8:27b"},
		"model and clear":  {"lab/qwen3.8:27b", "--clear"},
		"nothing":          {},
	} {
		if _, _, err := executeCLI(t, Deps{}, append([]string{"local-provider", "use"}, args...)...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if settings, _ := localproviders.LoadSettings(dataDir); settings.Default != "" {
		t.Errorf("a refused use still set the default to %q", settings.Default)
	}
	server.RequireNoGeneration(t)
}

// A server that is off now may be on later, so the default is saved with a
// warning instead of refused.
func TestLocalProviderUseSavesWhenTheServerIsDown(t *testing.T) {
	server, dataDir := localProviderFixture(t)
	server.Close()
	_, stderr, err := executeCLI(t, Deps{}, "local-provider", "use", "lab/qwen3.8:27b")
	if err != nil || !strings.Contains(stderr, "warning") {
		t.Fatalf("use with the server down: err = %v stderr = %q", err, stderr)
	}
	if settings, _ := localproviders.LoadSettings(dataDir); settings.Default != "lab/qwen3.8:27b" {
		t.Errorf("default = %q", settings.Default)
	}
}

func TestLocalProviderInstructionsLevel(t *testing.T) {
	_, dataDir := localProviderFixture(t)
	out, _, err := executeCLI(t, Deps{}, "local-provider", "instructions")
	if err != nil || strings.TrimSpace(out) != "compact" {
		t.Fatalf("default level = %q, %v; want compact", out, err)
	}
	for _, level := range []string{"none", "standard", "compact"} {
		if _, _, err := executeCLI(t, Deps{}, "local-provider", "instructions", level); err != nil {
			t.Fatalf("set %s: %v", level, err)
		}
		settings, err := localproviders.LoadSettings(dataDir)
		if err != nil || string(settings.InstructionsLevel()) != level {
			t.Fatalf("after setting %s: %+v, %v", level, settings, err)
		}
	}
	for _, bad := range []string{"verbose", " "} {
		if _, _, err := executeCLI(t, Deps{}, "local-provider", "instructions", bad); err == nil {
			t.Errorf("level %q accepted", bad)
		}
	}
}

func TestLocalProviderStatusReportsSetupWithoutPromptingAModel(t *testing.T) {
	server, dataDir := localProviderFixture(t)
	if err := localproviders.SetEndpointKey(dataDir, "lab", "sk-secret-value"); err != nil {
		t.Fatal(err)
	}
	server.RequireKey("sk-secret-value")
	if _, _, err := executeCLI(t, Deps{}, "local-provider", "use", "lab/gpt-oss:20b"); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeCLI(t, Deps{}, "local-provider", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"lab/gpt-oss:20b", "compact", "configured (unverified)", "Keys stored for", "lab"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	raw, _, err := executeCLI(t, Deps{}, "local-provider", "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var status localModelsStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	// Other model servers may be running on the machine, so only a floor is
	// asserted for the counts.
	if status.Default != "lab/gpt-oss:20b" || status.Readiness.State != localproviders.ReadinessConfigured || status.Readiness.ChatModels < 2 {
		t.Errorf("status = %+v", status)
	}
	if strings.Contains(out+raw, "sk-secret-value") {
		t.Error("status printed an endpoint key")
	}
	server.RequireNoGeneration(t)
}

// The runner is reachable only as `ao local-agent acp`, and starting it is
// not a way to prompt a model from the command line: it has no prompt flag.
func TestLocalAgentCommandTakesNoPrompt(t *testing.T) {
	cmd := newLocalAgentCommand()
	if !cmd.Hidden {
		t.Error("local-agent is visible before the harness has its documentation")
	}
	acp, _, err := cmd.Find([]string{"acp"})
	if err != nil || acp.Name() != "acp" {
		t.Fatalf("acp subcommand: %v", err)
	}
	for _, name := range []string{"prompt", "message", "test", "warm"} {
		if acp.Flags().Lookup(name) != nil {
			t.Errorf("local-agent acp has a --%s flag", name)
		}
	}
	for _, name := range []string{"model", "instructions-file"} {
		if acp.Flags().Lookup(name) == nil {
			t.Errorf("local-agent acp lacks --%s", name)
		}
	}
}
