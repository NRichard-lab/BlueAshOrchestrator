package cli

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

// updateOrigin is the BlueAsh update-server origin (https://host) that `ao start`
// downloads the desktop app from. There is deliberately NO default: it is
// stamped at build time, mirroring how version.go's Version is stamped:
//
//	go build -ldflags "-X github.com/aoagents/agent-orchestrator/backend/internal/cli.updateOrigin=https://<host>" ./cmd/ao
//
// When it is empty, AO_UPDATE_ORIGIN from the environment is used instead. If
// neither is set, `ao start` refuses to download (fail closed): it never falls
// back to an upstream GitHub release.
var updateOrigin = ""

// updateOriginEnv is the runtime fallback for a binary built without a stamped
// origin (for example a source build). A stamped origin always wins so a stray
// environment variable cannot redirect a release binary.
const updateOriginEnv = "AO_UPDATE_ORIGIN"

// bootstrapPath is the update-server directory holding the `ao start` assets.
const bootstrapPath = "/bootstrap/latest/"

// upstreamHostSuffixes are hosts of the upstream Orchestrator project and its
// hosting. An origin there would re-introduce the upstream update plane.
var upstreamHostSuffixes = []string{
	"github.com",
	"githubusercontent.com",
	"githubassets.com",
	"github.io",
	"orchestrator.inc",
	"orchestratorusercontent.com",
	"expo.dev",
	"expo.io",
}

var errUpdateOriginMissing = errors.New("ao start: no update server is configured. " +
	"This build has no stamped update origin and " + updateOriginEnv + " is not set. " +
	"Install the desktop app from your BlueAsh download page, or set " + updateOriginEnv + "=https://<update-host>")

// validateUpdateOrigin returns the normalized origin (scheme://host[:port], no
// trailing slash) or an error describing why raw is not an acceptable origin.
func validateUpdateOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errUpdateOriginMissing
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("update origin %q is not a valid URL", raw)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("update origin %q must use https", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("update origin %q must not contain credentials", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", fmt.Errorf("update origin %q must not contain a query or fragment", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("update origin %q must be a bare origin without a path", raw)
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return "", fmt.Errorf("update origin %q must be a public DNS host name, not an IP address", raw)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return "", fmt.Errorf("update origin %q must be a fully qualified public host name", raw)
	}
	for _, suffix := range upstreamHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return "", fmt.Errorf("update origin %q must not be an upstream Orchestrator or GitHub host", raw)
		}
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// resolveUpdateOrigin picks the stamped origin, else the environment, and
// validates it. There is no third source.
func resolveUpdateOrigin() (string, error) {
	if strings.TrimSpace(updateOrigin) != "" {
		return validateUpdateOrigin(updateOrigin)
	}
	return validateUpdateOrigin(os.Getenv(updateOriginEnv))
}

// downloadURL builds the bootstrap URL for asset on the update server.
func downloadURL(asset string) (string, error) {
	origin, err := resolveUpdateOrigin()
	if err != nil {
		return "", err
	}
	return origin + bootstrapPath + url.PathEscape(asset), nil
}
