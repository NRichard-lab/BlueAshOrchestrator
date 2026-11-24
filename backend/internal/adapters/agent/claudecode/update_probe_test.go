package claudecode

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func probeHasUpdaterOff(env []string) bool {
	for _, entry := range env {
		if entry == "DISABLE_AUTOUPDATER=1" {
			return true
		}
	}
	return false
}

func TestClaudeProbesCarryTheUpdaterSwitchOnlyWhenCompiledOut(t *testing.T) {
	t.Setenv("DISABLE_AUTOUPDATER", "")
	auth := func() ([]string, []string) {
		cmd := newClaudeAuthStatusCmd(context.Background(), "claude", "", nil)
		return cmd.Env, cmd.Args
	}
	version := func() ([]string, []string) {
		cmd := newClaudeVersionCmd(context.Background(), "claude")
		return cmd.Env, cmd.Args
	}

	env, args := auth()
	if probeHasUpdaterOff(env) || strings.Join(args[1:], " ") != "auth status" {
		t.Fatalf("legacy auth probe: env has switch=%v args=%v", probeHasUpdaterOff(env), args)
	}
	if env, _ := version(); probeHasUpdaterOff(env) {
		t.Fatal("legacy version probe must not carry the switch")
	}

	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })

	env, args = auth()
	if !probeHasUpdaterOff(env) || strings.Join(args[1:], " ") != "auth status" {
		t.Fatalf("BlueAshAI auth probe must carry DISABLE_AUTOUPDATER=1 and the same argv: env=%v args=%v", probeHasUpdaterOff(env), args)
	}
	if env, _ := version(); !probeHasUpdaterOff(env) {
		t.Fatal("BlueAshAI version probe must carry DISABLE_AUTOUPDATER=1")
	}
}
