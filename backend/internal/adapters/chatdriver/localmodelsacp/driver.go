// Package localmodelsacp binds the Local Models harness to the daemon's ACP
// Chat transport. The agent it launches is the daemon's own binary, running
// `ao local-agent acp`: the runner in internal/localagent.
//
// The seam is ACP on stdin and stdout, the same one every other ACP harness
// uses, so the runner could be replaced without touching the daemon.
package localmodelsacp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/localacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// instructionsDir names the session's standing-instructions file directory.
const instructionsDir = "local-models"

// New launches `ao local-agent acp` from the executable the plugin resolves,
// which is the daemon's own.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:        domain.HarnessLocalModels,
		Configure:      configure,
		SessionOptions: sessionOptions,
		// The same mapping the Local (OpenCode) harness uses: default asks
		// before edits and commands, accept-edits allows edits, auto and
		// bypass-permissions allow everything.
		PermissionPolicy:     localacp.PermissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
		PromptErrorFailure:   promptErrorFailure,
	}, log)
}

// configure builds the runner's arguments. The model and the standing
// instructions are passed at launch; neither makes the runner contact a model.
func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"local-agent", "acp"}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		args = append(args, "--model", model)
	}
	if cfg.SystemPrompt != "" {
		// Written to a file because the full standing instructions can pass
		// Windows' command-line limit. The runner reads it only when the
		// instructions level is "standard".
		path, err := acpdriver.SystemPromptFile(cfg, instructionsDir)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--instructions-file", path)
	}
	env := map[string]string{}
	if dataDir := strings.TrimSpace(cfg.DataDir); dataDir != "" {
		// The runner reads endpoints, keys and settings from the same data
		// directory the daemon uses.
		env[localproviders.DataDirEnv] = dataDir
	}
	return args, env, nil
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	model := strings.TrimSpace(settings.Model)
	if model == "" {
		return nil
	}
	return []acpdriver.SessionOption{{ID: "model", Value: model}}
}

func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if strings.TrimSpace(settings.Model) == "" {
		return nil
	}
	if _, _, err := localproviders.ParseModelID(settings.Model); err != nil {
		return fmt.Errorf("%w: %s", ports.ErrChatConfigOptionInvalid, err.Error())
	}
	return nil
}

// promptErrorFailure restates a failed turn in the runner's own words. The
// runner answers a failed session/prompt with JSON-RPC -32603 and puts the
// readable text in data.details; the persistent chat host wraps that data once
// more as data.providerData.
func promptErrorFailure(err error, _ bool) error {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return nil
	}
	data, _ := requestErr.Data.(map[string]any)
	if provider, ok := data["providerData"].(map[string]any); ok {
		data = provider
	}
	details, _ := data["details"].(string)
	if details = strings.TrimSpace(details); details == "" {
		return nil
	}
	return errors.New(details)
}
