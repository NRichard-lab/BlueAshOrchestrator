package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

// localProviderSettings reads local-models.json for a command that can still
// do its job without it. A file that cannot be read is reported on stderr and
// treated as holding no overrides.
func localProviderSettings(cmd *cobra.Command, dataDir string) localproviders.Settings {
	settings, err := localproviders.LoadSettings(dataDir)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", err)
		return localproviders.Settings{}
	}
	return settings
}

// newLocalProviderModelsCommand lists models with what each can do and where
// each value came from. It makes metadata requests only: no model is loaded
// and none is sent anything.
func newLocalProviderModelsCommand() *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "models [<endpoint>]",
		Short: "List models with their detected capabilities",
		Long: "List the models local endpoints serve, with what each can do: tool calls, " +
			"context size, thinking and vision.\n\n" +
			"Each value is followed by where it came from: override (you set it), observed " +
			"(seen in a real turn), detected (the server reported it) or assumed (nothing " +
			"reported it, so a small safe default is used).\n\n" +
			"Detection reads the servers' metadata routes only. It never loads a model and " +
			"never sends one a prompt. Embedding models cannot hold a conversation and are " +
			"left out unless --all is given.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			endpoints := localproviders.All(dataDir)
			if len(args) == 1 {
				endpoint, err := localproviders.Lookup(dataDir, args[0])
				if err != nil {
					return usageError{err}
				}
				endpoints = []localproviders.Endpoint{endpoint}
			}
			client := &http.Client{Timeout: localproviders.DefaultTimeout}
			discoveries := localproviders.DiscoverAll(cmd.Context(), client, endpoints, localProviderSettings(cmd, dataDir))
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(discoveries)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "MODEL\tTOOLS\tCONTEXT\tTHINKING\tVISION\tLOADED")
			rows := 0
			for _, discovery := range discoveries {
				for _, capabilities := range discovery.Capabilities {
					rows++
					_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", capabilities.ID,
						describeBool(capabilities.Tools), describeContext(capabilities.Context),
						describeBool(capabilities.Thinking), describeBool(capabilities.Vision), describeLoaded(capabilities))
				}
				if !all {
					continue
				}
				for _, model := range discovery.EmbeddingModels {
					rows++
					_, _ = fmt.Fprintf(writer, "%s\tembedding model, not usable for chat\t\t\t\t\n", localproviders.ModelID(discovery.Name, model))
				}
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			// A server that is not running is the common case, so say which
			// ones did not answer instead of printing an empty table.
			for _, discovery := range discoveries {
				if !discovery.Reachable {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s (%s): unreachable: %s\n", discovery.Name, discovery.BaseURL, discovery.Error)
				}
			}
			if rows == 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "No models found. Start a model server, or name one with `ao local-provider add`.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print endpoints, models and capabilities as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "Also list embedding models, which cannot be used for chat")
	return cmd
}

func describeBool(value localproviders.BoolValue) string {
	answer := "no"
	if value.Value {
		answer = "yes"
	}
	return fmt.Sprintf("%s (%s)", answer, value.Source)
}

func describeContext(value localproviders.IntValue) string {
	return fmt.Sprintf("%s (%s)", strconv.Itoa(value.Value), value.Source)
}

func describeLoaded(capabilities localproviders.Capabilities) string {
	if !capabilities.Loaded {
		return "no"
	}
	if capabilities.LoadedContext > 0 {
		return fmt.Sprintf("yes, %d context", capabilities.LoadedContext)
	}
	return "yes"
}

// newLocalProviderInspectCommand shows one model in full.
func newLocalProviderInspectCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect <endpoint>/<model>",
		Short: "Show what one local model can do, and where each value came from",
		Long: "Show what one local model can do, and where each value came from.\n\n" +
			"Reads the server's metadata routes only. It never loads the model and never " +
			"sends it a prompt.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError{errors.New("inspect requires one <endpoint>/<model>, for example ollama/qwen3:8b")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			endpointName, model, err := localproviders.ParseModelID(args[0])
			if err != nil {
				return usageError{err}
			}
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			endpoint, err := localproviders.Lookup(dataDir, endpointName)
			if err != nil {
				return usageError{err}
			}
			client := &http.Client{Timeout: localproviders.DefaultTimeout}
			capabilities, err := localproviders.Inspect(cmd.Context(), client, endpoint, model)
			if err != nil {
				return err
			}
			capabilities = localproviders.ApplyOverrides(capabilities, localProviderSettings(cmd, dataDir).Override(capabilities.ID))
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(capabilities)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			row := func(label, value string) {
				if value != "" {
					_, _ = fmt.Fprintf(writer, "%s\t%s\n", label, value)
				}
			}
			row("Model", capabilities.ID)
			row("Endpoint", fmt.Sprintf("%s (%s, %s)", endpoint.Name, endpoint.BaseURL, endpoint.Kind))
			if capabilities.Chat {
				row("Usable for chat", "yes")
			} else {
				row("Usable for chat", "no: the server reports it as an embedding model")
			}
			row("Tools", describeBool(capabilities.Tools))
			row("Context", describeContext(capabilities.Context))
			if capabilities.MaxContext > 0 {
				row("Maximum context", strconv.Itoa(capabilities.MaxContext))
			}
			row("Loaded", describeLoaded(capabilities))
			row("Thinking", describeBool(capabilities.Thinking))
			row("Vision", describeBool(capabilities.Vision))
			row("Server reports", strings.Join(capabilities.Reported, ", "))
			row("Family", capabilities.Family)
			row("Parameters", capabilities.ParameterSize)
			row("Quantization", capabilities.Quantization)
			row("Digest", capabilities.Digest)
			return writer.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the capabilities as JSON")
	return cmd
}

// newLocalProviderKeyCommand stores or clears the key sent to one endpoint.
// The key is read from stdin so it never appears in the process list or shell
// history.
func newLocalProviderKeyCommand() *cobra.Command {
	var clearKey bool
	cmd := &cobra.Command{
		Use:   "key <endpoint>",
		Short: "Set or clear the API key for a local endpoint (read from stdin)",
		Long: "Set or clear the API key for a local endpoint.\n\n" +
			"The key is read from stdin, for example:\n\n" +
			"  echo \"$STUDIO_KEY\" | ao local-provider key studio\n\n" +
			"It is sent as a bearer token on every request to that endpoint, the metadata " +
			"requests discovery makes included. It is stored in local-models.json under the " +
			"data directory and is never printed back. Use --clear to remove it.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError{errors.New("key requires one endpoint name")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			if _, err := localproviders.Lookup(dataDir, args[0]); err != nil {
				return usageError{err}
			}
			key := ""
			if !clearKey {
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				key = strings.TrimSpace(line)
				if key == "" {
					return usageError{errors.New("no key on stdin; pipe one in, or pass --clear to remove the stored key")}
				}
			}
			if err := localproviders.SetEndpointKey(dataDir, args[0], key); err != nil {
				return err
			}
			if clearKey {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cleared the key for %s\n", args[0])
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved the key for %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&clearKey, "clear", false, "Remove the stored key instead of reading one")
	return cmd
}

// newLocalProviderUseCommand sets the model the Local Models harness uses
// when a session names none.
func newLocalProviderUseCommand() *cobra.Command {
	var clearDefault bool
	cmd := &cobra.Command{
		Use:   "use <endpoint>/<model>",
		Short: "Set the default model for the Local Models harness",
		Long: "Set the model the Local Models harness uses when a session names none.\n\n" +
			"A session's own model (`ao spawn --model`, or the model control in Chat) and a " +
			"project's role model both win over this default.\n\n" +
			"The endpoint is checked over its metadata routes when it is reachable. The model " +
			"is not loaded and is sent nothing. Use --clear to remove the default.",
		Args: func(_ *cobra.Command, args []string) error {
			if clearDefault && len(args) == 0 {
				return nil
			}
			if len(args) != 1 || clearDefault {
				return usageError{errors.New("use requires one <endpoint>/<model>, or --clear alone")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			if clearDefault {
				if _, err := localproviders.UpdateSettings(dataDir, func(settings *localproviders.Settings) error {
					settings.Default = ""
					return nil
				}); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Cleared the Local Models default")
				return nil
			}
			endpointName, model, err := localproviders.ParseModelID(args[0])
			if err != nil {
				return usageError{err}
			}
			endpoint, err := localproviders.Lookup(dataDir, endpointName)
			if err != nil {
				return usageError{err}
			}
			id := localproviders.ModelID(endpoint.Name, model)
			client := &http.Client{Timeout: localproviders.DefaultTimeout}
			capabilities, inspectErr := localproviders.Inspect(cmd.Context(), client, endpoint, model)
			switch {
			case errors.Is(inspectErr, localproviders.ErrModelNotFound):
				return usageError{fmt.Errorf("%s does not have a model named %q; `ao local-provider models %s` lists what it serves", endpoint.Name, model, endpoint.Name)}
			case inspectErr == nil && !capabilities.Chat:
				return usageError{fmt.Errorf("%s is an embedding model and cannot hold a conversation", id)}
			}
			if _, err := localproviders.UpdateSettings(dataDir, func(settings *localproviders.Settings) error {
				settings.Default = id
				return nil
			}); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Local Models default is now %s\n", id)
			if inspectErr != nil {
				// Saved anyway: a server that is off now may be on later.
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not check the model: %s\n", inspectErr)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&clearDefault, "clear", false, "Remove the default instead of setting one")
	return cmd
}

// newLocalProviderInstructionsCommand shows or sets what the Local Models
// harness puts in front of the conversation.
func newLocalProviderInstructionsCommand() *cobra.Command {
	levels := make([]string, 0, len(localproviders.InstructionsLevels))
	for _, level := range localproviders.InstructionsLevels {
		levels = append(levels, string(level))
	}
	return &cobra.Command{
		Use:   "instructions [" + strings.Join(levels, "|") + "]",
		Short: "Show or set the standing-instructions level for the Local Models harness",
		Long: "Show or set what the Local Models harness puts in front of the conversation.\n\n" +
			"  standard  the full standing instructions every other harness gets (about 6,000 tokens)\n" +
			"  compact   a short role note written for small models (the default)\n" +
			"  none      nothing: the model sees only the conversation\n\n" +
			"With none the model does not know about reports, pull request rules or artifacts, " +
			"so the session is a plain assistant, not a managed worker.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				settings, err := localproviders.LoadSettings(dataDir)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), settings.InstructionsLevel())
				return nil
			}
			if strings.TrimSpace(args[0]) == "" {
				return usageError{errors.New("instructions level must be one of " + strings.Join(levels, ", "))}
			}
			level, err := localproviders.ValidateInstructions(args[0])
			if err != nil {
				return usageError{err}
			}
			if _, err := localproviders.UpdateSettings(dataDir, func(settings *localproviders.Settings) error {
				settings.Instructions = level
				return nil
			}); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Local Models instructions level is now %s\n", level)
			return nil
		},
	}
}

// localModelsStatus is what `ao local-provider status` reports.
type localModelsStatus struct {
	Default      string                                  `json:"default,omitempty"`
	Instructions localproviders.Instructions             `json:"instructions"`
	Readiness    localproviders.Readiness                `json:"readiness"`
	Overrides    map[string]localproviders.ModelOverride `json:"overrides,omitempty"`
	// EndpointsWithKeys names the endpoints a key is stored for. The keys
	// themselves are never printed.
	EndpointsWithKeys []string `json:"endpointsWithKeys,omitempty"`
}

// newLocalProviderStatusCommand reports how the Local Models harness is set
// up. Readiness comes from metadata routes: no model is prompted to find out
// whether it works.
func newLocalProviderStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the Local Models harness setup: default model, instructions level and readiness",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			settings, err := localproviders.LoadSettings(dataDir)
			if err != nil {
				return err
			}
			client := &http.Client{Timeout: localproviders.DefaultTimeout}
			status := localModelsStatus{
				Default: settings.Default, Instructions: settings.InstructionsLevel(),
				Readiness: localproviders.CheckReadiness(cmd.Context(), client, dataDir), Overrides: settings.Models,
			}
			for name := range settings.Endpoints {
				status.EndpointsWithKeys = append(status.EndpointsWithKeys, name)
			}
			sort.Strings(status.EndpointsWithKeys)
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(status)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defaultModel := status.Default
			if defaultModel == "" {
				defaultModel = "none (each session must name a model)"
			}
			_, _ = fmt.Fprintf(writer, "Default model\t%s\n", defaultModel)
			_, _ = fmt.Fprintf(writer, "Instructions\t%s\n", status.Instructions)
			if status.Readiness.State == localproviders.ReadinessConfigured {
				_, _ = fmt.Fprintf(writer, "Readiness\tconfigured (unverified): %d endpoint(s) answering, %d chat model(s)\n",
					status.Readiness.Reachable, status.Readiness.ChatModels)
			} else {
				_, _ = fmt.Fprintf(writer, "Readiness\tneeds setup: %s\n", status.Readiness.Reason)
			}
			if len(status.EndpointsWithKeys) > 0 {
				_, _ = fmt.Fprintf(writer, "Keys stored for\t%s\n", strings.Join(status.EndpointsWithKeys, ", "))
			}
			ids := make([]string, 0, len(status.Overrides))
			for id := range status.Overrides {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				_, _ = fmt.Fprintf(writer, "Override\t%s  %s\n", id, describeOverride(status.Overrides[id]))
			}
			return writer.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the status as JSON")
	return cmd
}

func describeOverride(override localproviders.ModelOverride) string {
	var parts []string
	if override.Context != nil {
		parts = append(parts, "context="+strconv.Itoa(*override.Context))
	}
	flag := func(name string, value *bool) {
		if value != nil {
			parts = append(parts, name+"="+strconv.FormatBool(*value))
		}
	}
	flag("tools", override.Tools)
	flag("thinking", override.Thinking)
	flag("vision", override.Vision)
	return strings.Join(parts, " ")
}
