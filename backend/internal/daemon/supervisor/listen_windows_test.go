//go:build windows

package supervisor

import "testing"

// The same table is asserted by the desktop app's TypeScript twin
// (frontend/src/main/daemon-run-file.test.ts); the app and daemon must derive
// the identical pipe or the liveness link never connects.
func TestPipeNameFromRunFileMatchesDesktopApp(t *testing.T) {
	cases := []struct{ runFile, want string }{
		{"", `\\.\pipe\ao-supervise`},
		{`C:\Users\a\.ao\running.json`, `\\.\pipe\ao-supervise`},
		{`C:\Users\a\.ao\dev\running.json`, `\\.\pipe\ao-supervise-dev`},
		{`D:\Apps\Orchestrator\data\running.json`, `\\.\pipe\ao-supervise-data`},
		{`D:\x\my dir_1\running.json`, `\\.\pipe\ao-supervise-my-dir-1`},
	}
	for _, c := range cases {
		if got := pipeNameFromRunFile(c.runFile); got != c.want {
			t.Errorf("pipeNameFromRunFile(%q) = %q, want %q", c.runFile, got, c.want)
		}
	}
}
