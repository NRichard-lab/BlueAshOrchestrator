package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
