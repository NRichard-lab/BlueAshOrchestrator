// Test-scoped build identity.
//
// A real NSIS installer built for local update testing must not be able to touch the
// installed Orchestrator. NSIS closes running apps by EXECUTABLE NAME, finds a previous
// install through the registry key derived from the appId, keeps the updater download
// cache under a named directory, and creates shortcuts named after the product. A test
// build therefore gets its own value for every one of those, derived from one short id,
// and the build refuses to run if it could also publish anything.
//
//   AO_TEST_IDENTITY=<id>   id: lowercase letter then 3-11 lowercase letters/digits
//
// Production builds never set it, and every value below is unchanged when it is unset.

import { BLUEASHAI_BRAND, LEGACY_BRAND } from "../src/shared/brand";

export type TestIdentity = {
	id: string;
	/** packaged binary name (no .exe); NSIS kills running processes by this name */
	executableName: string;
	/** electron-packager app name (output directory and display name) */
	packagerName: string;
	/** electron-builder appId; the install registry key is derived from it */
	appId: string;
	/** installer title, Apps & Features name, shortcut name */
	productName: string;
	/** installer file name pattern (electron-builder macros) */
	installerArtifactName: string;
	/** electron-updater cache directory name under %LOCALAPPDATA% */
	updaterCacheDirName: string;
};

const ID = /^[a-z][a-z0-9]{3,11}$/;
// Every product profile's production names, so a test build cannot collide with EITHER product
// (src/shared/brands.json). Lowercased for comparison.
const PRODUCTION_NAMES = [LEGACY_BRAND, BLUEASHAI_BRAND].flatMap((brand) =>
	[brand.executableName, brand.appId, brand.appUserModelId, brand.displayName, brand.packagerName, brand.updaterCacheDirName].map((name) =>
		name.toLowerCase(),
	),
);

export function resolveTestIdentity(env: NodeJS.ProcessEnv): TestIdentity | undefined {
	const id = env.AO_TEST_IDENTITY?.trim();
	if (!id) return undefined;
	if (!ID.test(id)) {
		throw new Error(`AO_TEST_IDENTITY must match ${ID} (got "${id}")`);
	}
	if (env.AO_RELEASE_REPO || env.AO_PUBLISHED_BUILD) {
		throw new Error("AO_TEST_IDENTITY builds must not set AO_RELEASE_REPO or AO_PUBLISHED_BUILD (no publishing, no Sentry)");
	}
	const identity: TestIdentity = {
		id,
		executableName: `ao-test-${id}`,
		packagerName: `AO Test ${id}`,
		appId: `dev.agent-orchestrator.test.${id}`,
		productName: `AO Update Test ${id}`,
		installerArtifactName: `AO Update Test ${id} Setup \${version}.\${ext}`,
		updaterCacheDirName: `ao-test-${id}-updater`,
	};
	for (const value of [identity.executableName, identity.appId, identity.productName]) {
		if (PRODUCTION_NAMES.some((name) => value.toLowerCase() === name)) {
			throw new Error(`test identity collides with a production name: ${value}`);
		}
	}
	return identity;
}

/** Deterministic pin for a test identity: unique run dir (so unique pipe names) and a port off the defaults. */
export function instancePinFor(identity: TestIdentity): { runFile: string; port: number } {
	let hash = 2166136261;
	for (const ch of identity.id) hash = Math.imul(hash ^ ch.charCodeAt(0), 16777619) >>> 0;
	return { runFile: `instance-${identity.id}/running.json`, port: 30000 + (hash % 30000) };
}

/**
 * The NSIS include for a TEST build: the production include (installer.nsh) plus, inside its
 * customInstall macro, the lines that write ao-instance.json beside the executable. The pin file is
 * only honored by builds compiled with the same identity (src/main/instance-pin.ts); production
 * installers never contain these lines.
 */
export function testInstallerInclude(identity: TestIdentity, productionInclude: string): string {
	const { runFile, port } = instancePinFor(identity);
	const marker = "!macro customInstall";
	if (!productionInclude.includes(marker)) throw new Error("installer.nsh has no customInstall macro to extend");
	const lines = [
		String.raw`  FileOpen $1 "$INSTDIR\ao-instance.json" w`,
		String.raw`  FileWrite $1 '{$\r$\n'`,
		String.raw`  FileWrite $1 '  "version": 1,$\r$\n'`,
		String.raw`  FileWrite $1 '  "identity": "${identity.id}",$\r$\n'`,
		String.raw`  FileWrite $1 '  "runFile": "${runFile}",$\r$\n'`,
		String.raw`  FileWrite $1 '  "port": ${port},$\r$\n'`,
		String.raw`  FileWrite $1 '  "skipProtocolRegistration": true,$\r$\n'`,
		String.raw`  FileWrite $1 '  "telemetryRemote": "off"$\r$\n'`,
		String.raw`  FileWrite $1 '}$\r$\n'`,
		String.raw`  FileClose $1`,
	];
	return productionInclude.replace(marker, `${marker}\n${lines.join("\n")}`);
}
