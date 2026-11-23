package claudecode

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// labelVersion reads a "5.5"-style version out of a picker label.
var labelVersion = regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\b`)

// launchModelArgument returns the value Claude Code is actually told to run.
func launchModelArgument(t *testing.T, cmd []string) string {
	t.Helper()
	for i, arg := range cmd {
		if arg == "--model" && i+1 < len(cmd) {
			return cmd[i+1]
		}
	}
	return ""
}

// TestPickerRowVersionReachesLaunchArgument traces one model selection the whole
// way: the catalog row a picker shows, the --model argument the launch actually
// carries, and the version that argument requests.
//
// The reported fault was choosing a row reading "Opus 5.5" and being served
// Claude Fable 5.2. Nothing rewrites the model on the way to the CLI — AO passes
// the stored ID verbatim — so the mismatch came from the row itself: the bare
// family alias "opus" was labelled with a version read off the provider list,
// while `--model opus` leaves the choice to whichever model the installed CLI
// resolves the alias to. A row may therefore name a version only when its own ID
// requests that version.
func TestPickerRowVersionReachesLaunchArgument(t *testing.T) {
	// The concrete models Claude Code reported, newest Opus last.
	provider := []ports.AgentModelInfo{
		{ID: "claude-opus-5-1", Label: "Claude Opus 5.1"},
		{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
		{ID: "claude-fable-5-2", Label: "Claude Fable 5.2"},
	}
	// What a picker lists: the concrete models plus the family aliases the CLI
	// also accepts, including a context variant.
	rows := modelcatalog.LabelClaudeAliasVersions(
		append(append([]ports.AgentModelInfo{}, provider...),
			ports.AgentModelInfo{ID: "opus", Label: "Opus"},
			ports.AgentModelInfo{ID: "opus[1m]", Label: "opus[1m]"},
		),
		provider,
	)

	plugin := &Plugin{resolvedBinary: "claude"}
	traced := 0
	for _, row := range rows {
		cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
			Config:      ports.AgentConfig{Model: row.ID},
			Permissions: ports.PermissionModeBypassPermissions,
			Session: ports.SessionRef{
				ID:       "sess-trace",
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"},
			},
		})
		if err != nil || !ok {
			t.Fatalf("restore for %q = (ok=%v, err=%v), want ok", row.ID, ok, err)
		}

		// The selected ID must reach the CLI untouched.
		got := launchModelArgument(t, cmd)
		if got != row.ID {
			t.Errorf("row %q (id %q) launched --model %q, want the selected id", row.Label, row.ID, got)
			continue
		}

		// The honesty invariant: a row naming a version must request it.
		version := labelVersion.FindStringSubmatch(row.Label)
		if version == nil {
			continue
		}
		traced++
		if want := version[1] + "-" + version[2]; !strings.Contains(got, want) {
			t.Errorf("row %q launches --model %q, which does not request version %s.%s",
				row.Label, got, version[1], version[2])
		}
	}
	if traced == 0 {
		t.Fatal("no row named a version, so the trace proved nothing")
	}
}
