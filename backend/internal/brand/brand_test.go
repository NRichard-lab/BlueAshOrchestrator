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
