package localproviders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveAndLoadLink(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveLink(dir, Link{EndpointName: "ollama", Model: "llama3.2"}); err != nil {
		t.Fatal(err)
	}
	link, ok, err := LoadLink(dir)
	if err != nil || !ok {
		t.Fatalf("LoadLink: ok=%v err=%v", ok, err)
	}
	if link.EndpointName != "ollama" || link.Model != "llama3.2" {
		t.Fatalf("link = %#v", link)
	}
	if err := ClearLink(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadLink(dir); err != nil || ok {
		t.Fatalf("after clear: ok=%v err=%v", ok, err)
	}
}

func TestSaveLinkRejectsUnknownEndpoint(t *testing.T) {
	if _, err := SaveLink(t.TempDir(), Link{EndpointName: "missing", Model: "m"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestProviderConfigFragmentUsesV1BaseAndPlaceholderKey(t *testing.T) {
	endpoint := Endpoint{Name: "lab", BaseURL: "http://127.0.0.1:9", Kind: KindOpenAICompatible}
	frag := ProviderConfigFragment(endpoint, Link{Model: "coder", APIKey: ""})
	provider := frag[ProviderID].(map[string]any)
	settings := provider["settings"].(map[string]string)
	if settings["baseURL"] != "http://127.0.0.1:9/v1" {
		t.Fatalf("baseURL = %q", settings["baseURL"])
	}
	if settings["apiKey"] != "local" {
		t.Fatalf("apiKey = %q", settings["apiKey"])
	}
	models := provider["models"].(map[string]any)
	if models["coder"] == nil {
		t.Fatalf("models = %#v", models)
	}
}

func TestOpenCodeModelID(t *testing.T) {
	if got := OpenCodeModelID("coder"); got != "ao-local/coder" {
		t.Fatalf("got %q", got)
	}
	if got := OpenCodeModelID("ao-local/coder"); got != "ao-local/coder" {
		t.Fatalf("got %q", got)
	}
}

func TestVerifyLinkedModelAgainstFakeEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "coder-v1"}},
		})
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	if _, err := Add(dir, Endpoint{Name: "lab", BaseURL: server.URL, Kind: KindOpenAICompatible}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveLink(dir, Link{EndpointName: "lab", Model: "coder-v1"}); err != nil {
		t.Fatal(err)
	}
	endpoint, link, err := VerifyLinkedModel(context.Background(), server.Client(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Name != "lab" || link.Model != "coder-v1" {
		t.Fatalf("endpoint=%#v link=%#v", endpoint, link)
	}
}

func TestLinkFilePermissionsHideSecretsFromGroup(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveLink(dir, Link{EndpointName: "ollama", Model: "m", APIKey: "secret-token"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, linkStoreName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("link file mode = %v, want no group/other bits", perm)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, linkStoreName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "secret-token") {
		t.Fatal("expected key stored in file")
	}
}
