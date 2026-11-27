package browserchrome

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

// brandedHost points this package at a product with its own host name, the way the BlueAshAI build
// is stamped. The variable (not a constant) is what makes that possible, so the tests below are
// also the guard that it stays one.
func brandedHost(t *testing.T, name string) {
	t.Helper()
	previous := HostName
	HostName = name
	t.Cleanup(func() { HostName = previous })
}

// requireManifestRegistration skips a test that asserts on manifest files in Chrome's own
// directory. Where Chrome has a host registry (Windows) no such file is written, and the registry
// cases below cover the same behavior.
func requireManifestRegistration(t *testing.T) {
	t.Helper()
	if platformRegistry() != nil {
		t.Skip("Chrome registers native hosts through the registry on this platform")
	}
}

func TestHostNameComesFromTheProductProfile(t *testing.T) {
	if HostName != brand.NativeMessagingHost {
		t.Fatalf("HostName = %q, want the product profile's %q", HostName, brand.NativeMessagingHost)
	}
	// Unstamped (source, test, legacy) builds are Orchestrator.
	if HostName != brand.LegacyNativeMessagingHost {
		t.Fatalf("an unstamped build must keep the Orchestrator host name, got %q", HostName)
	}

	brandedHost(t, "ai.blueash.browser_bridge")
	paths := NewPaths(filepath.Join("data"))
	if got, want := paths.HostManifestCopy(), filepath.Join("data", "browser-chrome", "ai.blueash.browser_bridge.json"); got != want {
		t.Fatalf("HostManifestCopy = %q, want %q", got, want)
	}
	browser := Browser{Name: "Google Chrome", UserDataDir: filepath.Join("chrome")}
	if got, want := browser.ManifestPath(), filepath.Join("chrome", "NativeMessagingHosts", "ai.blueash.browser_bridge.json"); got != want {
		t.Fatalf("ManifestPath = %q, want %q", got, want)
	}
}

func TestSetupUnderAProductsOwnHostNameLeavesTheLegacyNameFree(t *testing.T) {
	requireManifestRegistration(t)
	brandedHost(t, "ai.blueash.browser_bridge")
	installer, chromeDir, _ := testInstaller(t)
	if _, err := installer.Setup(); err != nil {
		t.Fatal(err)
	}
	// Only this product's name is registered; an installed Orchestrator's name is untouched.
	for name, want := range map[string]bool{"ai.blueash.browser_bridge": true, brand.LegacyNativeMessagingHost: false} {
		path := filepath.Join(chromeDir, "NativeMessagingHosts", name+".json")
		_, err := os.Stat(path)
		if got := err == nil; got != want {
			t.Fatalf("manifest %s present = %v, want %v", name, got, want)
		}
	}
	var manifest hostManifest
	data, err := os.ReadFile(filepath.Join(chromeDir, "NativeMessagingHosts", "ai.blueash.browser_bridge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "ai.blueash.browser_bridge" {
		t.Fatalf("manifest name = %q", manifest.Name)
	}
}

// An upgrade from a build that still shared the Orchestrator host name: the registration it wrote
// names this data directory's host, so it is ours to retire, and Chrome mode stays enabled.
func TestRefreshMovesThisInstallsLegacyRegistrationToItsOwnName(t *testing.T) {
	requireManifestRegistration(t)
	installer, chromeDir, _ := testInstaller(t)
	if _, err := installer.Setup(); err != nil { // the pre-rename build
		t.Fatal(err)
	}
	legacyManifest := filepath.Join(chromeDir, "NativeMessagingHosts", brand.LegacyNativeMessagingHost+".json")
	legacyCopy := installer.Paths.hostManifestCopy(brand.LegacyNativeMessagingHost)
	for _, path := range []string{legacyManifest, legacyCopy} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("setup did not write %s: %v", path, err)
		}
	}

	brandedHost(t, "ai.blueash.browser_bridge") // the upgraded build
	if err := installer.Refresh(); err != nil {
		t.Fatal(err)
	}
	if !installer.Status().Enabled {
		t.Fatal("Chrome mode turned itself off across the host rename")
	}
	if _, err := os.Stat(filepath.Join(chromeDir, "NativeMessagingHosts", "ai.blueash.browser_bridge.json")); err != nil {
		t.Fatalf("the new host name was not registered: %v", err)
	}
	for _, path := range []string{legacyManifest, legacyCopy} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the rename, so Chrome still has two hosts for one product", path)
		}
	}
}

// The same situation in the registry, which is how Windows registers hosts.
func TestRefreshMovesThisInstallsLegacyRegistryEntryAndSparesAnother(t *testing.T) {
	reg := &fakeRegistry{values: map[string]string{}}
	installer, _, _ := testInstaller(t)
	installer.registry = reg
	if _, err := installer.Setup(); err != nil {
		t.Fatal(err)
	}
	other, _, _ := testInstaller(t) // an installed Orchestrator: its own data directory
	installedOrchestrator := other.Paths.HostManifestCopy()

	brandedHost(t, "ai.blueash.browser_bridge")
	if err := installer.Refresh(); err != nil {
		t.Fatal(err)
	}
	if reg.values["ai.blueash.browser_bridge"] != installer.Paths.HostManifestCopy() {
		t.Fatalf("registry = %#v", reg.values)
	}
	if _, ok := reg.values[brand.LegacyNativeMessagingHost]; ok {
		t.Fatalf("this install kept the Orchestrator host name registered: %#v", reg.values)
	}

	// Now the other product holds the legacy name. Neither Refresh nor Remove may touch it.
	reg.values[brand.LegacyNativeMessagingHost] = installedOrchestrator
	if err := installer.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Remove(); err != nil {
		t.Fatal(err)
	}
	if reg.values[brand.LegacyNativeMessagingHost] != installedOrchestrator {
		t.Fatalf("an installed Orchestrator's registration was changed: %#v", reg.values)
	}
	if _, ok := reg.values["ai.blueash.browser_bridge"]; ok {
		t.Fatal("remove left this product's registration behind")
	}
}

// A legacy-named manifest written by another install must not look like ours to retire, and must
// not make Chrome mode report itself enabled.
func TestRefreshKeepsALegacyManifestFromAnotherInstall(t *testing.T) {
	requireManifestRegistration(t)
	installer, chromeDir, _ := testInstaller(t)
	foreign := filepath.Join(chromeDir, "NativeMessagingHosts", brand.LegacyNativeMessagingHost+".json")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte(`{"name":"` + brand.LegacyNativeMessagingHost + `","path":"/elsewhere/ao-browser-host"}`)
	if err := os.WriteFile(foreign, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	brandedHost(t, "ai.blueash.browser_bridge")
	if installer.Status().Enabled {
		t.Fatal("another install's registration counted as this product's")
	}
	if err := installer.Refresh(); err != nil {
		t.Fatal(err)
	}
	if installer.Status().Enabled {
		t.Fatal("refresh enabled Chrome mode that was never set up here")
	}
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != string(contents) {
		t.Fatalf("another install's manifest was changed: %q, %v", data, err)
	}
}
