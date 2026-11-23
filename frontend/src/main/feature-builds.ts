import { app } from "electron";
import type { UpdateSettings } from "./update-settings";
import { featureIndexUrl, readBakedUpdateOrigin } from "./update-origin";

// Feature builds are listed from the BlueAsh update server's feature index
// ({origin}/desktop/feature/index.json). The origin is the one baked into
// app-update.yml, so the picker and the updater always talk to the same server.
// With no valid baked origin (dev, or a legacy/invalid bundle) the picker is
// empty; there is no default origin and no fallback host.

// Feature builds older than this are dropped from the list, matching the
// cleanup workflow's 7-day expiry sweep so the app and CI agree on liveness.
const MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;

export interface FeatureBuild {
	pr: number;
	title: string;
	base: string;
	sha: string;
	slug: string;
	/** The version/tag of the build (e.g. "1.2.3-pr2270.0"). */
	buildId: string;
	publishedAt: string;
}

/**
 * Parse a version string for a feature-build prerelease identifier.
 * Matches "-pr<N>.<12-digit-ts>" (with optional leading "v"). Returns { pr } or
 * null. The anchor is kept in sync with frontend/scripts/feature-version.mjs's
 * parser (a local copy avoids a cross-dir import from the main process into the
 * build-scripts directory).
 */
export function parseFeatureBuild(version: string): { pr: number } | null {
	const m = version.match(/-pr(\d+)\.\d{12}/);
	if (!m) return null;
	const pr = parseInt(m[1], 10);
	return Number.isFinite(pr) && pr > 0 ? { pr } : null;
}

/** Return the feature-build pin for the currently running app version, or null. */
export function getActiveFeatureBuild(): { pr: number } | null {
	return parseFeatureBuild(app.getVersion());
}

const FETCH_TIMEOUT_MS = 10_000;

function parseIndexEntry(value: unknown): FeatureBuild | null {
	if (value === null || typeof value !== "object") return null;
	const e = value as Record<string, unknown>;
	if (typeof e.pr !== "number" || !Number.isInteger(e.pr) || e.pr <= 0) return null;
	if (typeof e.buildId !== "string" || typeof e.publishedAt !== "string") return null;
	if (!Number.isFinite(new Date(e.publishedAt).getTime())) return null;
	return {
		pr: e.pr,
		title: typeof e.title === "string" ? e.title : e.buildId,
		base: typeof e.base === "string" ? e.base : "",
		sha: typeof e.sha === "string" ? e.sha : "",
		slug: typeof e.slug === "string" ? e.slug : "",
		buildId: e.buildId,
		publishedAt: e.publishedAt,
	};
}

/**
 * Fetch and filter the live feature builds from the update server's feature
 * index: published within the last 7 days, grouped to the newest build per PR,
 * newest-first. The server owns PR liveness (it prunes merged/closed PRs from
 * the index), so the client makes no per-PR lookups. THROWS when the origin is
 * not configured or the index cannot be fetched, so callers can tell "no live
 * builds" apart from "could not reach the update server".
 */
async function collectFeatureBuilds(): Promise<FeatureBuild[]> {
	const origin = app.isPackaged ? readBakedUpdateOrigin() : undefined;
	if (!origin) throw new Error("update server is not configured");
	const res = await fetch(featureIndexUrl(origin), {
		cache: "no-store",
		headers: { Accept: "application/json", "User-Agent": `ao-desktop/${app.getVersion()}` },
		signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
	});
	if (!res.ok) throw new Error(`feature index ${res.status}`);
	const body = (await res.json()) as { builds?: unknown };
	if (!Array.isArray(body?.builds)) throw new Error("feature index is malformed");

	const cutoff = Date.now() - MAX_AGE_MS;
	const bestByPr = new Map<number, FeatureBuild>();
	for (const raw of body.builds) {
		const build = parseIndexEntry(raw);
		if (!build) continue;
		const publishedMs = new Date(build.publishedAt).getTime();
		if (publishedMs < cutoff) continue;
		const existing = bestByPr.get(build.pr);
		if (!existing || publishedMs > new Date(existing.publishedAt).getTime()) bestByPr.set(build.pr, build);
	}
	return [...bestByPr.values()].sort(
		(a, b) => new Date(b.publishedAt).getTime() - new Date(a.publishedAt).getTime(),
	);
}

/**
 * List available feature builds. Never throws: returns [] on network/HTTP
 * errors (the renderer just shows an empty picker). See collectFeatureBuilds
 * for the filter/group semantics.
 */
export async function listFeatureBuilds(): Promise<FeatureBuild[]> {
	try {
		return await collectFeatureBuilds();
	} catch (err) {
		console.warn("[feature-builds] failed to list feature builds:", err);
		return [];
	}
}

/**
 * Reconcile a pinned feature build against the live set. If the pinned PR no
 * longer has a live build (merged, closed, deleted, or expired past 7 days),
 * return settings with the pin cleared so the caller can fall back to the home
 * channel. A fetch failure is treated as "keep the pin" so a transient network
 * error never strands the user off their pinned build.
 */
export async function reconcileFeaturePin(
	settings: UpdateSettings,
): Promise<{ settings: UpdateSettings; cleared: boolean }> {
	if (!settings.feature) return { settings, cleared: false };
	let builds: FeatureBuild[];
	try {
		builds = await collectFeatureBuilds();
	} catch {
		return { settings, cleared: false };
	}
	if (builds.some((b) => b.pr === settings.feature!.pr)) return { settings, cleared: false };
	return { settings: { ...settings, feature: null }, cleared: true };
}
