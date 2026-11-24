package harnessupdate

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func compiledOut(t *testing.T) {
	t.Helper()
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
}

func TestLegacyBuildAddsNothingExceptTheAlwaysOnCodexSessionFlag(t *testing.T) {
	if Suppressed() || Env(domain.HarnessClaudeCode) != nil || CodexArgs() != nil || CursorArgs() != nil {
		t.Fatal("legacy build must add nothing")
	}
	in := []string{"cursor-agent", "status"}
	if got := WithCursorFlag(in); !reflect.DeepEqual(got, in) {
		t.Fatalf("legacy argv changed: %v", got)
	}
	if got := Prefix(CodexArgs(), []string{"app-server"}); !reflect.DeepEqual(got, []string{"app-server"}) {
		t.Fatalf("legacy probe argv changed: %v", got)
	}
	if got := CodexNoUpdateCheckArgs(); !reflect.DeepEqual(got, []string{"-c", "check_for_update_on_startup=false"}) {
		t.Fatalf("session flag changed: %v", got)
	}
	cmd := exec.Command("claude", "auth", "status")
	ApplyEnv(cmd, domain.HarnessClaudeCode)
	if cmd.Env != nil {
		t.Fatal("legacy probe environment must stay inherited")
	}
}

func TestCompiledOutBuildCarriesTheVerifiedSwitchesOnly(t *testing.T) {
	compiledOut(t)
	if got := Env(domain.HarnessClaudeCode); !reflect.DeepEqual(got, map[string]string{"DISABLE_AUTOUPDATER": "1"}) {
		t.Fatalf("claude env = %v", got)
	}
	if got := Env(""); got["DISABLE_AUTOUPDATER"] != "1" {
		t.Fatalf("preflight env = %v", got)
	}
	if len(Env(domain.HarnessCodex)) != 0 || len(Env(domain.HarnessCursor)) != 0 {
		t.Fatal("codex and cursor use flags, not environment")
	}
	if got := CodexArgs(); !reflect.DeepEqual(got, []string{"-c", "check_for_update_on_startup=false"}) {
		t.Fatalf("codex args = %v", got)
	}
	if got := CursorArgs(); !reflect.DeepEqual(got, []string{"--disable-auto-update"}) {
		t.Fatalf("cursor args = %v", got)
	}
	// The three switches that could not be verified must never appear anywhere.
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "NO_UPDATE_NOTIFIER", "npm_config_update_notifier"} {
		for _, h := range []domain.AgentHarness{"", domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessCursor} {
			if _, present := Env(h)[key]; present {
				t.Fatalf("%s must not be set", key)
			}
		}
	}
}

func TestPrefixAndWithCursorFlagNeverMutateTheirInput(t *testing.T) {
	compiledOut(t)
	args := []string{"app-server"}
	got := Prefix(CodexArgs(), args)
	if !reflect.DeepEqual(got, []string{"-c", "check_for_update_on_startup=false", "app-server"}) || !reflect.DeepEqual(args, []string{"app-server"}) {
		t.Fatalf("prefix = %v, input = %v", got, args)
	}
	argv := []string{"cursor-agent", "status"}
	if got := WithCursorFlag(argv); !reflect.DeepEqual(got, []string{"cursor-agent", "--disable-auto-update", "status"}) || !reflect.DeepEqual(argv, []string{"cursor-agent", "status"}) {
		t.Fatalf("cursor argv = %v, input = %v", got, argv)
	}
	if len(WithCursorFlag(nil)) != 0 {
		t.Fatal("empty argv must stay empty")
	}
}

func TestAppendEnvReplacesAnExistingValueAndStartsFromTheProcessEnvironment(t *testing.T) {
	compiledOut(t)
	out := AppendEnv([]string{"A=1", "DISABLE_AUTOUPDATER=0", "B=2"}, domain.HarnessClaudeCode)
	if !reflect.DeepEqual(out, []string{"A=1", "B=2", "DISABLE_AUTOUPDATER=1"}) {
		t.Fatalf("env = %v", out)
	}
	t.Setenv("HARNESSUPDATE_PROBE", "kept")
	cmd := exec.Command("claude", "--version")
	ApplyEnv(cmd, domain.HarnessClaudeCode)
	joined := strings.Join(cmd.Env, "\n")
	if !strings.Contains(joined, "DISABLE_AUTOUPDATER=1") || !strings.Contains(joined, "HARNESSUPDATE_PROBE=kept") {
		t.Fatal("probe env must be the process env plus the suppression")
	}
	if got := AppendEnv([]string{"A=1"}, domain.HarnessCodex); !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Fatalf("codex env must be untouched, got %v", got)
	}
}
