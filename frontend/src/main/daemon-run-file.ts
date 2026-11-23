import path from "node:path";

// The desktop app and the Go daemon must agree on ONE run file (running.json):
// the app reads it to adopt/restart the daemon and derives the supervisor
// named-pipe from its directory; the daemon writes it and listens on the pipe
// derived from the same directory (backend/internal/daemon/supervisor/
// listen_windows.go pipeNameFromRunFile). When they disagree the app's liveness
// link retries a pipe that never exists, the daemon never arms its watchdog,
// and a daemon exit finds no run file to restart from.

/**
 * Windows supervisor pipe for a run file. Must stay byte-identical to the Go
 * `pipeNameFromRunFile`; both are pinned by the same table in their tests.
 */
export function supervisorPipeFromRunFile(rfp: string | null): string {
	if (!rfp) return "\\\\.\\pipe\\ao-supervise";
	const dir = path.basename(path.dirname(rfp));
	if (dir === ".ao" || dir === "." || dir === "") return "\\\\.\\pipe\\ao-supervise";
	return "\\\\.\\pipe\\ao-supervise-" + dir.replace(/[^a-zA-Z0-9-]/g, "-");
}

/**
 * Where the DAEMON's handshake file (running.json) really is, as opposed to where
 * the desktop keeps its own state. The daemon, the `ao` CLI and the session
 * environment all use an explicit AO_RUN_FILE, else ~/.ao/running.json; the daemon
 * is not told about an install-tied profile's data folder. The desktop's state
 * directory (update settings, logs, markers) legitimately follows the install
 * folder, but adopting, restarting and supervising the daemon must look where the
 * daemon writes, or the liveness link retries a pipe that never exists and a daemon
 * exit finds no run file to restart from. Moving the daemon's run file instead
 * would hide it from the global CLI, so the app follows the daemon.
 */
export function daemonHandshakeRunFile(input: {
	envRunFile: string | undefined;
	isDev: boolean;
	/** The desktop's own run-file path (state dir anchor): dev path, or install-tied path. */
	stateRunFile: string | null;
	/** The path every other AO component defaults to (~/.ao/running.json). */
	defaultRunFile: string | null;
	installSource: "default" | string;
}): string | null {
	if (input.envRunFile) return input.envRunFile;
	if (input.isDev || input.installSource === "default") return input.stateRunFile;
	return input.defaultRunFile;
}
