// Build REAL NSIS installers for a local update test, under a test-scoped identity
// (makers/test-identity.ts) so they cannot collide with an installed Orchestrator.
//
//   node scripts/build-test-installers.mjs --id upd1a2 --origin https://updates.blueash-test.example \
//        --versions 0.99.1,0.99.2 --out <dir>
//
// For each version it stamps frontend/package.json (restored byte-for-byte afterwards, even on
// failure), runs `electron-forge make` for win32/x64 with AO_TEST_IDENTITY and AO_UPDATE_ORIGIN
// set, and copies the resulting installer (+ .blockmap) into <dir>. Nothing is published or
// uploaded. Run it with the repo's Node 22 toolchain (the Electron zip extraction used by
// electron-packager fails silently on Node 26); the Go toolchain must be on PATH.
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function arg(name) {
	const i = process.argv.indexOf(`--${name}`);
	return i >= 0 ? process.argv[i + 1] : undefined;
}

const id = arg("id");
const origin = arg("origin");
const out = arg("out");
const versions = (arg("versions") ?? "").split(",").map((v) => v.trim()).filter(Boolean);
if (!id || !origin || !out || versions.length === 0) {
	process.stderr.write("usage: build-test-installers.mjs --id <id> --origin <https origin> --versions v1,v2 --out <dir>\n");
	process.exit(2);
}
for (const v of versions) {
	if (!/^\d+\.\d+\.\d+$/.test(v)) throw new Error(`version must be plain semver, got ${v}`);
}

const pkgPath = join(frontend, "package.json");
const original = readFileSync(pkgPath);
mkdirSync(out, { recursive: true });

const forgeCli = join(frontend, "node_modules", "@electron-forge", "cli", "dist", "electron-forge.js");
const env = { ...process.env, AO_TEST_IDENTITY: id, AO_UPDATE_ORIGIN: origin };
delete env.AO_RELEASE_REPO;
delete env.AO_PUBLISHED_BUILD;

try {
	for (const version of versions) {
		const pkg = JSON.parse(original.toString("utf8"));
		pkg.version = version;
		writeFileSync(pkgPath, `${JSON.stringify(pkg, null, "\t")}\n`);
		rmSync(join(frontend, "out", "make"), { recursive: true, force: true });
		process.stdout.write(`== building ${version}\n`);
		const r = spawnSync(process.execPath, [forgeCli, "make", "--platform", "win32", "--arch", "x64"], { cwd: frontend, env, stdio: "inherit" });
		if (r.status !== 0) throw new Error(`forge make failed for ${version} (exit ${r.status})`);
		const made = readdirSync(join(frontend, "out", "make")).filter((f) => f.includes(`Setup ${version}`));
		if (!made.some((f) => f.endsWith(".exe"))) throw new Error(`no installer for ${version} in out/make: ${readdirSync(join(frontend, "out", "make")).join(", ")}`);
		for (const f of made) copyFileSync(join(frontend, "out", "make", f), join(out, f));
		process.stdout.write(`== collected: ${made.join(", ")}\n`);
	}
} finally {
	writeFileSync(pkgPath, original);
	process.stdout.write("== package.json restored\n");
}
