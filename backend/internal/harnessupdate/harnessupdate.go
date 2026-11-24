// Package harnessupdate is the single place that knows how AO asks agent CLIs not to update themselves
// or check for a newer version. Every launch path that starts a harness program (agent sessions,
// chat drivers, auth/readiness/capability probes) takes its switches from here, so the paths cannot
// drift apart.
//
// All suppression is gated on the compile-time brand flag (brand.UpdatesEnabled=false, BlueAshAI).
// With updates allowed (the legacy Orchestrator profile) every function here returns "nothing to add",
// except CodexNoUpdateCheckArgs, which legacy Codex session launches have always passed.
//
// Only switches verified against vendor documentation or the shipped binary are listed:
//
//   - Claude Code: DISABLE_AUTOUPDATER=1 (documented settings/env reference; present in claude.exe).
//   - Codex: -c check_for_update_on_startup=false (documented config key; present in codex.exe).
//   - Cursor: --disable-auto-update (hidden root option in cursor-agent 2026.11.19 and 2026.11.22; the
//     background update check is skipped when it is set; accepted before status, --version and acp;
//     undocumented, so BlueAshAI builds only).
//
// These are requests to third-party programs, not guarantees, and they do not reduce the harnesses'
// other traffic: measured on one machine, `codex app-server` makes the same chatgpt.com connections
// with and without the Codex flag, and `cursor-agent status` makes the same api2.cursor.sh
// connections with and without the Cursor flag. Deliberately NOT included (unverified, or wider than
// updates): CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, NO_UPDATE_NOTIFIER, npm_config_update_notifier.
package harnessupdate

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	claudeAutoUpdaterEnv = "DISABLE_AUTOUPDATER"
	codexUpdateCheckCfg  = "check_for_update_on_startup=false"
	cursorFlag           = "--disable-auto-update"
)

// Suppressed reports whether this build asks harness programs not to update themselves.
func Suppressed() bool { return !brand.UpdatesAllowed() }

// Env is the environment that asks harness to skip its own updater, empty unless Suppressed. An empty
// harness is the admission preflight, which assembles the union of every harness environment.
func Env(harness domain.AgentHarness) map[string]string {
	if !Suppressed() {
		return nil
	}
	env := map[string]string{}
	if harness == domain.HarnessClaudeCode || harness == "" {
		env[claudeAutoUpdaterEnv] = "1"
	}
	return env
}

// ApplyEnv adds Env(harness) to cmd's environment (starting from the process environment when cmd has
// none). Probe launches call this so they carry the same switches as sessions.
func ApplyEnv(cmd *exec.Cmd, harness domain.AgentHarness) {
	cmd.Env = AppendEnv(cmd.Env, harness)
}

// AppendEnv returns env with Env(harness) applied, replacing any existing value of the same key. A nil
// env means "inherit", so it starts from the process environment. Unchanged when not Suppressed.
func AppendEnv(env []string, harness domain.AgentHarness) []string {
	extra := Env(harness)
	if len(extra) == 0 {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+len(extra))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if overridden(extra, key) {
			continue
		}
		out = append(out, entry)
	}
	for key, value := range extra {
		out = append(out, key+"="+value)
	}
	return out
}

func overridden(extra map[string]string, key string) bool {
	if _, ok := extra[key]; ok {
		return true
	}
	if runtime.GOOS == "windows" {
		for k := range extra {
			if strings.EqualFold(k, key) {
				return true
			}
		}
	}
	return false
}

// CodexNoUpdateCheckArgs is the unconditional Codex switch: Codex agent sessions have always been
// launched with it, in every profile.
func CodexNoUpdateCheckArgs() []string { return []string{"-c", codexUpdateCheckCfg} }

// CodexArgs is the same switch for the launch paths that never carried it (account, readiness and
// capability probes and the chat app-server): present only when Suppressed. Put it before the subcommand.
func CodexArgs() []string {
	if !Suppressed() {
		return nil
	}
	return CodexNoUpdateCheckArgs()
}

// CursorArgs is cursor-agent's --disable-auto-update, only when Suppressed. Put it before the subcommand.
func CursorArgs() []string {
	if !Suppressed() {
		return nil
	}
	return []string{cursorFlag}
}

// WithCursorFlag inserts CursorArgs after the binary (argv[0]) of a full argv, leaving it untouched when
// there is nothing to add. The input is never modified.
func WithCursorFlag(argv []string) []string {
	extra := CursorArgs()
	if len(extra) == 0 || len(argv) == 0 {
		return argv
	}
	out := make([]string, 0, len(argv)+len(extra))
	out = append(out, argv[0])
	out = append(out, extra...)
	return append(out, argv[1:]...)
}

// Prefix returns extra followed by args as a fresh slice (args is never modified); when extra is empty
// it returns args unchanged.
func Prefix(extra, args []string) []string {
	if len(extra) == 0 {
		return args
	}
	out := make([]string, 0, len(extra)+len(args))
	out = append(out, extra...)
	return append(out, args...)
}
