package brand

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTextIsIdentityForUnstampedBuilds(t *testing.T) {
	if VisibleName != upstreamName {
		t.Fatalf("source default must stay %q so plain builds and tests are unchanged, got %q", upstreamName, VisibleName)
	}
	for _, s := range []string{"the Orchestrator daemon is not running", "Agent Orchestrator", "an Orchestrator host"} {
		if got := Text(s); got != s {
			t.Fatalf("Text(%q) = %q, want it unchanged", s, got)
		}
	}
}

// stamp sets VisibleName the way the BlueAshAI build's ldflags do.
func stamp(t *testing.T) {
	t.Helper()
	old := VisibleName
	t.Cleanup(func() { VisibleName = old })
	VisibleName = "Blue Ash AI"
}

func TestTextRewritesTheProductName(t *testing.T) {
	stamp(t)
	for _, c := range []struct{ in, want string }{
		{"Orchestrator", "Blue Ash AI"},
		{"the Orchestrator daemon is not running", "the Blue Ash AI daemon is not running"},
		{"Orchestrator's own data", "Blue Ash AI's own data"},
		{"Welcome to Agent Orchestrator", "Welcome to Blue Ash AI"},
		{"Agent Orchestrator", "Blue Ash AI"},
		{"About Orchestrator.inc", "About Blue Ash AI"},
		{"this is not an Orchestrator host", "this is not a Blue Ash AI host"},
		{"An Orchestrator daemon is already running", "A Blue Ash AI daemon is already running"},
		{"refusing to overwrite non-Orchestrator file", "refusing to overwrite non-Blue Ash AI file"},
		{"a session Orchestrator manages, with Orchestrator running", "a session Blue Ash AI manages, with Blue Ash AI running"},
		{"Orchestrators eigener Host", "Blue Ash AIs eigener Host"},
		{"no Orchestrator", "no Blue Ash AI"},
	} {
		if got := Text(c.in); got != c.want {
			t.Errorf("Text(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestTextLeavesIdentifiersAndUpstreamServicesAlone(t *testing.T) {
	stamp(t)
	for _, s := range []string{
		"KindOrchestrator",
		"Orchestrator_x and OrchestratorInc and spawnOrchestrator",
		"agent-orchestrator: managed opencode activity plugin",
		"Sign in to Orchestrator Cloud to use remote hosts.",
		"Lokale Orchestrator-Cloud-Anmeldung",
		"the Orchestrator Browser Bridge extension",
		"Orchestrator-Browser-Bridge",
	} {
		if got := Text(s); got != s {
			t.Errorf("Text(%q) = %q, want it unchanged", s, got)
		}
	}
	// A product mention next to a kept name is still rewritten.
	const mixed = "Orchestrator registers the Orchestrator Browser Bridge"
	if got, want := Text(mixed), "Blue Ash AI registers the Orchestrator Browser Bridge"; got != want {
		t.Errorf("Text(%q)\n got %q\nwant %q", mixed, got, want)
	}
}

// Callers wrap the format string, so the arguments are never rewritten. That is what keeps a path
// such as the installed app bundle intact inside a branded message.
func TestWrappingAFormatStringLeavesArgumentsAlone(t *testing.T) {
	stamp(t)
	const bundle = "/Applications/Agent Orchestrator.app"
	got := fmt.Sprintf(Text("could not stage Orchestrator's tmux under %s"), bundle)
	if want := "could not stage Blue Ash AI's tmux under /Applications/Agent Orchestrator.app"; got != want {
		t.Errorf("formatted\n got %q\nwant %q", got, want)
	}
	cause := errors.New("boom")
	err := fmt.Errorf(Text("write Orchestrator login-shell wrappers: %w"), cause)
	if want := "write Blue Ash AI login-shell wrappers: boom"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if !errors.Is(err, cause) {
		t.Error("%w wrapping must survive")
	}
}

func TestTextMatchesTheRulesForEveryByteBoundary(t *testing.T) {
	stamp(t)
	// Whatever the surroundings, Text must not drop or duplicate text other than the name itself.
	for _, around := range []string{"", " ", "x", "-", ".", "(", "s", "'s", " Cloud", "\n"} {
		for _, in := range []string{around + "Orchestrator", "Orchestrator" + around, around + "Orchestrator" + around} {
			got := Text(in)
			stripped := strings.ReplaceAll(strings.ReplaceAll(got, VisibleName, ""), upstreamName, "")
			want := strings.ReplaceAll(in, upstreamName, "")
			// "an " -> "a " is the one rewrite that touches a neighbouring word, and none of the
			// fixtures here contain it.
			if stripped != want {
				t.Errorf("Text(%q) = %q changed text outside the name (%q vs %q)", in, got, stripped, want)
			}
		}
	}
}

func ExampleText() {
	fmt.Println(Text("the Orchestrator daemon is not running"))
	// Output: the Orchestrator daemon is not running
}
