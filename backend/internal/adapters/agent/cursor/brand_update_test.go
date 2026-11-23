package cursor

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func TestWithoutAutoUpdateOnlyWhenUpdatesAreCompiledOut(t *testing.T) {
	in := []string{"cursor-agent", "--force", "hello"}
	if got := withoutAutoUpdate(in); !reflect.DeepEqual(got, in) {
		t.Fatalf("legacy argv changed: %v", got)
	}
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
	want := []string{"cursor-agent", "--disable-auto-update", "--force", "hello"}
	if got := withoutAutoUpdate(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	if got := withoutAutoUpdate(nil); len(got) != 0 {
		t.Fatalf("empty argv must stay empty, got %v", got)
	}
	if !reflect.DeepEqual(in, []string{"cursor-agent", "--force", "hello"}) {
		t.Fatal("input argv was mutated")
	}
}
