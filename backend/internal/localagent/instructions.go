package localagent

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

// compactInstructions is the short note sent at the compact level. It is
// written for small models: a few plain sentences, well under a hundred
// tokens, so it costs almost nothing of an 8,192-token context.
const compactInstructions = "You are a coding assistant working with a person in their project. " +
	"Answer directly and keep answers short. " +
	"If you are not sure, say so. " +
	"Never invent file contents or command output."

// standingInstructions returns the text that goes in front of the
// conversation for a level, or "" for none.
//
//   - none: nothing. The model sees only the conversation.
//   - compact: the short note above, with the project directory.
//   - standard: the full standing instructions the daemon gave this session.
//     Without them (the runner started on its own, outside a managed session)
//     the compact note is used, because there is nothing fuller to send.
func standingInstructions(level localproviders.Instructions, standard, cwd string) string {
	switch level {
	case localproviders.InstructionsNone:
		return ""
	case localproviders.InstructionsStandard:
		if text := strings.TrimSpace(standard); text != "" {
			return text
		}
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		return compactInstructions + " The project directory is " + cwd + "."
	}
	return compactInstructions
}

// readStandardInstructions reads the standing instructions file the daemon
// wrote for this session. A missing or unreadable file is no instructions.
func readStandardInstructions(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// titleLimit is the longest session title taken from a first message.
const titleLimit = 60

// titleFromMessage makes a session title from the person's first message: its
// first line, cut at a word boundary. No model is asked to write one.
func titleFromMessage(message string) string {
	line := ""
	for _, candidate := range strings.Split(message, "\n") {
		if candidate = strings.Join(strings.Fields(candidate), " "); candidate != "" {
			line = candidate
			break
		}
	}
	if utf8.RuneCountInString(line) <= titleLimit {
		return line
	}
	runes := []rune(line)[:titleLimit]
	cut := string(runes)
	if space := strings.LastIndex(cut, " "); space > titleLimit/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}
