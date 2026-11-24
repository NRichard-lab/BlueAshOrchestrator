package codex

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func TestLoginStatusProbeCarriesTheUpdateCheckSwitchOnlyWhenCompiledOut(t *testing.T) {
	if got := loginStatusArgs(); !reflect.DeepEqual(got, []string{"login", "status"}) {
		t.Fatalf("legacy probe argv changed: %v", got)
	}
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
	want := []string{"-c", "check_for_update_on_startup=false", "login", "status"}
	if got := loginStatusArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("probe argv = %v, want %v", got, want)
	}
}

// Sessions and probes must use the one shared definition of the switch.
func TestSessionAndProbeShareTheSameSwitch(t *testing.T) {
	var cmd []string
	appendNoUpdateCheckFlag(&cmd)
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
	if !reflect.DeepEqual(cmd, loginStatusArgs()[:2]) {
		t.Fatalf("session flag %v and probe flag %v drifted apart", cmd, loginStatusArgs()[:2])
	}
}
