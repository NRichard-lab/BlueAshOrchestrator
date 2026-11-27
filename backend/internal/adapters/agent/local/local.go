// Package local implements the Local coding harness: OpenCode 2 pointed at a
// saved OpenAI-compatible (or Ollama) endpoint and model from Local Models
// settings. AO does not invent a second tool runtime; it reuses OpenCode 2's
// workspace tools and permission modes, and injects the linked provider so
// normal coding turns call that endpoint.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin is the Local worker adapter.
type Plugin struct {
	agentbase.Base
	// Client is optional; tests inject a fake transport. Production probes use
	// the default localproviders timeout client.
	Client *http.Client
}

// New returns the Local adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)

// Manifest describes the Local harness honestly: it needs OpenCode 2 and a
// linked local endpoint/model.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          string(domain.HarnessLocal),
		Name:        "Local",
		Description: "Run coding workers against a linked Ollama, llama.cpp, or OpenAI-compatible endpoint via OpenCode 2.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// ResolveBinary requires OpenCode 2; Local does not ship a separate CLI.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return opencode.ResolveBinaryForMajor(ctx, 2)
}

// GetConfigSpec exposes the model field. The linked model is the default; a
// session override may still pick another id on the same endpoint.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Local model id (defaults to the model linked in Settings → Local models). Prefer an id listed by the endpoint.")
}

// GetLaunchCommand builds OpenCode 2 argv with the linked OpenAI-compatible
// provider injected into OPENCODE_CONFIG_CONTENT.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := opencode.ResolveBinaryForMajor(ctx, 2)
	if err != nil {
		return nil, err
	}
	return p.command(ctx, binary, cfg, "")
}

// GetRestoreCommand restores an OpenCode 2 native session with the same Local
// provider injection.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	nativeID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if nativeID == "" {
		return nil, false, nil
	}
	binary, err := opencode.ResolveBinaryForMajor(ctx, 2)
	if err != nil {
		return nil, false, err
	}
	cmd, err := p.command(ctx, binary, ports.LaunchConfig{
		Config: cfg.Config, DataDir: cfg.DataDir, SessionID: cfg.Session.ID,
		Permissions: cfg.Permissions, Prompt: cfg.Prompt, SystemPrompt: cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile, Env: cfg.Env,
	}, nativeID)
	return cmd, err == nil, err
}

func (p *Plugin) command(ctx context.Context, binary string, cfg ports.LaunchConfig, nativeID string) ([]string, error) {
	dataDir := strings.TrimSpace(cfg.DataDir)
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv(brand.Env("AO_DATA_DIR")))
	}
	endpoint, link, _, err := localproviders.ResolveLink(dataDir)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(cfg.Config.Model)
	if model == "" {
		model = link.Model
	}
	model = localproviders.OpenCodeModelID(model)

	prompt := cfg.SystemPrompt
	if prompt == "" && cfg.SystemPromptFile != "" {
		data, readErr := os.ReadFile(cfg.SystemPromptFile)
		if readErr != nil {
			return nil, fmt.Errorf("local: read system prompt: %w", readErr)
		}
		prompt = string(data)
	}
	existing := ""
	if cfg.Env != nil {
		existing = cfg.Env["OPENCODE_CONFIG_CONTENT"]
	}
	if existing == "" {
		existing = os.Getenv("OPENCODE_CONFIG_CONTENT")
	}
	content, err := prepareConfigContent(existing, cfg.SessionID, prompt, model, cfg.Permissions, endpoint, link)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dataHome, err := opencodev2.DataHome(ctx)
	if err != nil {
		return nil, fmt.Errorf("local: prepare data home: %w", err)
	}
	envArgs := []string{"XDG_DATA_HOME=" + dataHome, "OPENCODE_CONFIG_CONTENT=" + content}
	cmd := append([]string{"env"}, envArgs...)
	cmd = append(cmd, binary, "--standalone")
	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeAuto:
		cmd = append(cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		cmd = append(cmd, "--dangerously-skip-permissions")
	}
	if nativeID != "" {
		cmd = append(cmd, "--session", nativeID)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt="+cfg.Prompt)
	}
	return cmd, nil
}

func prepareConfigContent(existing, sessionID, prompt, model string, mode ports.PermissionMode, endpoint localproviders.Endpoint, link localproviders.Link) (string, error) {
	// Start from OpenCode 2's agent overlay, then merge the Local provider.
	base, err := prepareAgentOverlay(existing, sessionID, prompt, model, mode)
	if err != nil {
		return "", err
	}
	config := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(base), &config); err != nil {
		return "", fmt.Errorf("local: decode agent overlay: %w", err)
	}
	providers := map[string]any{}
	if raw, ok := config["providers"]; ok {
		if err := json.Unmarshal(raw, &providers); err != nil || providers == nil {
			return "", fmt.Errorf("local: OPENCODE_CONFIG_CONTENT providers must be an object")
		}
	}
	for id, provider := range localproviders.ProviderConfigFragment(endpoint, link) {
		providers[id] = provider
	}
	raw, err := json.Marshal(providers)
	if err != nil {
		return "", err
	}
	config["providers"] = raw
	// Prefer the linked/overridden model as the session default when not already set.
	if _, ok := config["model"]; !ok && model != "" {
		config["model"], err = json.Marshal(model)
		if err != nil {
			return "", err
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// prepareAgentOverlay mirrors OpenCode 2's agent/default_agent overlay without
// importing its unexported helper. Keep permission mapping identical.
func prepareAgentOverlay(existing, sessionID, prompt, model string, mode ports.PermissionMode) (string, error) {
	type permissionRule struct {
		Action   string `json:"action"`
		Resource string `json:"resource"`
		Effect   string `json:"effect"`
	}
	model = strings.TrimSpace(model)
	mode = ports.NormalizePermissionMode(mode)
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil || config == nil {
			return "", fmt.Errorf("local: OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	agents := map[string]json.RawMessage{}
	if raw, ok := config["agents"]; ok {
		if err := json.Unmarshal(raw, &agents); err != nil || agents == nil {
			return "", fmt.Errorf("local: OPENCODE_CONFIG_CONTENT agents must be an object")
		}
	}
	name := aoAgentName(sessionID)
	agent := map[string]any{}
	if raw, ok := agents[name]; ok {
		if err := json.Unmarshal(raw, &agent); err != nil || agent == nil {
			return "", fmt.Errorf("local: OPENCODE_CONFIG_CONTENT agent %q must be an object", name)
		}
	}
	agent["mode"] = "primary"
	if prompt != "" {
		agent["system"] = prompt
	}
	delete(agent, "model")
	if model != "" {
		agent["model"] = model
	}
	delete(agent, "permissions")
	switch mode {
	case ports.PermissionModeAcceptEdits:
		agent["permissions"] = []permissionRule{{Action: "edit", Resource: "*", Effect: "allow"}}
	case ports.PermissionModeBypassPermissions:
		agent["permissions"] = []permissionRule{{Action: "*", Resource: "*", Effect: "allow"}}
	}
	raw, err := json.Marshal(agent)
	if err != nil {
		return "", err
	}
	agents[name] = raw
	config["agents"], err = json.Marshal(agents)
	if err != nil {
		return "", err
	}
	config["default_agent"], err = json.Marshal(name)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func aoAgentName(sessionID string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.TrimSpace(sessionID))
	name = strings.Trim(name, "-_")
	if name == "" {
		return "ao-system-prompt"
	}
	return "ao-" + name
}

// SessionInfo returns standard OpenCode session metadata when present.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// AuthStatus is unauthorized until an endpoint+model is linked; configured when
// the link resolves and the endpoint answers; unknown when OpenCode 2 is missing.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := opencode.ResolveBinaryForMajor(ctx, 2); err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) || errors.Is(err, ports.ErrAgentBinaryIdentityUnknown) {
			return ports.AgentAuthStatusUnknown, err
		}
		return ports.AgentAuthStatusUnknown, err
	}
	dataDir := strings.TrimSpace(os.Getenv(brand.Env("AO_DATA_DIR")))
	if dataDir == "" {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	_, linked, err := localproviders.LoadLink(dataDir)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if !linked {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, _, err := localproviders.VerifyLinkedModel(probeCtx, p.client(), dataDir); err != nil {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	return ports.AgentAuthStatusConfigured, nil
}

func (p *Plugin) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: localproviders.DefaultTimeout}
}

// PrepareACPConfigContent injects the linked Local provider into OpenCode 2's
// ACP overlay. Secrets stay in the returned JSON for the child process only.
func PrepareACPConfigContent(existing, systemPrompt string, mode ports.PermissionMode, dataDir string) (string, error) {
	base, err := opencodev2.PrepareACPConfigContent(existing, systemPrompt, mode)
	if err != nil {
		return "", err
	}
	endpoint, link, _, err := localproviders.ResolveLink(dataDir)
	if err != nil {
		return "", err
	}
	config := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(base), &config); err != nil {
		return "", fmt.Errorf("local ACP: decode overlay: %w", err)
	}
	providers := map[string]any{}
	if raw, ok := config["providers"]; ok {
		if err := json.Unmarshal(raw, &providers); err != nil || providers == nil {
			return "", fmt.Errorf("local ACP: providers must be an object")
		}
	}
	for id, provider := range localproviders.ProviderConfigFragment(endpoint, link) {
		providers[id] = provider
	}
	raw, err := json.Marshal(providers)
	if err != nil {
		return "", err
	}
	config["providers"] = raw
	model := localproviders.OpenCodeModelID(link.Model)
	if model != "" {
		config["model"], err = json.Marshal(model)
		if err != nil {
			return "", err
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
