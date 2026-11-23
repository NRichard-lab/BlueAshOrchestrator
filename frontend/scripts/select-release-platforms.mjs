// Release platform selection for .github/workflows/build-artifacts.yml.
//
// The FIRST BlueAsh release ships Windows x64 only: macOS, Linux and the other
// desktop targets are deferred (their testing is not done), but their build
// definitions stay in the catalog below and are selectable at dispatch time with
// `platforms: all` or an explicit list. Nothing here removes platform support.
//
//   node scripts/select-release-platforms.mjs "<input>"      # prints {"include":[...]}
//   input: empty (default) -> win32-x64 | "all" | "win32-x64,linux-x64"
import { appendFileSync } from "node:fs";

// `artifact` is the pinned upload-artifact name the downstream conductor reads;
// do not rename without updating the conductor.
export const PLATFORM_CATALOG = [
	{ os: "macos-latest", platform: "darwin-arm64", artifact: "mac-arm64" },
	{ os: "macos-15-intel", platform: "darwin-x64", artifact: "mac-x64" },
	{ os: "windows-latest", platform: "win32-x64", artifact: "win" },
	// Linux tmux links glibc dynamically (#4616): build on the oldest supported baseline.
	{ os: "ubuntu-22.04", platform: "linux-x64", artifact: "linux" },
];

export const DEFAULT_RELEASE_PLATFORMS = ["win32-x64"];

/** Returns the catalog entries selected by `input` (throws on an unknown platform). */
export function selectPlatforms(input) {
	const raw = String(input ?? "").trim();
	if (raw === "") return PLATFORM_CATALOG.filter((p) => DEFAULT_RELEASE_PLATFORMS.includes(p.platform));
	if (raw.toLowerCase() === "all") return [...PLATFORM_CATALOG];
	const wanted = raw.split(",").map((s) => s.trim()).filter(Boolean);
	const known = new Set(PLATFORM_CATALOG.map((p) => p.platform));
	const unknown = wanted.filter((w) => !known.has(w));
	if (unknown.length > 0) {
		throw new Error(`unknown platform(s): ${unknown.join(", ")} (known: ${[...known].join(", ")}, or "all")`);
	}
	return PLATFORM_CATALOG.filter((p) => wanted.includes(p.platform));
}

if (process.argv[1]?.endsWith("select-release-platforms.mjs")) {
	try {
		const selected = selectPlatforms(process.argv[2]);
		const matrix = JSON.stringify({ include: selected });
		process.stdout.write(`${matrix}\n`);
		if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `matrix=${matrix}\n`);
	} catch (err) {
		process.stderr.write(`${err.message}\n`);
		process.exit(2);
	}
}
