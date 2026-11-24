package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

// newLocalProviderCommand groups the local model endpoints AO can see on this
// machine: the ones it probes by default and the ones the user names.
//
// AO does not call these endpoints to run a turn — a harness does — so the
// command's job is to answer "is it up, and what does it serve", and to keep a
// base URL a harness can be pointed at.
func newLocalProviderCommand(cmdCtx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "local-provider",
		Aliases: []string{"local-providers"},
		Short:   "Discover and manage local model endpoints (Ollama, llama.cpp, OpenAI-compatible)",
	}
	cmd.AddCommand(newLocalProviderListCommand(cmdCtx))
	cmd.AddCommand(newLocalProviderAddCommand())
	cmd.AddCommand(newLocalProviderRemoveCommand())
	return cmd
}

func localProviderDataDir() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return cfg.DataDir, nil
}

func newLocalProviderListCommand(_ *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Probe local model endpoints and list what they serve",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			// A mistyped AO_LLAMACPP_URL or AO_LLAMACPP_PORT is skipped rather
			// than fatal; say so on stderr, where it cannot corrupt --json.
			if problem := localproviders.LlamaCppOverrideProblem(); problem != nil {
				for _, line := range strings.Split(problem.Error(), "\n") {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", line)
				}
			}
			client := &http.Client{Timeout: localproviders.DefaultTimeout}
			results := localproviders.ProbeAll(cmd.Context(), client, localproviders.All(dataDir))
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(results)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "NAME\tURL\tKIND\tSTATUS\tMODELS")
			for _, result := range results {
				status := "unreachable"
				if result.Reachable {
					status = "up"
				}
				models := summarizeModels(result)
				_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", result.Name, result.BaseURL, result.Kind, status, models)
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			// An endpoint that is simply not running is the common case, so the
			// command succeeds and says so rather than failing.
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the probe results as JSON")
	return cmd
}

// summarizeModels keeps a long list readable while still naming enough of it to
// recognize the endpoint.
func summarizeModels(result localproviders.Result) string {
	if !result.Reachable {
		return result.Error
	}
	if len(result.Models) == 0 {
		return "none pulled"
	}
	const shown = 3
	if len(result.Models) <= shown {
		return strings.Join(result.Models, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(result.Models[:shown], ", "), len(result.Models)-shown)
}

func newLocalProviderAddCommand() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "add <name> <base-url>",
		Short: "Name a local model endpoint so it is probed and listed",
		Long: "Name a local model endpoint so it is probed and listed.\n\n" +
			"Ollama and llama.cpp's default addresses are probed already; this is for " +
			"anything else serving models on this machine or network — another port, " +
			"LM Studio, vLLM, a gateway, or a vendor's own local server such as Bionic.\n\n" +
			"The default kind, openai-compatible, asks GET <base-url>/v1/models. AO has " +
			"not verified any particular vendor against this route: an endpoint that " +
			"answers it is listed with its models, and one that does not is reported " +
			"with whatever it said instead.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return usageError{errors.New("add requires a name and a base URL")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			parsedKind, err := localproviders.ValidateKind(kind)
			if err != nil {
				return usageError{err}
			}
			added, err := localproviders.Add(dataDir, localproviders.Endpoint{
				Name: args[0], BaseURL: args[1], Kind: parsedKind,
			})
			if err != nil {
				return usageError{err}
			}
			// Probed at once: naming an endpoint is usually the moment the user
			// wants to know whether the address was right.
			result := localproviders.Probe(cmd.Context(), &http.Client{Timeout: localproviders.DefaultTimeout}, added)
			if result.Reachable {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s): up, %s\n", added.Name, added.BaseURL, summarizeModels(result))
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s): not reachable yet (%s)\n", added.Name, added.BaseURL, result.Error)
			return nil
		},
	}
	kinds := make([]string, 0, len(localproviders.Kinds))
	for _, k := range localproviders.Kinds {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	cmd.Flags().StringVar(&kind, "kind", "", "Request shape the endpoint answers: "+strings.Join(kinds, " or ")+" (default openai-compatible)")
	return cmd
}

func newLocalProviderRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Forget a named local model endpoint",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError{errors.New("rm requires one endpoint name")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := localProviderDataDir()
			if err != nil {
				return err
			}
			if err := localproviders.Remove(dataDir, args[0]); err != nil {
				return usageError{err}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", args[0])
			return nil
		},
	}
}
