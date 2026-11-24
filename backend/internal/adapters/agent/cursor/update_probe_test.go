package cursor

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessupdate"
)

func TestStatusProbeAndSessionLaunchCarryTheCursorFlagOnlyWhenCompiledOut(t *testing.T) {
	session := []string{"cursor-agent", "--force", "hello"}
	if got := statusArgs(); !reflect.DeepEqual(got, []string{"status"}) {
		t.Fatalf("legacy probe argv changed: %v", got)
	}
	if got := harnessupdate.WithCursorFlag(session); !reflect.DeepEqual(got, session) {
		t.Fatalf("legacy session argv changed: %v", got)
	}
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
	if got := statusArgs(); !reflect.DeepEqual(got, []string{"--disable-auto-update", "status"}) {
		t.Fatalf("BlueAshAI probe argv = %v", got)
	}
	if got := harnessupdate.WithCursorFlag(session); !reflect.DeepEqual(got, []string{"cursor-agent", "--disable-auto-update", "--force", "hello"}) {
		t.Fatalf("BlueAshAI session argv = %v", got)
	}
}
