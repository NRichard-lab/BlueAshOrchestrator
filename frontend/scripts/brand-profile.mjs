// The product identity profile for plain node build scripts.
//
// src/shared/brand.ts is the same data for TypeScript (Vite compiles the choice in); these scripts
// run under bare node, so they read brands.json and pick the profile from AO_BRAND themselves. Used
// by the macOS packaging scripts, which must stamp this product's bundle name and identifiers and
// never the other product's.
// A JSON import, not a read: it resolves the same under bare node and under the test runner.
import brands from "../src/shared/brands.json" with { type: "json" };

/** The profile named by AO_BRAND; anything but "blueashai" is the legacy Orchestrator profile. */
export function brandProfile(env = process.env) {
	return env.AO_BRAND === "blueashai" ? brands.blueashai : brands.legacy;
}

/**
 * Vite `define` for a bare-node build that compiles src/shared/brand.ts in, the same substitution
 * vite.brand.ts makes for the app's own bundles. Keep the two in step.
 */
export function brandDefine(env = process.env) {
	return { "process.env.AO_BRAND": JSON.stringify(env.AO_BRAND === "blueashai" ? "blueashai" : "") };
}

export { brands };
