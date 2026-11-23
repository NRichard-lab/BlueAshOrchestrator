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
	it("bakes the BlueAshAI updater cache dir, and a feed-less file when no origin is supplied", () => {
		const env = { AO_BRAND: "blueashai", AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN: "1" } as NodeJS.ProcessEnv;
		const yml = resolveAppUpdateYml(env);
		expect(yml).toContain(`updaterCacheDirName: ${BLUEASHAI_BRAND.updaterCacheDirName}`);
		expect(yml).not.toMatch(/provider|url:|github|orchestrator/i);
		expect(resolveAppUpdateYml({ AO_BRAND: "blueashai", AO_UPDATE_ORIGIN: "https://updates.example.test" } as NodeJS.ProcessEnv)).toContain("url: https://updates.example.test/desktop/stable/");
		// Production still refuses to package updates without an origin and without the explicit opt-out.
		expect(() => resolveAppUpdateYml({ AO_BRAND: "blueashai" } as NodeJS.ProcessEnv)).toThrow(/AO_UPDATE_ORIGIN is required/);
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
});
