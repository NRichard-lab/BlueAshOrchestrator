package sessionmanager

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The suppression values themselves live in internal/harnessupdate (one shared helper for sessions and
// probes); this test pins that a launched session actually receives them, and only when compiled out.
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
