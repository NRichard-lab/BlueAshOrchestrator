package domain

import (
	"slices"
	"testing"
)

func TestLocalModelsHarnessIsKnownAndChatOnly(t *testing.T) {
	if HarnessLocalModels != AgentHarness("local-models") {
		t.Fatalf("HarnessLocalModels = %q, want local-models", HarnessLocalModels)
	}
	if !HarnessLocalModels.IsKnown() || !slices.Contains(AllHarnesses, HarnessLocalModels) {
		t.Fatal("local-models is not a known, listed harness")
	}
	if !HarnessLocalModels.ChatOnly() {
		t.Error("local-models has no terminal UI in its first version and must be Chat-only")
	}
	if HarnessLocal.ChatOnly() {
		t.Error("Local (OpenCode) runs in a terminal too; it must not become Chat-only")
	}
}

// Only turns a person started may reach a local model. These two harnesses
// are the whole list, and no hosted harness may join it by accident.
func TestOnlyTheLocalHarnessesForbidBackgroundPrompts(t *testing.T) {
	for _, harness := range AllHarnesses {
		want := harness == HarnessLocal || harness == HarnessLocalModels
		if got := harness.NoBackgroundPrompts(); got != want {
			t.Errorf("%s NoBackgroundPrompts() = %v, want %v", harness, got, want)
		}
	}
}

// A CI failure or review comment forwarded into a session is a turn nobody
// typed. Local Models starts with that off; every other harness keeps its
// existing default.
func TestLocalModelsDoesNotForwardLifecycleMessagesByDefault(t *testing.T) {
	for _, harness := range AllHarnesses {
		want := harness != HarnessLocalModels
		if got := harness.ForwardsLifecycleMessagesByDefault(); got != want {
			t.Errorf("%s ForwardsLifecycleMessagesByDefault() = %v, want %v", harness, got, want)
		}
	}
}
