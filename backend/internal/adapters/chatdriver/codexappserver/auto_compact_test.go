package codexappserver

import "testing"

// Codex compacts on its own only when told where to: without the limit a long
// conversation keeps re-sending its history until a turn no longer fits, and
// only a manual compaction gets it moving again.
func TestThreadConfigCarriesTheAutoCompactLimit(t *testing.T) {
	config := threadConfig("", 200_000, nil)
	got, ok := config["model_auto_compact_token_limit"]
	if !ok {
		t.Fatalf("thread config has no compaction limit: %#v", config)
	}
	if got != 200_000 {
		t.Errorf("compaction limit = %v, want 200000", got)
	}
	// Left to app-server: AO has no measured reason to prefer either scope.
	if scope, ok := config["model_auto_compact_token_limit_scope"]; ok {
		t.Errorf("thread config pinned the limit scope to %v, want app-server's default", scope)
	}
}

func TestThreadConfigOmitsAnUnsetAutoCompactLimit(t *testing.T) {
	for _, tokens := range []int{0, -1} {
		config := threadConfig("high", tokens, nil)
		if _, ok := config["model_auto_compact_token_limit"]; ok {
			t.Errorf("tokens=%d set a compaction limit: %#v", tokens, config)
		}
		// The rest of the override is untouched by the compaction choice.
		if config["model_reasoning_effort"] != "high" {
			t.Errorf("tokens=%d lost the effort override: %#v", tokens, config)
		}
	}
}
