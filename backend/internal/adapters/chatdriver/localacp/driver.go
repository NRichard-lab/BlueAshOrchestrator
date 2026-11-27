// Package localacp binds the Local harness to OpenCode 2's ACP Chat transport
// with the linked OpenAI-compatible provider injected.
package localacp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/local"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/opencodeidentity"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New launches `opencode acp` with the Local provider overlay.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessLocal,
		Configure:            configure,
		SessionMode:          sessionMode,
		SessionOptions:       sessionOptions,
		PermissionPolicy:     permissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
		EncodeProviderConversationID: func(providerID string) string {
			return opencodeidentity.Encode(domain.HarnessLocal, providerID)
		},
		DecodeProviderConversationID: func(providerID string) (string, error) {
			return opencodeidentity.Decode(domain.HarnessLocal, providerID)
		},
	}, log)
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	dataDir := strings.TrimSpace(cfg.Env["AO_DATA_DIR"])
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	}
	content, err := local.PrepareACPConfigContent(cfg.Env["OPENCODE_CONFIG_CONTENT"], cfg.SystemPrompt, cfg.Permissions, dataDir)
	if err != nil {
		return nil, nil, err
	}
	dataHome, err := opencodev2.DataHome(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("local ACP: prepare data home: %w", err)
	}
	env := map[string]string{"OPENCODE_CONFIG_CONTENT": content, "XDG_DATA_HOME": dataHome}
	return []string{"acp"}, env, nil
}

func sessionMode(ports.PermissionMode) string { return "build" }

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := make([]acpdriver.SessionOption, 0, 2)
	model := strings.TrimSpace(settings.Model)
	if model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: localproviders.OpenCodeModelID(model)})
	}
	if settings.Effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "effort", Value: settings.Effort})
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Model == "" {
		return nil
	}
	model := localproviders.OpenCodeModelID(settings.Model)
	provider, id, found := strings.Cut(model, "/")
	if !found || provider == "" || id == "" ||
		strings.TrimSpace(provider) != provider || strings.TrimSpace(id) != id {
		return fmt.Errorf("%w: Local model %q must be a concrete model id", ports.ErrChatConfigOptionInvalid, settings.Model)
	}
	return nil
}

func permissionPolicy(mode ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	mode = ports.NormalizePermissionMode(mode)
	kind := params.ToolCall.Kind
	if mode != ports.PermissionModeAuto && mode != ports.PermissionModeBypassPermissions &&
		(mode != ports.PermissionModeAcceptEdits || kind == nil ||
			(*kind != acpsdk.ToolKindEdit && *kind != acpsdk.ToolKindDelete && *kind != acpsdk.ToolKindMove)) {
		return "", false
	}
	for _, option := range params.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}
