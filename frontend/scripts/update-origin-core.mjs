// Single implementation of the BlueAsh update-origin rules, shared by the
// Electron main process (src/main/update-origin.ts), forge.config.ts and the
// packaging/CI scripts (verify-update-origin.mjs, stage-update-tree.mjs).
// Dependency-free so CI can run it directly with plain node.
import { isIP } from "node:net";

// Hosts that belong to the upstream Orchestrator project or its hosting. An
// origin pointing at them would silently re-introduce the upstream update plane
// the migration removes, so they are rejected even though they are valid HTTPS.
export const UPSTREAM_HOST_SUFFIXES = [
	"github.com",
	"githubusercontent.com",
	"githubassets.com",
	"github.io",
	"orchestrator.inc",
	"orchestratorusercontent.com",
	"expo.dev",
	"expo.io",
];

export function isUpstreamHost(hostname) {
	const host = String(hostname).toLowerCase();
	return UPSTREAM_HOST_SUFFIXES.some((suffix) => host === suffix || host.endsWith(`.${suffix}`));
}

// parseUpdateOrigin validates a candidate update-server origin. Returns
// { ok: true, origin } (scheme + host[:port], no trailing slash) or
// { ok: false, reason }.
export function parseUpdateOrigin(raw) {
	if (typeof raw !== "string" || raw.trim() === "") {
		return { ok: false, reason: "update origin is not set" };
	}
	let url;
	try {
		url = new URL(raw.trim());
	} catch {
		return { ok: false, reason: "update origin is not a valid URL" };
	}
	if (url.protocol !== "https:") return { ok: false, reason: "update origin must use https" };
	if (url.username !== "" || url.password !== "") {
		return { ok: false, reason: "update origin must not contain credentials" };
	}
	if (url.search !== "" || url.hash !== "") {
		return { ok: false, reason: "update origin must not contain a query or fragment" };
	}
	if (url.pathname !== "/") return { ok: false, reason: "update origin must be a bare origin without a path" };
	const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, "");
	if (host === "" || host === "localhost" || host.endsWith(".localhost") || isIP(host) !== 0) {
		return { ok: false, reason: "update origin must be a public DNS host name" };
	}
	if (!host.includes(".")) return { ok: false, reason: "update origin must be a fully qualified host name" };
	if (isUpstreamHost(host)) {
		return { ok: false, reason: "update origin must not be an upstream Orchestrator or GitHub host" };
	}
	return { ok: true, origin: url.origin };
}
