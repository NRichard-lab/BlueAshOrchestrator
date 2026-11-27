package localproviders

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Source says where a capability value came from. Higher sources win, in the
// order override, observed, detected, assumed.
type Source string

// The four sources.
const (
	// SourceOverride is a value the user set in local-models.json.
	SourceOverride Source = "override"
	// SourceObserved is a value seen in a real turn a person started.
	SourceObserved Source = "observed"
	// SourceDetected is a value read from a metadata route.
	SourceDetected Source = "detected"
	// SourceAssumed means nothing reported the value.
	SourceAssumed Source = "assumed"
)

// AssumedContext is the context size used when no server reports one. It is
// small on purpose: a model wrongly treated as limited still works, and one
// wrongly treated as capable fails in the middle of a session.
const AssumedContext = 8192

// BoolValue is a yes/no capability and where it came from.
type BoolValue struct {
	Value  bool   `json:"value"`
	Source Source `json:"source"`
}

// IntValue is a numeric capability and where it came from.
type IntValue struct {
	Value  int    `json:"value"`
	Source Source `json:"source"`
}

// Capabilities is what is known about one model on one endpoint.
type Capabilities struct {
	// ID is <endpoint>/<model>.
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Kind     Kind   `json:"kind"`
	// Chat is false for a model the server says cannot hold a conversation,
	// such as an embedding model. Such a model is never offered for coding.
	Chat     bool      `json:"chat"`
	Tools    BoolValue `json:"tools"`
	Thinking BoolValue `json:"thinking"`
	Vision   BoolValue `json:"vision"`
	// Context is the number of tokens to budget a conversation against.
	Context IntValue `json:"context"`
	// MaxContext is the largest context the model supports, when reported.
	MaxContext int `json:"maxContext,omitempty"`
	// LoadedContext is the context the server allocated for the model it has
	// in memory now. Zero when the model is not loaded or the server does not
	// say.
	LoadedContext int  `json:"loadedContext,omitempty"`
	Loaded        bool `json:"loaded"`
	// Reported is the server's own capability list, unchanged.
	Reported      []string `json:"reported,omitempty"`
	Family        string   `json:"family,omitempty"`
	ParameterSize string   `json:"parameterSize,omitempty"`
	Quantization  string   `json:"quantization,omitempty"`
	// Digest identifies the model's files. A verified state is reset when it
	// changes.
	Digest    string `json:"digest,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// assumed is what is said about a model nothing has described.
func assumed(endpoint Endpoint, model string) Capabilities {
	return Capabilities{
		ID: ModelID(endpoint.Name, model), Endpoint: endpoint.Name, Model: model, Kind: endpoint.Kind,
		Chat:     true,
		Tools:    BoolValue{Source: SourceAssumed},
		Thinking: BoolValue{Source: SourceAssumed},
		Vision:   BoolValue{Source: SourceAssumed},
		Context:  IntValue{Value: AssumedContext, Source: SourceAssumed},
	}
}

// ApplyOverrides replaces detected values with the ones the user set.
func ApplyOverrides(capabilities Capabilities, override ModelOverride) Capabilities {
	if override.Context != nil {
		capabilities.Context = IntValue{Value: *override.Context, Source: SourceOverride}
	}
	if override.Tools != nil {
		capabilities.Tools = BoolValue{Value: *override.Tools, Source: SourceOverride}
	}
	if override.Thinking != nil {
		capabilities.Thinking = BoolValue{Value: *override.Thinking, Source: SourceOverride}
	}
	if override.Vision != nil {
		capabilities.Vision = BoolValue{Value: *override.Vision, Source: SourceOverride}
	}
	return capabilities
}

// ErrModelNotFound means the endpoint answered and does not have the model.
var ErrModelNotFound = errors.New("model not found on endpoint")

// Inspect reads what one model can do. It makes metadata requests only: it
// never loads a model and never sends one anything.
//
// The error is non-nil when the endpoint cannot be reached or says it does not
// have the model. Capabilities the server did not report come back as assumed,
// which is not an error.
func Inspect(ctx context.Context, client *http.Client, endpoint Endpoint, model string) (Capabilities, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return Capabilities{}, errors.New("model is required")
	}
	result := Probe(ctx, client, endpoint)
	if !result.Reachable {
		return Capabilities{}, fmt.Errorf("endpoint %q is unreachable: %s", endpoint.Name, result.Error)
	}
	listed := false
	for _, id := range result.Models {
		if id == model {
			listed = true
			break
		}
	}
	// Ollama can describe a model by a name its list spells differently
	// ("qwen3" for "qwen3:latest"), so its own answer decides. An
	// OpenAI-compatible list is the only authority there is.
	if !listed && endpoint.Kind != KindOllama {
		return Capabilities{}, fmt.Errorf("%w: %s does not list %q", ErrModelNotFound, endpoint.Name, model)
	}
	inspector := newInspector(ctx, client, endpoint, result.Models)
	capabilities, found := inspector.inspect(ctx, model)
	if !found {
		return Capabilities{}, fmt.Errorf("%w: %s does not have %q", ErrModelNotFound, endpoint.Name, model)
	}
	return capabilities, nil
}

// InspectAll reads capabilities for every listed model, in the order given.
// The endpoint-wide routes are requested once, not once per model.
func InspectAll(ctx context.Context, client *http.Client, endpoint Endpoint, models []string) []Capabilities {
	inspector := newInspector(ctx, client, endpoint, models)
	out := make([]Capabilities, len(models))
	// A handful at a time: /api/show is quick, and a local server is usually
	// one process that should not be handed thirty requests at once.
	const parallel = 4
	gate := make(chan struct{}, parallel)
	var group sync.WaitGroup
	for i, model := range models {
		group.Add(1)
		gate <- struct{}{}
		go func() {
			defer group.Done()
			defer func() { <-gate }()
			capabilities, found := inspector.inspect(ctx, model)
			if !found {
				capabilities = assumed(endpoint, model)
			}
			out[i] = capabilities
		}()
	}
	group.Wait()
	return out
}

// inspector holds the endpoint-wide answers shared by every model on it.
type inspector struct {
	client   *http.Client
	endpoint Endpoint
	listed   int

	// Ollama.
	tags    map[string]ollamaTag
	running map[string]ollamaRunning

	// OpenAI-compatible.
	openAI   map[string]openAIModel
	lmStudio map[string]lmStudioModel
	props    *llamaCppProps
}

func newInspector(ctx context.Context, client *http.Client, endpoint Endpoint, models []string) *inspector {
	in := &inspector{client: client, endpoint: endpoint, listed: len(models)}
	if endpoint.Kind == KindOllama {
		in.tags = ollamaTags(ctx, client, endpoint)
		in.running = ollamaRunningModels(ctx, client, endpoint)
		return in
	}
	in.openAI = openAIModels(ctx, client, endpoint)
	in.lmStudio = lmStudioModels(ctx, client, endpoint)
	in.props = llamaCppServerProps(ctx, client, endpoint)
	return in
}

func (in *inspector) inspect(ctx context.Context, model string) (Capabilities, bool) {
	if in.endpoint.Kind == KindOllama {
		return in.inspectOllama(ctx, model)
	}
	return in.inspectOpenAI(model), true
}

// --- Ollama ---------------------------------------------------------------

type ollamaDetails struct {
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

type ollamaTag struct {
	Name    string        `json:"name"`
	Model   string        `json:"model"`
	Size    int64         `json:"size"`
	Digest  string        `json:"digest"`
	Details ollamaDetails `json:"details"`
}

type ollamaRunning struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	ContextLength int    `json:"context_length"`
}

type ollamaShow struct {
	Capabilities []string       `json:"capabilities"`
	Parameters   string         `json:"parameters"`
	Details      ollamaDetails  `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
}

func ollamaTags(ctx context.Context, client *http.Client, endpoint Endpoint) map[string]ollamaTag {
	var payload struct {
		Models []ollamaTag `json:"models"`
	}
	if metadataJSON(ctx, client, endpoint, "/api/tags", nil, &payload) != nil {
		return nil
	}
	out := make(map[string]ollamaTag, len(payload.Models))
	for _, tag := range payload.Models {
		out[firstNonEmpty(tag.Name, tag.Model)] = tag
	}
	return out
}

func ollamaRunningModels(ctx context.Context, client *http.Client, endpoint Endpoint) map[string]ollamaRunning {
	var payload struct {
		Models []ollamaRunning `json:"models"`
	}
	if metadataJSON(ctx, client, endpoint, "/api/ps", nil, &payload) != nil {
		return nil
	}
	out := make(map[string]ollamaRunning, len(payload.Models))
	for _, running := range payload.Models {
		out[firstNonEmpty(running.Name, running.Model)] = running
	}
	return out
}

func (in *inspector) inspectOllama(ctx context.Context, model string) (Capabilities, bool) {
	capabilities := assumed(in.endpoint, model)
	var show ollamaShow
	err := metadataJSON(ctx, in.client, in.endpoint, "/api/show", map[string]string{"model": model}, &show)
	var noAnswer *errNoAnswer
	if errors.As(err, &noAnswer) && strings.HasPrefix(noAnswer.status, "404") {
		return Capabilities{}, false
	}
	details := show.Details
	if tag, ok := in.tags[model]; ok {
		capabilities.Digest, capabilities.SizeBytes = tag.Digest, tag.Size
		if details == (ollamaDetails{}) {
			details = tag.Details
		}
	}
	capabilities.Family = details.Family
	capabilities.ParameterSize = details.ParameterSize
	capabilities.Quantization = details.QuantizationLevel
	if running, ok := in.running[model]; ok {
		capabilities.Loaded = true
		capabilities.LoadedContext = running.ContextLength
	}
	if err != nil {
		// The server would not describe this model. What /api/tags and /api/ps
		// said still stands; the rest stays assumed.
		if capabilities.LoadedContext > 0 {
			capabilities.Context = IntValue{Value: capabilities.LoadedContext, Source: SourceDetected}
		}
		return capabilities, true
	}

	capabilities.MaxContext = ollamaMaxContext(show.ModelInfo)
	// An Ollama new enough to send the list sends it for every model, so a
	// capability missing from a list that is there is a detected "no". An older
	// server sends no list at all, and nothing is concluded from that.
	if len(show.Capabilities) > 0 {
		reported := append([]string(nil), show.Capabilities...)
		sort.Strings(reported)
		capabilities.Reported = reported
		has := func(name string) bool {
			for _, capability := range show.Capabilities {
				if capability == name {
					return true
				}
			}
			return false
		}
		capabilities.Chat = has("completion")
		capabilities.Tools = BoolValue{Value: has("tools"), Source: SourceDetected}
		capabilities.Thinking = BoolValue{Value: has("thinking"), Source: SourceDetected}
		capabilities.Vision = BoolValue{Value: has("vision"), Source: SourceDetected}
	}
	// What is loaded wins: asking for another size makes Ollama reload the
	// model. Then the size the Modelfile pins, then the model's maximum.
	pinned := ollamaParameter(show.Parameters, "num_ctx")
	if capabilities.MaxContext > 0 && pinned > capabilities.MaxContext {
		// A Modelfile can pin more than the model was trained for. The extra
		// is not usable context.
		pinned = capabilities.MaxContext
	}
	switch {
	case capabilities.LoadedContext > 0:
		capabilities.Context = IntValue{Value: capabilities.LoadedContext, Source: SourceDetected}
	case pinned > 0:
		capabilities.Context = IntValue{Value: pinned, Source: SourceDetected}
	case capabilities.MaxContext > 0:
		capabilities.Context = IntValue{Value: capabilities.MaxContext, Source: SourceDetected}
	}
	return capabilities, true
}

// ollamaMaxContext finds <architecture>.context_length in model_info.
func ollamaMaxContext(info map[string]any) int {
	if architecture, ok := info["general.architecture"].(string); ok {
		if value := jsonInt(info[architecture+".context_length"]); value > 0 {
			return value
		}
	}
	// A model_info without an architecture key still names its context length
	// under some prefix. Sorted so two such keys give the same answer twice.
	keys := make([]string, 0, len(info))
	for key := range info {
		if strings.HasSuffix(key, ".context_length") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value := jsonInt(info[key]); value > 0 {
			return value
		}
	}
	return 0
}

// ollamaParameter reads one numeric Modelfile parameter from the
// "name   value" lines /api/show returns.
func ollamaParameter(parameters, name string) int {
	for _, line := range strings.Split(parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == name {
			if value, err := strconv.Atoi(fields[1]); err == nil {
				return value
			}
		}
	}
	return 0
}

func jsonInt(value any) int {
	number, ok := value.(float64)
	if !ok || number < 1 || number > 1<<40 {
		return 0
	}
	return int(number)
}

// --- OpenAI-compatible -----------------------------------------------------
//
// Only the bare GET /v1/models list has been checked against a real server
// (Ollama's). The llama.cpp, LM Studio and vLLM fields below are read from
// those projects' documentation; a field that is absent or spelled otherwise
// leaves the value assumed, which is the safe direction.

// openAIModel is one /v1/models entry with the extra fields some servers add.
type openAIModel struct {
	ID string `json:"id"`
	// MaxModelLen is vLLM's maximum context.
	MaxModelLen int `json:"max_model_len"`
	// Meta is llama.cpp's description of the loaded model.
	Meta struct {
		NCtxTrain int `json:"n_ctx_train"`
	} `json:"meta"`
}

func openAIModels(ctx context.Context, client *http.Client, endpoint Endpoint) map[string]openAIModel {
	var payload struct {
		Data []openAIModel `json:"data"`
	}
	if metadataJSON(ctx, client, endpoint, "/v1/models", nil, &payload) != nil {
		return nil
	}
	out := make(map[string]openAIModel, len(payload.Data))
	for _, model := range payload.Data {
		out[strings.TrimSpace(model.ID)] = model
	}
	return out
}

// lmStudioModel is one entry of LM Studio's GET /api/v0/models.
type lmStudioModel struct {
	ID                  string    `json:"id"`
	Type                string    `json:"type"`
	Arch                string    `json:"arch"`
	Quantization        string    `json:"quantization"`
	State               string    `json:"state"`
	MaxContextLength    int       `json:"max_context_length"`
	LoadedContextLength int       `json:"loaded_context_length"`
	Capabilities        *[]string `json:"capabilities"`
}

func lmStudioModels(ctx context.Context, client *http.Client, endpoint Endpoint) map[string]lmStudioModel {
	var payload struct {
		Data []lmStudioModel `json:"data"`
	}
	if metadataJSON(ctx, client, endpoint, "/api/v0/models", nil, &payload) != nil {
		return nil
	}
	out := make(map[string]lmStudioModel, len(payload.Data))
	for _, model := range payload.Data {
		out[strings.TrimSpace(model.ID)] = model
	}
	return out
}

// llamaCppProps is the part of llama.cpp's GET /props that describes the one
// model the server has loaded.
type llamaCppProps struct {
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
	ModelPath  string `json:"model_path"`
	ModelAlias string `json:"model_alias"`
	Modalities *struct {
		Vision bool `json:"vision"`
	} `json:"modalities"`
	ChatTemplateCaps map[string]any `json:"chat_template_caps"`
}

func llamaCppServerProps(ctx context.Context, client *http.Client, endpoint Endpoint) *llamaCppProps {
	var props llamaCppProps
	if metadataJSON(ctx, client, endpoint, "/props", nil, &props) != nil {
		return nil
	}
	// Another server answering /props with some other JSON object decodes to
	// all zeroes, which says nothing.
	if props.DefaultGenerationSettings.NCtx == 0 && props.ModelPath == "" && props.ModelAlias == "" {
		return nil
	}
	return &props
}

// describes reports whether /props is about this model: the server serves only
// one, or names it.
func (p *llamaCppProps) describes(model string, listed int) bool {
	if listed == 1 {
		return true
	}
	return model == p.ModelAlias || model == p.ModelPath ||
		model == path.Base(strings.ReplaceAll(p.ModelPath, "\\", "/"))
}

func (in *inspector) inspectOpenAI(model string) Capabilities {
	capabilities := assumed(in.endpoint, model)
	if listed, ok := in.openAI[model]; ok {
		capabilities.MaxContext = max(listed.MaxModelLen, listed.Meta.NCtxTrain)
	}
	if studio, ok := in.lmStudio[model]; ok {
		capabilities.Family, capabilities.Quantization = studio.Arch, studio.Quantization
		capabilities.MaxContext = max(capabilities.MaxContext, studio.MaxContextLength)
		if studio.State == "loaded" {
			capabilities.Loaded = true
			capabilities.LoadedContext = studio.LoadedContextLength
		}
		switch studio.Type {
		case "embeddings":
			capabilities.Chat = false
		case "vlm":
			capabilities.Vision = BoolValue{Value: true, Source: SourceDetected}
		case "llm":
			capabilities.Vision = BoolValue{Value: false, Source: SourceDetected}
		}
		if studio.Capabilities != nil {
			reported := append([]string(nil), *studio.Capabilities...)
			sort.Strings(reported)
			capabilities.Reported = reported
			tools := false
			for _, capability := range reported {
				tools = tools || capability == "tool_use"
			}
			capabilities.Tools = BoolValue{Value: tools, Source: SourceDetected}
		}
	}
	if in.props != nil && in.props.describes(model, in.listed) {
		capabilities.Loaded = true
		if in.props.DefaultGenerationSettings.NCtx > 0 {
			capabilities.LoadedContext = in.props.DefaultGenerationSettings.NCtx
		}
		if in.props.Modalities != nil {
			capabilities.Vision = BoolValue{Value: in.props.Modalities.Vision, Source: SourceDetected}
		}
		for _, key := range []string{"supports_tools", "supports_tool_calls"} {
			if supported, ok := in.props.ChatTemplateCaps[key].(bool); ok {
				capabilities.Tools = BoolValue{Value: capabilities.Tools.Value || supported, Source: SourceDetected}
			}
		}
	}
	switch {
	case capabilities.LoadedContext > 0:
		capabilities.Context = IntValue{Value: capabilities.LoadedContext, Source: SourceDetected}
	case capabilities.MaxContext > 0:
		capabilities.Context = IntValue{Value: capabilities.MaxContext, Source: SourceDetected}
	}
	return capabilities
}

// --- Discovery with capabilities -------------------------------------------

// Discovery is a probe result with what each model can do.
type Discovery struct {
	Result
	// Capabilities describes each entry of Models, in the same order.
	Capabilities []Capabilities `json:"capabilities,omitempty"`
	// EmbeddingModels are the models the endpoint serves that cannot hold a
	// conversation. They are left out of Models so they are never offered as
	// coding models.
	EmbeddingModels []string `json:"embeddingModels,omitempty"`
}

// Discover probes one endpoint and inspects every model it lists, applying the
// user's overrides. Result.Models keeps only chat-capable models.
func Discover(ctx context.Context, client *http.Client, endpoint Endpoint, settings Settings) Discovery {
	discovery := Discovery{Result: Probe(ctx, client, endpoint)}
	if !discovery.Reachable || len(discovery.Models) == 0 {
		return discovery
	}
	chat := make([]string, 0, len(discovery.Models))
	for _, capabilities := range InspectAll(ctx, client, endpoint, discovery.Models) {
		if !capabilities.Chat {
			discovery.EmbeddingModels = append(discovery.EmbeddingModels, capabilities.Model)
			continue
		}
		chat = append(chat, capabilities.Model)
		discovery.Capabilities = append(discovery.Capabilities, ApplyOverrides(capabilities, settings.Override(capabilities.ID)))
	}
	discovery.Models = chat
	return discovery
}

// DiscoverAll runs Discover over every endpoint, keeping the input order.
func DiscoverAll(ctx context.Context, client *http.Client, endpoints []Endpoint, settings Settings) []Discovery {
	out := make([]Discovery, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, Discover(ctx, client, endpoint, settings))
	}
	return out
}
