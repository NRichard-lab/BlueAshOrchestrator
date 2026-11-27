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

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

var (
	// StateDirName is the per-user state directory under the home folder (~/.ao for Orchestrator).
	StateDirName = ".ao"
	// Port is the default API port as a decimal string; empty means the built-in default.
	Port = ""
	// WindowsAssetName is the unversioned Windows installer `ao start` downloads from the update server.
	WindowsAssetName = "agent-orchestrator-win32-x64.exe"
	// WindowsInstallDirName is the folder the per-user NSIS install uses under %LOCALAPPDATA%\Programs.
	WindowsInstallDirName = "Agent Orchestrator"
	// WindowsExeName is the installed app executable inside that folder.
	WindowsExeName = "agent-orchestrator.exe"
	// ExecutableName is the packaged app binary without an extension. The macOS and Linux release
	// asset names, the Linux AppImage file and its desktop entry are derived from it.
	ExecutableName = "agent-orchestrator"
	// MacAppName is the macOS bundle name without ".app" (the packager name in brands.json).
	MacAppName = "Agent Orchestrator"
	// ProtocolScheme is the URL scheme the desktop app registers and handles.
	ProtocolScheme = "ao-app"
	// NativeMessagingHost is the Chrome Native Messaging host name Chrome mode registers. Chrome
	// keys the registration by this name, so two products sharing it overwrite each other's.
	NativeMessagingHost = LegacyNativeMessagingHost

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

// Product identity and the environment-variable namespace.
//
// ID and DisplayName name the product that built this binary. The daemon writes ID into
// running.json and its /healthz and /readyz payloads so a client can refuse a daemon that belongs
// to a different product instead of attaching to it (see ForeignProduct for a missing field).
//
// EnvPrefix is the prefix of the instance variables (see instanceEnv). An instance variable locates
// or identifies ONE product's daemon and install: its run file, data directory, port, launch id and
// private runtime resources. Every process a daemon starts inherits them, so if BlueAshAI spelled
// them AO_*, an Orchestrator app started from a BlueAshAI terminal or agent would read BlueAshAI's
// coordinates, attach to BlueAshAI's daemon and never start its own (and the reverse). BlueAshAI
// therefore stamps EnvPrefix=BLUEASH: it reads and exports BLUEASH_RUN_FILE and friends, and never
// honors or passes on the AO_* spelling. Session-scoped variables (AO_SESSION_ID, AO_PROJECT_ID,
// ...) name a session, not a daemon, and keep the AO_ prefix in every build.
var (
	ID          = LegacyID
	DisplayName = "Orchestrator"
	EnvPrefix   = legacyEnvPrefix
)

const (
	// LegacyID is the product id of the upstream Orchestrator profile.
	LegacyID = "legacy"
	// LegacyNativeMessagingHost is the Orchestrator host name. BlueAshAI builds up to
	// 0.15.4-blueash.4 registered under it too, so a build with its own name retires a registration
	// of this one that points into its own data directory (see browserchrome).
	LegacyNativeMessagingHost = "dev.ao.browser_bridge"
	legacyEnvPrefix           = "AO"
)

// instanceEnv is the closed set of instance variables, by their legacy AO_ name.
var instanceEnv = map[string]bool{
	"AO_RUN_FILE":                    true,
	"AO_DATA_DIR":                    true,
	"AO_PORT":                        true,
	"AO_APP_RUN_ID":                  true,
	"AO_OWNER":                       true,
	"AO_ACP_RUNTIME_DIR":             true,
	"AO_BROWSER_RUNTIME_ADDRESS":     true,
	"AO_BROWSER_STREAM_ADDRESS":      true,
	"AO_BROWSER_RUNTIME_TOKEN":       true,
	"AO_BROWSER_RUNTIME_TOKEN_STDIN": true,
	"AO_TMUX_BINARY":                 true,
	"AO_TMUX_SOCKET_NAME":            true,
	"AO_WAKE_HELPER":                 true,
	"AO_APPIMAGE":                    true,
}

// Env returns the name this build uses for an instance variable given its legacy AO_ name: the
// same name for Orchestrator, BLUEASH_<rest> for BlueAshAI. It panics on a name outside the set so
// a typo or a session variable passed by mistake fails the first test that reaches it.
func Env(legacyName string) string {
	if !instanceEnv[legacyName] {
		panic("brand.Env: " + legacyName + " is not an instance variable")
	}
	if !envNamespaced() {
		return legacyName
	}
	return EnvPrefix + strings.TrimPrefix(legacyName, legacyEnvPrefix)
}

// RewriteEnvNames replaces every instance-variable name in text with the name this build uses.
// For embedded scripts and guides written with the legacy AO_ spellings; a no-op for Orchestrator.
// Longer names go first so AO_BROWSER_RUNTIME_TOKEN_STDIN is not rewritten as a prefix match.
func RewriteEnvNames(text []byte) []byte {
	if !envNamespaced() {
		return text
	}
	names := InstanceEnvNames()
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		text = bytes.ReplaceAll(text, []byte(name), []byte(Env(name)))
	}
	return text
}

// InstanceEnvNames returns the legacy AO_ names of every instance variable, sorted.
func InstanceEnvNames() []string {
	names := make([]string, 0, len(instanceEnv))
	for name := range instanceEnv {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ForeignEnvNames returns the instance-variable names that belong to another product in this
// build: the AO_ spellings when this build has its own prefix, none for Orchestrator itself.
func ForeignEnvNames() []string {
	if !envNamespaced() {
		return nil
	}
	return InstanceEnvNames()
}

// ScrubForeignEnv removes another product's instance variables from this process's environment.
// The daemon and CLI call it first thing, so a BlueAshAI process started from an Orchestrator
// terminal neither acts on Orchestrator's coordinates nor hands them to its own children next to
// BlueAshAI session variables.
func ScrubForeignEnv() {
	for _, name := range ForeignEnvNames() {
		_ = os.Unsetenv(name)
	}
}

// ReservedEnvName reports whether a variable name is in a namespace the product owns, which user
// configuration (project env, provider keys) may not set: AO_ always, plus this build's own prefix.
func ReservedEnvName(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasPrefix(upper, legacyEnvPrefix+"_") || (envNamespaced() && strings.HasPrefix(upper, strings.ToUpper(EnvPrefix)+"_"))
}

func envNamespaced() bool { return EnvPrefix != "" && EnvPrefix != legacyEnvPrefix }

// ForeignProduct reports whether a product id read from running.json or a health payload names a
// different product than this build. An empty id is not foreign: it comes from a build that
// predates the field (the upstream Orchestrator, and BlueAshAI up to 0.15.4-blueash.4), so it
// proves nothing either way and the caller's other checks (PID, service name, executable path)
// still decide.
func ForeignProduct(reported string) bool {
	reported = strings.TrimSpace(reported)
	return reported != "" && reported != ID
}

// ProductName is the visible name for a reported product id, for messages about a foreign daemon.
func ProductName(reported string) string {
	switch strings.TrimSpace(reported) {
	case ID:
		return DisplayName
	case LegacyID:
		return "Orchestrator"
	case "blueashai":
		return "BlueAshAI"
	default:
		return "another product (" + strings.TrimSpace(reported) + ")"
	}
}

// ForeignDaemonError describes a daemon that belongs to another product, or nil when it does not
// (see ForeignProduct). where says how it was found, for example a run-file path or a port.
func ForeignDaemonError(reported, where string) error {
	if !ForeignProduct(reported) {
		return nil
	}
	return fmt.Errorf("the daemon at %s belongs to %s, not %s; refusing to use it. "+
		"%s runs its own daemon: check that %s and %s, if set, point at this product's files",
		where, ProductName(reported), DisplayName, DisplayName, Env("AO_RUN_FILE"), Env("AO_DATA_DIR"))
}
