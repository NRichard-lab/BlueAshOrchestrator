import { readFileSync } from "node:fs";
import path from "node:path";
import { ENV } from "../shared/brand";

// Test-build-only instance pin.
//
// A real NSIS update relaunches the app through the shell (StdUtils.ExecShellAsUser), which does
// NOT inherit the caller's environment (verified with a probe installer). A test build that must
// not touch an installed Orchestrator therefore cannot rely on environment variables for its
// isolation: after an update it would come back with the user's defaults (the real ~/.ao run file,
// port 3001 and the shared pipe names).
//
// So test builds carry their identity COMPILE-TIME (vite.main.config.ts defines
// process.env.AO_TEST_BUILD_IDENTITY from AO_TEST_IDENTITY) and the test installer writes
// `ao-instance.json` beside the executable. This module only acts when the compiled-in identity is
// non-empty, so in every release artifact the constant is "" and the file is never read; a stray
// ao-instance.json next to a release build is ignored. The pin must name the same identity as the
// build, or the app refuses to start. Production daemon startup and legitimate install/auth state
// are untouched.

export const INSTANCE_PIN_FILE = "ao-instance.json";

export type InstancePin = {
	identity: string;
	runFile: string;
	port: number;
	telemetryRemote: "off";
};

const RESERVED_RUN_DIR_NAMES = new Set([".ao", ".blueashai", ".", "", "data", "dev"]);

export type PinParse = { ok: true; pin: InstancePin } | { ok: false; reason: string };

export function parseInstancePin(raw: string, installDir: string, buildIdentity: string): PinParse {
	let json: Record<string, unknown>;
	try {
		json = JSON.parse(raw) as Record<string, unknown>;
	} catch {
		return { ok: false, reason: "not valid JSON" };
	}
	if (json.version !== 1) return { ok: false, reason: "unsupported version" };
	if (json.identity !== buildIdentity) return { ok: false, reason: "identity does not match this build" };
	if (json.skipProtocolRegistration !== true) return { ok: false, reason: "skipProtocolRegistration must be true" };
	if (json.telemetryRemote !== "off") return { ok: false, reason: 'telemetryRemote must be "off"' };
	if (typeof json.runFile !== "string" || json.runFile.trim() === "") return { ok: false, reason: "runFile is required" };
	const runFile = path.isAbsolute(json.runFile) ? path.normalize(json.runFile) : path.resolve(installDir, json.runFile);
	const dirName = path.basename(path.dirname(runFile));
	if (RESERVED_RUN_DIR_NAMES.has(dirName)) {
		return { ok: false, reason: `run-file directory "${dirName}" maps to the shared default pipe names` };
	}
	const port = json.port;
	if (typeof port !== "number" || !Number.isInteger(port) || port < 20000 || port > 65000 || port === 3001 || port === 3011) {
		return { ok: false, reason: "port must be an integer in 20000-65000 other than 3001 and 3011" };
	}
	return { ok: true, pin: { identity: buildIdentity, runFile, port, telemetryRemote: "off" } };
}

/**
 * Applies the pin to `env` and returns an error string (the caller must then refuse to start), or
 * null. `buildIdentity` is the compile-time constant: when empty (every release build) this is a
 * no-op that does not even read the file.
 */
export function applyTestInstancePin(input: {
	buildIdentity: string;
	execDir: string | null;
	env: NodeJS.ProcessEnv;
	readText?: (file: string) => string;
}): string | null {
	if (!input.buildIdentity) return null;
	if (!input.execDir) return "a test build must run from an installed location";
	const file = path.join(input.execDir, INSTANCE_PIN_FILE);
	let raw: string;
	try {
		raw = (input.readText ?? ((f: string) => readFileSync(f, "utf8")))(file);
	} catch {
		return `${INSTANCE_PIN_FILE} is missing next to a test build (${input.buildIdentity})`;
	}
	const parsed = parseInstancePin(raw, input.execDir, input.buildIdentity);
	if (!parsed.ok) return `${INSTANCE_PIN_FILE} rejected: ${parsed.reason}`;
	// The pin wins over any inherited variable: a test build is never allowed to use the defaults.
	input.env[ENV.RUN_FILE] = parsed.pin.runFile;
	input.env[ENV.PORT] = String(parsed.pin.port);
	input.env.AO_SKIP_PROTOCOL_REGISTRATION = "1";
	input.env.AO_TELEMETRY_REMOTE = parsed.pin.telemetryRemote;
	return null;
}
