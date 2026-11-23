import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

const require = createRequire(import.meta.url);
const { applyBlueAshIdentity } = require("./app.config.js") as {
	applyBlueAshIdentity: (config: Record<string, any>, env: Record<string, string | undefined>) => Record<string, any>;
};
const app = require("./app.json").expo as Record<string, any>;

const PROJECT = "11111111-2222-4333-8444-555555555555";

describe("committed Expo config", () => {
	it("carries no upstream update or EAS identity", () => {
		const text = JSON.stringify(app);
		expect(text).not.toContain("2d7dec61");
		expect(text).not.toContain("u.expo.dev");
		expect(app.owner).toBeUndefined();
		expect(app.extra.eas).toBeUndefined();
		expect(app.updates.url).toBeUndefined();
	});
});

describe("applyBlueAshIdentity", () => {
	it("fails closed with no project: over-the-air updates are disabled and no URL is emitted", () => {
		const out = applyBlueAshIdentity(app, {});
		expect(out.updates.enabled).toBe(false);
		expect(out.updates.url).toBeUndefined();
		expect(out.extra.eas).toBeUndefined();
		expect(out.owner).toBeUndefined();
	});

	it("enables OTA only for the configured project", () => {
		const out = applyBlueAshIdentity(app, { AO_EXPO_PROJECT_ID: PROJECT, AO_EXPO_OWNER: "blueash" });
		expect(out.updates.url).toBe(`https://u.expo.dev/${PROJECT}`);
		expect(out.updates.enabled).not.toBe(false);
		expect(out.extra.eas.projectId).toBe(PROJECT);
		expect(out.owner).toBe("blueash");
	});

	it("refuses the upstream project, upstream owners, and malformed values", () => {
		expect(() => applyBlueAshIdentity(app, { AO_EXPO_PROJECT_ID: "2d7dec61-2206-4943-8582-a37e160907ea" })).toThrow(/upstream/);
		expect(() => applyBlueAshIdentity(app, { AO_EXPO_PROJECT_ID: "not-a-uuid" })).toThrow();
		expect(() => applyBlueAshIdentity(app, { AO_EXPO_PROJECT_ID: PROJECT, AO_EXPO_OWNER: "untrivial" })).toThrow(/upstream/);
		expect(() => applyBlueAshIdentity(app, { AO_IOS_BUNDLE_ID: "bad id" })).toThrow();
	});

	it("overrides store identifiers when provided and does not mutate the input", () => {
		const before = JSON.stringify(app);
		const out = applyBlueAshIdentity(app, { AO_IOS_BUNDLE_ID: "com.example.blueash", AO_ANDROID_PACKAGE: "com.example.blueash" });
		expect(out.ios.bundleIdentifier).toBe("com.example.blueash");
		expect(out.android.package).toBe("com.example.blueash");
		expect(JSON.stringify(app)).toBe(before);
	});
});
