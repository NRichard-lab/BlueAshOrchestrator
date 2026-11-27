package cli

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// A run file written by a different product's daemon must not be used: this build (legacy in
// tests) refuses one stamped by BlueAshAI, exactly as a BlueAshAI build refuses an Orchestrator one.
func writeForeignRunFile(t *testing.T, cfg testConfig, port int) {
	t.Helper()
	info := runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Now().UTC(), Product: "blueashai"}
	if err := runfile.Write(cfg.runFile, info); err != nil {
		t.Fatal(err)
	}
}

func foreignDaemonDeps(t *testing.T) Deps {
	t.Helper()
	return Deps{
		ProcessAlive: func(int) bool { return true },
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			t.Errorf("the other product daemon was contacted: %s", req.URL)
			return jsonResponse(http.StatusOK, "{}"), nil
		})},
	}
}

func TestStatusRefusesAnotherProductsRunFile(t *testing.T) {
	cfg := setConfigEnv(t)
	writeForeignRunFile(t, cfg, 3101)

	_, _, err := executeCLI(t, foreignDaemonDeps(t), "status", "--json")
	if err == nil {
		t.Fatal("status must fail on a run file from another product")
	}
	for _, want := range []string{"belongs to BlueAshAI", "not Orchestrator", cfg.runFile, "AO_RUN_FILE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err, want)
		}
	}
	// The foreign file is left exactly where it is.
	info, readErr := runfile.Read(cfg.runFile)
	if readErr != nil || info == nil || info.Product != "blueashai" {
		t.Fatalf("the other product run file was modified: %+v %v", info, readErr)
	}
}

func TestAPICallsRefuseAnotherProductsDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	writeForeignRunFile(t, cfg, 3101)

	_, _, err := executeCLI(t, foreignDaemonDeps(t), "project", "ls")
	if err == nil || !strings.Contains(err.Error(), "belongs to BlueAshAI") {
		t.Fatalf("expected a foreign-daemon error, got %v", err)
	}
}

func TestVerifyProbeOwnerRejectsAnotherProduct(t *testing.T) {
	probe := probeResult{Status: "ok", Service: "agent-orchestrator-daemon", PID: 7}
	if err := verifyProbeOwner(probe, 7, "healthz"); err != nil {
		t.Fatalf("a daemon that reports no product (older build) passes the other checks: %v", err)
	}
	probe.Product = "legacy"
	if err := verifyProbeOwner(probe, 7, "healthz"); err != nil {
		t.Fatalf("own product: %v", err)
	}
	probe.Product = "blueashai"
	if err := verifyProbeOwner(probe, 7, "healthz"); err == nil || !strings.Contains(err.Error(), "BlueAshAI") {
		t.Fatalf("a daemon from another product must be rejected, got %v", err)
	}
}
