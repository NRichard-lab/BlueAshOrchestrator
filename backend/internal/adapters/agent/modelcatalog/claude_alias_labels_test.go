package modelcatalog

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLabelClaudeAliasVersionsMarksFamiliesAsFloating(t *testing.T) {
	aliases := []ports.AgentModelInfo{
		{ID: "sonnet", Label: "Sonnet"}, {ID: "opus", Label: "Opus"},
		{ID: "haiku", Label: "Haiku"}, {ID: "fable", Label: "Fable 5.1"},
		{ID: "opus[1m]", Label: "Opus (1M context)"},
	}
	reference := []ports.AgentModelInfo{
		{ID: "claude-opus-5-1-20260301", Label: "Claude Opus 5.1"},
		{ID: "claude-opus-5-5-20260901", Label: "Claude Opus 5.5"},
		{ID: "claude-sonnet-5-5-20260901"}, // no display name: the ID carries the version
		{ID: "claude-haiku-5-5-20261001", Label: "Claude Haiku 5.5"},
		{ID: "claude-fable-5-1-20260401", Label: "Claude Fable 5.1"},
		{ID: "opus", Label: "Opus 9.9"}, // aliases are never a reference
	}
	got := map[string]string{}
	for _, item := range LabelClaudeAliasVersions(aliases, normalize(reference)) {
		got[item.ID] = item.Label
	}
	want := map[string]string{
		"opus": "Opus (latest)", "sonnet": "Sonnet (latest)", "haiku": "Haiku (latest)",
		"opus[1m]": "Opus (latest, 1M context)",
		// The provider's own version claim goes too: the ID is still floating.
		"fable": "Fable (latest)",
	}
	for id, label := range want {
		if got[id] != label {
			t.Fatalf("label for %s = %q, want %q (all: %v)", id, got[id], label, got)
		}
	}
}

// The regression behind the fix: a picker row reading "Opus 5.5" whose ID was
// the bare alias "opus" let the CLI serve a different model than the row named.
// No label may assert a version unless its ID requests that version.
func TestLabelClaudeAliasVersionsNeverVersionsAFloatingAlias(t *testing.T) {
	reference := []ports.AgentModelInfo{
		{ID: "claude-opus-5-5-20260901", Label: "Claude Opus 5.5"},
		{ID: "claude-fable-5-2-20261101", Label: "Claude Fable 5.2"},
	}
	aliases := []ports.AgentModelInfo{
		{ID: "opus", Label: "Opus 5.5"},  // an earlier guess
		{ID: "fable", Label: "Fable 5.2"}, // the provider's own claim
		{ID: "opus[1m]", Label: "Opus 5.5 (1M context)"},
	}
	for _, item := range LabelClaudeAliasVersions(aliases, normalize(reference)) {
		if claudeAliasPattern.MatchString(item.ID) && claudeDisplayVersionPattern.MatchString(item.Label) {
			t.Errorf("floating alias %q kept a version in its label %q", item.ID, item.Label)
		}
	}
	// Concrete IDs keep theirs: that is where a version belongs.
	for _, item := range LabelClaudeAliasVersions(reference, normalize(reference)) {
		if !claudeDisplayVersionPattern.MatchString(item.Label) {
			t.Errorf("concrete model %q lost its version label %q", item.ID, item.Label)
		}
	}
}

func TestLabelClaudeAliasVersionsKeepsConfiguredContextVariant(t *testing.T) {
	reference := []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}}
	// A configured default the catalog lacks is appended labeled with its ID.
	models := applyClaudeConfiguredDefault(append([]ports.AgentModelInfo(nil), reference...), "opus[1m]")
	got := map[string]string{}
	for _, item := range LabelClaudeAliasVersions(models, reference) {
		got[item.ID] = item.Label
	}
	if got["opus[1m]"] != "Opus (latest, 1M context)" {
		t.Fatalf("opus[1m] label = %q, want Opus (latest, 1M context) (all: %v)", got["opus[1m]"], got)
	}
	if got["claude-opus-5-5"] != "Claude Opus 5.5" {
		t.Fatalf("concrete label = %q, want it unchanged", got["claude-opus-5-5"])
	}
}

func TestLabelClaudeAliasVersionsKeepsAliasWithoutEvidence(t *testing.T) {
	aliases := []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}}
	for _, reference := range [][]ports.AgentModelInfo{
		nil,
		{{ID: "glm-5", Label: "GLM 5"}},
		{{ID: "sonnet", Label: "Sonnet"}},
	} {
		for _, item := range LabelClaudeAliasVersions(aliases, reference) {
			if item.ID == "opus" && item.Label != "Opus" {
				t.Fatalf("opus label = %q with reference %v, want the bare alias", item.Label, reference)
			}
		}
	}
}

func TestLabelClaudeAliasVersionsDoesNotMutateInput(t *testing.T) {
	models := []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}}
	reference := []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}}
	if got := LabelClaudeAliasVersions(models, reference); got[0].Label != "Opus (latest)" {
		t.Fatalf("label = %q, want Opus (latest)", got[0].Label)
	}
	if models[0].Label != "Opus" {
		t.Fatal("labeling must not mutate the caller's slice")
	}
}
