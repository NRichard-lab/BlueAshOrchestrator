package cli

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/localagent"
)

// newLocalAgentCommand groups the Local Models runner: the agent the daemon
// launches for the local-models harness. It is part of this binary, so the
// harness needs nothing else installed.
func newLocalAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "local-agent",
		Short:  "Run the built-in Local Models agent (internal)",
		Hidden: true,
	}
	cmd.AddCommand(newLocalAgentACPCommand())
	return cmd
}

// newLocalAgentACPCommand speaks ACP on stdin and stdout. Stdout carries
// protocol messages only; diagnostics go to stderr.
func newLocalAgentACPCommand() *cobra.Command {
	var model, instructionsFile string
	cmd := &cobra.Command{
		Use:   "acp",
		Short: "Serve the Local Models agent over ACP on stdin and stdout",
		Long: "Serve the Local Models agent over ACP on stdin and stdout.\n\n" +
			"The agent holds a conversation with a model on a local endpoint. It reads " +
			"endpoints, keys and settings from the Orchestrator data directory. It sends a " +
			"model a request only when the client delivers a message with session/prompt: " +
			"starting it, opening a session or loading one contacts no model server.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
			return localagent.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), localagent.Options{
				DataDir: cfg.DataDir, Model: model, InstructionsFile: instructionsFile, Version: Version, Log: log,
			})
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Model the session starts with, as <endpoint>/<model>")
	cmd.Flags().StringVar(&instructionsFile, "instructions-file", "", "File holding the full standing instructions, used at the standard instructions level")
	return cmd
}
