// Stage a BlueAsh update-server tree on local disk. LOCAL ONLY: this never
// uploads, publishes or contacts a network; the (external) publish step in
// docs/update-server.md copies the staged tree to the update origin in the
// order defined there.
//
//   node scripts/stage-update-tree.mjs --origin https://<host> --channel stable|nightly|feature \
//        --version <semver> --in <dir with built installers> --out <staging dir> \
//        [--pr <N>] [--git-sha <sha>] [--published-at <ISO>] [--important] \
//        [--floor-min <v>] [--floor-latest <v>]
//
// Layout produced (see docs/update-server.md):
//   desktop/<stable|nightly|feature/pr<N>>/  installers, <channel>[-mac|-linux].yml, digests.json
//   desktop/feature/index.json               merged feature-build index (feature channel)
//   desktop/meta/healthz
//   bootstrap/latest/                        unversioned ao-start aliases + manifest.json (stable)
//   policy/desktop-version-floor.json        only when --floor-min/--floor-latest is given
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { generateFeeds } from "./feed.mjs";
import { parseUpdateOrigin } from "./update-origin-core.mjs";

const INSTALLER = /\.(exe|zip|AppImage|deb|rpm|dmg)$/;
// The unversioned names `ao start` downloads (backend/internal/cli/start.go assetName).
const BOOTSTRAP_ASSETS = [
	"agent-orchestrator-darwin-arm64.zip",
	"agent-orchestrator-darwin-x64.zip",
	"agent-orchestrator-win32-x64.exe",
	"BlueAshAI-win32-x64.exe",
	"agent-orchestrator-linux-x64.AppImage",
	"BlueAshAI-darwin-arm64.zip",
	"BlueAshAI-darwin-x64.zip",
	"BlueAshAI-linux-x64.AppImage",
];

export function channelLayout(channel, pr) {
	if (channel === "stable") return { dir: "desktop/stable", manifest: "latest" };
	if (channel === "nightly") return { dir: "desktop/nightly", manifest: "nightly" };
	if (channel === "feature") {
		if (!Number.isInteger(pr) || pr <= 0) throw new Error("--pr must be a positive integer for the feature channel");
		return { dir: `desktop/feature/pr${pr}`, manifest: `pr${pr}` };
	}
	throw new Error(`unknown channel "${channel}" (expected stable, nightly or feature)`);
}

function sha256(path) {
	return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function writeJson(path, value) {
	mkdirSync(join(path, ".."), { recursive: true });
	writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

export async function stageUpdateTree(options) {
	const parsed = parseUpdateOrigin(options.origin);
	if (!parsed.ok) throw new Error(`--origin: ${parsed.reason}`);
	const origin = parsed.origin;
	const version = String(options.version ?? "");
	if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/.test(version)) {
		throw new Error(`--version must be semver, got "${version}"`);
	}
	const { dir, manifest } = channelLayout(options.channel, options.pr);
	const publishedAt = options.publishedAt ?? new Date().toISOString();
	const outChannel = join(options.out, dir);
	mkdirSync(outChannel, { recursive: true });

	const installers = readdirSync(options.in).filter((f) => INSTALLER.test(f) && statSync(join(options.in, f)).isFile());
	if (installers.length === 0) throw new Error(`no installers found in ${options.in}`);
	for (const name of installers) copyFileSync(join(options.in, name), join(outChannel, name));

	await generateFeeds(outChannel, version, manifest, publishedAt, options.important === true);

	const files = readdirSync(outChannel).filter((f) => f !== "digests.json").sort();
	const assets = files.map((name) => ({ name, sha256: sha256(join(outChannel, name)), size: statSync(join(outChannel, name)).size }));
	writeJson(join(outChannel, "digests.json"), {
		version,
		channel: options.channel === "feature" ? manifest : options.channel,
		gitSha: options.gitSha ?? null,
		publishedAt,
		assets,
	});

	mkdirSync(join(options.out, "desktop/meta"), { recursive: true });
	writeFileSync(join(options.out, "desktop/meta/healthz"), "ok\n");

	if (options.channel === "stable") {
		const boot = join(options.out, "bootstrap/latest");
		mkdirSync(boot, { recursive: true });
		const bootAssets = [];
		for (const name of BOOTSTRAP_ASSETS) {
			const src = join(options.in, name);
			if (!existsSync(src)) continue;
			copyFileSync(src, join(boot, name));
			bootAssets.push({ name, sha256: sha256(src), size: statSync(src).size, url: `${origin}/bootstrap/latest/${name}` });
		}
		writeJson(join(boot, "manifest.json"), { version, publishedAt, assets: bootAssets });
	}

	if (options.channel === "feature") {
		const indexPath = join(options.out, "desktop/feature/index.json");
		let builds = [];
		if (existsSync(indexPath)) {
			try {
				builds = JSON.parse(readFileSync(indexPath, "utf8")).builds ?? [];
			} catch {
				builds = [];
			}
		}
		builds = builds.filter((b) => b.pr !== options.pr);
		builds.push({
			pr: options.pr,
			title: options.title ?? `Feature build pr${options.pr}`,
			base: options.base ?? "",
			sha: options.gitSha ?? "",
			slug: manifest,
			buildId: version,
			publishedAt,
		});
		writeJson(indexPath, { builds });
	}

	if (options.floorMin || options.floorLatest) {
		writeJson(join(options.out, "policy/desktop-version-floor.json"), {
			min: options.floorMin ?? "",
			latest: options.floorLatest ?? "",
			downloadUrl: `${origin}/download/`,
		});
	}
	return { origin, dir, assets };
}

function parseArgs(argv) {
	const opts = {};
	const flags = { "--origin": "origin", "--channel": "channel", "--version": "version", "--in": "in", "--out": "out", "--git-sha": "gitSha", "--published-at": "publishedAt", "--floor-min": "floorMin", "--floor-latest": "floorLatest", "--title": "title", "--base": "base" };
	for (let i = 0; i < argv.length; i++) {
		const arg = argv[i];
		if (arg === "--important") opts.important = true;
		else if (arg === "--pr") opts.pr = Number.parseInt(argv[++i], 10);
		else if (flags[arg]) opts[flags[arg]] = argv[++i];
		else throw new Error(`unknown argument ${arg}`);
	}
	return opts;
}

if (import.meta.url === `file://${process.argv[1]}` || process.argv[1]?.endsWith("stage-update-tree.mjs")) {
	try {
		const opts = parseArgs(process.argv.slice(2));
		for (const required of ["origin", "channel", "version", "in", "out"]) {
			if (!opts[required]) throw new Error(`missing --${required === "in" ? "in" : required}`);
		}
		const result = await stageUpdateTree(opts);
		process.stdout.write(`staged ${result.assets.length} files under ${join(opts.out, result.dir)} for ${result.origin}\n`);
	} catch (err) {
		process.stderr.write(`${err.message}\n`);
		process.exit(2);
	}
}
