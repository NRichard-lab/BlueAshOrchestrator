// @vitest-environment node
import path from "node:path";
import { describe, expect, it } from "vitest";
import { daemonHandshakeRunFile, supervisorPipeFromRunFile } from "./daemon-run-file";

// The same table is asserted by the Go side
// (backend/internal/daemon/supervisor/listen_windows_test.go).
export const PIPE_CASES: Array<[string | null, string]> = [
	[null, "\\\\.\\pipe\\ao-supervise"],
	["C:\\Users\\a\\.ao\\running.json", "\\\\.\\pipe\\ao-supervise"],
	["C:\\Users\\a\\.ao\\dev\\running.json", "\\\\.\\pipe\\ao-supervise-dev"],
	["D:\\Apps\\Orchestrator\\data\\running.json", "\\\\.\\pipe\\ao-supervise-data"],
	["D:\\x\\my dir_1\\running.json", "\\\\.\\pipe\\ao-supervise-my-dir-1"],
];

describe("supervisorPipeFromRunFile", () => {
	// path.basename is platform specific; the table is Windows paths.
	it.skipIf(process.platform !== "win32").each(PIPE_CASES)("%s -> %s", (runFile, pipe) => {
		expect(supervisorPipeFromRunFile(runFile)).toBe(pipe);
	});
});

describe("daemonHandshakeRunFile", () => {
	// An install-tied profile keeps desktop STATE beside the install, but the daemon (and the
	// global `ao` CLI) still use ~/.ao/running.json.
	const stateRunFile = "D:/Apps/Orchestrator/data/running.json";
	const defaultRunFile = "C:/Users/u/.ao/running.json";
	const base = { envRunFile: undefined, isDev: false, stateRunFile, defaultRunFile };

	it("follows the daemon's real location for install-tied profiles (marker or env source)", () => {
		expect(daemonHandshakeRunFile({ ...base, installSource: "marker" })).toBe(defaultRunFile);
		expect(daemonHandshakeRunFile({ ...base, installSource: "env" })).toBe(defaultRunFile);
	});

	it("is unchanged for ordinary default profiles and dev builds", () => {
		const def = { ...base, stateRunFile: defaultRunFile };
		expect(daemonHandshakeRunFile({ ...def, installSource: "default" })).toBe(defaultRunFile);
		const dev = "C:/Users/u/.ao/dev/running.json";
		expect(daemonHandshakeRunFile({ ...base, stateRunFile: dev, isDev: true, installSource: "marker" })).toBe(dev);
	});

	it("honours an explicit AO_RUN_FILE in every case", () => {
		for (const installSource of ["default", "marker", "env"]) {
			expect(daemonHandshakeRunFile({ ...base, installSource, envRunFile: "X:/custom/running.json" })).toBe("X:/custom/running.json");
		}
	});

	it("lands on the directory whose name maps to the daemon's default supervisor pipe", () => {
		// ".ao" is the directory name both sides map to "ao-supervise" (see PIPE_CASES above).
		const handshake = daemonHandshakeRunFile({ ...base, installSource: "marker" });
		expect(path.posix.basename(path.posix.dirname(handshake ?? ""))).toBe(".ao");
	});
});
