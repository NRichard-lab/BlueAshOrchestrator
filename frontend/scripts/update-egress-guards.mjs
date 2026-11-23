// Safety guards for scripts/update-egress-harness.mjs.
//
// Why this exists: an earlier harness run used AO_RUN_FILE=<tmp>/home/.ao/running.json.
// Both the Go daemon and the desktop app derive their Windows named pipes
// (ao-supervise, ao-browser, ao-browser-stream) from the BASENAME of the run
// file's directory only, so ".ao" collided with the user's real installed
// Orchestrator: the test app attached to the host daemon's supervisor pipe and,
// when the harness killed it, the host daemon saw "last client gone" and shut
// down. These guards make that class of collision impossible to start, watch
// the host while the test runs, and restrict process cleanup to verified
// descendants of the test app.
import { randomBytes } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { join, resolve, sep, basename, dirname } from "node:path";

// Run-file directory basenames that map onto the default/installed pipe names.
export const FORBIDDEN_RUN_DIR_NAMES = new Set([".ao", ".", "", "data", "dev"]);

// Mirrors backend/internal/daemon/supervisor/listen_windows.go,
// backend/internal/browserruntime/listen_windows.go and
// frontend/src/main/daemon-run-file.ts: suffix from the run file's directory name.
export function pipeSuffixFromRunFile(runFile) {
	const dir = basename(dirname(runFile));
	if (FORBIDDEN_RUN_DIR_NAMES.has(dir)) return "";
	return `-${dir.replace(/[^a-zA-Z0-9-]/g, "-")}`;
}

export function derivedPipes(runFile) {
	const suffix = pipeSuffixFromRunFile(runFile);
	// browserstream: always "-<sanitized dir>"; the default dir ".ao" becomes "-ao".
	const streamName = `ao-browser-stream-${basename(dirname(runFile)).replace(/[^a-zA-Z0-9-]/g, "-")}`;
	return [`ao-supervise${suffix}`, `ao-browser${suffix}`, `ao-browser-chrome${suffix}`, streamName];
}

/** A fresh isolation plan for one harness run: unique run dir, data dir, port. */
export function planIsolation(workDir, port) {
	const id = `aotest-${randomBytes(4).toString("hex")}`;
	const stateDir = join(workDir, id);
	return { id, stateDir, runFile: join(stateDir, "running.json"), dataDir: join(stateDir, "data"), port };
}

/**
 * Refuse a plan that could touch the host instance. `host` describes it:
 * { port, runFile, dataDirs: string[], installDir }. `existingPipes` is the
 * current list of pipe names (read-only enumeration of \\.\pipe\).
 * Returns the list of violations (empty = safe).
 */
export function isolationViolations(plan, host, existingPipes = []) {
	const v = [];
	const lower = (p) => resolve(p).toLowerCase();
	const dir = basename(dirname(plan.runFile));
	if (FORBIDDEN_RUN_DIR_NAMES.has(dir)) v.push(`run-file directory name "${dir}" maps to the default pipe names`);
	if (plan.port === host.port) v.push(`test port ${plan.port} equals the host daemon port`);
	if (!Number.isInteger(plan.port) || plan.port < 20000 || plan.port > 65000) v.push(`test port ${plan.port} is outside 20000-65000`);
	if (plan.port === 3011) v.push("test port 3011 is the LAN listener port");
	if (host.runFile && lower(plan.runFile) === lower(host.runFile)) v.push("test run file is the host run file");
	for (const protectedDir of [...(host.dataDirs ?? []), host.installDir].filter(Boolean)) {
		const p = lower(protectedDir) + sep;
		if (lower(plan.runFile).startsWith(p) || lower(plan.dataDir).startsWith(p)) v.push(`test state lies inside protected host path ${protectedDir}`);
	}
	const existing = new Set(existingPipes.map((n) => n.toLowerCase()));
	for (const pipe of derivedPipes(plan.runFile)) {
		if (existing.has(pipe.toLowerCase())) v.push(`pipe ${pipe} already exists`);
	}
	return v;
}

export function listPipes() {
	try { return readdirSync("\\\\.\\pipe\\"); } catch { return []; }
}

/** Read-only identity of the host daemon: run-file pid + healthz status. */
export async function hostIdentity(hostRunFile, port, fetchImpl = fetch) {
	let pid = null;
	try { pid = JSON.parse(readFileSync(hostRunFile, "utf8")).pid ?? null; } catch { /* absent */ }
	let status = null;
	let error;
	// A fresh connection per attempt (keep-alive sockets to a daemon that closed them
	// idle would otherwise report a spurious reset), and one retry.
	for (let attempt = 0; attempt < 2 && status !== 200; attempt++) {
		try {
			const res = await fetchImpl(`http://127.0.0.1:${port}/healthz`, { signal: AbortSignal.timeout(3000), headers: { connection: "close" } });
			status = res.status;
		} catch (e) { status = null; error = e?.cause?.code ?? e?.message; }
	}
	return error && status !== 200 ? { pid, status, error } : { pid, status };
}

/**
 * Poll the host and call onChange(reason) the moment its daemon pid changes or
 * healthz stops answering 200. Host probes run in this process, outside the
 * test app's environment. Returns { stop, samples }.
 */
export function watchHost({ hostRunFile, port, intervalMs = 2000, onChange, identity = hostIdentity }) {
	const samples = [];
	let baseline = null;
	let stopped = false;
	let tripped = false;
	let consecutiveBad = 0;
	const tick = async () => {
		if (stopped) return;
		const now = await identity(hostRunFile, port);
		samples.push({ t: new Date().toISOString(), ...now });
		baseline ??= now;
		const unhealthy = now.status !== 200;
		consecutiveBad = unhealthy ? consecutiveBad + 1 : 0;
		if (!tripped && ((unhealthy && consecutiveBad >= 2) || now.pid !== baseline.pid)) {
			tripped = true;
			onChange?.(`host daemon changed: baseline ${JSON.stringify(baseline)} now ${JSON.stringify(now)}`);
		}
	};
	const timer = setInterval(() => { void tick(); }, intervalMs);
	void tick();
	return { samples, stop: () => { stopped = true; clearInterval(timer); } };
}

/**
 * Decide which processes may be terminated. `table` is [{ pid, ppid, path }].
 * Only descendants of `rootPid` whose executable lives under `testAppDir` are
 * returned in `kill`; anything else in the tree is returned in `refuse` and is
 * never touched. A pid in `hostPids`, or a path under `installDir`, is refused
 * even if it appears in the tree.
 */
export function verifiedKillList({ table, rootPid, testAppDir, installDir, hostPids = [] }) {
	const under = (p, d) => Boolean(p) && Boolean(d) && resolve(p).toLowerCase().startsWith(resolve(d).toLowerCase() + sep);
	const byPid = new Map(table.map((p) => [p.pid, p]));
	const tree = new Set([rootPid]);
	let grew = true;
	while (grew) {
		grew = false;
		for (const p of table) if (tree.has(p.ppid) && !tree.has(p.pid)) { tree.add(p.pid); grew = true; }
	}
	const kill = [];
	const refuse = [];
	for (const pid of tree) {
		const p = byPid.get(pid);
		if (!p) continue;
		// The test build may live INSIDE the host install folder (a worktree under it), so the
		// install dir cannot be a deny rule; host pids and host-only paths are.
		const hostPath = under(p.path, installDir) && !under(p.path, testAppDir);
		const safe = under(p.path, testAppDir) && !hostPath && !hostPids.includes(pid);
		(safe ? kill : refuse).push(p);
	}
	return { kill, refuse };
}
