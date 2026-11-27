// Package localmodels exposes the Local Models harness: local models run
// through the runner built into the ao binary (internal/localagent), with no
// third-party CLI.
//
// It is the second local harness. The first, "local", runs OpenCode 2 against
// one linked endpoint and model; this one offers every chat model on every
// known endpoint and is Chat-only.
package localmodels

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin is the Local Models agent adapter.
type Plugin struct {
	agentbase.Base
	// Client is optional; tests inject one. Production uses the localproviders
	// default, which times out quickly.
	Client *http.Client

	binaryMu       sync.Mutex
	resolvedBinary string
	executable     func() (string, error)
}

// New returns the Local Models adapter.
func New() *Plugin { return &Plugin{executable: os.Executable} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)

// Manifest describes the harness.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:   string(domain.HarnessLocalModels),
		Name: "Local Models",
		Description: "Run local models on Ollama, llama.cpp or any OpenAI-compatible server through the runner built into Orchestrator. " +
			"Nothing to install. Only turns you start are sent to a model.",
		Version:      "0.1.0",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// GetConfigSpec exposes the model field.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model as <endpoint>/<model>, for example ollama/qwen3:8b. Defaults to the one set with `ao local-provider use`.")
}

// GetLaunchCommand rejects terminal launches: the harness is Chat-only.
func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("local models is available in chat mode only")
}

// ResolveBinary returns Orchestrator's own executable: the runner is part of it.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	resolve := p.executable
	if resolve == nil {
		resolve = os.Executable
	}
	binary, err := resolve()
	if err != nil {
		return "", err
	}
	p.resolvedBinary = strings.TrimSpace(binary)
	return p.resolvedBinary, nil
}

// InvalidateBinaryResolution clears the cached executable path.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

// readinessTimeout bounds the metadata requests of one readiness check.
const readinessTimeout = 5 * time.Second

// AuthStatus reports readiness from metadata probes. It never sends a model
// anything: "configured" means an endpoint answers and lists a chat-capable
// model, not that a model has been tried.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	return p.authStatus(ctx, localproviders.DataDir(nil))
}

// AuthStatusWithEnv checks readiness against the data directory a session's
// environment names.
func (p *Plugin) AuthStatusWithEnv(ctx context.Context, env map[string]string) (ports.AgentAuthStatus, error) {
	return p.authStatus(ctx, localproviders.DataDir(env))
}

func (p *Plugin) authStatus(ctx context.Context, dataDir string) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if dataDir == "" {
		return ports.AgentAuthStatusUnknown, errors.New("local models: the data directory could not be resolved")
	}
	probeCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	if localproviders.CheckReadiness(probeCtx, p.Client, dataDir).State == localproviders.ReadinessConfigured {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnauthorized, nil
}
