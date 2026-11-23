package systeminstall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

// withUpdatesCompiledOut stamps the BlueAshAI build setting for one test (the same value build-daemon.mjs
// passes through ldflags) and restores it afterwards.
func withUpdatesCompiledOut(t *testing.T) {
	t.Helper()
	old := brand.UpdatesEnabled
	brand.UpdatesEnabled = "false"
	t.Cleanup(func() { brand.UpdatesEnabled = old })
}

func TestCompiledOutUpdatesMakeNoRegistryOrVendorRequest(t *testing.T) {
	withUpdatesCompiledOut(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	if _, err := fetchRegistry(context.Background(), srv.Client(), srv.URL, ""); !errors.Is(err, ErrUpdatesDisabled) {
		t.Fatalf("fetchRegistry err = %v, want ErrUpdatesDisabled", err)
	}
	if _, err := npmLatestTag(context.Background(), srv.Client(), "@scope/pkg"); err == nil {
		t.Fatal("npm lookup must fail without a request")
	}
	if _, err := githubLatestReleaseTag(context.Background(), srv.Client(), "owner/repo"); !errors.Is(err, ErrUpdatesDisabled) {
		t.Fatalf("githubLatestReleaseTag err = %v, want ErrUpdatesDisabled", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("%d requests reached the server; a build with updates compiled out must send none", got)
	}
}

func TestLegacyBuildStillFetchesReleaseMetadata(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"latest":"1.2.3"}`))
	}))
	defer srv.Close()
	if _, err := fetchRegistry(context.Background(), srv.Client(), srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatal("the legacy profile must keep its release lookups")
	}
}

func TestCompiledOutUpdatesForceAdvisoryDisabledEvenIfDepsAskForChecks(t *testing.T) {
	withUpdatesCompiledOut(t)
	s := NewWithDeps(nil, nil, Deps{DisableUpdateChecks: false})
	if !s.updateChecksDisabled {
		t.Fatal("a build with updates compiled out must disable advisories whatever the daemon config says")
	}
}

func TestCompiledOutUpdatesRejectUpdateOperationButNotInstall(t *testing.T) {
	withUpdatesCompiledOut(t)
	s := newTestService("darwin", "npm")
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, ""); !errors.Is(err, ErrUpdatesDisabled) {
		t.Fatalf("update operation err = %v, want ErrUpdatesDisabled", err)
	}
	// Install/verify (C5) are not update checks and must stay reachable: they must not report ErrUpdatesDisabled.
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationInstall, ""); errors.Is(err, ErrUpdatesDisabled) {
		t.Fatal("install must not be blocked by the update switch")
	}
}
