import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { BLUEASHAI_BRAND, LEGACY_BRAND, resolveBrand } from "../src/shared/brand";
import { resolveAppUpdateYml } from "./update-feed";
import { resolveTestIdentity } from "./test-identity";

const read = (rel: string) => readFileSync(join(__dirname, "..", rel), "utf8");
const pkg = JSON.parse(read("package.json"));

describe("BlueAshAI package metadata", () => {
	it("carries the product name, author, homepage and a fork-lineage preview version", () => {
		expect(pkg.productName).toBe("BlueAshAI");
		expect(pkg.author).toBe("BlueAshAI");
		expect(pkg.homepage).toBe("https://github.com/NRichard-lab/BlueAshOrchestrator");
		expect(pkg.version).toMatch(/^0\.15\.4-blueash\.\d+$/);
		expect(read("index.html")).toContain("<title>BlueAshAI</title>");
	});
});

describe("BlueAshAI update cache and fail-closed updates", () => {
	it("bakes the BlueAshAI updater cache dir and always a feed-less file: its profile has updates disabled", () => {
		for (const env of [
			{ AO_BRAND: "blueashai", AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN: "1" },
			// No opt-out variable needed: updates are compiled out, so there is nothing to configure.
			{ AO_BRAND: "blueashai" },
		] as NodeJS.ProcessEnv[]) {
			const yml = resolveAppUpdateYml(env);
			expect(yml).toContain(`updaterCacheDirName: ${BLUEASHAI_BRAND.updaterCacheDirName}`);
			expect(yml).not.toMatch(/^(provider|url|owner|repo|token):/m);
			expect(yml).not.toMatch(/github|orchestratorinc|untrivial/i);
		}
	});

	it("refuses to bake an update origin or a publish target into a profile with updates disabled", () => {
		expect(() => resolveAppUpdateYml({ AO_BRAND: "blueashai", AO_UPDATE_ORIGIN: "https://updates.example.test" } as NodeJS.ProcessEnv)).toThrow(/must not be set/);
	});

	it("a profile with updates still requires an origin or the explicit opt-out (legacy behavior unchanged)", () => {
		expect(() => resolveAppUpdateYml({} as NodeJS.ProcessEnv)).toThrow(/AO_UPDATE_ORIGIN is required/);
		expect(resolveAppUpdateYml({ AO_UPDATE_ORIGIN: "https://updates.example.test" } as NodeJS.ProcessEnv)).toContain("url: https://updates.example.test/desktop/stable/");
	});

	it("the legacy profile keeps the Orchestrator cache dir", () => {
		expect(resolveAppUpdateYml({ AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN: "1" } as NodeJS.ProcessEnv)).toContain(LEGACY_BRAND.updaterCacheDirName);
	});
});

describe("production build settings", () => {
	it("a production BlueAshAI build is not a test build", () => {
		expect(resolveTestIdentity({ AO_BRAND: "blueashai" } as NodeJS.ProcessEnv)).toBeUndefined();
		expect(resolveBrand("blueashai").id).toBe("blueashai");
	});

	it("the release workflow builds with AO_BRAND=blueashai and names the Windows asset after the product", () => {
		const wf = readFileSync(join(__dirname, "..", "..", ".github", "workflows", "build-artifacts.yml"), "utf8");
		expect(wf).toMatch(/AO_BRAND:\s*blueashai/);
		expect(wf).toContain(`name="${BLUEASHAI_BRAND.windowsAssetName}"`);
		expect(wf).not.toMatch(/AO_TEST_IDENTITY/);
	});

	it("the release workflow bakes no update origin or publish target and verifies compiled-out updates", () => {
		const wf = readFileSync(join(__dirname, "..", "..", ".github", "workflows", "build-artifacts.yml"), "utf8");
		// Only comments may name the variables; no step may set or pass them.
		const live = wf.split("\n").filter((line) => !line.trim().startsWith("#")).join("\n");
		expect(live).not.toMatch(/AO_UPDATE_ORIGIN/);
		expect(live).not.toMatch(/AO_RELEASE_REPO/);
		expect(live).toContain("verify-update-origin.mjs brand src/shared/brands.json blueashai");
		expect(live).toContain("verify-update-origin.mjs app out --no-feed --require-updates-off");
	});

	it("scheduled workflows no longer poll vendor or release hosts for a product that updates nothing", () => {
		const read = (name: string) => readFileSync(join(__dirname, "..", "..", ".github", "workflows", name), "utf8");
		for (const name of ["harness-release-feeds.yml", "release-latest-guard.yml"]) {
			const live = read(name).split("\n").filter((line) => !line.trim().startsWith("#")).join("\n");
			expect(live, name).not.toMatch(/cron:/);
		}
		expect(read("harness-release-feeds.yml")).not.toMatch(/^\s*pull_request:/m);
	});
});
