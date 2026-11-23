import { readFileSync } from "node:fs";
import path from "node:path";
import { UPDATES_ENABLED } from "../shared/brand";
import { parseUpdateOrigin, type UpdateOriginResult } from "../../scripts/update-origin-core.mjs";

// The BlueAsh update server is addressed by ONE configurable HTTPS origin. It is
// baked into the bundled app-update.yml at package time (AO_UPDATE_ORIGIN, see
// forge.config.ts) and every runtime update-plane URL (channel feeds, version
// floor, feature-build index) is derived from it. There is deliberately no
// default origin and no fallback host: when the origin is missing or invalid the
// update plane is disabled (fail closed) rather than pointed anywhere else.
//
// Tree layout (docs/update-server.md):
//   {origin}/desktop/stable/            latest.yml, latest-mac.yml, assets
//   {origin}/desktop/nightly/           nightly.yml, nightly-mac.yml, assets
//   {origin}/desktop/feature/pr{N}/     pr{N}.yml, pr{N}-mac.yml, assets
//   {origin}/desktop/feature/index.json live feature builds
//   {origin}/policy/desktop-version-floor.json
//   {origin}/bootstrap/latest/          ao start downloads

export const STABLE_FEED_PATH = "/desktop/stable/";
export const FLOOR_PATH = "/policy/desktop-version-floor.json";
export const FEATURE_INDEX_PATH = "/desktop/feature/index.json";

export { parseUpdateOrigin, type UpdateOriginResult };

/** Channel directory URL (trailing slash) that electron-updater's generic provider reads. */
export function desktopFeedUrl(
	origin: string,
	selection: { channel: "latest" | "nightly"; feature?: { pr: number } | null },
): string {
	if (selection.feature) {
		if (!Number.isInteger(selection.feature.pr) || selection.feature.pr <= 0) {
			throw new Error("feature pin must be a positive PR number");
		}
		return `${origin}/desktop/feature/pr${selection.feature.pr}/`;
	}
	return selection.channel === "nightly" ? `${origin}/desktop/nightly/` : `${origin}${STABLE_FEED_PATH}`;
}

export function floorUrl(origin: string): string {
	return `${origin}${FLOOR_PATH}`;
}

export function featureIndexUrl(origin: string): string {
	return `${origin}${FEATURE_INDEX_PATH}`;
}

export function defaultDownloadUrl(origin: string): string {
	return `${origin}/download/`;
}

/** True only when `candidate` is an https URL on exactly the update origin. */
export function isUrlOnOrigin(candidate: unknown, origin: string): candidate is string {
	if (typeof candidate !== "string") return false;
	try {
		const url = new URL(candidate);
		return url.protocol === "https:" && url.origin === origin && url.username === "" && url.password === "";
	} catch {
		return false;
	}
}

/**
 * Extract the origin from the text of a baked app-update.yml. The file must
 * declare the generic provider and a stable-channel URL on a valid origin;
 * anything else (including the legacy GitHub-provider form) yields undefined.
 */
export function originFromAppUpdateYml(yml: string): string | undefined {
	const provider = /^provider:\s*(.+)$/m.exec(yml)?.[1]?.trim();
	if (provider !== "generic") return undefined;
	const url = /^url:\s*(.+)$/m.exec(yml)?.[1]?.trim().replace(/^['"]|['"]$/g, "");
	if (!url?.endsWith(STABLE_FEED_PATH)) return undefined;
	const parsed = parseUpdateOrigin(url.slice(0, -STABLE_FEED_PATH.length));
	return parsed.ok ? parsed.origin : undefined;
}

/** Render the app-update.yml electron-updater reads at runtime. */
export function renderAppUpdateYml(origin: string, updaterCacheDirName: string): string {
	return [
		"provider: generic",
		`url: ${origin}${STABLE_FEED_PATH}`,
		`updaterCacheDirName: ${updaterCacheDirName}`,
		"",
	].join("\n");
}

/**
 * The update origin baked into this packaged build, or undefined (dev builds,
 * missing/legacy/invalid app-update.yml). Callers must treat undefined as
 * "update plane disabled"; there is no fallback origin.
 */
export function readBakedUpdateOrigin(resourcesPath: string = process.resourcesPath): string | undefined {
	// Compile-time brand switch: a build that must never update has no update origin, whatever file it ships.
	if (!UPDATES_ENABLED) return undefined;
	try {
		return originFromAppUpdateYml(readFileSync(path.join(resourcesPath, "app-update.yml"), "utf8"));
	} catch {
		return undefined;
	}
}
