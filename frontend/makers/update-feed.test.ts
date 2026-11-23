import { describe, expect, it } from "vitest";
import { parseReleaseRepo, resolveAppUpdateYml } from "./update-feed";

describe("baked update feed", () => {
	it("bakes a generic-provider feed from AO_UPDATE_ORIGIN with no GitHub coordinates", () => {
		const yml = resolveAppUpdateYml({ AO_UPDATE_ORIGIN: "https://updates.example.test" });
		expect(yml).toContain("provider: generic");
		expect(yml).toContain("url: https://updates.example.test/desktop/stable/");
		expect(yml).not.toMatch(/github|owner:|repo:|Untrivial|OrchestratorInc|aoagents/i);
	});

	it("refuses to package without a valid origin (no default, no upstream fallback)", () => {
		expect(() => resolveAppUpdateYml({})).toThrow(/AO_UPDATE_ORIGIN is required/);
		expect(() => resolveAppUpdateYml({ AO_UPDATE_ORIGIN: "http://updates.example.test" })).toThrow(/https/);
		expect(() => resolveAppUpdateYml({ AO_UPDATE_ORIGIN: "https://github.com" })).toThrow(/upstream/);
		// A release repo must not stand in for the origin.
		expect(() => resolveAppUpdateYml({ AO_RELEASE_REPO: "Someone/repo" })).toThrow(/AO_UPDATE_ORIGIN/);
	});

	it("bakes a feed-less file only for an explicit local unconfigured build", () => {
		const yml = resolveAppUpdateYml({ AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN: "1" });
		expect(yml).not.toMatch(/provider|url:|github/i);
		// An invalid configured origin still fails even with the escape hatch.
		expect(() => resolveAppUpdateYml({ AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN: "1", AO_UPDATE_ORIGIN: "http://x.test" })).toThrow();
	});

	it("has no GitHub publisher unless AO_RELEASE_REPO is set explicitly, and never an upstream owner", () => {
		expect(parseReleaseRepo(undefined)).toBeUndefined();
		expect(parseReleaseRepo("Fork/repo")).toEqual({ owner: "Fork", name: "repo" });
		expect(() => parseReleaseRepo("Untrivial-ai/agent-orchestrator")).toThrow(/upstream/);
		expect(() => parseReleaseRepo("OrchestratorInc/agent-orchestrator")).toThrow(/upstream/);
		expect(() => parseReleaseRepo("aoagents/x")).toThrow(/upstream/);
		expect(() => parseReleaseRepo("justone")).toThrow(/owner\/repo/);
	});
});
