package brand

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func stampBlueAsh(t *testing.T) {
	t.Helper()
	oldID, oldName, oldPrefix := ID, DisplayName, EnvPrefix
	t.Cleanup(func() { ID, DisplayName, EnvPrefix = oldID, oldName, oldPrefix })
	ID, DisplayName, EnvPrefix = "blueashai", "BlueAshAI", "BLUEASH"
}

func TestEnvKeepsLegacyNamesForOrchestrator(t *testing.T) {
	if ID != LegacyID || EnvPrefix != "AO" {
		t.Fatalf("source defaults must stay legacy: id %q prefix %q", ID, EnvPrefix)
	}
	for _, name := range InstanceEnvNames() {
		if got := Env(name); got != name {
			t.Fatalf("Env(%q) = %q in an unstamped build", name, got)
		}
	}
	if got := ForeignEnvNames(); got != nil {
		t.Fatalf("Orchestrator has no foreign instance variables, got %v", got)
	}
}

func TestEnvUsesOwnPrefixWhenStamped(t *testing.T) {
	stampBlueAsh(t)
	for legacy, want := range map[string]string{
		"AO_RUN_FILE":                    "BLUEASH_RUN_FILE",
		"AO_DATA_DIR":                    "BLUEASH_DATA_DIR",
		"AO_PORT":                        "BLUEASH_PORT",
		"AO_APP_RUN_ID":                  "BLUEASH_APP_RUN_ID",
		"AO_OWNER":                       "BLUEASH_OWNER",
		"AO_BROWSER_RUNTIME_TOKEN_STDIN": "BLUEASH_BROWSER_RUNTIME_TOKEN_STDIN",
	} {
		if got := Env(legacy); got != want {
			t.Fatalf("Env(%q) = %q, want %q", legacy, got, want)
		}
	}
	// No BlueAshAI instance variable may keep a name an Orchestrator build reads.
	for _, name := range InstanceEnvNames() {
		if got := Env(name); strings.HasPrefix(got, "AO_") {
			t.Fatalf("Env(%q) = %q still uses the AO_ namespace", name, got)
		}
	}
}

func TestEnvRejectsSessionAndUnknownNames(t *testing.T) {
	for _, name := range []string{"AO_SESSION_ID", "AO_PROJECT_ID", "AO_RUNFILE", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("Env(%q) must panic: it is not an instance variable", name)
				}
			}()
			_ = Env(name)
		}()
	}
}

func TestScrubForeignEnvRemovesOnlyAnotherProductsInstanceVariables(t *testing.T) {
	t.Setenv("AO_RUN_FILE", "/home/u/.ao/running.json")
	t.Setenv("AO_DATA_DIR", "/home/u/.ao/data")
	t.Setenv("AO_PORT", "3001")
	t.Setenv("AO_APP_RUN_ID", "apprun-other")
	t.Setenv("AO_SESSION_ID", "proj-1")
	t.Setenv("BLUEASH_RUN_FILE", "/home/u/.blueashai/running.json")

	// Orchestrator: these are its own variables; nothing is removed.
	ScrubForeignEnv()
	if os.Getenv("AO_RUN_FILE") == "" {
		t.Fatal("an unstamped build must keep its own AO_RUN_FILE")
	}

	stampBlueAsh(t)
	ScrubForeignEnv()
	for _, name := range []string{"AO_RUN_FILE", "AO_DATA_DIR", "AO_PORT", "AO_APP_RUN_ID"} {
		if _, present := os.LookupEnv(name); present {
			t.Fatalf("%s must be removed from a BlueAshAI process", name)
		}
	}
	if os.Getenv("AO_SESSION_ID") != "proj-1" {
		t.Fatal("session variables keep the AO_ prefix and must survive")
	}
	if os.Getenv("BLUEASH_RUN_FILE") == "" {
		t.Fatal("the build's own instance variable must survive")
	}
}

func TestForeignProduct(t *testing.T) {
	if ForeignProduct("") || ForeignProduct("legacy") || !ForeignProduct("blueashai") {
		t.Fatal("legacy build: empty and legacy are not foreign, blueashai is")
	}
	if err := ForeignDaemonError("", "x"); err != nil {
		t.Fatalf("a daemon that reports no product predates the field and is not refused: %v", err)
	}
	stampBlueAsh(t)
	if ForeignProduct("") || ForeignProduct("blueashai") || !ForeignProduct("legacy") {
		t.Fatal("BlueAshAI build: empty and blueashai are not foreign, legacy is")
	}
	err := ForeignDaemonError("legacy", `C:\Users\u\.ao\running.json`)
	if err == nil {
		t.Fatal("an Orchestrator daemon must be refused by a BlueAshAI build")
	}
	for _, want := range []string{"Orchestrator", "BlueAshAI", `C:\Users\u\.ao\running.json`, "BLUEASH_RUN_FILE", "BLUEASH_DATA_DIR"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q should mention %q", err, want)
		}
	}
}

func TestReservedEnvName(t *testing.T) {
	if !ReservedEnvName("AO_ANYTHING") || !ReservedEnvName("ao_port") || ReservedEnvName("BLUEASH_PORT") || ReservedEnvName("PATH") {
		t.Fatal("legacy build reserves exactly the AO_ namespace")
	}
	stampBlueAsh(t)
	if !ReservedEnvName("AO_ANYTHING") || !ReservedEnvName("BLUEASH_PORT") || !ReservedEnvName("blueash_run_file") || ReservedEnvName("PATH") {
		t.Fatal("BlueAshAI build reserves AO_ and BLUEASH_")
	}
}

// Every instance variable must be named through Env. A bare "AO_RUN_FILE" literal in product code
// would make a BlueAshAI build read or export Orchestrator's name again, which is the leak this
// namespace exists to prevent. testenv lists raw names on purpose (it clears a host session's
// variables from the test process).
func TestInstanceVariablesAreOnlyNamedThroughEnv(t *testing.T) {
	names := InstanceEnvNames()
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	literal := regexp.MustCompile(`"(` + strings.Join(quoted, "|") + `)"`)
	wrapped := regexp.MustCompile(`brand\.Env\("(` + strings.Join(quoted, "|") + `)"\)`)

	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() {
			if strings.HasSuffix(slash, "internal/brand") || strings.HasSuffix(slash, "internal/testenv") {
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
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if len(literal.FindAllString(line, -1)) > len(wrapped.FindAllString(line, -1)) {
				offenders = append(offenders, slash+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("instance variables named without brand.Env:\n%s", strings.Join(offenders, "\n"))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestRewriteEnvNames(t *testing.T) {
	script := []byte("routing.AO_DATA_DIR; ['AO_SESSION_ID','AO_RUN_FILE']; AO_BROWSER_RUNTIME_TOKEN_STDIN AO_BROWSER_RUNTIME_TOKEN")
	if got := string(RewriteEnvNames(script)); got != string(script) {
		t.Fatalf("an unstamped build must leave text alone, got %q", got)
	}
	stampBlueAsh(t)
	want := "routing.BLUEASH_DATA_DIR; ['AO_SESSION_ID','BLUEASH_RUN_FILE']; BLUEASH_BROWSER_RUNTIME_TOKEN_STDIN BLUEASH_BROWSER_RUNTIME_TOKEN"
	if got := string(RewriteEnvNames(script)); got != want {
		t.Fatalf("RewriteEnvNames = %q, want %q", got, want)
	}
}
