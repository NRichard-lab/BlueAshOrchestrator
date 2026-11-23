// @vitest-environment node
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

// The blockmap builder needs real installers; the feed contents are covered by
// feed.test.mjs. Here only the staged tree layout matters.
vi.mock("./blockmap.mjs", () => ({
	writeBlockmap: vi.fn(async (filePath) => {
		writeFileSync(`${filePath}.blockmap`, "fake-blockmap");
		return { sha512: "MOCKED_BLOCKMAP_SHA512", size: 999 };
	}),
}));

import { stageUpdateTree, channelLayout } from "./stage-update-tree.mjs";
import { checkAppUpdateYml, checkBrandFlags, checkFeedlessAppUpdateYml, scanApp, scanSource, RELEASE_CRITICAL_SOURCES, REQUIRED_DAEMON_STAMPS } from "./verify-update-origin.mjs";
import { parseUpdateOrigin } from "./update-origin-core.mjs";
import { PLATFORM_CATALOG, selectPlatforms } from "./select-release-platforms.mjs";

const ORIGIN = "https://updates.example.test";
const repoRoot = resolve(import.meta.dirname, "..", "..");

let dir;
beforeEach(() => {
	dir = mkdtempSync(join(tmpdir(), "ao-update-plane-"));
});
afterEach(() => {
	rmSync(dir, { recursive: true, force: true });
});

function makeInstallers(version) {
	const input = join(dir, "in");
	mkdirSync(input, { recursive: true });
	for (const name of [
		`Agent.Orchestrator.Setup.${version}.exe`,
		`Agent.Orchestrator-darwin-arm64-${version}.zip`,
		"agent-orchestrator-darwin-arm64.zip",
		"agent-orchestrator-win32-x64.exe",
	]) {
		writeFileSync(join(input, name), `bytes:${name}`);
	}
	return input;
}

describe("update-origin-core", () => {
	it("accepts a BlueAsh-style origin and rejects upstream/unsafe ones", () => {
		expect(parseUpdateOrigin(ORIGIN)).toEqual({ ok: true, origin: ORIGIN });
		for (const bad of ["http://updates.example.test", "https://github.com", "https://raw.githubusercontent.com", "https://x.orchestrator.inc", "https://127.0.0.1", "https://updates.example.test/p"]) {
			expect(parseUpdateOrigin(bad).ok).toBe(false);
		}
	});
});

describe("stageUpdateTree", () => {
	it("stages the stable tree, digests, bootstrap set and floor under the origin layout", async () => {
		const input = makeInstallers("1.2.3");
		const out = join(dir, "out");
		await stageUpdateTree({ origin: ORIGIN, channel: "stable", version: "1.2.3", in: input, out, gitSha: "abc", floorLatest: "1.2.3" });
		expect(existsSync(join(out, "desktop/stable/latest.yml"))).toBe(true);
		expect(existsSync(join(out, "desktop/stable/latest-mac.yml"))).toBe(true);
		const digests = JSON.parse(readFileSync(join(out, "desktop/stable/digests.json"), "utf8"));
		expect(digests).toMatchObject({ version: "1.2.3", channel: "stable", gitSha: "abc" });
		expect(digests.assets.every((a) => /^[0-9a-f]{64}$/.test(a.sha256))).toBe(true);
		expect(readFileSync(join(out, "desktop/meta/healthz"), "utf8")).toBe("ok\n");
		const boot = JSON.parse(readFileSync(join(out, "bootstrap/latest/manifest.json"), "utf8"));
		expect(boot.assets.map((a) => a.name).sort()).toEqual(["agent-orchestrator-darwin-arm64.zip", "agent-orchestrator-win32-x64.exe"]);
		expect(boot.assets.every((a) => a.url.startsWith(`${ORIGIN}/bootstrap/latest/`))).toBe(true);
		expect(JSON.parse(readFileSync(join(out, "policy/desktop-version-floor.json"), "utf8"))).toEqual({
			min: "",
			latest: "1.2.3",
			downloadUrl: `${ORIGIN}/download/`,
		});
	});

	it("stages nightly and feature channels in their own directories and indexes feature builds", async () => {
		const input = makeInstallers("1.2.3-nightly.202611230000");
		const out = join(dir, "out");
		await stageUpdateTree({ origin: ORIGIN, channel: "nightly", version: "1.2.3-nightly.202611230000", in: input, out });
		expect(existsSync(join(out, "desktop/nightly/nightly.yml"))).toBe(true);
		expect(existsSync(join(out, "bootstrap"))).toBe(false);

		const fin = makeInstallers("1.2.3-pr7.202611230000");
		await stageUpdateTree({ origin: ORIGIN, channel: "feature", pr: 7, version: "1.2.3-pr7.202611230000", in: fin, out, publishedAt: "2026-11-23T00:00:00.000Z" });
		expect(existsSync(join(out, "desktop/feature/pr7/pr7.yml"))).toBe(true);
		const index = JSON.parse(readFileSync(join(out, "desktop/feature/index.json"), "utf8"));
		expect(index.builds).toHaveLength(1);
		expect(index.builds[0]).toMatchObject({ pr: 7, buildId: "1.2.3-pr7.202611230000", publishedAt: "2026-11-23T00:00:00.000Z" });
		// Restaging the same PR replaces its entry instead of duplicating it.
		await stageUpdateTree({ origin: ORIGIN, channel: "feature", pr: 7, version: "1.2.3-pr7.202611230000", in: fin, out });
		expect(JSON.parse(readFileSync(join(out, "desktop/feature/index.json"), "utf8")).builds).toHaveLength(1);
	});

	it("refuses an invalid origin, a bad version, and a missing PR", async () => {
		const input = makeInstallers("1.2.3");
		const out = join(dir, "out");
		await expect(stageUpdateTree({ origin: "https://github.com", channel: "stable", version: "1.2.3", in: input, out })).rejects.toThrow(/--origin/);
		await expect(stageUpdateTree({ origin: ORIGIN, channel: "stable", version: "latest", in: input, out })).rejects.toThrow(/semver/);
		expect(() => channelLayout("feature", undefined)).toThrow(/--pr/);
		expect(() => channelLayout("beta")).toThrow(/unknown channel/);
	});
});

describe("verify-update-origin", () => {
	const goodYml = `provider: generic\nurl: ${ORIGIN}/desktop/stable/\nupdaterCacheDirName: agent-orchestrator-updater\n`;

	it("accepts the baked generic feed and checks the expected origin", () => {
		expect(checkAppUpdateYml(goodYml, ORIGIN)).toEqual([]);
		expect(checkAppUpdateYml(goodYml, "https://other.example.test")[0]).toMatch(/!= expected/);
	});

	it("rejects the legacy github provider and upstream hosts", () => {
		expect(checkAppUpdateYml("provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n").length).toBeGreaterThan(0);
		expect(checkAppUpdateYml("provider: generic\nurl: https://github.com/desktop/stable/\n").join()).toMatch(/upstream/);
		expect(checkAppUpdateYml("provider: generic\nurl: http://updates.example.test/desktop/stable/\n").join()).toMatch(/https/);
	});

	it("scans a packaged app tree: passes clean, fails on an upstream URL in any file", () => {
		const app = join(dir, "app");
		mkdirSync(join(app, "Resources"), { recursive: true });
		writeFileSync(join(app, "Resources/app-update.yml"), goodYml);
		writeFileSync(join(app, "Resources/app.asar"), Buffer.from("xx https://example.test/ok xx"));
		expect(scanApp(app, ORIGIN).problems).toEqual([]);

		writeFileSync(join(app, "Resources/daemon"), Buffer.concat([Buffer.from([0, 1, 2]), Buffer.from("https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/x"), Buffer.from([0])]));
		const result = scanApp(app, ORIGIN);
		expect(result.problems.join("\n")).toMatch(/daemon: contains denied upstream pattern "raw\.githubusercontent\.com\/Untrivial-ai\/"/);
	});

	it("fails when no app-update.yml is present", () => {
		mkdirSync(join(dir, "empty"));
		expect(scanApp(join(dir, "empty")).problems[0]).toMatch(/no app-update\.yml/);
	});

	it("keeps every release-critical source path free of upstream update pointers", () => {
		for (const rel of RELEASE_CRITICAL_SOURCES) expect(existsSync(join(repoRoot, rel)), rel).toBe(true);
		expect(scanSource(repoRoot).problems).toEqual([]);
	});
});

describe("release platform selection (first release is Windows only)", () => {
	it("defaults to win32-x64 only", () => {
		expect(selectPlatforms("").map((p) => p.platform)).toEqual(["win32-x64"]);
		expect(selectPlatforms(undefined).map((p) => p.platform)).toEqual(["win32-x64"]);
		expect(selectPlatforms("  ").map((p) => p.platform)).toEqual(["win32-x64"]);
	});

	it("keeps every other platform defined and selectable", () => {
		expect(PLATFORM_CATALOG.map((p) => p.platform).sort()).toEqual(["darwin-arm64", "darwin-x64", "linux-x64", "win32-x64"]);
		expect(selectPlatforms("all")).toHaveLength(4);
		expect(selectPlatforms("linux-x64, win32-x64").map((p) => p.platform).sort()).toEqual(["linux-x64", "win32-x64"]);
		expect(selectPlatforms("darwin-arm64")[0]).toMatchObject({ os: "macos-latest", artifact: "mac-arm64" });
	});

	it("rejects unknown platforms instead of silently building nothing", () => {
		expect(() => selectPlatforms("freebsd-x64")).toThrow(/unknown platform/);
	});

	it("is wired into the workflow as a default-empty input feeding the build matrix", () => {
		const wf = readFileSync(join(repoRoot, ".github/workflows/build-artifacts.yml"), "utf8");
		expect(wf).toMatch(/platforms:[\s\S]*default: ""/);
		expect(wf).toContain("matrix: ${{ fromJSON(needs.select.outputs.matrix) }}");
		expect(wf).toContain("select-release-platforms.mjs");
	});
});

describe("feed-less (fail-closed) preview builds", () => {
	const feedless = "# No update origin configured at build time; updates are disabled.\nupdaterCacheDirName: blueashai-updater\n";

	it("accepts only a yml with no feed of any kind", () => {
		expect(checkFeedlessAppUpdateYml(feedless)).toEqual([]);
		expect(checkAppUpdateYml(feedless, undefined, { feedless: true })).toEqual([]);
		for (const bad of [
			"provider: generic\nurl: https://updates.example.test/desktop/stable/\nupdaterCacheDirName: x\n",
			"provider: github\nowner: a\nrepo: b\nupdaterCacheDirName: x\n",
			"url: https://updates.example.test/\nupdaterCacheDirName: x\n",
			"token: abc\nupdaterCacheDirName: x\n",
			"# nothing\n",
		]) {
			expect(checkFeedlessAppUpdateYml(bad).length, bad).toBeGreaterThan(0);
		}
	});

	it("a normal scan still demands a valid feed, and --no-feed scans still deny upstream strings", () => {
		expect(checkAppUpdateYml(feedless).length).toBeGreaterThan(0);
		const app = join(dir, "app");
		mkdirSync(join(app, "Resources"), { recursive: true });
		writeFileSync(join(app, "Resources/app-update.yml"), feedless);
		writeFileSync(join(app, "Resources/app.asar"), Buffer.from("fine"));
		expect(scanApp(app, undefined, { feedless: true }).problems).toEqual([]);
		expect(scanApp(app, undefined).problems.length).toBeGreaterThan(0);
		writeFileSync(join(app, "Resources/daemon"), Buffer.from("https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/x"));
		expect(scanApp(app, undefined, { feedless: true }).problems.join()).toMatch(/denied upstream pattern/);
	});
});

describe("compiled-out updates (BlueAshAI profile)", () => {
	const feedless = "# No update feed.\nupdaterCacheDirName: blueashai-updater\n";

	it("checkBrandFlags demands all three switches be off in the named profile", () => {
		const brands = JSON.parse(readFileSync(join(repoRoot, "frontend", "src", "shared", "brands.json"), "utf8"));
		expect(checkBrandFlags(brands, "blueashai")).toEqual([]);
		// The legacy profile legitimately keeps them on, so the guard is about the named profile only.
		expect(checkBrandFlags(brands, "legacy").length).toBe(3);
		expect(checkBrandFlags({ blueashai: { updatesEnabled: true, remoteTelemetryEnabled: false, pricingCatalogSyncEnabled: false } }).join()).toMatch(/updatesEnabled/);
		expect(checkBrandFlags({ blueashai: { updatesEnabled: false } }).length).toBe(2);
		expect(checkBrandFlags({}, "blueashai")[0]).toMatch(/no "blueashai" profile/);
	});

	function stagedApp({ stamps, manifest } = {}) {
		const app = join(dir, "app-off");
		mkdirSync(join(app, "Resources", "daemon"), { recursive: true });
		writeFileSync(join(app, "Resources", "app-update.yml"), feedless);
		writeFileSync(join(app, "Resources", "app.asar"), Buffer.from("fine"));
		writeFileSync(join(app, "Resources", "daemon", "ao.exe"), Buffer.from(`go build -ldflags=${(stamps ?? REQUIRED_DAEMON_STAMPS).map((x) => "-X x/" + x).join(" ")}`));
		if (manifest) writeFileSync(join(app, "Resources", manifest), "version: 1.0.0\n");
		return app;
	}

	it("--require-updates-off passes only a feed-less app whose daemon carries every stamp and which ships no manifest", () => {
		expect(scanApp(stagedApp(), undefined, { feedless: true, requireUpdatesOff: true }).problems).toEqual([]);
	});

	it("fails when a daemon stamp is missing, the daemon is absent, or an update manifest ships", () => {
		for (const missing of REQUIRED_DAEMON_STAMPS) {
			const app = stagedApp({ stamps: REQUIRED_DAEMON_STAMPS.filter((x) => x !== missing) });
			expect(scanApp(app, undefined, { feedless: true, requireUpdatesOff: true }).problems.join(), missing).toContain(missing);
			rmSync(app, { recursive: true, force: true });
		}
		const app = stagedApp({ manifest: "latest.yml" });
		expect(scanApp(app, undefined, { feedless: true, requireUpdatesOff: true }).problems.join()).toMatch(/update manifest ships/);
		rmSync(join(app, "Resources", "daemon"), { recursive: true, force: true });
		expect(scanApp(app, undefined, { feedless: true, requireUpdatesOff: true }).problems.join()).toMatch(/no daemon binary/);
	});

	it("without the flag nothing changes for builds that still ship updates", () => {
		const app = stagedApp({ stamps: [] });
		expect(scanApp(app, undefined, { feedless: true }).problems).toEqual([]);
	});
});
