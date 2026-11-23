// app.json stays the source of truth for the Expo config. This file puts
// google-services.json where app.json already expects it and applies the
// per-build BlueAsh update/EAS identity from the environment (see below).
//
// The file is gitignored (origin is public), and EAS Build only uploads what git
// tracks — so it has to reach the builder some other way. It arrives as the
// GOOGLE_SERVICES_JSON *file* environment variable:
//
//   eas env:set --name GOOGLE_SERVICES_JSON --type file \
//     --value ./google-services.json --visibility secret \
//     --environment preview --environment production
//
// EAS materialises it at an absolute path that differs per build and hands us that
// path in the env var. Emitting that path as `android.googleServicesFile` would make
// the resolved config — and therefore the fingerprint runtime version — differ
// between EAS Build and the machine that publishes updates, which is the exact class
// of bug the ignorePaths in fingerprint.config.js exist to prevent. So instead of
// emitting the path, copy the file to the fixed location app.json already points at
// and leave the config value alone. Locally the env var is unset (secret variables
// are not readable outside EAS) and the file is simply already there.
//
// This replaces the temp-commit dance that used to force the file past EAS's VCS
// client. That dance deleted a line from .gitignore, which is itself a fingerprint
// input, so every build carried a runtime no normal working tree could reproduce.
const fs = require("fs");
const path = require("path");

const target = path.join(__dirname, "google-services.json");
const provided = process.env.GOOGLE_SERVICES_JSON;

if (provided && path.resolve(provided) !== target) {
	fs.copyFileSync(provided, target);
}

// Update/EAS identity. The Expo project (OTA update URL, EAS project id, owner) is
// NOT in app.json: committing it would pin every build to one Expo account. It is
// supplied per build from the environment and there is no default. Without
// AO_EXPO_PROJECT_ID, over-the-air updates are disabled (fail closed): the app
// never points at an Expo project this build was not configured for.
//
//   AO_EXPO_PROJECT_ID   EAS project UUID (enables OTA at https://u.expo.dev/<id>)
//   AO_EXPO_OWNER        Expo account/organization that owns that project (optional)
//   AO_IOS_BUNDLE_ID / AO_ANDROID_PACKAGE
//                        store identifiers for a BlueAsh listing (optional override)
const PROJECT_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const OWNER = /^[A-Za-z0-9][A-Za-z0-9_-]*$/;
const APP_ID = /^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$/;
// The upstream Orchestrator Expo project and owner; refusing them keeps a stray
// environment from re-adopting the upstream update channel.
const UPSTREAM_PROJECT_IDS = ["2d7dec61-2206-4943-8582-a37e160907ea"];
const UPSTREAM_OWNERS = ["untrivial", "untrivial-ai", "orchestratorinc", "aoagents"];

function applyBlueAshIdentity(config, env) {
	const projectId = env.AO_EXPO_PROJECT_ID?.trim();
	const owner = env.AO_EXPO_OWNER?.trim();
	const next = { ...config };
	if (env.AO_IOS_BUNDLE_ID?.trim()) {
		if (!APP_ID.test(env.AO_IOS_BUNDLE_ID.trim())) throw new Error("AO_IOS_BUNDLE_ID is not a valid bundle identifier");
		next.ios = { ...config.ios, bundleIdentifier: env.AO_IOS_BUNDLE_ID.trim() };
	}
	if (env.AO_ANDROID_PACKAGE?.trim()) {
		if (!APP_ID.test(env.AO_ANDROID_PACKAGE.trim())) throw new Error("AO_ANDROID_PACKAGE is not a valid package name");
		next.android = { ...config.android, package: env.AO_ANDROID_PACKAGE.trim() };
	}
	if (owner) {
		if (!OWNER.test(owner) || UPSTREAM_OWNERS.includes(owner.toLowerCase())) {
			throw new Error("AO_EXPO_OWNER must be a BlueAsh-owned Expo account, not an upstream owner");
		}
		next.owner = owner;
	}
	if (!projectId) {
		// Fail closed: no project, no OTA channel.
		next.updates = { ...config.updates, enabled: false };
		delete next.updates.url;
		return next;
	}
	if (!PROJECT_ID.test(projectId) || UPSTREAM_PROJECT_IDS.includes(projectId.toLowerCase())) {
		throw new Error("AO_EXPO_PROJECT_ID must be a BlueAsh-owned EAS project UUID, not the upstream project");
	}
	next.updates = { ...config.updates, url: `https://u.expo.dev/${projectId}` };
	next.extra = { ...config.extra, eas: { ...config.extra?.eas, projectId } };
	return next;
}

module.exports = ({ config }) => {
	const identified = applyBlueAshIdentity(config, process.env);
	// Locally the env var is unset and the file is meant to be sitting in the
	// checkout. When it is not (a fresh clone, or anyone who only needs the
	// Android app to build), the config above still points at it and `expo
	// prebuild` stops with "Cannot copy google-services.json ... Ensure the source
	// and destination paths exist". Dropping the key in that case costs push and
	// keeps the build. With the file present nothing changes.
	if (fs.existsSync(target)) return identified;
	const { googleServicesFile: _unused, ...android } = identified.android ?? {};
	return { ...identified, android };
};
module.exports.applyBlueAshIdentity = applyBlueAshIdentity;
