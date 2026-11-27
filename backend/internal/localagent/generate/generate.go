// Package generate is the only code in the Local Models harness that asks a
// model to produce output.
//
// The rule it exists to keep: a request reaches a model's generation routes
// only as part of a turn a person started. Chat, the single entry point, takes
// a Turn, and a Turn can only be made by UserTurn from a person's message. No
// test prompt, warm-up, title or summary request can be written against this
// package without first having a user message in hand.
//
// Only the runner (internal/localagent) may import this package. A test in
// that package fails if anything else does.
package generate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

// Turn is proof that a person started a turn. Its zero value is not a turn,
// and Chat refuses it.
type Turn struct {
	id      string
	session string
}

// ErrNoUserMessage is returned when a turn is requested without a message from
// a person.
var ErrNoUserMessage = errors.New("a turn needs a message from a person")

// ErrNotAUserTurn is returned by Chat for a Turn that UserTurn did not make.
var ErrNotAUserTurn = errors.New("generation is only allowed inside a turn a person started")

// UserTurn issues the Turn for a message a person sent in a session. It is the
// only way to obtain one, and it refuses an empty message.
func UserTurn(sessionID, userMessage string) (Turn, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Turn{}, errors.New("a turn needs a session")
	}
	if strings.TrimSpace(userMessage) == "" {
		return Turn{}, ErrNoUserMessage
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Turn{}, err
	}
	return Turn{id: hex.EncodeToString(raw[:]), session: sessionID}, nil
}

// ID identifies the turn in logs.
func (t Turn) ID() string { return t.id }

// Roles a message can have.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one entry of the conversation sent to the model.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Thinking is the model's reasoning for an assistant message. It is kept
	// for the transcript and never sent back to the model.
	Thinking string `json:"thinking,omitempty"`
}

// Request is one model call inside a turn.
type Request struct {
	Endpoint localproviders.Endpoint
	// Model is the name as the endpoint lists it, without the endpoint prefix.
	Model    string
	Messages []Message
	// NumCtx is the context size to ask Ollama for. Zero leaves the server's
	// default. Ignored by OpenAI-compatible servers, which fix it at load.
	NumCtx int
	// Think asks Ollama to return reasoning separately. Nil leaves the
	// server's default.
	Think *bool
}

// Delta is a piece of streamed output.
type Delta struct {
	Content  string
	Thinking string
}

// Usage is what the server reported the call consumed.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Response is a completed model call.
type Response struct {
	Message Message
	Usage   Usage
	// FirstToken is how long the first piece of output took to arrive, which
	// on a cold model is mostly load time.
	FirstToken time.Duration
	Duration   time.Duration
}

// Client sends generation requests.
type Client struct {
	// HTTP is the client used. It must not set a Timeout: a cold model can
	// take minutes to load before the first byte. Cancel the context instead.
	HTTP *http.Client
}

// Chat runs one model call for turn, calling onDelta as output arrives. It
// uses Ollama's native route for an Ollama endpoint and chat completions for
// everything else.
func (c *Client) Chat(ctx context.Context, turn Turn, request Request, onDelta func(Delta)) (Response, error) {
	if turn.id == "" {
		return Response{}, ErrNotAUserTurn
	}
	if strings.TrimSpace(request.Model) == "" {
		return Response{}, errors.New("no model selected")
	}
	if onDelta == nil {
		onDelta = func(Delta) {}
	}
	started := time.Now()
	stream := &stream{started: started, onDelta: onDelta}
	var err error
	if request.Endpoint.Kind == localproviders.KindOllama {
		err = c.ollama(ctx, request, stream)
	} else {
		err = c.openAI(ctx, request, stream)
	}
	response := Response{
		Message: Message{Role: RoleAssistant, Content: stream.content.String(), Thinking: stream.thinking.String()},
		Usage:   stream.usage, FirstToken: stream.firstToken, Duration: time.Since(started),
	}
	return response, err
}

// stream accumulates one call's output.
type stream struct {
	started    time.Time
	onDelta    func(Delta)
	content    strings.Builder
	thinking   strings.Builder
	usage      Usage
	firstToken time.Duration
}

func (s *stream) emit(delta Delta) {
	if delta.Content == "" && delta.Thinking == "" {
		return
	}
	if s.firstToken == 0 {
		s.firstToken = time.Since(s.started)
	}
	s.content.WriteString(delta.Content)
	s.thinking.WriteString(delta.Thinking)
	s.onDelta(delta)
}

func (c *Client) post(ctx context.Context, endpoint localproviders.Endpoint, route string, body any) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, localproviders.RootURL(endpoint)+route, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(endpoint.APIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s did not answer: %w", endpoint.Name, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer func() { _ = response.Body.Close() }()
		return nil, serverError(endpoint, response)
	}
	return response, nil
}

// ServerError is a refusal from the model server, in its own words.
type ServerError struct {
	Endpoint string
	Status   int
	Message  string
}

func (e *ServerError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s answered HTTP %d", e.Endpoint, e.Status)
	}
	return fmt.Sprintf("%s answered HTTP %d: %s", e.Endpoint, e.Status, e.Message)
}

func serverError(endpoint localproviders.Endpoint, response *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return &ServerError{Endpoint: endpoint.Name, Status: response.StatusCode, Message: errorText(raw)}
}

// errorText pulls the message out of {"error":"..."} or
// {"error":{"message":"..."}}, falling back to the raw body.
func errorText(raw []byte) string {
	var plain struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &plain) == nil && plain.Error != "" {
		return plain.Error
	}
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &nested) == nil && nested.Error.Message != "" {
		return nested.Error.Message
	}
	return strings.TrimSpace(string(raw))
}

// maxLineBytes bounds one streamed line. A server is trusted no more than any
// other HTTP peer.
const maxLineBytes = 8 << 20

// lines calls each for every non-empty line of body.
func lines(body io.Reader, each func([]byte) error) error {
	reader := bufio.NewReaderSize(body, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > maxLineBytes {
			return errors.New("the server sent a line larger than 8MiB")
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if eachErr := each(trimmed); eachErr != nil {
				return eachErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// --- Ollama: POST /api/chat ------------------------------------------------

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func wireMessages(messages []Message) []wireMessage {
	out := make([]wireMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, wireMessage{Role: message.Role, Content: message.Content})
	}
	return out
}

func (c *Client) ollama(ctx context.Context, request Request, out *stream) error {
	body := map[string]any{
		"model":    request.Model,
		"messages": wireMessages(request.Messages),
		"stream":   true,
	}
	if request.NumCtx > 0 {
		// Set on every request so the server's default cannot silently
		// truncate the conversation.
		body["options"] = map[string]any{"num_ctx": request.NumCtx}
	}
	if request.Think != nil {
		body["think"] = *request.Think
	}
	response, err := c.post(ctx, request.Endpoint, "/api/chat", body)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	done := false
	err = lines(response.Body, func(line []byte) error {
		var chunk struct {
			Message struct {
				Content  string `json:"content"`
				Thinking string `json:"thinking"`
			} `json:"message"`
			Done            bool   `json:"done"`
			Error           string `json:"error"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			return fmt.Errorf("%s sent a line that is not JSON: %w", request.Endpoint.Name, err)
		}
		if chunk.Error != "" {
			return &ServerError{Endpoint: request.Endpoint.Name, Status: http.StatusOK, Message: chunk.Error}
		}
		out.emit(Delta{Content: chunk.Message.Content, Thinking: chunk.Message.Thinking})
		if chunk.Done {
			done = true
			out.usage = Usage{PromptTokens: chunk.PromptEvalCount, CompletionTokens: chunk.EvalCount}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !done {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%s closed the stream before the answer was complete", request.Endpoint.Name)
	}
	return nil
}

// --- OpenAI-compatible: POST /v1/chat/completions --------------------------

func (c *Client) openAI(ctx context.Context, request Request, out *stream) error {
	body := map[string]any{
		"model":    request.Model,
		"messages": wireMessages(request.Messages),
		"stream":   true,
		// Asks for token counts on the last chunk. Servers that do not know
		// the option ignore it.
		"stream_options": map[string]any{"include_usage": true},
	}
	response, err := c.post(ctx, request.Endpoint, "/v1/chat/completions", body)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	finished := false
	err = lines(response.Body, func(line []byte) error {
		payload, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			// Comment and event lines carry nothing. A bare JSON object here is
			// a server reporting an error without the SSE framing.
			if bytes.HasPrefix(line, []byte("{")) {
				if message := errorText(line); message != "" {
					return &ServerError{Endpoint: request.Endpoint.Name, Status: http.StatusOK, Message: message}
				}
			}
			return nil
		}
		payload = bytes.TrimSpace(payload)
		if bytes.Equal(payload, []byte("[DONE]")) {
			finished = true
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return fmt.Errorf("%s sent a chunk that is not JSON: %w", request.Endpoint.Name, err)
		}
		if len(chunk.Error) > 0 && !bytes.Equal(chunk.Error, []byte("null")) {
			return &ServerError{Endpoint: request.Endpoint.Name, Status: http.StatusOK, Message: errorText(payload)}
		}
		for _, choice := range chunk.Choices {
			thinking := choice.Delta.ReasoningContent
			if thinking == "" {
				thinking = choice.Delta.Reasoning
			}
			out.emit(Delta{Content: choice.Delta.Content, Thinking: thinking})
			if choice.FinishReason != nil {
				finished = true
			}
		}
		if chunk.Usage != nil {
			out.usage = Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !finished {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%s closed the stream before the answer was complete", request.Endpoint.Name)
	}
	return nil
}
