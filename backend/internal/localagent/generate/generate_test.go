package generate_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/localagent/generate"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders/localproviderstest"
)

func request(server *localproviderstest.Server, kind localproviders.Kind) generate.Request {
	return generate.Request{
		Endpoint: server.Endpoint("lab", kind), Model: "m",
		Messages: []generate.Message{{Role: generate.RoleUser, Content: "hello"}},
	}
}

// The rule the package exists for: no Turn, no request.
func TestChatRefusesAnythingThatIsNotAUserTurn(t *testing.T) {
	server := localproviderstest.New(t)
	server.OllamaChat("", "should never be sent")
	server.OpenAIChat("should never be sent")
	client := &generate.Client{HTTP: server.Client()}

	for _, kind := range []localproviders.Kind{localproviders.KindOllama, localproviders.KindOpenAICompatible} {
		// The zero Turn is what code that has no user message would hold.
		_, err := client.Chat(context.Background(), generate.Turn{}, request(server, kind), nil)
		if !errors.Is(err, generate.ErrNotAUserTurn) {
			t.Errorf("%s: error = %v, want ErrNotAUserTurn", kind, err)
		}
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("a request was sent without a user turn: %v", requests)
	}
}

func TestUserTurnNeedsAMessageFromAPerson(t *testing.T) {
	for _, message := range []string{"", "   ", "\n\t"} {
		if _, err := generate.UserTurn("session", message); !errors.Is(err, generate.ErrNoUserMessage) {
			t.Errorf("UserTurn(%q) error = %v, want ErrNoUserMessage", message, err)
		}
	}
	if _, err := generate.UserTurn("", "hello"); err == nil {
		t.Error("UserTurn accepted a message with no session")
	}
	first, err := generate.UserTurn("session", "hello")
	if err != nil || first.ID() == "" {
		t.Fatalf("UserTurn = %+v, %v", first, err)
	}
	second, _ := generate.UserTurn("session", "hello")
	if first.ID() == second.ID() {
		t.Error("two turns share an id")
	}
}

func userTurn(t *testing.T) generate.Turn {
	t.Helper()
	turn, err := generate.UserTurn("session", "hello")
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestChatStreamsFromOllama(t *testing.T) {
	server := localproviderstest.New(t)
	server.OllamaChat("thinking it over", "Hel", "lo")
	client := &generate.Client{HTTP: server.Client()}

	var deltas []generate.Delta
	response, err := client.Chat(context.Background(), userTurn(t), request(server, localproviders.KindOllama), func(delta generate.Delta) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Message.Content != "Hello" || response.Message.Thinking != "thinking it over" || response.Message.Role != generate.RoleAssistant {
		t.Errorf("message = %+v", response.Message)
	}
	if response.Usage != (generate.Usage{PromptTokens: 21, CompletionTokens: 7}) {
		t.Errorf("usage = %+v", response.Usage)
	}
	if len(deltas) != 3 || deltas[0].Thinking == "" || deltas[1].Content != "Hel" {
		t.Errorf("deltas = %+v, want the thinking then two pieces", deltas)
	}
	if response.FirstToken <= 0 || response.Duration < response.FirstToken {
		t.Errorf("timings = first %v total %v", response.FirstToken, response.Duration)
	}
}

func TestChatStreamsFromChatCompletions(t *testing.T) {
	server := localproviderstest.New(t)
	server.OpenAIChat("Hel", "lo")
	client := &generate.Client{HTTP: server.Client()}
	response, err := client.Chat(context.Background(), userTurn(t), request(server, localproviders.KindOpenAICompatible), nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Message.Content != "Hello" || response.Usage != (generate.Usage{PromptTokens: 30, CompletionTokens: 5}) {
		t.Errorf("response = %+v", response)
	}
}

func TestChatReportsServerErrorsInTheServersWords(t *testing.T) {
	cases := map[string]struct {
		kind    localproviders.Kind
		route   string
		handler http.HandlerFunc
		want    string
	}{
		"ollama status": {localproviders.KindOllama, "POST /api/chat", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"model \"m\" not found"}`, http.StatusNotFound)
		}, `model "m" not found`},
		"ollama mid-stream": {localproviders.KindOllama, "POST /api/chat", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"message":{"content":"par"},"done":false}` + "\n" + `{"error":"out of memory"}` + "\n"))
		}, "out of memory"},
		"openai status": {localproviders.KindOpenAICompatible, "POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":{"message":"context length exceeded"}}`, http.StatusBadRequest)
		}, "context length exceeded"},
		"openai mid-stream": {localproviders.KindOpenAICompatible, "POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`data: {"error":{"message":"backend crashed"}}` + "\n\n"))
		}, "backend crashed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := localproviderstest.New(t)
			server.Handle(tc.route, tc.handler)
			client := &generate.Client{HTTP: server.Client()}
			_, err := client.Chat(context.Background(), userTurn(t), request(server, tc.kind), nil)
			var serverErr *generate.ServerError
			if !errors.As(err, &serverErr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want a ServerError mentioning %q", err, tc.want)
			}
		})
	}
}

func TestChatReportsATruncatedStream(t *testing.T) {
	server := localproviderstest.New(t)
	server.Handle("POST /api/chat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"content":"half"},"done":false}` + "\n"))
	})
	client := &generate.Client{HTTP: server.Client()}
	response, err := client.Chat(context.Background(), userTurn(t), request(server, localproviders.KindOllama), nil)
	if err == nil || !strings.Contains(err.Error(), "before the answer was complete") {
		t.Fatalf("error = %v, want a truncated-stream error", err)
	}
	// What did arrive is still returned, so a caller can show it.
	if response.Message.Content != "half" {
		t.Errorf("content = %q", response.Message.Content)
	}
}

func TestChatSendsTheEndpointKeyAndOllamaOptions(t *testing.T) {
	server := localproviderstest.New(t)
	server.OllamaChat("", "ok")
	server.RequireKey("k-secret")
	client := &generate.Client{HTTP: server.Client()}
	call := request(server, localproviders.KindOllama)
	think := true
	call.NumCtx, call.Think = 4096, &think

	if _, err := client.Chat(context.Background(), userTurn(t), call, nil); err == nil {
		t.Fatal("a keyed server accepted a request without its key")
	}
	call.Endpoint.APIKey = "k-secret"
	if _, err := client.Chat(context.Background(), userTurn(t), call, nil); err != nil {
		t.Fatalf("Chat with key: %v", err)
	}
	bodies := server.Bodies("POST /api/chat")
	last := bodies[len(bodies)-1]
	for _, want := range []string{`"num_ctx":4096`, `"think":true`, `"stream":true`} {
		if !strings.Contains(last, want) {
			t.Errorf("request body lacks %s: %s", want, last)
		}
	}
}

func TestChatNeedsAModel(t *testing.T) {
	server := localproviderstest.New(t)
	client := &generate.Client{HTTP: server.Client()}
	call := request(server, localproviders.KindOllama)
	call.Model = " "
	if _, err := client.Chat(context.Background(), userTurn(t), call, nil); err == nil {
		t.Fatal("Chat accepted a request with no model")
	}
	if len(server.Requests()) != 0 {
		t.Fatal("a request was sent with no model")
	}
}
