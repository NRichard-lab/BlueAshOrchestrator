// Package localagent is the Local Models runner: a small ACP agent built into
// the ao binary that holds a conversation with a model on a local endpoint.
//
// It needs no third-party CLI. The daemon launches it as `ao local-agent acp`
// and talks to it over stdin and stdout, the way it talks to every other ACP
// chat harness.
//
// The runner sends a model something only inside Prompt, which is the ACP
// request a person's message arrives in. Everything else it does with an
// endpoint (finding out what a model can do, checking it is there) goes
// through localproviders, which can only make metadata requests.
package localagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/localagent/generate"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

// AgentName identifies the runner in the ACP handshake.
const AgentName = "ao-local-models"

// Options configure one runner process.
type Options struct {
	// DataDir is the Orchestrator data directory: endpoints, settings and
	// transcripts live under it.
	DataDir string
	// Model is the <endpoint>/<model> the session starts with. Empty falls
	// back to the default in local-models.json.
	Model string
	// InstructionsFile holds the full standing instructions the daemon gave
	// this session. It is only read at the standard instructions level.
	InstructionsFile string
	// Generation is the HTTP client for model calls. It must not have a
	// Timeout. Nil uses a plain client.
	Generation *http.Client
	// Metadata is the HTTP client for metadata requests. Nil uses the
	// localproviders default, which times out quickly.
	Metadata *http.Client
	Version  string
	Log      *slog.Logger
}

// updater is the part of the ACP connection the agent sends through.
type updater interface {
	SessionUpdate(ctx context.Context, params acp.SessionNotification) error
}

// Agent implements the agent side of ACP.
type Agent struct {
	opts   Options
	store  store
	client *generate.Client
	conn   updater
	log    *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

var (
	_ acp.Agent       = (*Agent)(nil)
	_ acp.AgentLoader = (*Agent)(nil)
)

// session is one conversation held in memory.
type session struct {
	mu         sync.Mutex
	transcript transcript
	// cancel is non-nil while a turn runs.
	cancel context.CancelFunc
}

// New returns a runner that is not yet connected to a client.
func New(opts Options) *Agent {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Agent{
		opts:     opts,
		store:    newStore(opts.DataDir),
		client:   &generate.Client{HTTP: opts.Generation},
		log:      log,
		sessions: map[string]*session{},
	}
}

// Serve runs the runner over an ACP connection until the client disconnects
// or ctx ends.
func Serve(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	agent := New(opts)
	conn := acp.NewAgentSideConnection(agent, out, in)
	if opts.Log != nil {
		conn.SetLogger(opts.Log)
	}
	agent.conn = conn
	select {
	case <-conn.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// failure is an error a person can read. The daemon's binding takes the text
// from data.details, the place the ACP SDK puts a plain handler error.
func failure(format string, args ...any) error {
	text := fmt.Sprintf(format, args...)
	return &acp.RequestError{Code: -32603, Message: text, Data: map[string]any{"details": text}}
}

// errSessionNotFound uses the wording the daemon recognises as "this agent no
// longer holds that session", so it reloads the session instead of failing.
func errSessionNotFound() error { return failure("session not found") }

// Initialize answers the ACP handshake.
func (a *Agent) Initialize(_ context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:        true,
			PromptCapabilities: acp.PromptCapabilities{EmbeddedContext: true},
		},
		AgentInfo:   &acp.Implementation{Name: AgentName, Title: acp.Ptr("Local Models"), Version: a.opts.Version},
		AuthMethods: []acp.AuthMethod{},
	}, nil
}

// Authenticate has nothing to do: a local endpoint's key is configuration,
// not a login.
func (a *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

// Logout is not supported.
func (a *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, acp.NewMethodNotFound(acp.AgentMethodLogout)
}

// ListSessions is not supported.
func (a *Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}

// ResumeSession is not supported; LoadSession restores a conversation.
func (a *Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionResume)
}

// CloseSession is not supported.
func (a *Agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionClose)
}

// SetSessionMode accepts any mode. Permission decisions are made per tool
// call through session/request_permission, so the runner keeps no mode.
func (a *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

// NewSession opens a conversation. It makes no request to any endpoint.
func (a *Agent) NewSession(_ context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	id, err := newSessionID()
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	now := time.Now().UTC()
	opened := &session{transcript: transcript{
		ID: id, Cwd: params.Cwd, Model: strings.TrimSpace(a.opts.Model), CreatedAt: now, UpdatedAt: now,
	}}
	a.mu.Lock()
	a.sessions[id] = opened
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: acp.SessionId(id), ConfigOptions: modelOptions(opened.transcript.Model)}, nil
}

// LoadSession restores a conversation from its transcript and replays it to
// the client. It makes no request to any endpoint.
func (a *Agent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	id := string(params.SessionId)
	stored, found, err := a.store.load(id)
	if errors.Is(err, errInvalidSessionID) {
		return acp.LoadSessionResponse{}, &acp.RequestError{Code: -32002, Message: "Resource not found", Data: map[string]any{"details": "session not found"}}
	}
	if err != nil {
		return acp.LoadSessionResponse{}, failure("%v", err)
	}
	if !found {
		// A session that never completed a turn has no transcript. It resumes
		// as the empty conversation it is.
		now := time.Now().UTC()
		stored = transcript{ID: id, Model: strings.TrimSpace(a.opts.Model), CreatedAt: now, UpdatedAt: now}
	}
	if cwd := strings.TrimSpace(params.Cwd); cwd != "" {
		stored.Cwd = cwd
	}
	// The model this launch names wins over the one the transcript last used:
	// the daemon passes the session's current choice.
	if model := strings.TrimSpace(a.opts.Model); model != "" {
		stored.Model = model
	}
	a.mu.Lock()
	a.sessions[id] = &session{transcript: stored}
	a.mu.Unlock()

	for _, message := range stored.Messages {
		for _, update := range replayUpdates(message) {
			if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: params.SessionId, Update: update}); err != nil {
				return acp.LoadSessionResponse{}, err
			}
		}
	}
	return acp.LoadSessionResponse{ConfigOptions: modelOptions(stored.Model)}, nil
}

func replayUpdates(message storedMessage) []acp.SessionUpdate {
	switch message.Role {
	case generate.RoleUser:
		update := acp.UpdateUserMessageText(message.Content)
		update.UserMessageChunk.MessageId = acp.Ptr(message.ID)
		return []acp.SessionUpdate{update}
	case generate.RoleAssistant:
		var updates []acp.SessionUpdate
		if message.Thinking != "" {
			updates = append(updates, thoughtUpdate(message.ID, message.Thinking))
		}
		if message.Content != "" {
			updates = append(updates, messageUpdate(message.ID, message.Content))
		}
		return updates
	}
	return nil
}

func messageUpdate(messageID, text string) acp.SessionUpdate {
	update := acp.UpdateAgentMessageText(text)
	update.AgentMessageChunk.MessageId = acp.Ptr(messageID)
	return update
}

func thoughtUpdate(messageID, text string) acp.SessionUpdate {
	update := acp.UpdateAgentThoughtText(text)
	update.AgentThoughtChunk.MessageId = acp.Ptr(messageID + "-thought")
	return update
}

// modelOptions advertises the session's model as an ACP config option, so the
// client can read which model is current. The list of models to choose from
// comes from the daemon's model catalog, not from here.
func modelOptions(current string) []acp.SessionConfigOption {
	if current == "" {
		return []acp.SessionConfigOption{}
	}
	choices := acp.SessionConfigSelectOptionsUngrouped{{Name: current, Value: acp.SessionConfigValueId(current)}}
	option := acp.NewSessionConfigOptionSelect(acp.SessionConfigValueId(current), acp.SessionConfigSelectOptions{Ungrouped: &choices})
	option.Select.Id = "model"
	option.Select.Name = "Model"
	option.Select.Category = acp.Ptr(acp.SessionConfigOptionCategoryModel)
	return []acp.SessionConfigOption{option}
}

// SetSessionConfigOption changes the session's model. It makes no request to
// any endpoint: the model is checked when a turn uses it.
func (a *Agent) SetSessionConfigOption(_ context.Context, params acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	if params.ValueId == nil || params.ValueId.ConfigId != "model" {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"details": "the only session option is model"})
	}
	current := a.session(string(params.ValueId.SessionId))
	if current == nil {
		return acp.SetSessionConfigOptionResponse{}, errSessionNotFound()
	}
	model := strings.TrimSpace(string(params.ValueId.Value))
	if _, _, err := localproviders.ParseModelID(model); err != nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"details": err.Error()})
	}
	current.mu.Lock()
	current.transcript.Model = model
	current.mu.Unlock()
	return acp.SetSessionConfigOptionResponse{ConfigOptions: modelOptions(model)}, nil
}

func (a *Agent) session(id string) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

// Cancel stops the session's running turn.
func (a *Agent) Cancel(_ context.Context, params acp.CancelNotification) error {
	if current := a.session(string(params.SessionId)); current != nil {
		current.mu.Lock()
		cancel := current.cancel
		current.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return nil
}

// promptText flattens what a person sent into the text given to the model.
// Embedded text resources (a file the person attached) are included under
// their name. Images and audio are not supported yet and are named so the
// person can see they were left out.
func promptText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			parts = append(parts, block.Text.Text)
		case block.Resource != nil && block.Resource.Resource.TextResourceContents != nil:
			resource := block.Resource.Resource.TextResourceContents
			parts = append(parts, "File "+resource.Uri+":\n"+resource.Text)
		case block.ResourceLink != nil:
			parts = append(parts, "Referenced file: "+block.ResourceLink.Uri)
		case block.Image != nil:
			parts = append(parts, "[An image was attached. This harness cannot send images yet.]")
		case block.Audio != nil:
			parts = append(parts, "[Audio was attached. This harness cannot send audio.]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// target is the endpoint and model a turn runs on, with what is known about
// the model.
type target struct {
	endpoint     localproviders.Endpoint
	model        string
	capabilities localproviders.Capabilities
}

// metadataTimeout bounds the metadata requests made before a turn.
const metadataTimeout = 10 * time.Second

// resolve finds the model a turn uses, first match wins: the session's model
// (from the launch or the model control), then the default in
// local-models.json. It reads the model's capabilities over metadata routes.
func (a *Agent) resolve(ctx context.Context, sessionModel string, settings localproviders.Settings) (target, error) {
	id := strings.TrimSpace(sessionModel)
	if id == "" {
		id = settings.Default
	}
	if id == "" {
		return target{}, failure("No model is selected. Pick one in the model control, or set a default with `ao local-provider use <endpoint>/<model>`.")
	}
	endpointName, model, err := localproviders.ParseModelID(id)
	if err != nil {
		return target{}, failure("%v", err)
	}
	endpoint, err := localproviders.Lookup(a.opts.DataDir, endpointName)
	if err != nil {
		return target{}, failure("%v", err)
	}
	metadataCtx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	capabilities, err := localproviders.Inspect(metadataCtx, a.opts.Metadata, endpoint, model)
	if err != nil {
		if errors.Is(err, localproviders.ErrModelNotFound) {
			return target{}, failure("%s does not have a model named %q. `ao local-provider models %s` lists what it serves.", endpoint.Name, model, endpoint.Name)
		}
		return target{}, failure("%v. Is the model server running?", err)
	}
	capabilities = localproviders.ApplyOverrides(capabilities, settings.Override(capabilities.ID))
	if !capabilities.Chat {
		return target{}, failure("%s is an embedding model and cannot hold a conversation. Pick a chat model.", capabilities.ID)
	}
	return target{endpoint: endpoint, model: model, capabilities: capabilities}, nil
}

// Prompt runs one turn. It is the only method that sends a model anything,
// and it does so with a Turn made from the message the person sent.
func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	sessionID := string(params.SessionId)
	current := a.session(sessionID)
	if current == nil {
		return acp.PromptResponse{}, errSessionNotFound()
	}
	userText := promptText(params.Prompt)
	turn, err := generate.UserTurn(sessionID, userText)
	if err != nil {
		return acp.PromptResponse{}, acp.NewInvalidParams(map[string]any{"details": err.Error()})
	}

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	current.mu.Lock()
	if current.cancel != nil {
		current.mu.Unlock()
		return acp.PromptResponse{}, failure("a turn is already running in this session")
	}
	current.cancel = cancel
	history := append([]storedMessage(nil), current.transcript.Messages...)
	sessionModel, cwd := current.transcript.Model, current.transcript.Cwd
	current.mu.Unlock()
	defer func() {
		current.mu.Lock()
		current.cancel = nil
		current.mu.Unlock()
	}()

	settings, err := localproviders.LoadSettings(a.opts.DataDir)
	if err != nil {
		return acp.PromptResponse{}, failure("%v", err)
	}
	where, err := a.resolve(turnCtx, sessionModel, settings)
	if err != nil {
		return acp.PromptResponse{}, err
	}

	messages := make([]generate.Message, 0, len(history)+2)
	if instructions := standingInstructions(settings.InstructionsLevel(), readStandardInstructions(a.opts.InstructionsFile), cwd); instructions != "" {
		messages = append(messages, generate.Message{Role: generate.RoleSystem, Content: instructions})
	}
	for _, message := range history {
		messages = append(messages, generate.Message{Role: message.Role, Content: message.Content})
	}
	messages = append(messages, generate.Message{Role: generate.RoleUser, Content: userText})

	request := generate.Request{Endpoint: where.endpoint, Model: where.model, Messages: messages}
	if where.endpoint.Kind == localproviders.KindOllama {
		request.NumCtx = where.capabilities.Context.Value
		if where.capabilities.Thinking.Value {
			request.Think = acp.Ptr(true)
		}
	}

	userMessage := storedMessage{ID: newMessageID(), Role: generate.RoleUser, Content: userText}
	if params.MessageId != nil && strings.TrimSpace(*params.MessageId) != "" {
		userMessage.ID = strings.TrimSpace(*params.MessageId)
	}
	reply := storedMessage{ID: newMessageID(), Role: generate.RoleAssistant}
	send := func(update acp.SessionUpdate) {
		// A failed notification means the client is gone; the turn's own
		// context ends with the connection.
		_ = a.conn.SessionUpdate(turnCtx, acp.SessionNotification{SessionId: params.SessionId, Update: update})
	}
	onDelta := func(delta generate.Delta) {
		if delta.Thinking != "" {
			send(thoughtUpdate(reply.ID, delta.Thinking))
		}
		if delta.Content != "" {
			send(messageUpdate(reply.ID, delta.Content))
		}
	}

	response, err := a.client.Chat(turnCtx, turn, request, onDelta)
	if err != nil && request.Think != nil && thinkingRefused(err) && response.Message.Content == "" {
		// The server says this model cannot think after all. The same turn is
		// sent once more without asking for it.
		request.Think = nil
		response, err = a.client.Chat(turnCtx, turn, request, onDelta)
	}
	reply.Content, reply.Thinking = response.Message.Content, response.Message.Thinking

	cancelled := turnCtx.Err() != nil
	if err != nil && !cancelled {
		a.log.Warn("local models turn failed", "session", sessionID, "turn", turn.ID(), "model", where.capabilities.ID, "error", err)
		return acp.PromptResponse{}, failure("%v", err)
	}

	// A completed turn is kept. So is a stopped one that had begun to answer:
	// the person saw that text, so the model should see it next turn too.
	if !cancelled || reply.Content != "" {
		first := len(history) == 0
		kept := []storedMessage{userMessage}
		if reply.Content != "" || reply.Thinking != "" {
			kept = append(kept, reply)
		}
		current.mu.Lock()
		current.transcript.Messages = append(current.transcript.Messages, kept...)
		current.transcript.UpdatedAt = time.Now().UTC()
		title := ""
		if first && current.transcript.Title == "" {
			current.transcript.Title = titleFromMessage(userText)
			title = current.transcript.Title
		}
		snapshot := current.transcript
		snapshot.Messages = append([]storedMessage(nil), current.transcript.Messages...)
		current.mu.Unlock()
		if err := a.store.save(snapshot); err != nil {
			a.log.Warn("local models transcript not saved", "session", sessionID, "error", err)
		}
		if title != "" {
			// The title is the person's own first message, cut to length. No
			// model is asked for one.
			send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Title: acp.Ptr(title)}})
		}
	}
	if cancelled {
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}

	used := response.Usage.PromptTokens + response.Usage.CompletionTokens
	if used > 0 {
		send(acp.SessionUpdate{UsageUpdate: &acp.SessionUsageUpdate{
			Used: used, Size: where.capabilities.Context.Value,
			// A local model costs nothing per token.
			Cost: &acp.Cost{Amount: 0, Currency: "USD"},
		}})
	}
	return acp.PromptResponse{
		StopReason:    acp.StopReasonEndTurn,
		UserMessageId: acp.Ptr(userMessage.ID),
		Usage: &acp.Usage{
			InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens, TotalTokens: used,
		},
	}, nil
}

// thinkingRefused reports a server rejecting the request because the model
// does not support the think option.
func thinkingRefused(err error) bool {
	var server *generate.ServerError
	if !errors.As(err, &server) || server.Status != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(server.Message)
	return strings.Contains(message, "think")
}
