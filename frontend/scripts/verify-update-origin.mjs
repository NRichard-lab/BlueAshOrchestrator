// Static scan gate for the BlueAsh update plane (plan checkpoints C9 and C12).
//
//   node scripts/verify-update-origin.mjs app <dir> [--origin https://<host>]
//       Unpacked/packaged app (or its Resources dir): app-update.yml must be the
//       generic provider on a valid BlueAsh origin (and equal --origin /
//       AO_UPDATE_ORIGIN when given), and no file may contain an upstream
//       release/update URL. Reads raw bytes, so app.asar and the daemon binary
//       are scanned without unpacking.
//   node scripts/verify-update-origin.mjs origin <https://host>
//       Validate a candidate AO_UPDATE_ORIGIN value.
//   (add --no-feed to `app` or `yml` to accept ONLY a deliberately feed-less app-update.yml)
//   node scripts/verify-update-origin.mjs yml <app-update.yml> [--origin https://<host>]
//       Just the baked feed file (cheap pre-package/post-package check).
//   (add --require-updates-off to `app` to also require the daemon's updates/telemetry/pricing stamps and no update manifests)
//   node scripts/verify-update-origin.mjs brand <brands.json> [profile]
//       The profile (default blueashai) must keep updates, remote telemetry and the price-catalog fetch off.
//   node scripts/verify-update-origin.mjs source <repoRoot>
//       Release-critical source paths must not name the upstream release plane.
//
// Exit 0 = pass, 1 = deny hit or invalid feed, 2 = usage error. Never touches
// the network.
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { parseUpdateOrigin } from "./update-origin-core.mjs";

// Deny patterns for packaged artifacts: update/download plane URLs of the
// upstream project. Plain module paths (github.com/aoagents/...) and human-facing
// repository/doc links (issues, docs pages) are not update endpoints and are
// intentionally not listed; runtime egress proves they are never requested.
export const ARTIFACT_DENY = [
	"raw.githubusercontent.com/Untrivial-ai/",
	"Untrivial-ai/agent-orchestrator/releases",
	"raw.githubusercontent.com/OrchestratorInc/",
	"api.github.com/repos/Untrivial-ai/",
	"api.github.com/repos/OrchestratorInc/",
	"github.com/OrchestratorInc/agent-orchestrator/releases",
	"repos/Untrivial-ai/",
	"repos/OrchestratorInc/agent-orchestrator/releases",
	"owner: Untrivial-ai",
	"owner: OrchestratorInc",
];

// Broader deny list for release-critical source files.
export const SOURCE_DENY = [
	"Untrivial-ai",
	"OrchestratorInc/agent-orchestrator",
	"AgentWrapper/agent-orchestrator",
	"raw.githubusercontent.com",
	"releases/latest",
	"provider: github",
];

// Source files on the update/download/release path. Test files are excluded
// because they legitimately use upstream names as negative fixtures.
export const RELEASE_CRITICAL_SOURCES = [
	"frontend/forge.config.ts",
	"frontend/makers/update-feed.ts",
	"frontend/src/main/auto-updater.ts",
	"frontend/src/main/feature-builds.ts",
	"frontend/src/main/desktop-version-floor.ts",
	"frontend/src/main/update-origin.ts",
	"frontend/scripts/update-origin-core.mjs",
	"frontend/scripts/stage-update-tree.mjs",
	"backend/internal/cli/start.go",
	"backend/internal/cli/update_origin.go",
	"scripts/bootstrap-self-hosted.sh",
	"scripts/setup-self-hosted.sh",
	"desktop-version-floor.json",
	".github/workflows/build-artifacts.yml",
	"README.md",
	"frontend/src/docs/src/components/mdx.tsx",
];

// Files that legitimately mention an upstream name for a documented reason, as
// { file, pattern } pairs. Empty today: the deny/upstream lists spell hosts in
// lower case or in test files, which this scan does not read.
export const SOURCE_ALLOW = [];

const MAX_SCAN_BYTES = 512 * 1024 * 1024;

function* walk(dir) {
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		const full = join(dir, entry.name);
		if (entry.isDirectory()) yield* walk(full);
		else if (entry.isFile()) yield full;
	}
}

// A deliberately feed-less app-update.yml (local preview builds, AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN=1):
// no provider, url, owner, repo or token of any kind, so the packaged app fails closed ("no valid update
// server configured") and can never reach any host. Anything else is rejected, including a half-written feed.
export function checkFeedlessAppUpdateYml(yml) {
	const problems = [];
	for (const key of ["provider", "url", "owner", "repo", "token", "private", "channel", "host", "publisherName"]) {
		if (new RegExp(`^${key}:`, "m").test(yml)) problems.push(`feed-less app-update.yml must not declare "${key}"`);
	}
	if (!/^updaterCacheDirName:\s*\S+/m.test(yml)) problems.push("feed-less app-update.yml has no updaterCacheDirName");
	return problems;
}

export function checkAppUpdateYml(yml, expectedOrigin, { feedless = false } = {}) {
	if (feedless) return checkFeedlessAppUpdateYml(yml);
	const problems = [];
	const provider = /^provider:\s*(.+)$/m.exec(yml)?.[1]?.trim();
	if (provider !== "generic") problems.push(`app-update.yml provider is "${provider ?? "missing"}", expected "generic"`);
	if (/^(owner|repo):/m.test(yml)) problems.push("app-update.yml carries GitHub owner/repo coordinates");
	const url = /^url:\s*(.+)$/m.exec(yml)?.[1]?.trim().replace(/^['"]|['"]$/g, "");
	if (!url) {
		problems.push("app-update.yml has no url");
		return problems;
	}
	const suffix = "/desktop/stable/";
	if (!url.endsWith(suffix)) {
		problems.push(`app-update.yml url "${url}" does not end with ${suffix}`);
		return problems;
	}
	const parsed = parseUpdateOrigin(url.slice(0, -suffix.length));
	if (!parsed.ok) problems.push(`app-update.yml origin invalid: ${parsed.reason}`);
	else if (expectedOrigin) {
		const expected = parseUpdateOrigin(expectedOrigin);
		if (!expected.ok) problems.push(`expected origin invalid: ${expected.reason}`);
		else if (expected.origin !== parsed.origin) problems.push(`app-update.yml origin ${parsed.origin} != expected ${expected.origin}`);
	}
	return problems;
}

// The Go daemon is stamped with these by ldflags (frontend/scripts/build-daemon.mjs) when AO_BRAND=blueashai, and Go
// writes the ldflags into the binary's build info as plain text. Their presence in the shipped ao/ao.exe is the
// evidence that the compiled daemon has updates, remote telemetry and the price-catalog fetch compiled out.
export const REQUIRED_DAEMON_STAMPS = [
	"brand.UpdatesEnabled=false",
	"brand.RemoteTelemetryEnabled=false",
	"brand.PricingCatalogSyncEnabled=false",
];

// Manifests an update server would serve; none may ship inside a build whose updates are compiled out.
const FEED_MANIFEST_NAME = /^(latest|nightly|beta|alpha|pr\d+|dev-app-update)(-mac|-linux)?\.yml$/i;

/** The BlueAsh product profile must keep every update/remote switch off (brands.json). Pure: takes parsed JSON. */
export function checkBrandFlags(brands, id = "blueashai") {
	const profile = brands?.[id];
	if (!profile) return [`brands.json has no "${id}" profile`];
	const problems = [];
	for (const flag of ["updatesEnabled", "remoteTelemetryEnabled", "pricingCatalogSyncEnabled"]) {
		if (profile[flag] !== false) problems.push(`brands.json ${id}.${flag} must be false (found ${JSON.stringify(profile[flag])})`);
	}
	return problems;
}

export function scanApp(dir, expectedOrigin, options = {}) {
	const problems = [];
	if (options.requireUpdatesOff) {
		const daemons = [...walk(dir)].filter((f) => /(^|[\\/])ao(\.exe)?$/.test(f));
		if (daemons.length === 0) problems.push(`--require-updates-off: no daemon binary (ao/ao.exe) found under ${dir}`);
		for (const daemon of daemons) {
			const data = readFileSync(daemon);
			for (const stamp of REQUIRED_DAEMON_STAMPS) {
				if (!data.includes(Buffer.from(stamp))) problems.push(`${relative(dir, daemon)}: daemon is not stamped with ${stamp}`);
			}
		}
		for (const file of walk(dir)) {
			const base = file.split(/[\\/]/).pop();
			if (FEED_MANIFEST_NAME.test(base)) problems.push(`${relative(dir, file)}: update manifest ships in a build with updates compiled out`);
		}
	}
	const ymls = [...walk(dir)].filter((f) => f.endsWith("app-update.yml"));
	if (ymls.length === 0) problems.push(`no app-update.yml found under ${dir}`);
	for (const yml of ymls) {
		for (const p of checkAppUpdateYml(readFileSync(yml, "utf8"), expectedOrigin, options)) problems.push(`${relative(dir, yml)}: ${p}`);
	}
	const needles = ARTIFACT_DENY.map((p) => [p, Buffer.from(p)]);
	let scanned = 0;
	const evidence = [];
	for (const file of walk(dir)) {
		const size = statSync(file).size;
		if (size > MAX_SCAN_BYTES) {
			problems.push(`${relative(dir, file)}: larger than ${MAX_SCAN_BYTES} bytes, not scanned`);
			continue;
		}
		const data = readFileSync(file);
		scanned++;
		for (const [pattern, needle] of needles) {
			if (data.includes(needle)) problems.push(`${relative(dir, file)}: contains denied upstream pattern "${pattern}"`);
		}
		if (file.endsWith("app-update.yml")) evidence.push({ file: relative(dir, file), sha256: createHash("sha256").update(data).digest("hex") });
	}
	return { problems, scanned, evidence };
}

export function scanSource(root) {
	const problems = [];
	for (const rel of RELEASE_CRITICAL_SOURCES) {
		const file = join(root, rel);
		if (!existsSync(file)) {
			problems.push(`${rel}: release-critical file is missing`);
			continue;
		}
		const text = readFileSync(file, "utf8");
		for (const pattern of SOURCE_DENY) {
			if (!text.includes(pattern)) continue;
			if (SOURCE_ALLOW.some((a) => a.file === rel && a.pattern === pattern)) continue;
			problems.push(`${rel}: contains denied upstream pattern "${pattern}"`);
		}
	}
	return { problems };
}

function main(argv) {
	const [mode, target, ...rest] = argv;
	if (!mode || !target) {
		process.stderr.write("usage: verify-update-origin.mjs app <dir> [--origin URL] | origin <URL> | yml <file> [--origin URL] | source <repoRoot>\n");
		return 2;
	}
	let problems;
	if (mode === "app") {
		const i = rest.indexOf("--origin");
		const origin = (i >= 0 ? rest[i + 1] : undefined) ?? process.env.AO_UPDATE_ORIGIN;
		const result = scanApp(target, origin, { feedless: rest.includes("--no-feed"), requireUpdatesOff: rest.includes("--require-updates-off") });
		problems = result.problems;
		process.stdout.write(`scanned ${result.scanned} files\n`);
		for (const e of result.evidence) process.stdout.write(`evidence ${e.file} sha256=${e.sha256}\n`);
	} else if (mode === "origin") {
		const parsed = parseUpdateOrigin(target);
		problems = parsed.ok ? [] : [`origin ${target}: ${parsed.reason}`];
	} else if (mode === "yml") {
		const i = rest.indexOf("--origin");
		const origin = (i >= 0 ? rest[i + 1] : undefined) ?? process.env.AO_UPDATE_ORIGIN;
		problems = checkAppUpdateYml(readFileSync(target, "utf8"), origin, { feedless: rest.includes("--no-feed") });
	} else if (mode === "source") {
		problems = scanSource(target).problems;
	} else if (mode === "brand") {
		problems = checkBrandFlags(JSON.parse(readFileSync(target, "utf8")), rest[0] ?? "blueashai");
	} else {
		process.stderr.write(`unknown mode ${mode}\n`);
		return 2;
	}
	for (const p of problems) process.stderr.write(`FAIL ${p}\n`);
	process.stdout.write(problems.length === 0 ? "update-origin verification passed\n" : `update-origin verification failed (${problems.length})\n`);
	return problems.length === 0 ? 0 : 1;
}

if (process.argv[1]?.endsWith("verify-update-origin.mjs")) process.exit(main(process.argv.slice(2)));
