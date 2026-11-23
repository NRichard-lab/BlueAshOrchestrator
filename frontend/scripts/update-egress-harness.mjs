// Local egress harness for a REAL packaged build (plan checkpoint C10).
//
//   node scripts/update-egress-harness.mjs --app <path to agent-orchestrator exe> \
//        --scenario stable|nightly|feature|outage|control --out <report dir> [--wait-ms 25000]
//
// Everything stays on this machine:
//   * a local HTTPS feed serves the BlueAsh update tree for the test host
//     `updates.blueash-test.example` with a leaf cert from a throwaway test CA;
//   * a deny/record forward proxy is the ONLY route the app may use. It tunnels
//     CONNECT to the test host (port 443) into the local feed and answers 403 to
//     everything else WITHOUT ever contacting it, recording each attempt;
//   * the app runs with an isolated USERPROFILE (so ~/.ao is a temp dir), the
//     proxy configured for Chromium (--proxy-server) and Node (HTTPS_PROXY +
//     NODE_USE_ENV_PROXY), the test CA for Node (NODE_EXTRA_CA_CERTS) and a pin
//     for the test leaf's SPKI for Chromium (--ignore-certificate-errors-spki-list).
//     The pin is a launch flag of this harness; product code has no TLS bypass.
//   * the app's process tree is sampled with netstat so a connection that skips
//     the proxy shows up as direct egress.
// The `control` scenario proves the instrumentation: it deliberately requests
// upstream hosts from a Node client and from Chromium (a page navigation) and
// the proxy must record them as DENIED.
//
// Windows only for the process sampler (uses netstat/PowerShell); the proxy,
// feed and report logic are portable.
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import http from "node:http";
import https from "node:https";
import net from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, sep } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { derivedPipes, hostIdentity, isolationViolations, listPipes, planIsolation, verifiedKillList, watchHost } from "./update-egress-guards.mjs";

export const TEST_HOST = "updates.blueash-test.example";
export const UPSTREAM = /(^|\.)(github\.com|githubusercontent\.com|githubassets\.com|github\.io|orchestrator\.inc|orchestratorusercontent\.com|expo\.dev|aoagents\.dev)$/i;
const NEWER = "99.0.0";

const step = (msg) => process.stderr.write(`[harness ${new Date().toISOString()}] ${msg}
`);

function arg(name, fallback) {
	const i = process.argv.indexOf(`--${name}`);
	return i >= 0 ? process.argv[i + 1] : fallback;
}

function openssl(args, cwd) {
	const r = spawnSync("openssl", args, { cwd, encoding: "utf8" });
	if (r.status !== 0) throw new Error(`openssl ${args.join(" ")} failed: ${r.stderr}`);
	return r.stdout;
}

export function makeTestPki(dir) {
	openssl(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.pem", "-days", "2", "-subj", "/CN=AO local test CA (throwaway)"], dir);
	openssl(["req", "-newkey", "rsa:2048", "-nodes", "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", `/CN=${TEST_HOST}`], dir);
	writeFileSync(join(dir, "ext.cnf"), `subjectAltName=DNS:${TEST_HOST}\nbasicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth\n`);
	openssl(["x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial", "-out", "leaf.pem", "-days", "2", "-extfile", "ext.cnf"], dir);
	const pub = spawnSync("openssl", ["x509", "-in", "leaf.pem", "-pubkey", "-noout"], { cwd: dir, encoding: "utf8" }).stdout;
	const der = spawnSync("openssl", ["pkey", "-pubin", "-outform", "der"], { cwd: dir, input: pub });
	const spki = createHash("sha256").update(der.stdout).digest("base64");
	return { ca: join(dir, "ca.pem"), key: join(dir, "leaf.key"), cert: join(dir, "leaf.pem"), spki };
}

function yml(version, name, extra = "") {
	return `version: ${version}\nfiles:\n  - url: ${name}\n    sha512: ${Buffer.alloc(64, 1).toString("base64")}\n    size: 1024\npath: ${name}\nsha512: ${Buffer.alloc(64, 1).toString("base64")}\nreleaseDate: '2026-11-23T00:00:00.000Z'\n${extra}`;
}

export function startFeed(pki, scenario, log) {
	const server = https.createServer({ key: readFileSync(pki.key), cert: readFileSync(pki.cert) }, (req, res) => {
		const entry = { t: Date.now(), method: req.method, url: req.url, host: req.headers.host, ua: req.headers["user-agent"] };
		log.push(entry);
		if (scenario === "outage") {
			entry.status = 503;
			res.writeHead(503).end("unavailable");
			return;
		}
		const url = req.url.split("?")[0];
		const json = (v) => res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(v));
		const text = (v) => res.writeHead(200, { "content-type": "text/yaml" }).end(v);
		entry.status = 200;
		if (url === "/policy/desktop-version-floor.json") return json({ min: "", latest: scenario === "stable" ? NEWER : "", downloadUrl: "" });
		if (url === "/desktop/meta/healthz") return text("ok\n");
		if (url === "/desktop/feature/index.json") {
			return json({ builds: [{ pr: 7, title: "Feature build pr7", base: "main", sha: "abc1234", slug: "pr7", buildId: `${NEWER}-pr7.202611230000`, publishedAt: new Date().toISOString() }] });
		}
		let m;
		if ((m = /^\/desktop\/stable\/latest(-mac|-linux)?\.yml$/.exec(url))) return text(yml(NEWER, `agent-orchestrator-${NEWER}-setup.exe`));
		if ((m = /^\/desktop\/nightly\/nightly(-mac|-linux)?\.yml$/.exec(url))) return text(yml(`${NEWER}-nightly.202611230000`, `agent-orchestrator-nightly-setup.exe`));
		if ((m = /^\/desktop\/feature\/pr7\/pr7(-mac|-linux)?\.yml$/.exec(url))) return text(yml(`${NEWER}-pr7.202611230000`, `agent-orchestrator-pr7-setup.exe`));
		entry.status = 404;
		res.writeHead(404).end("not found");
	});
	server.on("tlsClientError", () => undefined);
	server.on("clientError", (_err, socket) => socket.destroy());
	return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ server, port: server.address().port })));
}

export function startProxy(feedPort, log, daemonPort) {
	const isTestDaemon = (host, port) => daemonPort !== undefined && (host === "127.0.0.1" || String(host).endsWith(".localhost")) && String(port) === String(daemonPort);
	const server = http.createServer((req, res) => {
		let u;
		try { u = new URL(req.url); } catch { u = null; }
		if (u && isTestDaemon(u.hostname, u.port)) {
			// The app's own renderer-to-daemon traffic: forwarded to the TEST daemon only.
			log.push({ t: Date.now(), kind: "http", target: req.url, allowed: true, loopback: true });
			const up = http.request({ host: "127.0.0.1", port: u.port, path: u.pathname + u.search, method: req.method, headers: req.headers }, (r) => {
				if (res.headersSent) return;
				res.writeHead(r.statusCode ?? 502, r.headers);
				r.pipe(res);
			});
			up.on("error", () => { if (res.headersSent) res.destroy(); else res.writeHead(502).end(); });
			req.pipe(up);
			return;
		}
		log.push({ t: Date.now(), kind: "http", target: req.url, allowed: false });
		res.writeHead(403).end("denied by egress harness");
	});
	server.on("clientError", (_err, socket) => socket.destroy());
	server.on("connect", (req, socket) => {
		socket.on("error", () => undefined); // client-side resets are expected and non-fatal
		const [host, port] = req.url.split(":");
		const toFeed = host.toLowerCase() === TEST_HOST && port === "443";
		const toDaemon = isTestDaemon(host, port);
		const allowed = toFeed || toDaemon;
		const entry = { t: Date.now(), kind: "connect", target: req.url, allowed, ...(toDaemon ? { loopback: true } : {}) };
		log.push(entry);
		if (!allowed) {
			socket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
			return;
		}
		const upstream = net.connect(toFeed ? feedPort : Number(port), "127.0.0.1", () => {
			entry.established = true;
			socket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
			upstream.pipe(socket);
			socket.pipe(upstream);
		});
		upstream.on("error", () => {
			entry.tunnelError = true;
			// Answer like a real proxy would for an unreachable origin instead of dropping the
			// socket, which clients treat as a transient reset and retry immediately.
			if (!socket.destroyed && !entry.established) socket.end("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n");
			else socket.destroy();
		});
		socket.on("error", () => upstream.destroy());
	});
	return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ server, port: server.address().port })));
}

function exec(cmd, args) {
	return new Promise((resolve) => {
		const c = spawn(cmd, args, { stdio: ["ignore", "pipe", "ignore"] });
		let out = "";
		c.stdout.on("data", (d) => { out += d; });
		c.on("error", () => resolve(""));
		c.on("close", () => resolve(out));
	});
}

async function psTree(rootPid) {
	const raw = await exec("powershell", ["-NoProfile", "-Command", "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name | ConvertTo-Json -Compress"]);
	let all = [];
	try { all = JSON.parse(raw); } catch { return new Map(); }
	const kids = new Map([[rootPid, "root"]]);
	let grew = true;
	while (grew) {
		grew = false;
		for (const p of all) if (kids.has(p.ParentProcessId) && !kids.has(p.ProcessId)) { kids.set(p.ProcessId, p.Name); grew = true; }
	}
	return kids;
}

async function sampleConnections(rootPid, seen) {
	const [names, out] = await Promise.all([psTree(rootPid), exec("netstat", ["-ano", "-p", "TCP"])]);
	for (const line of out.split(String.fromCharCode(10))) {
		const m = /^\s*TCP\s+(\S+)\s+(\S+)\s+(\S+)\s+(\d+)\s*$/.exec(line);
		if (!m) continue;
		const pid = Number(m[4]);
		if (!names.has(pid)) continue;
		const remote = m[2];
		if (/^(127\.0\.0\.1|\[::1\]|0\.0\.0\.0|\[::\]):/.test(remote)) continue;
		seen.set(`${names.get(pid)}(${pid}) -> ${remote} ${m[3]}`, true);
	}
}

async function cdp(port, evaluate) {
	const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
	for (const t of targets.filter((x) => x.type === "page" && x.webSocketDebuggerUrl)) {
		const ws = new WebSocket(t.webSocketDebuggerUrl);
		await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
		let id = 0;
		const call = (method, params) => new Promise((resolve) => {
			const timer = setTimeout(() => resolve({ timeout: true }), 15000);
			const mine = ++id;
			const onMessage = (ev) => { const d = JSON.parse(ev.data); if (d.id === mine) { ws.removeEventListener("message", onMessage); clearTimeout(timer); resolve(d); } };
			ws.addEventListener("message", onMessage);
			ws.send(JSON.stringify({ id: mine, method, params }));
		});
		const probe = await call("Runtime.evaluate", { expression: "typeof window.ao?.updates?.check", returnByValue: true });
		if (probe.result?.result?.value === "function") return { call, close: () => ws.close() };
		ws.close();
	}
	return undefined;
}

async function evalIn(page, expression) {
	const r = await page.call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
	return r.result?.exceptionDetails ? { error: r.result.exceptionDetails.text } : r.result?.result?.value;
}

// The ao-app: URL handler lives in HKCU (per OS user, not per data directory), so
// a profile override does not isolate it. The harness therefore (1) launches the
// app with AO_SKIP_PROTOCOL_REGISTRATION=1 so the test instance never claims the
// scheme, and (2) only READS the handler before and after the run to prove it was
// untouched. It never writes or deletes registry values.
const PROTOCOL_KEY = "Registry::HKEY_CURRENT_USER\\Software\\Classes\\ao-app";
async function protocolCommand() {
	const raw = await exec("powershell", ["-NoProfile", "-Command", `(Get-ItemProperty '${PROTOCOL_KEY}${"\\"}shell${"\\"}open${"\\"}command' -ErrorAction SilentlyContinue).'(default)'`]);
	return raw.trim() || null;
}

async function processTable() {
	const raw = await exec("powershell", ["-NoProfile", "-Command", "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,ExecutablePath | ConvertTo-Json -Compress"]);
	try { return JSON.parse(raw).map((p) => ({ pid: p.ProcessId, ppid: p.ParentProcessId, path: p.ExecutablePath ?? "" })); } catch { return []; }
}

async function freePort() {
	// Windows hands out ephemeral ports sequentially (the counter can sit above the
	// accepted range), so pick a random candidate in range and test-bind it.
	for (let attempt = 0; attempt < 200; attempt++) {
		const port = 30000 + Math.floor(Math.random() * 30000);
		const ok = await new Promise((resolve) => {
			const srv = net.createServer();
			srv.once("error", () => resolve(false));
			srv.listen(port, "127.0.0.1", () => srv.close(() => resolve(true)));
		});
		if (ok) return port;
	}
	throw new Error("could not find a free test port in 30000-60000");
}

/** Identify the host (real, installed) Orchestrator read-only: run file, port, install dir. */
async function discoverHost(realHome) {
	const runFile = process.env.AO_RUN_FILE || join(realHome, ".ao", "running.json");
	let info = {};
	try { info = JSON.parse(readFileSync(runFile, "utf8")); } catch { /* none running */ }
	let installDir;
	if (info.pid) {
		const me = (await processTable()).find((p) => p.pid === info.pid);
		// the daemon lives at <install>/resources/daemon/ao.exe; three levels up is the install dir
		if (me?.path) installDir = join(me.path, "..", "..", "..");
	}
	const dataDirs = [join(realHome, ".ao")];
	if (installDir) dataDirs.push(join(installDir, "data"));
	return { runFile, port: info.port ?? 3001, pid: info.pid ?? null, installDir: installDir && join(installDir), dataDirs };
}

export async function run({ app, scenario, out, waitMs }) {
	mkdirSync(out, { recursive: true });
	const work = mkdtempSync(join(tmpdir(), "ao-egress-"));
	const pki = makeTestPki(work);
	const feedLog = [];
	const proxyLog = [];
	const plan = planIsolation(work, await freePort());
	const feed = await startFeed(pki, scenario, feedLog);
	const proxy = await startProxy(feed.port, proxyLog, plan.port);
	if (scenario === "outage") {
		// A hard outage: the feed process is gone, so the proxy tunnel fails.
		await new Promise((r) => feed.server.close(r));
		feed.server.closeAllConnections?.();
	}
	const home = join(work, "home");
	const realHome = process.env.USERPROFILE;
	const host = await discoverHost(realHome);
	const violations = isolationViolations(plan, host, listPipes());
	if (violations.length > 0) throw new Error("refusing to start, isolation not guaranteed: " + violations.join("; "));
	const hostBefore = await hostIdentity(host.runFile, host.port);
	if (host.pid && hostBefore.status !== 200) throw new Error(`host daemon is not healthy before the test (${JSON.stringify(hostBefore)}); not starting`);
	step(`isolation ${plan.id} port ${plan.port}; host daemon pid ${host.pid} port ${host.port} healthz ${hostBefore.status}`);
	if (process.argv.includes("--preflight-only")) {
		proxy.server.close();
		feed.server.close();
		const pre = { preflight: "ok", plan: { id: plan.id, runFile: plan.runFile, port: plan.port }, derivedPipes: derivedPipes(plan.runFile), host: { pid: host.pid, port: host.port, installDir: host.installDir, healthz: hostBefore.status } };
		writeFileSync(join(out, `${scenario}.preflight.json`), JSON.stringify(pre, null, 2));
		return pre;
	}
	// Windows resolves known folders through %USERPROFILE%; they must exist or Electron cannot start.
	for (const d of ["Downloads", "Documents", "Desktop", "Pictures", "Music", "Videos", "AppData/Roaming", "AppData/Local"]) mkdirSync(join(home, d), { recursive: true });
	mkdirSync(plan.dataDir, { recursive: true });
	const settings = scenario === "nightly" ? { enabled: false, channel: "nightly", nightlyAck: true, feature: null }
		: scenario === "feature" ? { enabled: false, channel: "latest", nightlyAck: false, feature: { pr: 7 } }
		: { enabled: false, channel: "latest", nightlyAck: false, feature: null };
	writeFileSync(join(plan.stateDir, "update-settings.json"), JSON.stringify(settings));
	const proxyUrl = `http://127.0.0.1:${proxy.port}`;
	const cdpPort = 9300 + Math.floor(Math.random() * 500);
	const env = {
		...process.env,
		USERPROFILE: home, HOME: home, APPDATA: join(home, "AppData", "Roaming"), LOCALAPPDATA: join(home, "AppData", "Local"),
		AO_DATA_DIR: plan.dataDir, AO_RUN_FILE: plan.runFile, AO_PORT: String(plan.port),
		HTTPS_PROXY: proxyUrl, HTTP_PROXY: proxyUrl, https_proxy: proxyUrl, http_proxy: proxyUrl, NO_PROXY: "", no_proxy: "",
		NODE_USE_ENV_PROXY: "1", NODE_EXTRA_CA_CERTS: pki.ca, AO_SKIP_PROTOCOL_REGISTRATION: "1",
	};
	step(`feed :${feed.port} proxy :${proxy.port} cdp :${cdpPort}`);
	const protocolBefore = await protocolCommand();
	const hostTable = await processTable();
	const hostPids = hostTable.filter((p) => host.installDir && p.path.toLowerCase().startsWith(host.installDir.toLowerCase() + sep)).map((p) => p.pid);
	let aborted;
	let child;
	const watcher = watchHost({
		hostRunFile: host.runFile, port: host.port,
		onChange: (reason) => { aborted = reason; step(`ABORT: ${reason}`); void cleanup(); },
	});
	const emergency = async (why) => { step(`emergency stop: ${why}`); await cleanup(); watcher.stop(); process.exit(1); };
	process.on("uncaughtException", (e) => { void emergency(String(e?.stack ?? e)); });
	process.on("unhandledRejection", (e) => { void emergency(String(e)); });
	process.on("SIGINT", () => { void emergency("SIGINT"); });
	const cleanup = async () => {
		if (!child?.pid) return;
		const table = await processTable();
		const { kill, refuse } = verifiedKillList({ table, rootPid: child.pid, testAppDir: dirname(app), installDir: host.installDir, hostPids });
		// Daemon last: killing it first leaves the renderer retrying a dead port in a hot loop
		// (observed: ~15k connections, exhausting ephemeral ports with TIME_WAIT sockets).
		const ordered = [...kill].sort((a, b) => Number(/[\\/]ao\.exe$/i.test(a.path)) - Number(/[\\/]ao\.exe$/i.test(b.path)));
		for (const p of ordered) spawnSync("taskkill", ["/PID", String(p.pid), "/F"], { stdio: "ignore" });
		return { killed: kill.map((p) => `${p.pid} ${p.path}`), refused: refuse.map((p) => `${p.pid} ${p.path}`) };
	};
	child = spawn(app, [
		`--proxy-server=${proxyUrl}`, "--proxy-bypass-list=<-loopback>",
		`--ignore-certificate-errors-spki-list=${pki.spki}`, `--remote-debugging-port=${cdpPort}`,
	], { env, stdio: "ignore", windowsHide: false });
	const direct = new Map();
	let sampling = false;
	const sampler = setInterval(() => {
		if (sampling) return;
		sampling = true;
		sampleConnections(child.pid, direct).catch(() => undefined).finally(() => { sampling = false; });
	}, 3000);
	const result = { scenario, app, startedAt: new Date().toISOString(), testHost: TEST_HOST, spki: pki.spki, settings, isolation: { id: plan.id, runFile: plan.runFile, dataDir: plan.dataDir, port: plan.port, host: { pid: host.pid, port: host.port, installDir: host.installDir } } };
	try {
		let page;
		for (let i = 0; i < 60 && !page; i++) {
			await sleep(1000);
			try { page = await cdp(cdpPort); } catch { /* not up yet */ }
		}
		result.rendererReached = Boolean(page);
		step(`renderer reached: ${result.rendererReached}`);
		await sleep(waitMs ?? 25000); // launch-time automatic check + floor check
		if (page) {
			step("collecting status / manual check");
			result.statusAfterLaunch = await evalIn(page, "window.ao.updates.getStatus()");
			result.manualCheck = await evalIn(page, "window.ao.updates.check().then(() => 'resolved', (e) => 'rejected: ' + e.message)");
			await sleep(4000);
			result.statusAfterManual = await evalIn(page, "window.ao.updates.getStatus()");
			step("feature builds");
			result.featureBuilds = await evalIn(page, "window.ao.featureBuilds.list()");
			if (scenario === "control") {
				// Chromium path: navigate a fresh page to upstream hosts. The proxy must deny.
				for (const u of ["https://github.com/Untrivial-ai/agent-orchestrator/releases/latest", "https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/desktop-version-floor.json"]) {
					await evalIn(page, `fetch(${JSON.stringify(u)}, { mode: "no-cors" }).then(() => "ok", (e) => "blocked: " + e.message)`);
					const t = await fetch(`http://127.0.0.1:${cdpPort}/json/new?${encodeURIComponent(u)}`, { method: "PUT" }).catch(() => undefined);
					void t;
				}
				await sleep(4000);
			}
			page.close();
		}
		if (scenario === "control") {
			// Node path: a client using the same env-proxy mechanism as Electron's main process.
			// Async on purpose: the proxy lives in THIS process, so a blocking spawnSync would
			// stop it from answering and leave the client waiting out its header timeout.
			const script = "for (const u of ['https://api.github.com/repos/Untrivial-ai/agent-orchestrator/releases','https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/desktop-version-floor.json']) fetch(u, { signal: AbortSignal.timeout(8000) }).then(r=>console.log(u,r.status),e=>console.log(u,'blocked',String(e.cause?.message||e.message)))";
			const node = await new Promise((resolve) => {
				const c = spawn(process.execPath, ["-e", script], { env, stdio: ["ignore", "pipe", "ignore"] });
				let out = "";
				c.stdout.on("data", (d) => { out += d; });
				c.on("close", () => resolve(out));
				setTimeout(() => c.kill(), 20000).unref();
			});
			result.nodeControl = node.trim().split("\n");
		}
		await sleep(3000);
	} finally {
		clearInterval(sampler);
		result.cleanup = await cleanup();
		watcher.stop();
		await sleep(1500);
		const protocolAfter = await protocolCommand();
		result.protocolHandler = { before: protocolBefore, after: protocolAfter, unchanged: protocolBefore === protocolAfter };
		proxy.server.close();
		feed.server.close();
	}
	const denied = proxyLog.filter((e) => !e.allowed);
	result.feedRequests = feedLog.map((e) => `${e.status ?? "-"} ${e.method} ${e.url}`);
	const perSecond = new Map();
	for (const e of proxyLog) {
		const k = `${Math.floor((e.t - Date.parse(result.startedAt)) / 1000)}s ${e.target.replace(/:\d{5}/, ":PORT")}`;
		perSecond.set(k, (perSecond.get(k) ?? 0) + 1);
	}
	result.busiestSeconds = [...perSecond.entries()].sort((a, b) => b[1] - a[1]).slice(0, 5).map(([k, v]) => `${v} x ${k}`);
	result.proxyEvents = proxyLog.map((e) => `${e.allowed ? "ALLOW" : "DENY "} ${e.kind} ${e.target}${e.tunnelError ? " (tunnel error)" : ""}`);
	result.upstreamAttempts = denied.filter((e) => UPSTREAM.test(String(e.target).replace(/^https?:\/\//, "").split(/[:/]/)[0])).map((e) => e.target);
	result.otherDeniedAttempts = denied.filter((e) => !result.upstreamAttempts.includes(e.target)).map((e) => e.target);
	result.directConnectionsFromProcessTree = [...direct.keys()];
	// Attribute proxy-denied hostnames to request URLs: the Go daemon logs the full
	// URL of failed outbound requests ('Get "https://..."'), which the CONNECT-only
	// proxy cannot see. Unique URLs, not counts.
	try {
		const log = readFileSync(join(plan.dataDir, "daemon.log"), "utf8");
		const urls = new Set();
		for (const m of log.matchAll(/Get \\"(https?:\/\/[^\\"]+)/g)) urls.add(m[1]);
		result.daemonLoggedRequestUrls = [...urls];
		result.daemonLoggedPricingRefresh = (log.match(/pricing catalog refresh failed/g) ?? []).length;
	} catch { result.daemonLoggedRequestUrls = []; }
	const hostAfter = await hostIdentity(host.runFile, host.port);
	result.hostDaemon = { before: hostBefore, after: hostAfter, samples: watcher.samples.length, unchanged: !aborted && hostAfter.status === 200 && hostAfter.pid === hostBefore.pid, aborted };
	result.finishedAt = new Date().toISOString();
	writeFileSync(join(out, `${scenario}.json`), JSON.stringify(result, null, 2));
	return result;
}

if (process.argv[1]?.endsWith("update-egress-harness.mjs")) {
	const app = arg("app");
	const scenario = arg("scenario", "stable");
	const out = arg("out");
	if (!app || !out) {
		process.stderr.write("usage: update-egress-harness.mjs --app <exe> --scenario stable|nightly|feature|outage|control --out <dir> [--wait-ms N]\n");
		process.exit(2);
	}
	const result = await run({ app, scenario, out, waitMs: Number(arg("wait-ms", "25000")) });
	process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
	process.exit(0);
}
