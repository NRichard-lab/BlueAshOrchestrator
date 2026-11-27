package config

import (
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func stampBlueAsh(t *testing.T) {
	t.Helper()
	oldID, oldPrefix, oldDir, oldPort := brand.ID, brand.EnvPrefix, brand.StateDirName, brand.Port
	t.Cleanup(func() { brand.ID, brand.EnvPrefix, brand.StateDirName, brand.Port = oldID, oldPrefix, oldDir, oldPort })
	brand.ID, brand.EnvPrefix, brand.StateDirName, brand.Port = "blueashai", "BLUEASH", ".blueashai", "3101"
}

// The reproduced startup failure: a process started inside one product inherits that product's
// daemon coordinates. A BlueAshAI build must not act on Orchestrator's AO_* coordinates.
func TestBlueAshBuildIgnoresOrchestratorCoordinates(t *testing.T) {
	stampBlueAsh(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AO_RUN_FILE", filepath.Join(home, ".ao", "running.json"))
	t.Setenv("AO_DATA_DIR", filepath.Join(home, ".ao", "data"))
	t.Setenv("AO_PORT", "3001")
	t.Setenv("BLUEASH_RUN_FILE", "")
	t.Setenv("BLUEASH_DATA_DIR", "")
	t.Setenv("BLUEASH_PORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".blueashai", "running.json"); cfg.RunFilePath != want {
		t.Fatalf("run file = %q, want the BlueAshAI default %q", cfg.RunFilePath, want)
	}
	if want := filepath.Join(home, ".blueashai", "data"); cfg.DataDir != want {
		t.Fatalf("data dir = %q, want the BlueAshAI default %q", cfg.DataDir, want)
	}
	if cfg.Port != 3101 {
		t.Fatalf("port = %d, want the BlueAshAI default 3101", cfg.Port)
	}
}

func TestBlueAshBuildHonorsItsOwnCoordinates(t *testing.T) {
	stampBlueAsh(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AO_RUN_FILE", filepath.Join(home, ".ao", "running.json"))
	t.Setenv("AO_DATA_DIR", filepath.Join(home, ".ao", "data"))
	t.Setenv("AO_PORT", "3001")
	runFile := filepath.Join(home, "custom", "running.json")
	dataDir := filepath.Join(home, "install", "data")
	t.Setenv("BLUEASH_RUN_FILE", runFile)
	t.Setenv("BLUEASH_DATA_DIR", dataDir)
	t.Setenv("BLUEASH_PORT", "3555")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunFilePath != runFile || cfg.DataDir != dataDir || cfg.StateDir != dataDir || cfg.Port != 3555 {
		t.Fatalf("got run file %q, data dir %q, state dir %q, port %d", cfg.RunFilePath, cfg.DataDir, cfg.StateDir, cfg.Port)
	}
}

// The mirror image: an Orchestrator (unstamped) build ignores BlueAshAI's names, so nothing
// BlueAshAI exports into a session can redirect it.
func TestOrchestratorBuildIgnoresBlueAshCoordinates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AO_RUN_FILE", "")
	t.Setenv("AO_DATA_DIR", "")
	t.Setenv("AO_PORT", "")
	t.Setenv("BLUEASH_RUN_FILE", filepath.Join(home, ".blueashai", "running.json"))
	t.Setenv("BLUEASH_DATA_DIR", filepath.Join(home, "install", "data"))
	t.Setenv("BLUEASH_PORT", "3101")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunFilePath != filepath.Join(home, ".ao", "running.json") || cfg.DataDir != filepath.Join(home, ".ao", "data") || cfg.Port != DefaultPort {
		t.Fatalf("got run file %q, data dir %q, port %d", cfg.RunFilePath, cfg.DataDir, cfg.Port)
	}
}
