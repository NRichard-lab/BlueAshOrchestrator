package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateUpdateOrigin(t *testing.T) {
	good := map[string]string{
		"https://updates.example.test":      "https://updates.example.test",
		" https://Updates.Example.test/ ":   "https://updates.example.test",
		"https://updates.example.test:8443": "https://updates.example.test:8443",
		"https://notgithub.com":             "https://notgithub.com",
	}
	for in, want := range good {
		got, err := validateUpdateOrigin(in)
		if err != nil || got != want {
			t.Errorf("validateUpdateOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "not a url", "http://updates.example.test", "ftp://updates.example.test",
		"https://user:pw@updates.example.test", "https://updates.example.test/path",
		"https://updates.example.test/?a=1", "https://updates.example.test/#x",
		"https://localhost", "https://127.0.0.1", "https://[::1]", "https://intranet",
		"https://github.com", "https://raw.githubusercontent.com", "https://api.github.com",
		"https://orchestrator.inc", "https://auth.orchestrator.inc", "https://u.expo.dev",
		"https://someone.github.io",
	}
	for _, in := range bad {
		if got, err := validateUpdateOrigin(in); err == nil {
			t.Errorf("validateUpdateOrigin(%q) = %q, want error", in, got)
		}
	}
}

func TestResolveUpdateOriginPrefersStampedThenEnvThenFailsClosed(t *testing.T) {
	orig := updateOrigin
	t.Cleanup(func() { updateOrigin = orig })

	updateOrigin = ""
	t.Setenv(updateOriginEnv, "")
	if _, err := resolveUpdateOrigin(); err == nil {
		t.Fatal("expected a fail-closed error with no stamped origin and no env")
	}
	if _, err := downloadURL("x.zip"); err == nil {
		t.Fatal("downloadURL must fail closed without an origin")
	}

	t.Setenv(updateOriginEnv, "https://env.example.test")
	if got, err := resolveUpdateOrigin(); err != nil || got != "https://env.example.test" {
		t.Fatalf("env origin = %q, %v", got, err)
	}

	updateOrigin = "https://stamped.example.test"
	if got, err := resolveUpdateOrigin(); err != nil || got != "https://stamped.example.test" {
		t.Fatalf("stamped origin must win over env, got %q, %v", got, err)
	}

	// An invalid stamped origin is an error, never a silent fall through to env.
	updateOrigin = "https://github.com"
	if _, err := resolveUpdateOrigin(); err == nil {
		t.Fatal("invalid stamped origin must fail, not fall back")
	}
}

func TestDownloadURLNeverNamesUpstream(t *testing.T) {
	orig := updateOrigin
	t.Cleanup(func() { updateOrigin = orig })
	updateOrigin = ""
	t.Setenv(updateOriginEnv, "")
	for _, asset := range []string{"agent-orchestrator-darwin-arm64.zip", "agent-orchestrator-win32-x64.exe", "agent-orchestrator-linux-x64.AppImage"} {
		if got, err := downloadURL(asset); err == nil || strings.Contains(got, "github") {
			t.Fatalf("downloadURL(%q) = %q, %v; want fail-closed error", asset, got, err)
		}
	}
}

func TestDownloadRefusesCrossHostRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("elsewhere"))
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := &commandContext{deps: Deps{}.withDefaults()}
	var buf strings.Builder
	err := c.download(context.Background(), &buf, srv.URL, "a.zip", filepath.Join(t.TempDir(), "a.zip"))
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("expected refusal of a cross-host redirect, got %v", err)
	}
}
