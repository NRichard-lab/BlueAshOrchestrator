package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Every background model call (next-message suggestions, model-written
// titles, branch names) passes one of these two entry points. On the local
// harnesses both refuse before a driver is even looked up, which is why a
// service with nothing wired is enough to show it: the refusal does not
// depend on any driver, setting or session state.
func TestBackgroundTasksNeverReachALocalModel(t *testing.T) {
	service := &Service{}
	for _, harness := range []domain.AgentHarness{domain.HarnessLocal, domain.HarnessLocalModels} {
		_, err := service.RunBackgroundTask(context.Background(), harness, ports.ChatStartConfig{}, "Name this task")
		if !errors.Is(err, ports.ErrBackgroundPromptsDisabled) {
			t.Errorf("%s RunBackgroundTask err = %v, want ErrBackgroundPromptsDisabled", harness, err)
		}
		_, err = service.RunPromptSuggestionTask(context.Background(), "mer-1", "turn-1", harness, ports.ChatStartConfig{}, "Suggest the next message")
		if !errors.Is(err, ports.ErrBackgroundPromptsDisabled) {
			t.Errorf("%s RunPromptSuggestionTask err = %v, want ErrBackgroundPromptsDisabled", harness, err)
		}
	}
}
