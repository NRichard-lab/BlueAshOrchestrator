package localagent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/localagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

// recordingClient is the client side of ACP in these tests. It keeps every
// update the runner sends.
type recordingClient struct {
	mu      sync.Mutex
	updates []acp.SessionUpdate
}

var _ acp.Client = (*recordingClient)(nil)

func (c *recordingClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, params.Update)
	return nil
}

func (c *recordingClient) snapshot() []acp.SessionUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.SessionUpdate(nil), c.updates...)
}

// text joins the streamed reply, the streamed thinking and the replayed user
// messages seen so far.
func (c *recordingClient) text() (reply, thinking, user string) {
	var replyText, thinkingText, userText strings.Builder
	for _, update := range c.snapshot() {
		switch {
		case update.AgentMessageChunk != nil && update.AgentMessageChunk.Content.Text != nil:
			replyText.WriteString(update.AgentMessageChunk.Content.Text.Text)
		case update.AgentThoughtChunk != nil && update.AgentThoughtChunk.Content.Text != nil:
			thinkingText.WriteString(update.AgentThoughtChunk.Content.Text.Text)
		case update.UserMessageChunk != nil && update.UserMessageChunk.Content.Text != nil:
			userText.WriteString(update.UserMessageChunk.Content.Text.Text + "|")
		}
	}
	return replyText.String(), thinkingText.String(), userText.String()
}

func (c *recordingClient) title() string {
	for _, update := range c.snapshot() {
		if update.SessionInfoUpdate != nil && update.SessionInfoUpdate.Title != nil {
			return *update.SessionInfoUpdate.Title
		}
	}
	return ""
}

func (*recordingClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, acp.NewMethodNotFound(acp.ClientMethodSessionRequestPermission)
}

func (*recordingClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (*recordingClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (*recordingClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (*recordingClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (*recordingClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (*recordingClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (*recordingClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}

// fixture is a data directory with a fake Ollama registered as "lab", and the
// models the design was checked against.
type fixture struct {
	dataDir string
	server  *localproviderstest.Server
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	dataDir := t.TempDir()
	server := localproviderstest.NewOllama(t,
		localproviderstest.OllamaModel{Name: "qwen3.8:27b", Capabilities: []string{"completion", "tools", "thinking"}, MaxContext: 262144, LoadedContext: 32768, Digest: "sha256:bbb"},
		localproviderstest.OllamaModel{Name: "plain:1b", Capabilities: []string{"completion"}, MaxContext: 8192},
		localproviderstest.OllamaModel{Name: "nomic-embed-text:latest", Capabilities: []string{"embedding"}, MaxContext: 2048},
	)
	if _, err := localproviders.Add(dataDir, server.Endpoint("lab", localproviders.KindOllama)); err != nil {
		t.Fatal(err)
	}
	return fixture{dataDir: dataDir, server: server}
}

// connect starts a runner and returns the client side of its connection.
func connect(t *testing.T, opts localagent.Options) (*acp.ClientSideConnection, *recordingClient) {
	t.Helper()
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = localagent.Serve(ctx, agentIn, agentOut, opts)
	}()
	client := &recordingClient{}
	conn := acp.NewClientSideConnection(client, clientOut, clientIn)
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = agentOut.Close()
		<-done
	})
	if _, err := conn.Initialize(context.Background(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return conn, client
}

func newSession(t *testing.T, conn *acp.ClientSideConnection) acp.SessionId {
	t.Helper()
	response, err := conn.NewSession(context.Background(), acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	return response.SessionId
}

func prompt(t *testing.T, conn *acp.ClientSideConnection, id acp.SessionId, text string) (acp.PromptResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return conn.Prompt(ctx, acp.PromptRequest{SessionId: id, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
}

// chatRequest is the part of an /api/chat body the tests read.
type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Options map[string]any `json:"options"`
	Think   *bool          `json:"think"`
	Stream  bool           `json:"stream"`
}

func chatRequests(t *testing.T, server *localproviderstest.Server, route string) []chatRequest {
	t.Helper()
	var out []chatRequest
	for _, body := range server.Bodies(route) {
		var request chatRequest
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatalf("decode %s body: %v\n%s", route, err, body)
		}
		out = append(out, request)
	}
	return out
}

func TestNothingIsSentBeforeAPersonSpeaks(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("", "hi")
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/qwen3.8:27b"})

	// Start a session, change its model, and reload it: everything a daemon
	// does around a conversation except deliver a person's message.
	id := newSession(t, conn)
	if _, err := conn.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{SessionId: id, ConfigId: "model", Value: "lab/plain:1b"},
	}); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if _, err := conn.LoadSession(context.Background(), acp.LoadSessionRequest{SessionId: id, Cwd: t.TempDir(), McpServers: []acp.McpServer{}}); err != nil {
		t.Fatalf("session/load: %v", err)
	}

	// No test prompt, no warm-up, not even a metadata request.
	if requests := fx.server.Requests(); len(requests) != 0 {
		t.Fatalf("the runner contacted the model server before any message: %v", requests)
	}
}

func TestATurnSendsThePersonsMessageOnceAndStreamsTheAnswer(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("Considering.", "Hello", " there")
	conn, client := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/qwen3.8:27b"})
	id := newSession(t, conn)

	response, err := prompt(t, conn, id, "Say hello.\nSecond line.")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q", response.StopReason)
	}
	if response.Usage == nil || response.Usage.InputTokens != 21 || response.Usage.OutputTokens != 7 || response.Usage.TotalTokens != 28 {
		t.Errorf("usage = %+v, want the server's 21 in and 7 out", response.Usage)
	}
	reply, thinking, _ := client.text()
	if reply != "Hello there" || thinking != "Considering." {
		t.Errorf("streamed reply = %q thinking = %q", reply, thinking)
	}
	// The title is the person's first line. No second model call writes one.
	if client.title() != "Say hello." {
		t.Errorf("title = %q, want the first line of the message", client.title())
	}

	// Exactly one generation request, and it is the person's turn.
	if sent := fx.server.GenerationRequests(); len(sent) != 1 || sent[0] != "POST /api/chat" {
		t.Fatalf("generation requests = %v, want exactly one chat call", sent)
	}
	request := chatRequests(t, fx.server, "POST /api/chat")[0]
	if request.Model != "qwen3.8:27b" || !request.Stream {
		t.Errorf("request model = %q stream = %v", request.Model, request.Stream)
	}
	// The context the server has loaded is asked for on every request, so its
	// default cannot silently truncate the conversation, and matching what is
	// loaded avoids a reload.
	if numCtx, _ := request.Options["num_ctx"].(float64); numCtx != 32768 {
		t.Errorf("num_ctx = %v, want the loaded 32768", request.Options["num_ctx"])
	}
	if request.Think == nil || !*request.Think {
		t.Errorf("think = %v, want true for a model that reports thinking", request.Think)
	}
	// Default instructions level: one short compact note, then the message.
	if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v, want the compact note and the person's message", request.Messages)
	}
	if request.Messages[1].Content != "Say hello.\nSecond line." {
		t.Errorf("user message = %q, want it unchanged", request.Messages[1].Content)
	}
	if note := request.Messages[0].Content; len(note) > 600 || !strings.Contains(note, "coding assistant") {
		t.Errorf("compact note = %q, want a short role note", note)
	}

	var usage *acp.SessionUsageUpdate
	for _, update := range client.snapshot() {
		if update.UsageUpdate != nil {
			usage = update.UsageUpdate
		}
	}
	if usage == nil || usage.Used != 28 || usage.Size != 32768 || usage.Cost == nil || usage.Cost.Amount != 0 {
		t.Errorf("usage update = %+v, want 28 of 32768 at zero cost", usage)
	}
}

func TestInstructionsLevels(t *testing.T) {
	standing := filepath.Join(t.TempDir(), "system-prompt.md")
	if err := os.WriteFile(standing, []byte("FULL STANDING INSTRUCTIONS"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		level      localproviders.Instructions
		wantSystem string // "" means no system message at all
	}{
		{localproviders.InstructionsNone, ""},
		{localproviders.InstructionsCompact, "coding assistant"},
		{localproviders.InstructionsStandard, "FULL STANDING INSTRUCTIONS"},
	}
	for _, tc := range cases {
		t.Run(string(tc.level), func(t *testing.T) {
			fx := newFixture(t)
			fx.server.OllamaChat("", "ok")
			if _, err := localproviders.UpdateSettings(fx.dataDir, func(settings *localproviders.Settings) error {
				settings.Instructions = tc.level
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/plain:1b", InstructionsFile: standing})
			if _, err := prompt(t, conn, newSession(t, conn), "hello"); err != nil {
				t.Fatalf("prompt: %v", err)
			}
			request := chatRequests(t, fx.server, "POST /api/chat")[0]
			if tc.wantSystem == "" {
				// Pure pass-through: the model sees only what the person typed.
				if len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "hello" {
					t.Fatalf("messages = %+v, want only the person's message", request.Messages)
				}
				return
			}
			if len(request.Messages) != 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, tc.wantSystem) {
				t.Fatalf("messages = %+v, want a system message containing %q", request.Messages, tc.wantSystem)
			}
			if tc.level == localproviders.InstructionsCompact && strings.Contains(request.Messages[0].Content, "FULL STANDING") {
				t.Error("the compact level sent the full standing instructions")
			}
		})
	}
}

func TestLaterTurnsCarryTheConversation(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("", "first answer")
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/plain:1b"})
	id := newSession(t, conn)
	if _, err := prompt(t, conn, id, "one"); err != nil {
		t.Fatal(err)
	}
	fx.server.OllamaChat("", "second answer")
	if _, err := prompt(t, conn, id, "two"); err != nil {
		t.Fatal(err)
	}
	requests := chatRequests(t, fx.server, "POST /api/chat")
	if len(requests) != 2 {
		t.Fatalf("chat calls = %d, want one per message", len(requests))
	}
	var roles, contents []string
	for _, message := range requests[1].Messages {
		roles = append(roles, message.Role)
		contents = append(contents, message.Content)
	}
	if strings.Join(roles, ",") != "system,user,assistant,user" || contents[1] != "one" || contents[2] != "first answer" || contents[3] != "two" {
		t.Errorf("second request = %v %v, want the first exchange then the new message", roles, contents)
	}
	// A model that does not report thinking is not asked to think.
	if requests[0].Think != nil {
		t.Errorf("think = %v for a model without the capability", *requests[0].Think)
	}
}

func TestATranscriptSurvivesARestart(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("thought", "remembered")
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/qwen3.8:27b"})
	id := newSession(t, conn)
	if _, err := prompt(t, conn, id, "remember this"); err != nil {
		t.Fatal(err)
	}
	transcripts, _ := filepath.Glob(filepath.Join(fx.dataDir, "local-models", "sessions", "*.json"))
	if len(transcripts) != 1 {
		t.Fatalf("transcripts = %v, want one under local-models/sessions", transcripts)
	}

	// A new runner process, as after a daemon restart.
	before := len(fx.server.Requests())
	restarted, client := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/qwen3.8:27b"})
	if _, err := restarted.LoadSession(context.Background(), acp.LoadSessionRequest{SessionId: id, Cwd: t.TempDir(), McpServers: []acp.McpServer{}}); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	reply, thinking, user := client.text()
	if reply != "remembered" || thinking != "thought" || user != "remember this|" {
		t.Errorf("replayed reply = %q thinking = %q user = %q", reply, thinking, user)
	}
	// Restoring a conversation reads a file. It does not touch the server.
	if after := len(fx.server.Requests()); after != before {
		t.Errorf("session/load made %d request(s) to the model server", after-before)
	}

	fx.server.OllamaChat("", "still here")
	if _, err := prompt(t, restarted, id, "and now?"); err != nil {
		t.Fatal(err)
	}
	requests := chatRequests(t, fx.server, "POST /api/chat")
	last := requests[len(requests)-1]
	if len(last.Messages) != 4 || last.Messages[1].Content != "remember this" || last.Messages[2].Content != "remembered" {
		t.Errorf("request after restart = %+v, want the restored exchange", last.Messages)
	}
}

func TestOpenAICompatibleEndpointsUseChatCompletions(t *testing.T) {
	t.Setenv(localproviders.EnvLlamaCppURL, "")
	t.Setenv(localproviders.EnvLlamaCppPort, "")
	dataDir := t.TempDir()
	server := localproviderstest.New(t)
	server.HandleJSON("GET /v1/models", map[string]any{"data": []map[string]any{{"id": "org/coder", "max_model_len": 16384}}})
	server.OpenAIChat("Hi", " from", " vLLM")
	server.RequireKey("k-secret")
	if _, err := localproviders.Add(dataDir, server.Endpoint("vllm", localproviders.KindOpenAICompatible)); err != nil {
		t.Fatal(err)
	}
	if err := localproviders.SetEndpointKey(dataDir, "vllm", "k-secret"); err != nil {
		t.Fatal(err)
	}
	conn, client := connect(t, localagent.Options{DataDir: dataDir, Model: "vllm/org/coder"})
	response, err := prompt(t, conn, newSession(t, conn), "hello")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if reply, _, _ := client.text(); reply != "Hi from vLLM" {
		t.Errorf("reply = %q", reply)
	}
	if response.Usage == nil || response.Usage.InputTokens != 30 || response.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", response.Usage)
	}
	if sent := server.GenerationRequests(); len(sent) != 1 || sent[0] != "POST /v1/chat/completions" {
		t.Fatalf("generation requests = %v", sent)
	}
	request := chatRequests(t, server, "POST /v1/chat/completions")[0]
	if request.Model != "org/coder" || request.Options != nil || request.Think != nil {
		t.Errorf("request = %+v, want the bare model name and no Ollama options", request)
	}
}

func TestTheSettingsDefaultIsUsedWhenTheSessionNamesNoModel(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("", "ok")
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir})
	id := newSession(t, conn)

	// No session model and no default: the turn is refused with what to do,
	// and nothing is sent.
	_, err := prompt(t, conn, id, "hello")
	if err == nil || !strings.Contains(err.Error(), "No model is selected") {
		t.Fatalf("error = %v, want guidance to pick a model", err)
	}
	if len(fx.server.Requests()) != 0 {
		t.Fatalf("requests = %v, want none", fx.server.Requests())
	}

	if _, err := localproviders.UpdateSettings(fx.dataDir, func(settings *localproviders.Settings) error {
		settings.Default = "lab/plain:1b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := prompt(t, conn, id, "hello"); err != nil {
		t.Fatalf("prompt with default: %v", err)
	}
	if request := chatRequests(t, fx.server, "POST /api/chat")[0]; request.Model != "plain:1b" {
		t.Errorf("model = %q, want the settings default", request.Model)
	}
}

func TestTurnsThatCannotRunSendTheModelNothing(t *testing.T) {
	cases := map[string]struct {
		model string
		want  string
	}{
		"embedding model":  {"lab/nomic-embed-text:latest", "embedding model"},
		"missing model":    {"lab/absent:1b", "does not have a model"},
		"unknown endpoint": {"nowhere/model", "unknown local endpoint"},
		"malformed id":     {"no-slash", "must look like"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t)
			fx.server.OllamaChat("", "should never be sent")
			conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: tc.model})
			_, err := prompt(t, conn, newSession(t, conn), "hello")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
			fx.server.RequireNoGeneration(t)
		})
	}
}

func TestAnEmptyMessageIsNotATurn(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("", "x")
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/plain:1b"})
	if _, err := prompt(t, conn, newSession(t, conn), "   "); err == nil {
		t.Fatal("an empty message started a turn")
	}
	if len(fx.server.Requests()) != 0 {
		t.Fatalf("requests = %v, want none", fx.server.Requests())
	}
}

func TestStoppingATurnCancelsTheRequest(t *testing.T) {
	fx := newFixture(t)
	started := make(chan struct{})
	released := make(chan struct{})
	fx.server.Handle("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "partial"}, "done": false})
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(released)
	})
	conn, client := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/plain:1b"})
	id := newSession(t, conn)

	type result struct {
		response acp.PromptResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := prompt(t, conn, id, "long question")
		done <- result{response, err}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never reached the server")
	}
	if err := conn.Cancel(context.Background(), acp.CancelNotification{SessionId: id}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil || outcome.response.StopReason != acp.StopReasonCancelled {
			t.Fatalf("outcome = %+v, %v, want a cancelled turn", outcome.response, outcome.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not stop")
	}
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("the request to the model server was not cancelled")
	}
	if reply, _, _ := client.text(); reply != "partial" {
		t.Errorf("reply = %q, want the part that had arrived", reply)
	}

	// The part the person saw is in the conversation for the next turn.
	fx.server.OllamaChat("", "continued")
	if _, err := prompt(t, conn, id, "go on"); err != nil {
		t.Fatal(err)
	}
	requests := chatRequests(t, fx.server, "POST /api/chat")
	last := requests[len(requests)-1]
	if len(last.Messages) != 4 || last.Messages[2].Content != "partial" {
		t.Errorf("request after a stop = %+v, want the partial answer kept", last.Messages)
	}
}

func TestAServerErrorFailsTheTurnInTheServersWords(t *testing.T) {
	fx := newFixture(t)
	fx.server.Handle("POST /api/chat", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model requires more system memory (41.2 GiB) than is available (30.1 GiB)"}`, http.StatusInternalServerError)
	})
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/plain:1b"})
	id := newSession(t, conn)
	_, err := prompt(t, conn, id, "hello")
	if err == nil || !strings.Contains(err.Error(), "more system memory") {
		t.Fatalf("error = %v, want the server's message", err)
	}
	// A failed turn leaves no trace in the conversation.
	fx.server.OllamaChat("", "ok")
	if _, err := prompt(t, conn, id, "again"); err != nil {
		t.Fatal(err)
	}
	requests := chatRequests(t, fx.server, "POST /api/chat")
	if last := requests[len(requests)-1]; len(last.Messages) != 2 || last.Messages[1].Content != "again" {
		t.Errorf("request after a failure = %+v, want only the new message", last.Messages)
	}
}

func TestAModelThatRefusesThinkingIsRetriedWithoutIt(t *testing.T) {
	fx := newFixture(t)
	calls := 0
	fx.server.Handle("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body chatRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Think != nil {
			http.Error(w, `{"error":"\"qwen3.8:27b\" does not support thinking"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "fine"}, "done": true, "prompt_eval_count": 3, "eval_count": 1})
	})
	conn, client := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/qwen3.8:27b"})
	if _, err := prompt(t, conn, newSession(t, conn), "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if reply, _, _ := client.text(); reply != "fine" || calls != 2 {
		t.Errorf("reply = %q after %d calls, want one retry without think", reply, calls)
	}
}

func TestTheModelControlSwitchesModelsBetweenTurns(t *testing.T) {
	fx := newFixture(t)
	fx.server.OllamaChat("", "ok")
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir, Model: "lab/qwen3.8:27b"})
	id := newSession(t, conn)
	response, err := conn.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{SessionId: id, ConfigId: "model", Value: "lab/plain:1b"},
	})
	if err != nil {
		t.Fatalf("set model: %v", err)
	}
	if len(response.ConfigOptions) != 1 || response.ConfigOptions[0].Select == nil || response.ConfigOptions[0].Select.CurrentValue != "lab/plain:1b" {
		t.Errorf("config options = %+v, want the new model current", response.ConfigOptions)
	}
	if _, err := conn.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{SessionId: id, ConfigId: "model", Value: "not-an-id"},
	}); err == nil {
		t.Error("a malformed model id was accepted")
	}
	if _, err := prompt(t, conn, id, "hello"); err != nil {
		t.Fatal(err)
	}
	if request := chatRequests(t, fx.server, "POST /api/chat")[0]; request.Model != "plain:1b" {
		t.Errorf("model = %q, want the one chosen in the model control", request.Model)
	}
}

func TestLoadRefusesAnIDItDidNotIssue(t *testing.T) {
	fx := newFixture(t)
	conn, _ := connect(t, localagent.Options{DataDir: fx.dataDir})
	for _, id := range []string{"../../etc/passwd", "lm-short", "other-agent-session", `lm-..\..\secrets0000000000`} {
		if _, err := conn.LoadSession(context.Background(), acp.LoadSessionRequest{SessionId: acp.SessionId(id), Cwd: t.TempDir(), McpServers: []acp.McpServer{}}); err == nil {
			t.Errorf("session/load accepted %q", id)
		}
	}
}
