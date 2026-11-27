import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { BLUEASHAI_BRAND, ENV, INSTANCE_ENV, LEGACY_BRAND, brandEnvName, productName, scrubForeignInstanceEnv } from "./brand";

describe("instance variable namespace", () => {
	it("keeps the AO_ names for Orchestrator", () => {
		expect(LEGACY_BRAND.envPrefix).toBe("AO");
		for (const name of INSTANCE_ENV) expect(brandEnvName(name, LEGACY_BRAND)).toBe(name);
		// The unit-test build is the legacy profile, so ENV is the legacy spelling.
		expect(ENV.RUN_FILE).toBe("AO_RUN_FILE");
		expect(ENV.DATA_DIR).toBe("AO_DATA_DIR");
		expect(ENV.PORT).toBe("AO_PORT");
	});

	it("gives BlueAshAI its own names, none of which an Orchestrator build reads", () => {
		expect(brandEnvName("AO_RUN_FILE", BLUEASHAI_BRAND)).toBe("BLUEASH_RUN_FILE");
		expect(brandEnvName("AO_DATA_DIR", BLUEASHAI_BRAND)).toBe("BLUEASH_DATA_DIR");
		expect(brandEnvName("AO_PORT", BLUEASHAI_BRAND)).toBe("BLUEASH_PORT");
		expect(brandEnvName("AO_APP_RUN_ID", BLUEASHAI_BRAND)).toBe("BLUEASH_APP_RUN_ID");
		for (const name of INSTANCE_ENV) {
			const branded = brandEnvName(name, BLUEASHAI_BRAND);
			expect(branded.startsWith("AO_")).toBe(false);
			expect(INSTANCE_ENV as readonly string[]).not.toContain(branded);
		}
	});

	it("lists the same instance variables as the Go daemon", () => {
		const goSource = readFileSync(
			path.resolve(__dirname, "../../../backend/internal/brand/brand.go"),
			"utf8",
		);
		const block = /var instanceEnv = map\[string\]bool\{([\s\S]*?)\n\}/.exec(goSource)?.[1] ?? "";
		const goNames = [...block.matchAll(/"(AO_[A-Z_]+)"/g)].map((match) => match[1]).sort();
		expect(goNames.length).toBeGreaterThan(0);
		expect([...INSTANCE_ENV].sort()).toEqual(goNames);
	});

	it("stamps the Go binary with the same product id and prefix", () => {
		const script = readFileSync(path.resolve(__dirname, "../../scripts/build-daemon.mjs"), "utf8");
		expect(script).toContain("${brandPackage}.ID=${b.id}");
		expect(script).toContain("${brandPackage}.EnvPrefix=${b.envPrefix}");
		expect(script).toContain("${brandPackage}.DisplayName=${b.displayName}");
	});
});

describe("scrubForeignInstanceEnv", () => {
	const inherited = () => ({
		AO_RUN_FILE: "C:\\Users\\u\\.ao\\running.json",
		AO_DATA_DIR: "C:\\Users\\u\\.ao\\data",
		AO_PORT: "3001",
		AO_APP_RUN_ID: "apprun-orchestrator",
		AO_OWNER: "app",
		ao_acp_runtime_dir: "C:\\Orchestrator\\resources\\acp-runtime",
		AO_SESSION_ID: "proj-1",
		AO_PROJECT_ID: "proj",
		BLUEASH_RUN_FILE: "C:\\Users\\u\\.blueashai\\running.json",
		PATH: "C:\\Windows",
	});

	it("removes the Orchestrator coordinates a BlueAshAI process inherited", () => {
		const env: Record<string, string | undefined> = inherited();
		const removed = scrubForeignInstanceEnv(env, BLUEASHAI_BRAND);
		expect(removed.sort()).toEqual(
			["AO_APP_RUN_ID", "AO_DATA_DIR", "AO_OWNER", "AO_PORT", "AO_RUN_FILE", "ao_acp_runtime_dir"].sort(),
		);
		expect(Object.keys(env).sort()).toEqual(["AO_PROJECT_ID", "AO_SESSION_ID", "BLUEASH_RUN_FILE", "PATH"]);
	});

	it("leaves an Orchestrator process alone: the AO_ names are its own", () => {
		const env: Record<string, string | undefined> = inherited();
		expect(scrubForeignInstanceEnv(env, LEGACY_BRAND)).toEqual([]);
		expect(env.AO_RUN_FILE).toBe("C:\\Users\\u\\.ao\\running.json");
	});
});

describe("productName", () => {
	it("names known products and flags unknown ones", () => {
		expect(productName("legacy")).toBe("Orchestrator.inc");
		expect(productName("blueashai")).toBe("BlueAshAI");
		expect(productName("something-else")).toBe("another product (something-else)");
	});
});
