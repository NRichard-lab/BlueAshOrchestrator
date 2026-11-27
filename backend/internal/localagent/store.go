package localagent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// transcriptVersion is the only transcript shape this build writes.
const transcriptVersion = 1

// sessionIDPrefix marks ids this runner issued.
const sessionIDPrefix = "lm-"

// storedMessage is one conversation entry kept on disk.
type storedMessage struct {
	// ID is stable across replays, so a reloaded history groups the same way
	// it did live.
	ID       string `json:"id"`
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

// transcript is one session's conversation.
type transcript struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Cwd       string          `json:"cwd"`
	Model     string          `json:"model,omitempty"`
	Title     string          `json:"title,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Messages  []storedMessage `json:"messages"`
}

// store keeps transcripts under <data dir>/local-models/sessions.
type store struct {
	dir string
}

func newStore(dataDir string) store {
	return store{dir: filepath.Join(dataDir, "local-models", "sessions")}
}

// newSessionID returns a fresh id.
func newSessionID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return sessionIDPrefix + hex.EncodeToString(raw[:]), nil
}

func newMessageID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// validSessionID accepts only ids this runner issues. The id becomes a file
// name, so anything else, a path separator above all, is refused.
func validSessionID(id string) bool {
	rest, ok := strings.CutPrefix(id, sessionIDPrefix)
	if !ok || len(rest) != 24 {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}

var errInvalidSessionID = errors.New("not a Local Models session id")

func (s store) path(id string) (string, error) {
	if !validSessionID(id) {
		return "", errInvalidSessionID
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// load reads a transcript. found is false when the session has no completed
// turn yet, which is not an error: nothing is written before one.
func (s store) load(id string) (t transcript, found bool, err error) {
	path, err := s.path(id)
	if err != nil {
		return transcript{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return transcript{}, false, nil
		}
		return transcript{}, false, fmt.Errorf("read transcript: %w", err)
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return transcript{}, false, fmt.Errorf("transcript %s is not valid JSON: %w", id, err)
	}
	if t.Version > transcriptVersion {
		return transcript{}, false, fmt.Errorf("transcript %s was written by a newer version", id)
	}
	t.ID = id
	return t, true, nil
}

// save writes a transcript atomically. Transcripts hold the user's own
// conversation, so the file is private to them.
func (s store) save(t transcript) error {
	path, err := s.path(t.ID)
	if err != nil {
		return err
	}
	t.Version = transcriptVersion
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}
