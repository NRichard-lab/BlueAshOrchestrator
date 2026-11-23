import { defineConfig } from "vite";
import { sentrySourceMaps } from "./vite.sentry";
import { resolveTestIdentity } from "./makers/test-identity";

const sentry = sentrySourceMaps();

// better-sqlite3 is a native module rebuilt for Electron by Forge. Keep it out
// of Vite's bundle so the packaged app loads the rebuilt binary from
// node_modules (the auto-unpack plugin moves that binary outside app.asar).
export default defineConfig({
	// Set only by the release build (build-artifacts.yml). Without it a packaged
	// copy never initializes Sentry (src/shared/sentry-config.ts).
	define: {
		"process.env.AO_PUBLISHED_BUILD": JSON.stringify(process.env.AO_PUBLISHED_BUILD === "1" ? "1" : ""),
		// Compile-time test identity (makers/test-identity.ts). Empty in every release build, which
		// makes src/main/instance-pin.ts inert there; a runtime environment variable cannot set it.
		"process.env.AO_TEST_BUILD_IDENTITY": JSON.stringify(resolveTestIdentity(process.env)?.id ?? ""),
	},
	plugins: sentry.plugins,
	build: {
		sourcemap: sentry.sourcemap,
		rollupOptions: {
			external: ["better-sqlite3"],
		},
	},
});
