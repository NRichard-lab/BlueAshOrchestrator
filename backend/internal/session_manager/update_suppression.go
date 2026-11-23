package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// harnessUpdateSuppressionEnv returns the environment that asks an agent CLI not to update itself or
// check for a newer version, for builds with updates compiled out (brand.UpdatesEnabled=false); it is
// empty otherwise, so legacy builds are unchanged.
//
// Only variables confirmed against the vendor's documentation or the shipped binary are listed. This is a
// request to the vendor's program, not a guarantee: AO cannot control what a third-party CLI does, and a
// CLI absent here (or one that ignores the variable) may still check for updates on its own.
//
//   - Claude Code: DISABLE_AUTOUPDATER=1. Documented in Claude Code's settings reference and present in the
//     claude.exe binary checked.
//
// Not added on purpose (could not be verified, or is broader than updates):
// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC (documented, but it also silences Claude's telemetry, error
// reporting and feedback command, a wider behavior change than this task), and the npm-ecosystem
// NO_UPDATE_NOTIFIER / npm_config_update_notifier conventions (no vendor documentation found for them).
// Codex and Cursor are handled by launch flags (codex -c check_for_update_on_startup=false, cursor-agent
// --disable-auto-update), not environment.
func harnessUpdateSuppressionEnv(harness domain.AgentHarness) map[string]string {
	if brand.UpdatesAllowed() {
		return nil
	}
	env := map[string]string{}
	// An empty harness is the admission preflight, which assembles the union of every harness's env.
	if harness == domain.HarnessClaudeCode || harness == "" {
		env["DISABLE_AUTOUPDATER"] = "1"
	}
	return env
}
