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
import { checkAppUpdateYml, scanApp, scanSource, RELEASE_CRITICAL_SOURCES } from "./verify-update-origin.mjs";
import { parseUpdateOrigin } from "./update-origin-core.mjs";

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
