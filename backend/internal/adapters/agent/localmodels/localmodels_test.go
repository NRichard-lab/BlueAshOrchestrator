package localmodels

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifestNamesTheHarness(t *testing.T) {
	manifest := New().Manifest()
	if manifest.ID != string(domain.HarnessLocalModels) || manifest.Name != "Local Models" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestTheRunnerIsTheExecutableOfTheDaemon(t *testing.T) {
	calls := 0
	plugin := &Plugin{executable: func() (string, error) { calls++; return " /opt/ao/ao ", nil }}
	for range 2 {
		got, err := plugin.ResolveBinary(context.Background())
		if err != nil || got != "/opt/ao/ao" {
			t.Fatalf("binary = %q, %v", got, err)
		}
	}
	if calls != 1 {
		t.Errorf("executable resolved %d times, want it cached", calls)
	}
	plugin.InvalidateBinaryResolution()
	if _, err := plugin.ResolveBinary(context.Background()); err != nil || calls != 2 {
		t.Errorf("after invalidation: calls = %d, err = %v", calls, err)
	}
	failing := &Plugin{executable: func() (string, error) { return "", errors.New("no executable") }}
	status, err := failing.AuthStatusWithEnv(context.Background(), map[string]string{"AO_DATA_DIR": t.TempDir()})
	if err == nil || status != ports.AgentAuthStatusUnknown {
		t.Errorf("status = %v, err = %v; want unknown with the resolve error", status, err)
	}
}

func TestTerminalLaunchIsRefused(t *testing.T) {
	_, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err == nil || !strings.Contains(err.Error(), "chat mode only") {
		t.Fatalf("err = %v, want a chat-only refusal", err)
	}
}

// Readiness is read from metadata routes. Reporting "configured" must not cost
// the user a model load, so no generation request may be made to find out.
func TestReadinessComesFromMetadataOnly(t *testing.T) {
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	dir := t.TempDir()
	server := localproviderstest.NewOllama(t,
		localproviderstest.OllamaModel{Name: "qwen3:8b", Capabilities: []string{"completion", "tools"}, MaxContext: 32768},
	)
	if _, err := localproviders.Add(dir, server.Endpoint("lab", localproviders.KindOllama)); err != nil {
		t.Fatal(err)
	}
	plugin := &Plugin{Client: server.OnlyClient(), executable: func() (string, error) { return "ao", nil }}
	env := map[string]string{"AO_DATA_DIR": dir}
	status, err := plugin.AuthStatusWithEnv(context.Background(), env)
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %v, %v; want configured", status, err)
	}
	server.RequireNoGeneration(t)

	server.Close()
	status, err = plugin.AuthStatusWithEnv(context.Background(), env)
	if err != nil || status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status with the server stopped = %v, %v; want unauthorized (needs setup)", status, err)
	}
}
