package localproviders

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadSettingsDefaultsWhenTheFileIsMissing(t *testing.T) {
	settings, err := LoadSettings(t.TempDir())
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.Version != SettingsVersion || settings.Default != "" {
		t.Errorf("settings = %+v, want empty version-1 defaults", settings)
	}
	// The approved default: a short compact note, with none one setting away.
	if settings.InstructionsLevel() != InstructionsCompact {
		t.Errorf("instructions level = %q, want compact", settings.InstructionsLevel())
	}
}

func TestSettingsRoundTripTheDocumentedShape(t *testing.T) {
	dir := t.TempDir()
	document := `{
  "version": 1,
  "default": "ollama/gpt-oss-20b-mxfp4:latest",
  "instructions": "none",
  "endpoints": { "studio": { "apiKey": "k-123" } },
  "models": { "ollama/gemma4:e2b": { "context": 32768, "tools": false } }
}`
	if err := os.WriteFile(filepath.Join(dir, settingsStoreName), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.Default != "ollama/gpt-oss-20b-mxfp4:latest" || settings.InstructionsLevel() != InstructionsNone {
		t.Errorf("settings = %+v", settings)
	}
	if settings.EndpointKey("STUDIO") != "k-123" {
		t.Errorf("endpoint key lookup must ignore case, got %q", settings.EndpointKey("STUDIO"))
	}
	override := settings.Override("ollama/gemma4:e2b")
	if override.Context == nil || *override.Context != 32768 || override.Tools == nil || *override.Tools {
		t.Errorf("override = %+v", override)
	}
	if override.Thinking != nil || override.Vision != nil {
		t.Errorf("override = %+v, want only what the file set", override)
	}

	saved, err := SaveSettings(dir, settings)
	if err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	reloaded, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Default != saved.Default || reloaded.EndpointKey("studio") != "k-123" || *reloaded.Override("ollama/gemma4:e2b").Context != 32768 {
		t.Errorf("reloaded = %+v", reloaded)
	}
}

func TestSettingsRejectWhatNoCommandCouldHaveWritten(t *testing.T) {
	cases := map[string]string{
		"not json":         `{`,
		"newer version":    `{"version": 2}`,
		"bad instructions": `{"version":1,"instructions":"loud"}`,
		"bad default":      `{"version":1,"default":"no-slash"}`,
		"bad model id":     `{"version":1,"models":{"noslash":{"context":10}}}`,
		"zero context":     `{"version":1,"models":{"ollama/x":{"context":0}}}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, settingsStoreName), []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSettings(dir); err == nil {
				t.Fatalf("LoadSettings accepted %s", document)
			}
			// A file that cannot be read must not be overwritten by an update,
			// or the user's keys in it would be lost.
			if _, err := UpdateSettings(dir, func(*Settings) error { return nil }); err == nil {
				t.Fatal("UpdateSettings overwrote a file it could not read")
			}
			after, _ := os.ReadFile(filepath.Join(dir, settingsStoreName))
			if string(after) != document {
				t.Errorf("file changed to %s", after)
			}
		})
	}
}

func TestParseModelIDSplitsAtTheFirstSlash(t *testing.T) {
	endpoint, model, err := ParseModelID("vllm/org/coder:latest")
	if err != nil || endpoint != "vllm" || model != "org/coder:latest" {
		t.Errorf("ParseModelID = %q, %q, %v", endpoint, model, err)
	}
	if ModelID(endpoint, model) != "vllm/org/coder:latest" {
		t.Error("ModelID does not invert ParseModelID")
	}
	for _, bad := range []string{"", "ollama", "/model", "ollama/"} {
		if _, _, err := ParseModelID(bad); err == nil {
			t.Errorf("ParseModelID(%q) accepted", bad)
		}
	}
}

func TestSetEndpointKeyStoresClearsAndAttaches(t *testing.T) {
	clearLlamaCppOverrides(t)
	dir := t.TempDir()
	if _, err := Add(dir, Endpoint{Name: "studio", BaseURL: "http://127.0.0.1:1234"}); err != nil {
		t.Fatal(err)
	}
	if err := SetEndpointKey(dir, "nope", "k"); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("unknown endpoint error = %v", err)
	}
	if err := SetEndpointKey(dir, "Studio", "  k-123 "); err != nil {
		t.Fatalf("SetEndpointKey: %v", err)
	}
	// A built-in can hold a key too: a keyed server may sit on a default port.
	if err := SetEndpointKey(dir, "llama.cpp", "k-llama"); err != nil {
		t.Fatalf("SetEndpointKey builtin: %v", err)
	}
	keys := map[string]string{}
	for _, endpoint := range All(dir) {
		keys[endpoint.Name] = endpoint.APIKey
	}
	if keys["studio"] != "k-123" || keys["llama.cpp"] != "k-llama" || keys["ollama"] != "" {
		t.Errorf("attached keys = %v", keys)
	}
	found, err := Lookup(dir, "studio")
	if err != nil || found.APIKey != "k-123" {
		t.Errorf("Lookup = %+v, %v", found, err)
	}

	// Neither store that describes endpoints may hold the key in the clear
	// outside local-models.json.
	stored, _ := os.ReadFile(filepath.Join(dir, storeName))
	if strings.Contains(string(stored), "k-123") {
		t.Errorf("%s holds the key: %s", storeName, stored)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, settingsStoreName))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("settings file mode = %v, %v, want 0600", info.Mode().Perm(), err)
		}
	}

	if err := SetEndpointKey(dir, "studio", ""); err != nil {
		t.Fatal(err)
	}
	if found, _ := Lookup(dir, "studio"); found.APIKey != "" {
		t.Errorf("key survived a clear: %q", found.APIKey)
	}
}

func TestAKeyDoesNotFollowAnEndpointToAnotherAddress(t *testing.T) {
	clearLlamaCppOverrides(t)
	dir := t.TempDir()
	if _, err := Add(dir, Endpoint{Name: "box", BaseURL: "http://10.0.0.5:8000"}); err != nil {
		t.Fatal(err)
	}
	if err := SetEndpointKey(dir, "box", "k-box"); err != nil {
		t.Fatal(err)
	}
	// Re-adding at the same address keeps the key.
	if _, err := Add(dir, Endpoint{Name: "box", BaseURL: "http://10.0.0.5:8000/"}); err != nil {
		t.Fatal(err)
	}
	if found, _ := Lookup(dir, "box"); found.APIKey != "k-box" {
		t.Fatalf("key lost on an unchanged address: %q", found.APIKey)
	}
	// A new address is another server: it must not be sent the old key.
	if _, err := Add(dir, Endpoint{Name: "box", BaseURL: "http://10.0.0.9:8000"}); err != nil {
		t.Fatal(err)
	}
	if found, _ := Lookup(dir, "box"); found.APIKey != "" {
		t.Fatalf("key followed the endpoint to a new address: %q", found.APIKey)
	}

	if err := SetEndpointKey(dir, "box", "k-new"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, "box"); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings(dir)
	if err != nil || settings.EndpointKey("box") != "" {
		t.Fatalf("key survived removing its endpoint: %+v, %v", settings, err)
	}
}

func TestEndpointJSONNeverCarriesTheKey(t *testing.T) {
	clearLlamaCppOverrides(t)
	dir := t.TempDir()
	if _, err := Add(dir, Endpoint{Name: "box", BaseURL: "http://10.0.0.5:8000"}); err != nil {
		t.Fatal(err)
	}
	if err := SetEndpointKey(dir, "box", "k-box"); err != nil {
		t.Fatal(err)
	}
	// Add reads and rewrites the endpoint store; the attached key must not be
	// written into it.
	if _, err := Add(dir, Endpoint{Name: "other", BaseURL: "http://10.0.0.6:8000"}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, storeName))
	if err != nil || strings.Contains(string(stored), "k-box") || strings.Contains(strings.ToLower(string(stored)), "apikey") {
		t.Errorf("endpoint store = %s, %v", stored, err)
	}
}
