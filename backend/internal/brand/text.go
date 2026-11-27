package brand

import "strings"

// upstreamName is the product name as written in this source tree.
const upstreamName = "Orchestrator"

// kept are upstream's own service names: the product name followed by one of these stays as written.
var kept = []string{" Cloud", "-Cloud", " Browser Bridge", " Browser-Bridge", "-Browser-Bridge"}

// Text returns s with the product name rewritten for this build, so a message keeps upstream's
// wording in the source and reads correctly in either product:
//
//	brand.Text("the Orchestrator daemon is not running")
//	fmt.Errorf(brand.Text("could not create the Orchestrator data directory at %s: %w"), dir, err)
//
// Wrapping the format string and not the result keeps the arguments untouched, so a path or a
// session title that happens to contain the upstream name is passed through as it is.
//
// An unstamped build (plain `go build`, `go test`, the legacy product) returns s unchanged, so
// existing output and test expectations hold.
//
// Text is for prose a person reads. It is not for: the orchestrator *session* role, which keeps its
// name in both products; text addressed to an agent, which is branded where the prompt is built;
// identifiers such as a git author name, an app bundle path or a managed-file sentinel; and the
// names of upstream's own services (Orchestrator Cloud, the Orchestrator Browser Bridge), which
// Text leaves alone wherever they appear.
//
// frontend/src/shared/brand-text.ts applies the same rules to the UI's strings; keep the two in step.
func Text(s string) string {
	if VisibleName == upstreamName || !strings.Contains(s, upstreamName) {
		return s
	}
	var b strings.Builder
	for rest := s; ; {
		at := strings.Index(rest, upstreamName)
		if at < 0 {
			b.WriteString(rest)
			return b.String()
		}
		end := at + len(upstreamName)
		after := rest[end:]
		// A trailing "s" is a plural or a German genitive; it stays after the new name.
		plural := strings.HasPrefix(after, "s")
		if plural {
			after = after[1:]
		}
		// Leave the word alone where it is part of a longer identifier (KindOrchestrator,
		// Orchestrator_x) or names one of upstream's own services.
		if at > 0 && isWordByte(rest[at-1]) || after != "" && isWordByte(after[0]) || startsWithAny(rest[end:], kept) {
			b.WriteString(rest[:end])
			rest = rest[end:]
			continue
		}
		lead := rest[:at]
		// "Orchestrator.inc" is upstream's display name: the whole token becomes the product name.
		after = strings.TrimPrefix(after, ".inc")
		// "Agent Orchestrator" is the old two-word name; "an Orchestrator" needs its article fixed.
		switch {
		case strings.HasSuffix(lead, "Agent "):
			lead = strings.TrimSuffix(lead, "Agent ")
		case strings.HasSuffix(lead, "an "):
			lead = strings.TrimSuffix(lead, "an ") + "a "
		case strings.HasSuffix(lead, "An "):
			lead = strings.TrimSuffix(lead, "An ") + "A "
		}
		b.WriteString(lead)
		b.WriteString(VisibleName)
		if plural {
			b.WriteString("s")
		}
		rest = after
	}
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func startsWithAny(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
