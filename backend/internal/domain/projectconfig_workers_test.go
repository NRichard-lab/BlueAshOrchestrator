package domain

import (
	"reflect"
	"testing"
)

func TestActiveWorkersPutsTheDefaultFirst(t *testing.T) {
	config := ProjectConfig{
		Worker:  RoleOverride{Harness: HarnessCodex},
		Workers: []RoleOverride{{Harness: HarnessClaudeCode}, {Harness: HarnessAider}},
	}
	want := []RoleOverride{{Harness: HarnessCodex}, {Harness: HarnessClaudeCode}, {Harness: HarnessAider}}
	if got := config.ActiveWorkers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("active workers = %+v, want the default first then the extras: %+v", got, want)
	}
}

// The list's order comes from a picker, so it must not be able to change which
// harness a spawn with no --agent resolves to.
func TestActiveWorkersDropsRepeatsKeepingTheDefault(t *testing.T) {
	config := ProjectConfig{
		Worker: RoleOverride{Harness: HarnessClaudeCode},
		Workers: []RoleOverride{
			{Harness: HarnessCodex},
			{Harness: HarnessClaudeCode, AgentConfig: AgentConfig{Model: "ignored-duplicate"}},
			{Harness: HarnessCodex},
		},
	}
	got := config.ActiveWorkers()
	want := []RoleOverride{{Harness: HarnessClaudeCode}, {Harness: HarnessCodex}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active workers = %+v, want %+v", got, want)
	}
}

func TestActiveWorkersIsEmptyWithoutAPinnedAgent(t *testing.T) {
	// No worker pinned at all: the caller keeps its own fallback rather than
	// being handed one blank harness.
	if got := (ProjectConfig{}).ActiveWorkers(); len(got) != 0 {
		t.Fatalf("active workers = %+v, want none", got)
	}
	// Extras without a default still answer with the extras, never a blank.
	config := ProjectConfig{Workers: []RoleOverride{{Harness: HarnessCodex}}}
	want := []RoleOverride{{Harness: HarnessCodex}}
	if got := config.ActiveWorkers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("active workers = %+v, want %+v", got, want)
	}
}

func TestActiveWorkersCarriesEachHarnessConfig(t *testing.T) {
	config := ProjectConfig{
		Worker:  RoleOverride{Harness: HarnessClaudeCode, AgentConfig: AgentConfig{Model: "opus"}},
		Workers: []RoleOverride{{Harness: HarnessCodex, AgentConfig: AgentConfig{Effort: "high"}}},
	}
	got := config.ActiveWorkers()
	if len(got) != 2 || got[0].AgentConfig.Model != "opus" || got[1].AgentConfig.Effort != "high" {
		t.Fatalf("active workers lost their per-harness config: %+v", got)
	}
}

// A project listing extras must resolve exactly as it did before, or adding one
// would quietly move every spawn and reviewer default.
func TestExtraWorkersDoNotChangeTheResolvedDefaults(t *testing.T) {
	base := ProjectConfig{Worker: RoleOverride{Harness: HarnessClaudeCode}}
	withExtras := base
	withExtras.Workers = []RoleOverride{{Harness: HarnessCodex}}

	if base.Worker.Harness != withExtras.Worker.Harness {
		t.Fatalf("default worker changed: %q then %q", base.Worker.Harness, withExtras.Worker.Harness)
	}
	baseReviewer, baseConfig := base.DefaultWorkerReviewer()
	extraReviewer, extraConfig := withExtras.DefaultWorkerReviewer()
	if baseReviewer != extraReviewer || baseConfig != extraConfig {
		t.Fatalf("default reviewer changed: (%q, %+v) then (%q, %+v)",
			baseReviewer, baseConfig, extraReviewer, extraConfig)
	}
}
