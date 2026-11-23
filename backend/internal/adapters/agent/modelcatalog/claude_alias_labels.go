package modelcatalog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeAliasFamilies are the Claude Code model aliases that name a family
// rather than a version ("opus" runs the newest Opus Claude Code knows).
var claudeAliasFamilies = []string{"fable", "opus", "sonnet", "haiku"}

// claudeAliasPattern matches a bare family alias, optionally with a context
// suffix such as "opus[1m]".
var claudeAliasPattern = regexp.MustCompile(`^(fable|opus|sonnet|haiku)(\[[^\]]+\])?$`)

// claudeDisplayVersionPattern reads "5.5" out of a provider display name such
// as "Claude Opus 5.5".
var claudeDisplayVersionPattern = regexp.MustCompile(`\b(\d{1,2}(?:\.\d{1,2})?)\b`)

// LabelClaudeAliasVersions marks Claude Code family aliases as floating
// ("opus" becomes "Opus (latest)") when reference, a catalog the provider
// itself reported, contains that family. Aliases whose family is absent from
// reference, or whose label already names a version, keep their label.
//
// A family alias is resolved by the CLI, not by AO: `--model opus` runs
// whichever Opus the installed Claude Code picks, which is not necessarily the
// newest Opus in the provider's list. Labelling the alias with a version read
// off that list therefore promised a specific model AO was not asking for, and
// a user who chose "Opus 5.5" could be served another model entirely. Only
// concrete IDs carry a version in their label now, so every version a picker
// shows is one the launch argument actually requests.
func LabelClaudeAliasVersions(models, reference []ports.AgentModelInfo) []ports.AgentModelInfo {
	if len(models) == 0 || len(reference) == 0 {
		return models
	}
	newest := make(map[string][]int, len(claudeAliasFamilies))
	for _, item := range reference {
		if claudeAliasPattern.MatchString(strings.ToLower(strings.TrimSpace(item.ID))) {
			continue
		}
		family, version := claudeFamilyVersion(item)
		if family == "" {
			continue
		}
		if current, ok := newest[family]; !ok || compareVersions(version, current) > 0 {
			newest[family] = version
		}
	}
	if len(newest) == 0 {
		return models
	}
	out := make([]ports.AgentModelInfo, len(models))
	copy(out, models)
	for i, item := range out {
		match := claudeAliasPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(item.ID)))
		if match == nil {
			continue
		}
		if _, ok := newest[match[1]]; !ok {
			continue
		}
		// The ID matched claudeAliasPattern, so it is floating by construction.
		// Any version in the incoming label — the provider's or an earlier
		// guess — is dropped rather than carried on a label whose ID does not
		// request it.
		base := strings.ToUpper(match[1][:1]) + match[1][1:]
		// Keep a context variant ("opus[1m]") apart from the family's plain
		// model, or the picker lists the same name twice.
		if window := strings.Trim(match[2], "[]"); window != "" {
			out[i].Label = base + " (latest, " + strings.ToUpper(window) + " context)"
			continue
		}
		out[i].Label = base + " (latest)"
	}
	return out
}

// claudeFamilyVersion reads the family and major.minor version of a concrete
// provider model, preferring its display name and falling back to the ID
// ("claude-opus-5-5-20260901"). Snapshot dates are never versions.
func claudeFamilyVersion(item ports.AgentModelInfo) (string, []int) {
	label := strings.ToLower(item.Label)
	id := strings.ToLower(item.ID)
	for _, family := range claudeAliasFamilies {
		if !strings.Contains(label, family) && !strings.Contains(id, family) {
			continue
		}
		if _, afterFamily, found := strings.Cut(label, family); found && label != id {
			if match := claudeDisplayVersionPattern.FindString(family + afterFamily); match != "" {
				return family, parseVersion(strings.Split(match, "."))
			}
		}
		if _, rest, ok := strings.Cut(id, family); ok {
			parts := make([]string, 0, 2)
			for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) {
				if len(part) > 2 || len(parts) == 2 {
					break
				}
				parts = append(parts, part)
			}
			if len(parts) > 0 {
				return family, parseVersion(parts)
			}
		}
		return "", nil
	}
	return "", nil
}

func parseVersion(parts []string) []int {
	version := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		version = append(version, n)
	}
	return version
}
