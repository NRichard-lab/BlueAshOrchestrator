package localmodelsacp

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureLaunchesTheBuiltInRunner(t *testing.T) {
	dataDir := t.TempDir()
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{
		SessionID: "mer-1", DataDir: dataDir, Model: " ollama/qwen3:8b ", SystemPrompt: "standing instructions",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 6 || !reflect.DeepEqual(args[:4], []string{"local-agent", "acp", "--model", "ollama/qwen3:8b"}) || args[4] != "--instructions-file" {
		t.Fatalf("args = %v", args)
	}
	// The instructions travel in a file, not on the command line: the full
	// text can pass the Windows command-line limit.
	written, err := os.ReadFile(args[5])
	if err != nil || string(written) != "standing instructions" {
		t.Fatalf("instructions file = %q, %v", written, err)
	}
	if !strings.HasPrefix(args[5], dataDir) {
		t.Errorf("instructions file %s is outside the data directory %s", args[5], dataDir)
	}
	if env["AO_DATA_DIR"] != dataDir {
		t.Errorf("env = %v, want the data directory the daemon uses", env)
	}
}

func TestConfigureWithNothingChosenPassesNoFlags(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{"local-agent", "acp"}) || len(env) != 0 {
		t.Fatalf("args = %v env = %v", args, env)
	}
}

func TestSessionOptionsCarryOnlyAChosenModel(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Fatalf("options with no model = %+v", got)
	}
	got := sessionOptions(ports.ChatTurnSettings{Model: " lab/hf.co/org/coder:Q4 "})
	if len(got) != 1 || got[0].ID != "model" || got[0].Value != "lab/hf.co/org/coder:Q4" {
		t.Fatalf("options = %+v", got)
	}
}

func TestTurnSettingsRequireAnEndpointQualifiedModel(t *testing.T) {
	for _, model := range []string{"", "ollama/qwen3:8b", "lab/hf.co/org/coder:Q4"} {
		if err := validateTurnSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Model: model}); err != nil {
			t.Errorf("model %q rejected: %v", model, err)
		}
	}
	for _, model := range []string{"qwen3:8b", "/qwen3:8b", "ollama/"} {
		err := validateTurnSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Model: model})
		if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Errorf("model %q: err = %v, want ErrChatConfigOptionInvalid", model, err)
		}
	}
}

func TestPromptErrorFailureUsesTheWordsOfTheRunner(t *testing.T) {
	const text = "ollama answered 404: model \"gone\" not found"
	direct := &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": text}}
	wrapped := &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{
		"providerData": map[string]any{"details": text},
	}}
	for name, err := range map[string]error{"direct": direct, "wrapped by the persistent host": wrapped} {
		if got := promptErrorFailure(err, false); got == nil || got.Error() != text {
			t.Errorf("%s: failure = %v, want %q", name, got, text)
		}
	}
	// Anything else keeps the classification the transport gave it.
	for name, err := range map[string]error{
		"not a request error": errors.New("pipe closed"),
		"no details":          &acpsdk.RequestError{Code: -32603, Message: "Internal error"},
		"blank details":       &acpsdk.RequestError{Code: -32603, Data: map[string]any{"details": "  "}},
	} {
		if got := promptErrorFailure(err, false); got != nil {
			t.Errorf("%s: failure = %v, want nil", name, got)
		}
	}
}
