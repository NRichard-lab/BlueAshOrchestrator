package brand

import "testing"

func TestDefaultsAreLegacyOrchestrator(t *testing.T) {
	if StateDirName != ".ao" || Port != "" {
		t.Fatalf("source defaults must stay legacy so tests and plain builds are unchanged: %q %q", StateDirName, Port)
	}
	if got := DefaultPort(3001); got != 3001 {
		t.Fatalf("DefaultPort(3001) = %d", got)
	}
}

func TestFeatureSwitchesDefaultOnAndOnlyExactFalseTurnsThemOff(t *testing.T) {
	oldU, oldT, oldP := UpdatesEnabled, RemoteTelemetryEnabled, PricingCatalogSyncEnabled
	t.Cleanup(func() { UpdatesEnabled, RemoteTelemetryEnabled, PricingCatalogSyncEnabled = oldU, oldT, oldP })
	if !UpdatesAllowed() || !RemoteTelemetryAllowed() || !PricingCatalogSyncAllowed() {
		t.Fatal("unstamped (legacy/test) builds must keep every feature on")
	}
	UpdatesEnabled, RemoteTelemetryEnabled, PricingCatalogSyncEnabled = "false", "true", "true"
	if UpdatesAllowed() || !RemoteTelemetryAllowed() || !PricingCatalogSyncAllowed() {
		t.Fatal("flags must be independent: only updates off")
	}
	UpdatesEnabled, RemoteTelemetryEnabled, PricingCatalogSyncEnabled = "true", "false", "true"
	if !UpdatesAllowed() || RemoteTelemetryAllowed() || !PricingCatalogSyncAllowed() {
		t.Fatal("flags must be independent: only telemetry off")
	}
	UpdatesEnabled, RemoteTelemetryEnabled, PricingCatalogSyncEnabled = "true", "true", "false"
	if !UpdatesAllowed() || !RemoteTelemetryAllowed() || PricingCatalogSyncAllowed() {
		t.Fatal("flags must be independent: only pricing off")
	}
	for _, junk := range []string{"", "FALSE", "0", "no"} {
		UpdatesEnabled = junk
		if !UpdatesAllowed() {
			t.Fatalf("%q must not switch updates off; only the exact stamp \"false\" does", junk)
		}
	}
}

func TestStampedValues(t *testing.T) {
	oldDir, oldPort := StateDirName, Port
	t.Cleanup(func() { StateDirName, Port = oldDir, oldPort })
	StateDirName, Port = ".blueashai", "3101"
	if got := DefaultPort(3001); got != 3101 {
		t.Fatalf("DefaultPort = %d, want 3101", got)
	}
	for _, bad := range []string{"abc", "0", "70000", "-1"} {
		Port = bad
		if got := DefaultPort(3001); got != 3001 {
			t.Fatalf("invalid stamped port %q must fall back, got %d", bad, got)
		}
	}
}
