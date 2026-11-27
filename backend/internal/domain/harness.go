package domain

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

// Supported agent harnesses.
const (
	HarnessClaudeCode  AgentHarness = "claude-code"
	HarnessCodex       AgentHarness = "codex"
	HarnessAider       AgentHarness = "aider"
	HarnessOpenCode    AgentHarness = "opencode"
	HarnessOpenCodeV2  AgentHarness = "opencode-v2"
	HarnessGrok        AgentHarness = "grok"
	HarnessDroid       AgentHarness = "droid"
	HarnessAmp         AgentHarness = "amp"
	HarnessAgy         AgentHarness = "agy"
	HarnessCrush       AgentHarness = "crush"
	HarnessCursor      AgentHarness = "cursor"
	HarnessQwen        AgentHarness = "qwen"
	HarnessGemini      AgentHarness = "gemini"
	HarnessCopilot     AgentHarness = "copilot"
	HarnessGoose       AgentHarness = "goose"
	HarnessAuggie      AgentHarness = "auggie"
	HarnessContinue    AgentHarness = "continue"
	HarnessDevin       AgentHarness = "devin"
	HarnessCline       AgentHarness = "cline"
	HarnessKimi        AgentHarness = "kimi"
	HarnessMuse        AgentHarness = "muse"
	HarnessKiro        AgentHarness = "kiro"
	HarnessKilocode    AgentHarness = "kilocode"
	HarnessVibe        AgentHarness = "vibe"
	HarnessPi          AgentHarness = "pi"
	HarnessKimchi      AgentHarness = "kimchi"
	HarnessPrimeAgent  AgentHarness = "prime-agent"
	HarnessAutohand    AgentHarness = "autohand"
	HarnessOMP         AgentHarness = "omp"
	HarnessFX          AgentHarness = "fx"
	HarnessUnreal      AgentHarness = "unreal-agent"
	HarnessCodewhale   AgentHarness = "codewhale"
	HarnessMiMoCode    AgentHarness = "mimo-code"
	HarnessMiniMaxCode AgentHarness = "minimax-code"
	HarnessMemcode     AgentHarness = "memcode"
	HarnessDeepSeek    AgentHarness = "deepseek-harness"
	HarnessOpenHands   AgentHarness = "openhands"
	HarnessCommandCode AgentHarness = "command-code"
	HarnessDockerAgent AgentHarness = "docker-agent"
	HarnessHermes      AgentHarness = "hermes"
	HarnessStakpak     AgentHarness = "stakpak"
	// HarnessLocal runs coding workers against a linked Ollama / llama.cpp /
	// OpenAI-compatible endpoint via OpenCode 2.
	HarnessLocal AgentHarness = "local"
	// HarnessLocalModels runs local models through the runner built into the
	// ao binary. It needs no third-party CLI, offers every chat model on every
	// known endpoint, and sends a model nothing a person did not start.
	HarnessLocalModels AgentHarness = "local-models"
	// HarnessFake is retained for existing test fixtures and historical session
	// rows, but is not user-selectable.
	HarnessFake AgentHarness = "fake"
)

// AllHarnesses lists every supported harness. It is the canonical set used to
// validate user-supplied harness names (e.g. per-project role overrides).
var AllHarnesses = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessAider, HarnessOpenCode, HarnessOpenCodeV2, HarnessGrok,
	HarnessDroid, HarnessAmp, HarnessAgy, HarnessCrush, HarnessCursor, HarnessQwen, HarnessGemini,
	HarnessCopilot, HarnessGoose, HarnessAuggie, HarnessContinue, HarnessDevin,
	HarnessCline, HarnessKimi, HarnessMuse, HarnessKiro, HarnessKilocode, HarnessVibe, HarnessPi,
	HarnessKimchi, HarnessPrimeAgent, HarnessAutohand,
	HarnessOMP, HarnessFX, HarnessUnreal, HarnessCodewhale, HarnessMiMoCode, HarnessMiniMaxCode, HarnessDeepSeek, HarnessOpenHands,
	HarnessCommandCode, HarnessDockerAgent, HarnessMemcode, HarnessHermes, HarnessStakpak,
	HarnessLocal, HarnessLocalModels,
}

// ChatOnly reports whether h has no terminal interface, so every session on it
// runs in Chat.
func (h AgentHarness) ChatOnly() bool {
	return h == HarnessUnreal || h == HarnessDockerAgent || h == HarnessLocalModels
}

// NoBackgroundPrompts reports whether Orchestrator must never send this
// harness's model a prompt of its own. On these harnesses the only requests
// that reach a model are turns a person started: no next-message suggestions,
// no model-written titles or branch names, no summaries.
//
// Both local harnesses declare it. A local model is the user's own hardware,
// often a single slot that a background request would occupy or reload, and
// the user asked that nothing be sent on Orchestrator's initiative.
func (h AgentHarness) NoBackgroundPrompts() bool {
	return h == HarnessLocal || h == HarnessLocalModels
}

// ForwardsLifecycleMessagesByDefault reports whether a new session on h starts
// with CI failures and review comments forwarded to it. They are messages
// Orchestrator writes, so Local Models starts with them off; the session's
// existing switches turn them on.
func (h AgentHarness) ForwardsLifecycleMessagesByDefault() bool {
	return h != HarnessLocalModels
}

// IsKnown reports whether h is one of the supported harnesses.
func (h AgentHarness) IsKnown() bool {
	for _, k := range AllHarnesses {
		if h == k {
			return true
		}
	}
	return false
}
