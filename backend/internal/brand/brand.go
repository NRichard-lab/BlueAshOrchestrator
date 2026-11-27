// Package brand holds the product-identity values that must differ between the upstream
// Orchestrator and BlueAshAI so the two can run side by side: the per-user state directory name
// and the default API port. Windows named pipes need nothing extra: they are derived from the run
// file's directory name (see supervisor/listen_windows.go), so a different state directory name
// already yields different pipe names.
//
// The defaults are the legacy Orchestrator values, so tests and plain `go build` are unchanged. The
// BlueAshAI desktop build stamps the BlueAshAI values with ldflags (frontend/scripts/build-daemon.mjs
// reading frontend/src/shared/brand.ts); the TypeScript and Go sides must agree.
package brand

import "strconv"

var (
	// StateDirName is the per-user state directory under the home folder (~/.ao for Orchestrator).
	StateDirName = ".ao"
	// VisibleName is the product name as written in text a person reads: CLI output and help,
	// API error messages, diagnostics. Text() rewrites it into upstream's wording (see text.go).
	// It is deliberately separate from WindowsInstallDirName and the packaging names, which are
	// on-disk identities rather than prose.
	VisibleName = "Orchestrator"
	// Port is the default API port as a decimal string; empty means the built-in default.
	Port = ""
	// WindowsAssetName is the unversioned Windows installer `ao start` downloads from the update server.
	WindowsAssetName = "agent-orchestrator-win32-x64.exe"
	// WindowsInstallDirName is the folder the per-user NSIS install uses under %LOCALAPPDATA%\Programs.
	WindowsInstallDirName = "Agent Orchestrator"
	// WindowsExeName is the installed app executable inside that folder.
	WindowsExeName = "agent-orchestrator.exe"

	// UpdatesEnabled, RemoteTelemetryEnabled and PricingCatalogSyncEnabled are compile-time feature
	// switches stamped by ldflags from brands.json. They are strings because -X only sets strings; only
	// the exact value "false" turns a feature off, so an unstamped (test, plain go build, legacy) binary
	// keeps today's behavior. There is deliberately no environment override: a shipped binary cannot be
	// re-enabled by a variable.
	//
	// UpdatesEnabled covers the daemon's harness/agent-CLI "newer version" advisory and every remote
	// lookup behind it. RemoteTelemetryEnabled covers PostHog. PricingCatalogSyncEnabled covers the
	// LiteLLM model price catalog refresh. They are separate so egress can be attributed per feature.
	UpdatesEnabled            = "true"
	RemoteTelemetryEnabled    = "true"
	PricingCatalogSyncEnabled = "true"
)

// UpdatesAllowed reports whether this build may check for or advise about updates.
func UpdatesAllowed() bool { return UpdatesEnabled != "false" }

// RemoteTelemetryAllowed reports whether this build may send telemetry to a remote service.
func RemoteTelemetryAllowed() bool { return RemoteTelemetryEnabled != "false" }

// PricingCatalogSyncAllowed reports whether this build may download the model price catalog.
func PricingCatalogSyncAllowed() bool { return PricingCatalogSyncEnabled != "false" }

// DefaultPort returns the stamped default port, or fallback when none is stamped or it is invalid.
func DefaultPort(fallback int) int {
	if Port == "" {
		return fallback
	}
	n, err := strconv.Atoi(Port)
	if err != nil || n < 1 || n > 65535 {
		return fallback
	}
	return n
}
