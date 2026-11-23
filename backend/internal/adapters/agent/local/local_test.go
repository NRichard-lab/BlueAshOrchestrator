package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifestIDIsLocal(t *testing.T) {
	if got := New().Manifest().ID; got != string(domain.HarnessLocal) {
		t.Fatalf("Manifest.ID = %q", got)
	}
}

func TestGetLaunchCommandInjectsLinkedProviderAndModel(t *testing.T) {
	modelServer := newScriptedOpenAI(t)
	dataDir := t.TempDir()
	if _, err := localproviders.Add(dataDir, localproviders.Endpoint{
		Name: "lab", BaseURL: modelServer.URL, Kind: localproviders.KindOpenAICompatible,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := localproviders.SaveLink(dataDir, localproviders.Link{
		EndpointName: "lab", Model: "coder-v1", APIKey: "test-key",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	writeFakeOpenCode(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))

	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir: dataDir, SessionID: "sess-1", Permissions: ports.PermissionModeAuto,
		Config: ports.AgentConfig{Model: "coder-v1"}, SystemPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd, "\x00")
	if !strings.Contains(joined, "--standalone") || !strings.Contains(joined, "--auto") {
		t.Fatalf("argv = %v", cmd)
	}
	content := envValue(cmd, "OPENCODE_CONFIG_CONTENT")
	if content == "" {
		t.Fatalf("missing OPENCODE_CONFIG_CONTENT in %v", cmd)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	providers := config["providers"].(map[string]any)
	local := providers[localproviders.ProviderID].(map[string]any)
	settings := local["settings"].(map[string]any)
	if got := settings["baseURL"].(string); got != modelServer.URL+"/v1" {
		t.Fatalf("baseURL = %q", got)
	}
	if got := settings["apiKey"].(string); got != "test-key" {
		t.Fatalf("apiKey leaked mismatch = %q", got)
	}
	agent := config["agents"].(map[string]any)["ao-sess-1"].(map[string]any)
	if got := agent["model"].(string); got != "ao-local/coder-v1" {
		t.Fatalf("agent model = %q", got)
	}
	if strings.Contains(strings.Join(cmd, " "), "test-key") && !strings.Contains(content, "test-key") {
		t.Fatal("unexpected key placement")
	}
}

func TestAuthStatusUnauthorizedUntilLinked(t *testing.T) {
	binDir := t.TempDir()
	writeFakeOpenCode(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)

	status, err := New().AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q, want unauthorized", status)
	}

	modelServer := newScriptedOpenAI(t)
	if _, err := localproviders.Add(dataDir, localproviders.Endpoint{
		Name: "lab", BaseURL: modelServer.URL, Kind: localproviders.KindOpenAICompatible,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := localproviders.SaveLink(dataDir, localproviders.Link{EndpointName: "lab", Model: "coder-v1"}); err != nil {
		t.Fatal(err)
	}
	status, err = New().AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q, want configured", status)
	}
}

// TestLocalHarnessFakeEndpointRoundTrip verifies settings → launch → concrete
// model id → tool exchange → session text response against a scripted
// OpenAI-compatible server, using a fake opencode binary that performs the
// HTTP loop AO expects OpenCode 2 to run.
func TestLocalHarnessFakeEndpointRoundTrip(t *testing.T) {
	modelServer := newScriptedOpenAI(t)
	dataDir := t.TempDir()
	if _, err := localproviders.Add(dataDir, localproviders.Endpoint{
		Name: "lab", BaseURL: modelServer.URL, Kind: localproviders.KindOpenAICompatible,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := localproviders.SaveLink(dataDir, localproviders.Link{
		EndpointName: "lab", Model: "coder-v1",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	writeRoundTripOpenCode(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AO_DATA_DIR", dataDir)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))

	argv, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir: dataDir, SessionID: "work-1", Permissions: ports.PermissionModeBypassPermissions,
		Config: ports.AgentConfig{Model: "coder-v1"}, Prompt: "fix the bug",
		SystemPrompt: "You are a coding agent.",
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := execEnvArgv(t, argv)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fake opencode: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "MODEL=ao-local/coder-v1") {
		t.Fatalf("missing model marker in %q", text)
	}
	if !strings.Contains(text, "TOOL=shell") {
		t.Fatalf("missing tool exchange in %q", text)
	}
	if !strings.Contains(text, "RESPONSE=Edited main.go") {
		t.Fatalf("missing session response in %q", text)
	}
	if !strings.Contains(modelServer.lastModel, "coder-v1") {
		t.Fatalf("server saw model %q", modelServer.lastModel)
	}
	if modelServer.sawTools < 1 {
		t.Fatal("expected tool definitions on the model request")
	}
	if strings.Contains(text, "test-key") {
		t.Fatal("token appeared in harness output")
	}
}

type scriptedOpenAI struct {
	URL       string
	mu        sync.Mutex
	lastModel string
	sawTools  int
}

func newScriptedOpenAI(t *testing.T) *scriptedOpenAI {
	t.Helper()
	model := &scriptedOpenAI{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models" || r.URL.Path == "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "coder-v1"}},
			})
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Model string `json:"model"`
				Tools []any  `json:"tools"`
			}
			_ = json.Unmarshal(body, &payload)
			model.mu.Lock()
			model.lastModel = payload.Model
			if len(payload.Tools) > 0 {
				model.sawTools++
			}
			calls := model.sawTools
			model.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if len(payload.Tools) > 0 && calls == 1 {
				args, _ := json.Marshal(map[string]string{"command": "echo ok"})
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id": "call_1", "type": "function",
							"function": map[string]any{"name": "shell", "arguments": string(args)},
						}},
					}, "finish_reason": "tool_calls"}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant", "content": "Edited main.go",
				}, "finish_reason": "stop"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	model.URL = server.URL
	return model
}

func envValue(argv []string, key string) string {
	prefix := key + "="
	for _, arg := range argv {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func writeFakeOpenCode(t *testing.T, binDir string) {
	t.Helper()
	buildOpenCodeFixture(t, binDir, `package main
import ("fmt"; "os")
func main() {
  if len(os.Args) > 1 && os.Args[1] == "--version" {
    fmt.Println("opencode 2.0.0")
    return
  }
  fmt.Println("fake-opencode")
}
`)
}

func writeRoundTripOpenCode(t *testing.T, binDir string) {
	t.Helper()
	// Fake opencode reads OPENCODE_CONFIG_CONTENT, POSTs chat/completions with
	// tools to the injected baseURL, prints model/tool/response markers.
	buildOpenCodeFixture(t, binDir, `package main
import (
  "bytes"
  "encoding/json"
  "fmt"
  "io"
  "net/http"
  "os"
  "strings"
)
func main() {
  if len(os.Args) > 1 && os.Args[1] == "--version" {
    fmt.Println("opencode 2.0.0")
    return
  }
  content := os.Getenv("OPENCODE_CONFIG_CONTENT")
  var cfg map[string]any
  if err := json.Unmarshal([]byte(content), &cfg); err != nil { panic(err) }
  providers := cfg["providers"].(map[string]any)
  local := providers["ao-local"].(map[string]any)
  settings := local["settings"].(map[string]any)
  baseURL := settings["baseURL"].(string)
  apiKey := settings["apiKey"].(string)
  model := cfg["model"].(string)
  fmt.Println("MODEL="+model)
  tools := []any{map[string]any{"type":"function","function":map[string]any{"name":"shell","parameters":map[string]any{"type":"object"}}}}
  body1, _ := json.Marshal(map[string]any{"model": strings.TrimPrefix(model, "ao-local/"), "messages":[]any{map[string]string{"role":"user","content":"fix"}}, "tools": tools})
  req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body1))
  req.Header.Set("Authorization", "Bearer "+apiKey)
  req.Header.Set("Content-Type", "application/json")
  res, err := http.DefaultClient.Do(req)
  if err != nil { panic(err) }
  defer res.Body.Close()
  raw, _ := io.ReadAll(res.Body)
  var first map[string]any
  _ = json.Unmarshal(raw, &first)
  choices := first["choices"].([]any)
  msg := choices[0].(map[string]any)["message"].(map[string]any)
  if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
    fn := calls[0].(map[string]any)["function"].(map[string]any)
    fmt.Println("TOOL="+fn["name"].(string))
  }
  body2, _ := json.Marshal(map[string]any{"model": strings.TrimPrefix(model, "ao-local/"), "messages":[]any{
    map[string]string{"role":"user","content":"fix"},
    map[string]any{"role":"tool","content":"ok"},
  }})
  req2, _ := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body2))
  req2.Header.Set("Authorization", "Bearer "+apiKey)
  req2.Header.Set("Content-Type", "application/json")
  res2, err := http.DefaultClient.Do(req2)
  if err != nil { panic(err) }
  defer res2.Body.Close()
  raw2, _ := io.ReadAll(res2.Body)
  var second map[string]any
  _ = json.Unmarshal(raw2, &second)
  msg2 := second["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
  fmt.Println("RESPONSE="+msg2["content"].(string))
}
`)
}

func buildOpenCodeFixture(t *testing.T, binDir, src string) {
	t.Helper()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(binDir, "opencode")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-o", out, srcPath)
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake opencode: %v\n%s", err, combined)
	}
}

func execEnvArgv(t *testing.T, argv []string) *exec.Cmd {
	t.Helper()
	if len(argv) == 0 {
		t.Fatal("empty argv")
	}
	if filepath.Base(argv[0]) != "env" {
		return exec.Command(argv[0], argv[1:]...)
	}
	var envs []string
	i := 1
	for ; i < len(argv); i++ {
		if !strings.Contains(argv[i], "=") {
			break
		}
		envs = append(envs, argv[i])
	}
	if i >= len(argv) {
		t.Fatalf("no binary in %v", argv)
	}
	cmd := exec.Command(argv[i], argv[i+1:]...)
	cmd.Env = append(os.Environ(), envs...)
	return cmd
}

func TestPrepareACPConfigContentIncludesProvider(t *testing.T) {
	server := newScriptedOpenAI(t)
	dataDir := t.TempDir()
	if _, err := localproviders.Add(dataDir, localproviders.Endpoint{
		Name: "lab", BaseURL: server.URL, Kind: localproviders.KindOpenAICompatible,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := localproviders.SaveLink(dataDir, localproviders.Link{EndpointName: "lab", Model: "coder-v1"}); err != nil {
		t.Fatal(err)
	}
	content, err := PrepareACPConfigContent(`{"agents":{}}`, "rules", ports.PermissionModeDefault, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, localproviders.ProviderID) || !strings.Contains(content, "coder-v1") {
		t.Fatalf("content = %s", content)
	}
	if strings.Contains(fmt.Sprintf("%v", content), "Bearer") {
		t.Fatal("unexpected bearer wording")
	}
}
