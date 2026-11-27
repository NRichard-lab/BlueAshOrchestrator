package localproviders

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// settingsStoreName is the Local Models harness's own file. It is separate
// from local-harness.json so the two local harnesses cannot overwrite each
// other.
const settingsStoreName = "local-models.json"

// SettingsVersion is the only shape of local-models.json this build writes.
const SettingsVersion = 1

// Instructions is how much standing text the Local Models runner puts in front
// of the user's message.
type Instructions string

// The three instructions levels.
const (
	// InstructionsStandard sends the full standing instructions every other
	// harness gets.
	InstructionsStandard Instructions = "standard"
	// InstructionsCompact sends a short role and tool-use note written for
	// small models.
	InstructionsCompact Instructions = "compact"
	// InstructionsNone sends nothing: the model sees only the conversation,
	// and tool definitions if it supports tools.
	InstructionsNone Instructions = "none"
)

// DefaultInstructions is the level used when the file names none.
const DefaultInstructions = InstructionsCompact

// InstructionsLevels are the accepted values, in the order they are offered.
var InstructionsLevels = []Instructions{InstructionsStandard, InstructionsCompact, InstructionsNone}

// ValidateInstructions accepts a known level, defaulting to DefaultInstructions.
func ValidateInstructions(raw string) (Instructions, error) {
	switch level := Instructions(strings.TrimSpace(raw)); level {
	case "":
		return DefaultInstructions, nil
	case InstructionsStandard, InstructionsCompact, InstructionsNone:
		return level, nil
	default:
		return "", fmt.Errorf("unknown instructions level %q; use %s, %s or %s",
			raw, InstructionsStandard, InstructionsCompact, InstructionsNone)
	}
}

// EndpointSettings is what the user stored about one endpoint.
type EndpointSettings struct {
	// APIKey is sent as a bearer token on metadata and generation requests to
	// this endpoint. Never log this field or return it from an API.
	APIKey string `json:"apiKey,omitempty"`
}

// ModelOverride holds the detected values the user replaced. A nil field is
// not overridden.
type ModelOverride struct {
	Context  *int  `json:"context,omitempty"`
	Tools    *bool `json:"tools,omitempty"`
	Thinking *bool `json:"thinking,omitempty"`
	Vision   *bool `json:"vision,omitempty"`
}

func (o ModelOverride) empty() bool {
	return o.Context == nil && o.Tools == nil && o.Thinking == nil && o.Vision == nil
}

// Settings is the content of local-models.json.
type Settings struct {
	Version int `json:"version"`
	// Default is the <endpoint>/<model> used when a session names none.
	Default string `json:"default,omitempty"`
	// Instructions is the standing-instructions level. Empty reads as
	// DefaultInstructions.
	Instructions Instructions `json:"instructions,omitempty"`
	// Endpoints is keyed by endpoint name.
	Endpoints map[string]EndpointSettings `json:"endpoints,omitempty"`
	// Models is keyed by <endpoint>/<model>. Only overrides are stored.
	Models map[string]ModelOverride `json:"models,omitempty"`
}

// InstructionsLevel is the level in effect.
func (s Settings) InstructionsLevel() Instructions {
	if s.Instructions == "" {
		return DefaultInstructions
	}
	return s.Instructions
}

// EndpointKey returns the key stored for an endpoint name, or "".
func (s Settings) EndpointKey(name string) string {
	for stored, endpoint := range s.Endpoints {
		if strings.EqualFold(stored, name) {
			return strings.TrimSpace(endpoint.APIKey)
		}
	}
	return ""
}

// Override returns the user's overrides for a model id.
func (s Settings) Override(id string) ModelOverride {
	return s.Models[id]
}

// ModelID joins an endpoint name and a model name into the id both local
// harnesses use.
func ModelID(endpoint, model string) string {
	return endpoint + "/" + model
}

// ParseModelID splits <endpoint>/<model> at the first slash. Endpoint names
// cannot contain one, so everything after it is the model, slashes included.
func ParseModelID(id string) (endpoint, model string, err error) {
	endpoint, model, found := strings.Cut(strings.TrimSpace(id), "/")
	if !found || endpoint == "" || model == "" {
		return "", "", fmt.Errorf("model id %q must look like <endpoint>/<model>, for example ollama/qwen3:8b", id)
	}
	return endpoint, model, nil
}

// settingsMu serializes read-modify-write cycles inside one process. Two
// processes are kept apart only by the atomic rename, which is enough for a
// file a person edits one command at a time.
var settingsMu sync.Mutex

// LoadSettings reads local-models.json. A missing file is the defaults. A file
// that cannot be parsed, or that a newer build wrote, is an error: silently
// treating it as empty would let the next save discard the user's keys.
func LoadSettings(dataDir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, settingsStoreName))
	if err != nil {
		if os.IsNotExist(err) {
			return Settings{Version: SettingsVersion}, nil
		}
		return Settings{}, fmt.Errorf("local models settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("local models settings: %s is not valid JSON: %w", settingsStoreName, err)
	}
	if settings.Version > SettingsVersion {
		return Settings{}, fmt.Errorf("local models settings: %s has version %d, newer than this build understands (%d)",
			settingsStoreName, settings.Version, SettingsVersion)
	}
	settings.Version = SettingsVersion
	if err := settings.normalize(); err != nil {
		return Settings{}, fmt.Errorf("local models settings: %w", err)
	}
	return settings, nil
}

// normalize trims what was typed and rejects values no command could have
// written.
func (s *Settings) normalize() error {
	s.Default = strings.TrimSpace(s.Default)
	if s.Default != "" {
		if _, _, err := ParseModelID(s.Default); err != nil {
			return fmt.Errorf("default: %w", err)
		}
	}
	if s.Instructions != "" {
		level, err := ValidateInstructions(string(s.Instructions))
		if err != nil {
			return err
		}
		s.Instructions = level
	}
	for name, endpoint := range s.Endpoints {
		endpoint.APIKey = strings.TrimSpace(endpoint.APIKey)
		if endpoint.APIKey == "" {
			delete(s.Endpoints, name)
			continue
		}
		s.Endpoints[name] = endpoint
	}
	for id, override := range s.Models {
		if _, _, err := ParseModelID(id); err != nil {
			return fmt.Errorf("models: %w", err)
		}
		if override.Context != nil && *override.Context < 1 {
			return fmt.Errorf("models: %s context must be a positive number of tokens", id)
		}
		if override.empty() {
			delete(s.Models, id)
		}
	}
	return nil
}

// SaveSettings validates and writes local-models.json with mode 0600, because
// it may hold endpoint keys.
func SaveSettings(dataDir string, settings Settings) (Settings, error) {
	settings.Version = SettingsVersion
	if err := settings.normalize(); err != nil {
		return Settings{}, fmt.Errorf("local models settings: %w", err)
	}
	if len(settings.Endpoints) == 0 {
		settings.Endpoints = nil
	}
	if len(settings.Models) == 0 {
		settings.Models = nil
	}
	// Keys are written on purpose: this 0600 file is where they are kept.
	data, err := json.MarshalIndent(settings, "", "  ") //nolint:gosec // G117: the settings store is the endpoint keys' home
	if err != nil {
		return Settings{}, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Settings{}, fmt.Errorf("local models settings: %w", err)
	}
	path := filepath.Join(dataDir, settingsStoreName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return Settings{}, fmt.Errorf("local models settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return Settings{}, fmt.Errorf("local models settings: %w", err)
	}
	return settings, nil
}

// UpdateSettings loads the settings, applies change and saves the result.
func UpdateSettings(dataDir string, change func(*Settings) error) (Settings, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settings, err := LoadSettings(dataDir)
	if err != nil {
		return Settings{}, err
	}
	if err := change(&settings); err != nil {
		return Settings{}, err
	}
	return SaveSettings(dataDir, settings)
}

// ErrUnknownEndpoint means a name matches neither a built-in nor a stored
// endpoint.
var ErrUnknownEndpoint = errors.New("unknown local endpoint")

// Lookup finds a built-in or stored endpoint by name, with its key attached.
func Lookup(dataDir, name string) (Endpoint, error) {
	endpoint, ok := lookupEndpoint(dataDir, strings.TrimSpace(name))
	if !ok {
		return Endpoint{}, fmt.Errorf("%w %q; `ao local-provider ls` lists the known ones", ErrUnknownEndpoint, name)
	}
	return endpoint, nil
}

// SetEndpointKey stores the key sent to an endpoint, or clears it when key is
// empty. The endpoint must exist, so a typo cannot park a key under a name
// that a later `add` would silently pick up.
func SetEndpointKey(dataDir, name, key string) error {
	endpoint, err := Lookup(dataDir, name)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	_, err = UpdateSettings(dataDir, func(settings *Settings) error {
		forgetEndpointKey(settings, endpoint.Name)
		if key == "" {
			return nil
		}
		if settings.Endpoints == nil {
			settings.Endpoints = map[string]EndpointSettings{}
		}
		settings.Endpoints[endpoint.Name] = EndpointSettings{APIKey: key}
		return nil
	})
	return err
}

func forgetEndpointKey(settings *Settings, name string) {
	for stored := range settings.Endpoints {
		if strings.EqualFold(stored, name) {
			delete(settings.Endpoints, stored)
		}
	}
}

// dropEndpointKey removes a key when its endpoint is removed or moved to
// another address. A key belongs to the server it was given for, not to the
// name. A settings file that cannot be read is left alone and reported.
func dropEndpointKey(dataDir, name string) error {
	settings, err := LoadSettings(dataDir)
	if err != nil {
		return err
	}
	if settings.EndpointKey(name) == "" {
		return nil
	}
	_, err = UpdateSettings(dataDir, func(settings *Settings) error {
		forgetEndpointKey(settings, name)
		return nil
	})
	return err
}
