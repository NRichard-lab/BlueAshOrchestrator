package cursoracp

import (
	"context"
	"reflect"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func TestAcpLaunchCarriesTheCursorFlagOnlyWhenCompiledOut(t *testing.T) {
	legacy, _, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil || !reflect.DeepEqual(legacy, []string{"acp"}) {
		t.Fatalf("legacy argv = %v (err %v)", legacy, err)
	}
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
	got, _, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil || !reflect.DeepEqual(got, []string{"--disable-auto-update", "acp"}) {
		t.Fatalf("BlueAshAI argv = %v (err %v)", got, err)
	}
}
