package codexappserver

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func TestCodexProbeAndChatLaunchesCarryTheUpdateCheckSwitchOnlyWhenCompiledOut(t *testing.T) {
	flag := []string{"-c", "check_for_update_on_startup=false"}
	managed := []string{"-c", `cli_auth_credentials_store="file"`, "app-server"}
	if !reflect.DeepEqual(appServerArgs(false), []string{"app-server"}) || !reflect.DeepEqual(appServerArgs(true), managed) {
		t.Fatalf("legacy app-server argv changed: %v / %v", appServerArgs(false), appServerArgs(true))
	}
	if got := schemaProbeArgs("d"); !reflect.DeepEqual(got, []string{"app-server", "generate-json-schema", "--experimental", "--out", "d"}) {
		t.Fatalf("legacy schema probe argv changed: %v", got)
	}
	if got := probeCLIArgs([]string{"login", "--help"}); !reflect.DeepEqual(got, []string{"login", "--help"}) {
		t.Fatalf("legacy CLI probe argv changed: %v", got)
	}

	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })

	cases := map[string][2][]string{
		"app-server":         {appServerArgs(false), append(append([]string{}, flag...), "app-server")},
		"managed app-server": {appServerArgs(true), append(append([]string{}, flag...), managed...)},
		"schema probe":       {schemaProbeArgs("d"), append(append([]string{}, flag...), "app-server", "generate-json-schema", "--experimental", "--out", "d")},
		"cli probe":          {probeCLIArgs([]string{"login", "--help"}), append(append([]string{}, flag...), "login", "--help")},
	}
	for name, c := range cases {
		if !reflect.DeepEqual(c[0], c[1]) {
			t.Errorf("%s: argv = %v, want %v", name, c[0], c[1])
		}
	}
}
