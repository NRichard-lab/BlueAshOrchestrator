package sessionmanager

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHarnessUpdateSuppressionEnvOnlyWhenUpdatesAreCompiledOut(t *testing.T) {
	if got := harnessUpdateSuppressionEnv(domain.HarnessClaudeCode); got != nil {
		t.Fatalf("legacy build must add nothing, got %v", got)
	}
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })

	if got := harnessUpdateSuppressionEnv(domain.HarnessClaudeCode); got["DISABLE_AUTOUPDATER"] != "1" {
		t.Fatalf("claude-code env = %v, want DISABLE_AUTOUPDATER=1", got)
	}
	if got := harnessUpdateSuppressionEnv(""); got["DISABLE_AUTOUPDATER"] != "1" {
		t.Fatalf("admission preflight env = %v, want the union", got)
	}
	if got := harnessUpdateSuppressionEnv(domain.HarnessCodex); len(got) != 0 {
		t.Fatalf("codex is handled by a launch flag, not env; got %v", got)
	}
	// Variables that could not be verified must not creep in.
	for _, h := range []domain.AgentHarness{domain.HarnessClaudeCode, ""} {
		for _, unverified := range []string{"NO_UPDATE_NOTIFIER", "npm_config_update_notifier", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"} {
			if _, present := harnessUpdateSuppressionEnv(h)[unverified]; present {
				t.Fatalf("%s must not be set (unverified or out of scope)", unverified)
			}
		}
	}
}

func TestRuntimeEnvCarriesUpdateSuppressionOnlyWhenCompiledOut(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	t.Setenv("DISABLE_AUTOUPDATER", "")
	legacy := manager.runtimeEnvForHarness("session", "project", "", nil, domain.HarnessClaudeCode)
	if legacy["DISABLE_AUTOUPDATER"] == "1" {
		t.Fatal("legacy build must not suppress the vendor updater")
	}
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
	// A project that tries to turn the updater back on must not win: the brand switch is not user-overridable here.
	env := manager.runtimeEnvForHarness("session", "project", "", map[string]string{"DISABLE_AUTOUPDATER": "0"}, domain.HarnessClaudeCode)
	if env["DISABLE_AUTOUPDATER"] != "1" {
		t.Fatalf("DISABLE_AUTOUPDATER = %q, want 1", env["DISABLE_AUTOUPDATER"])
	}
}
