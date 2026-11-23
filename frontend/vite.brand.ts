// Compile-time product profile (see src/shared/brand.ts). AO_BRAND=blueashai is the production build
// setting; anything else compiles the legacy profile. Because it is a build-time constant, a runtime
// environment variable cannot change which product a shipped binary is.
export function brandDefine(env: NodeJS.ProcessEnv = process.env): Record<string, string> {
	return { "process.env.AO_BRAND": JSON.stringify(env.AO_BRAND === "blueashai" ? "blueashai" : "") };
}
