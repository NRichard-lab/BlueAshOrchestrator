import { parseUpdateOrigin, renderAppUpdateYml } from "../src/main/update-origin";
import { resolveTestIdentity } from "./test-identity";
import { resolveBrand } from "../src/shared/brand";

// Owners that are the upstream Orchestrator project. A publish target there would
// push BlueAsh builds into the upstream release plane.
const UPSTREAM_RELEASE_OWNERS = ["untrivial-ai", "orchestratorinc", "aoagents", "agentwrapper"];

// parseReleaseRepo turns an "owner/repo" string (from AO_RELEASE_REPO) into the
// publisher-github { owner, name } shape. There is no default: undefined means
// "do not publish to GitHub" and a malformed or upstream value throws.
export function parseReleaseRepo(value: string | undefined): { owner: string; name: string } | undefined {
	if (!value) return undefined;
	const [owner, name, ...rest] = value.split("/");
	if (!owner || !name || rest.length > 0) {
		throw new Error(`AO_RELEASE_REPO must be "owner/repo", got "${value}"`);
	}
	if (UPSTREAM_RELEASE_OWNERS.includes(owner.toLowerCase())) {
		throw new Error(`AO_RELEASE_REPO must not target the upstream owner "${owner}"`);
	}
	return { owner, name };
}

// resolveAppUpdateYml renders the app-update.yml baked into the bundle.
export function resolveAppUpdateYml(env: NodeJS.ProcessEnv): string {
	const cacheDir = resolveTestIdentity(env)?.updaterCacheDirName ?? resolveBrand(env.AO_BRAND).updaterCacheDirName;
	const raw = env.AO_UPDATE_ORIGIN;
	if (!raw && env.AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN === "1") {
		// No feed: runtime treats this as "update server not configured" and
		// disables updates. Never a placeholder host.
		return `# No update origin configured at build time; updates are disabled.
updaterCacheDirName: ${cacheDir}
`;
	}
	const parsed = parseUpdateOrigin(raw);
	if (!parsed.ok) {
		throw new Error(
			`AO_UPDATE_ORIGIN is required to package a build with updates: ${parsed.reason}. ` +
				"Set it to the BlueAsh update server origin (https://<host>), or set " +
				"AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN=1 for a local build with updates disabled.",
		);
	}
	return renderAppUpdateYml(parsed.origin, cacheDir);
}
