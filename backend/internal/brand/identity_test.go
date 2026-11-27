package brand

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func stampBlueAshNames(t *testing.T) {
	t.Helper()
	oldExe, oldApp, oldScheme, oldHost := ExecutableName, MacAppName, ProtocolScheme, NativeMessagingHost
	t.Cleanup(func() {
		ExecutableName, MacAppName, ProtocolScheme, NativeMessagingHost = oldExe, oldApp, oldScheme, oldHost
	})
	ExecutableName, MacAppName = "BlueAshAI", "BlueAshAI"
	ProtocolScheme, NativeMessagingHost = "blueashai", "ai.blueash.browser_bridge"
}

func TestProductNamesDefaultToOrchestrator(t *testing.T) {
	// An unstamped binary (source build, tests, the legacy product) must behave exactly as before.
	for name, got := range map[string]string{
		"ExecutableName":      ExecutableName,
		"MacAppName":          MacAppName,
		"ProtocolScheme":      ProtocolScheme,
		"NativeMessagingHost": NativeMessagingHost,
		"WindowsExeName":      WindowsExeName,
	} {
		want := map[string]string{
			"ExecutableName":      "agent-orchestrator",
			"MacAppName":          "Agent Orchestrator",
			"ProtocolScheme":      "ao-app",
			"NativeMessagingHost": LegacyNativeMessagingHost,
			"WindowsExeName":      "agent-orchestrator.exe",
		}[name]
		if got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if LegacyNativeMessagingHost != "dev.ao.browser_bridge" {
		t.Fatalf("LegacyNativeMessagingHost = %q", LegacyNativeMessagingHost)
	}
}

// The names a stamped build must not share with the other product: each one is a place where two
// products would otherwise resolve, download, launch or register the same thing.
func TestStampedNamesShareNothingWithOrchestrator(t *testing.T) {
	legacy := map[string]string{
		"ExecutableName":      ExecutableName,
		"MacAppName":          MacAppName,
		"ProtocolScheme":      ProtocolScheme,
		"NativeMessagingHost": NativeMessagingHost,
		"StateDirName":        StateDirName,
	}
	stampBlueAshNames(t)
	for name, before := range legacy {
		after := map[string]string{
			"ExecutableName":      ExecutableName,
			"MacAppName":          MacAppName,
			"ProtocolScheme":      ProtocolScheme,
			"NativeMessagingHost": NativeMessagingHost,
			"StateDirName":        StateDirName,
		}[name]
		if name == "StateDirName" {
			continue // stamped separately; covered by the brand smoke and brand_test.go
		}
		if after == before {
			t.Fatalf("%s is still the Orchestrator value %q after stamping", name, before)
		}
	}
	// Chrome accepts only lowercase letters, digits, underscore and dot in a host name.
	if !regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`).MatchString(NativeMessagingHost) {
		t.Fatalf("NativeMessagingHost %q is not a name Chrome accepts", NativeMessagingHost)
	}
}

// Every product-identity name must be read from this package, never written out in another file:
// a literal is a value that keeps saying "Orchestrator" in a BlueAshAI build.
func TestProductNamesAreOnlyWrittenInThisPackage(t *testing.T) {
	forbidden := regexp.MustCompile(strings.Join([]string{
		`Agent Orchestrator\.app`,               // the macOS bundle name
		`Agent Orchestrator-`,                   // versioned/arch artifact names
		`dev\.ao\.browser_bridge`,               // the Chrome native messaging host
		`dev\.agent-orchestrator\.desktop`,      // the macOS/electron-builder app id
		`agent-orchestrator-(darwin|linux|win)`, // release asset names
		`agent-orchestrator\.AppImage`,
		`agent-orchestrator-ao-app\.desktop`,
		`"ao-app"`, // the URL scheme
	}, "|"))

	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() {
			if strings.HasSuffix(slash, "internal/brand") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(slash, ".go") || strings.HasSuffix(slash, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments may still name Orchestrator's values as the example they are.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if forbidden.MatchString(line) {
				offenders = append(offenders, slash+":"+itoa(i+1)+": "+trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("product-identity names written outside internal/brand:\n%s", strings.Join(offenders, "\n"))
	}
}
