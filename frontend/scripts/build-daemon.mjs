import { mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { meetsMinimumVersion, parseGoVersion, parseMinimumGoVersion } from "./go-version.mjs";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const repoRoot = resolve(frontendRoot, "..");
const backendRoot = join(repoRoot, "backend");
const outDir = join(frontendRoot, "daemon");
const outPath = join(outDir, process.platform === "win32" ? "ao.exe" : "ao");
const isWindowsDev = process.platform === "win32" && process.argv.includes("--dev");
const windowsDevOutDir = join(outDir, `dev-${Date.now()}-${process.pid}`);
const buildOutPath = isWindowsDev ? join(windowsDevOutDir, "ao.exe") : outPath;
const windowsDevManifestPath = join(outDir, "dev-daemon.json");
const minimumGoVersion = parseMinimumGoVersion(readFileSync(join(backendRoot, "go.mod"), "utf8"));

if (!minimumGoVersion) {
	console.error("Could not determine the required Go version from backend/go.mod.");
	process.exit(1);
}

const versionResult = spawnSync("go", ["version"], { encoding: "utf8", windowsHide: true });
if (versionResult.error) {
	console.error(
		`Go ${minimumGoVersion.join(".")}+ is required, but Go could not be started: ${versionResult.error.message}`,
	);
	process.exit(1);
}
const actualGoVersion = parseGoVersion(versionResult.stdout);
if (versionResult.status !== 0 || !actualGoVersion || !meetsMinimumVersion(actualGoVersion, minimumGoVersion)) {
	const found = actualGoVersion ? actualGoVersion.join(".") : versionResult.stdout.trim() || "unknown";
	console.error(`Go ${minimumGoVersion.join(".")}+ required, found ${found} — upgrade at https://go.dev/dl/`);
	process.exit(1);
}

if (isWindowsDev) {
	mkdirSync(windowsDevOutDir, { recursive: true });
} else if (process.platform === "win32") {
	// A running dev daemon may still hold an older dev-* binary open. Keep the
	// output directory in place and remove only the files that the packaged
	// build owns; locked dev folders can remain without affecting the bundled
	// daemon path.
	mkdirSync(outDir, { recursive: true });
	rmSync(outPath, { force: true });
	rmSync(windowsDevManifestPath, { force: true });
	cleanupOldWindowsDevDaemons(undefined);
} else {
	rmSync(outDir, { recursive: true, force: true });
	mkdirSync(outDir, { recursive: true });
}

// Stamp `ao version` so a packaged CLI names its build instead of "dev".
const cliPackage = "github.com/aoagents/agent-orchestrator/backend/internal/cli";
const appVersion = JSON.parse(readFileSync(join(frontendRoot, "package.json"), "utf8")).version;
const ldflags = [
	`-X ${cliPackage}.Version=${process.argv.includes("--dev") ? `${appVersion}-dev` : appVersion}`,
	`-X ${cliPackage}.Date=${new Date().toISOString().replace(/\.\d+Z$/, "Z")}`,
];
// Product identity profile (frontend/src/shared/brands.json). AO_BRAND=blueashai is the production
// build setting; unset keeps the legacy Orchestrator defaults compiled into the Go packages.
if (process.env.AO_BRAND === "blueashai") {
	const brands = JSON.parse(readFileSync(new URL("../src/shared/brands.json", import.meta.url), "utf8"));
	const b = brands.blueashai;
	const brandPackage = "github.com/aoagents/agent-orchestrator/backend/internal/brand";
	ldflags.push(`-X ${brandPackage}.StateDirName=${b.stateDirName}`);
	ldflags.push(`-X ${brandPackage}.Port=${b.defaultPort}`);
	ldflags.push(`-X ${brandPackage}.WindowsAssetName=${b.windowsAssetName}`);
	ldflags.push(`-X ${brandPackage}.WindowsInstallDirName=${b.windowsInstallDirName}`);
	ldflags.push(`-X ${brandPackage}.WindowsExeName=${b.executableName}.exe`);
	// macOS/Linux names `ao start` resolves, downloads and registers, and the Chrome host name.
	ldflags.push(`-X ${brandPackage}.ExecutableName=${b.executableName}`);
	ldflags.push(`-X ${brandPackage}.MacAppName=${b.packagerName}`);
	ldflags.push(`-X ${brandPackage}.ProtocolScheme=${b.protocolScheme}`);
	ldflags.push(`-X ${brandPackage}.NativeMessagingHost=${b.nativeMessagingHost}`);
	// Product identity (running.json / health payload) and the instance-variable prefix, so the
	// daemon and CLI read and export BLUEASH_RUN_FILE and friends instead of Orchestrator's AO_* names.
	ldflags.push(`-X ${brandPackage}.ID=${b.id}`);
	ldflags.push(`-X ${brandPackage}.DisplayName=${b.displayName}`);
	ldflags.push(`-X ${brandPackage}.EnvPrefix=${b.envPrefix}`);
	// Compile-time feature switches (brands.json). Three separate flags so egress attribution stays clean.
	ldflags.push(`-X ${brandPackage}.UpdatesEnabled=${b.updatesEnabled === false ? "false" : "true"}`);
	ldflags.push(`-X ${brandPackage}.RemoteTelemetryEnabled=${b.remoteTelemetryEnabled === false ? "false" : "true"}`);
	ldflags.push(`-X ${brandPackage}.PricingCatalogSyncEnabled=${b.pricingCatalogSyncEnabled === false ? "false" : "true"}`);
}
const commit = spawnSync("git", ["rev-parse", "--short=12", "HEAD"], {
	cwd: repoRoot,
	encoding: "utf8",
	windowsHide: true,
});
if (commit.status === 0 && commit.stdout.trim()) {
	ldflags.push(`-X ${cliPackage}.Commit=${commit.stdout.trim()}`);
}
// `ao remote-host link` must sign in with the same WorkOS client as this
// desktop build, or the machine lands in a different AO account.
const workOSClientID = process.env.VITE_WORKOS_CLIENT_ID?.trim();
if (workOSClientID) {
	ldflags.push(`-X ${cliPackage}.defaultWorkOSClientID=${workOSClientID}`);
}

const result = spawnSync("go", ["build", "-ldflags", ldflags.join(" "), "-o", buildOutPath, "./cmd/ao"], {
	cwd: backendRoot,
	stdio: "inherit",
	windowsHide: true,
});

if (result.error) {
	console.error(`failed to start go build: ${result.error.message}`);
	process.exit(1);
}

if (result.status !== 0) {
	process.exit(result.status ?? 1);
}

if (isWindowsDev) {
	writeFileSync(windowsDevManifestPath, `${JSON.stringify({ path: buildOutPath }, null, 2)}\n`);
	cleanupOldWindowsDevDaemons(buildOutPath);
}

function cleanupOldWindowsDevDaemons(activePath) {
	const activeDir = activePath ? dirname(activePath) : "";
	let entries;
	try {
		entries = readdirSync(outDir, { withFileTypes: true })
			.filter((entry) => entry.isDirectory() && entry.name.startsWith("dev-"))
			.map((entry) => {
				const dir = join(outDir, entry.name);
				return { dir, mtimeMs: statSync(dir).mtimeMs };
			})
			.sort((a, b) => b.mtimeMs - a.mtimeMs);
	} catch {
		return;
	}
	for (const entry of entries.slice(activePath ? 5 : 0)) {
		if (entry.dir === activeDir) continue;
		try {
			rmSync(entry.dir, { recursive: true, force: true });
		} catch {
			// Old session processes can keep their exe locked. They will be cleaned
			// up by a later dev build after the process exits.
		}
	}
}
