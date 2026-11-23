// Isolated smoke test of the production BlueAshAI installer (Windows).
//
//   node scripts/brand-smoke.mjs --installer "<path>\BlueAshAI Setup <version>.exe" --out <report dir>
//
// It installs the REAL installer silently into a temp folder, launches the installed app with its
// DEFAULT state directory, port and pipe names (only the user-profile root, so ".blueashai" lands in a
// temp dir, and the proxy are overridden), checks what it created, and uninstalls. It asserts, with
// the installed Orchestrator running next to it, that BlueAshAI:
//   * uses its own product/exe names, NSIS key and state dir (.blueashai under the temp profile),
//   * binds its own port (not 3001) and its own pipes (not ao-supervise / ao-browser...),
//   * does not touch the installed Orchestrator daemon (pid + /healthz), its ao-app handler, its
//     HKCU Uninstall key, or any other HKCU key set beyond its own two (removed again on uninstall),
//   * fails closed on update checks (feed-less build) and sends no request to any host: every outbound
//     connection goes through a deny/record proxy that forwards NOTHING.
// AO_SKIP_PROTOCOL_REGISTRATION=1 is set so the smoke does not write the blueashai URL scheme to the
// real registry; protocol selection is covered by unit tests and the static scan.
// Safety gates abort BEFORE launch and tear everything down on any host change.
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve, sep } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { hostIdentity, listPipes, verifiedKillList, watchHost } from "./update-egress-guards.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const brands = JSON.parse(readFileSync(join(here, "..", "src", "shared", "brands.json"), "utf8"));
const BRAND = brands.blueashai;
const LEGACY = brands.legacy;
const step = (m) => process.stderr.write(`[smoke ${new Date().toISOString()}] ${m}\n`);
const arg = (n) => { const i = process.argv.indexOf(`--${n}`); return i >= 0 ? process.argv[i + 1] : undefined; };

function exec(cmd, args, opts = {}) {
	return new Promise((res) => {
		const c = spawn(cmd, args, { stdio: ["ignore", "pipe", "pipe"], ...opts });
		let out = "";
		c.stdout.on("data", (d) => { out += d; });
		c.stderr.on("data", (d) => { out += d; });
		c.on("error", (e) => res({ code: -1, out: String(e) }));
		c.on("close", (code) => res({ code, out }));
	});
}
const ps = (script) => exec("powershell", ["-NoProfile", "-Command", script]);
async function processTable() {
	const r = await ps("Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,ExecutablePath | ConvertTo-Json -Compress");
	try { return JSON.parse(r.out).map((p) => ({ pid: p.ProcessId, ppid: p.ParentProcessId, name: p.Name, path: p.ExecutablePath ?? "" })); } catch { return []; }
}
const protocolCommand = async (scheme) => (await ps(`(Get-ItemProperty 'Registry::HKEY_CURRENT_USER\\Software\\Classes\\${scheme}\\shell\\open\\command' -ErrorAction SilentlyContinue).'(default)'`)).out.trim() || null;
async function keyNames(path) {
	const r = await ps(`(Get-ChildItem 'Registry::${path}' -ErrorAction SilentlyContinue | ForEach-Object { $_.PSChildName }) -join [char]10`);
	return new Set(r.out.split("\n").map((s) => s.trim()).filter(Boolean));
}
const uninstallKeys = () => keyNames("HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall");
const softwareKeys = () => keyNames("HKEY_CURRENT_USER\\Software");
const classesKeys = () => keyNames("HKEY_CURRENT_USER\\Software\\Classes");
async function uninstallEntry(name) {
	const r = await ps(`Get-ItemProperty 'Registry::HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${name}' | Select-Object DisplayName,DisplayVersion,UninstallString | ConvertTo-Json -Compress`);
	try { return JSON.parse(r.out); } catch { return null; }
}
async function fileFacts(file) {
	const r = await ps(`$i=Get-Item -LiteralPath '${file}'; [pscustomobject]@{ProductName=$i.VersionInfo.ProductName;ProductVersion=$i.VersionInfo.ProductVersion;FileDescription=$i.VersionInfo.FileDescription;CompanyName=$i.VersionInfo.CompanyName;Signature=[string](Get-AuthenticodeSignature -LiteralPath '${file}').Status;Length=$i.Length} | ConvertTo-Json -Compress`);
	try { return JSON.parse(r.out); } catch { return { error: r.out.trim() }; }
}
const portFree = (port) => new Promise((r) => { const s = net.createServer(); s.once("error", () => r(false)); s.listen(port, "127.0.0.1", () => s.close(() => r(true))); });

// deny/record proxy: forwards NOTHING (no feed exists in this build, nothing is allowed).
function startDenyProxy(log) {
	const server = http.createServer((req, res) => { log.push({ t: Date.now(), kind: "http", target: req.url, allowed: false }); res.writeHead(403).end(); });
	server.on("clientError", (_e, s) => s.destroy());
	server.on("connect", (req, socket) => {
		socket.on("error", () => undefined);
		const [host, port] = req.url.split(":");
		const loopback = host === "127.0.0.1" || host.endsWith(".localhost");
		// The app talks to its OWN daemon over loopback through the proxy flag; only that port is relayed.
		if (loopback && Number(port) === BRAND.defaultPort) {
			log.push({ t: Date.now(), kind: "connect", target: req.url, allowed: true, loopback: true });
			const up = net.connect(Number(port), "127.0.0.1", () => { socket.write("HTTP/1.1 200 Connection Established\r\n\r\n"); up.pipe(socket); socket.pipe(up); });
			up.on("error", () => socket.destroy());
			return;
		}
		log.push({ t: Date.now(), kind: "connect", target: req.url, allowed: false });
		socket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
	});
	return new Promise((r) => server.listen(0, "127.0.0.1", () => r({ server, port: server.address().port })));
}

async function cdpPage(port) {
	const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
	for (const t of targets.filter((x) => x.type === "page" && x.webSocketDebuggerUrl)) {
		const ws = new WebSocket(t.webSocketDebuggerUrl);
		await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
		let id = 0;
		const call = (method, params) => new Promise((resolveP) => {
			const mine = ++id;
			const timer = setTimeout(() => resolveP({ timeout: true }), 20000);
			const onMessage = (ev) => { const d = JSON.parse(ev.data); if (d.id === mine) { ws.removeEventListener("message", onMessage); clearTimeout(timer); resolveP(d); } };
			ws.addEventListener("message", onMessage);
			ws.send(JSON.stringify({ id: mine, method, params }));
		});
		const probe = await call("Runtime.evaluate", { expression: "typeof window.ao?.updates?.check", returnByValue: true });
		if (probe.result?.result?.value === "function") return { call, close: () => ws.close() };
		ws.close();
	}
	return undefined;
}
const evalIn = async (page, expression) => { const r = await page.call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }); return r.result?.exceptionDetails ? { error: r.result.exceptionDetails.text } : r.result?.result?.value; };

async function main() {
	const installer = resolve(arg("installer") ?? "");
	const out = resolve(arg("out") ?? ".");
	if (!existsSync(installer)) throw new Error(`installer not found: ${installer}`);
	mkdirSync(out, { recursive: true });
	const report = { startedAt: new Date().toISOString(), installer, brand: BRAND.id, checks: {}, safety: {} };
	const check = (name, ok, data) => { report.checks[name] = { ok: Boolean(ok), ...(data === undefined ? {} : { data }) }; step(`${ok ? "PASS" : "FAIL"} ${name}`); return ok; };

	// ---- host baseline (read-only)
	const realHome = process.env.USERPROFILE;
	const hostRunFile = join(realHome, ".ao", "running.json");
	let hostRun = {};
	try { hostRun = JSON.parse(readFileSync(hostRunFile, "utf8")); } catch { /* none */ }
	const table0 = await processTable();
	const hostProc = table0.find((p) => p.pid === hostRun.pid);
	const hostInstall = hostProc?.path ? resolve(dirname(hostProc.path), "..", "..") : undefined;
	const hostPids = table0.filter((p) => hostInstall && p.path.toLowerCase().startsWith(hostInstall.toLowerCase() + sep) && !p.path.toLowerCase().includes(`${sep}worktrees${sep}`)).map((p) => p.pid);
	const hostPort = hostRun.port ?? LEGACY.defaultPort;
	const hostBefore = await hostIdentity(hostRunFile, hostPort);
	const handlerBefore = { orchestrator: await protocolCommand(LEGACY.protocolScheme), blueashai: await protocolCommand(BRAND.protocolScheme) };
	const uninstallBefore = await uninstallKeys();
	const softwareBefore = await softwareKeys();
	const classesBefore = await classesKeys();
	const pipesBefore = new Set(listPipes());
	report.safety.hostBefore = { pid: hostRun.pid, port: hostPort, health: hostBefore, installDir: hostInstall, handlerBefore };
	if (hostRun.pid && hostBefore.status !== 200) throw new Error(`host daemon unhealthy before the smoke: ${JSON.stringify(hostBefore)}`);
	step(`host baseline: daemon pid ${hostRun.pid} port ${hostPort} healthz ${hostBefore.status}`);

	const work = mkdtempSync(join(tmpdir(), "ao-brand-"));
	const instDir = join(work, "inst", BRAND.windowsInstallDirName);
	const home = join(work, "home");
	for (const d of ["Downloads", "Documents", "Desktop", "Pictures", "Music", "Videos", "AppData/Roaming", "AppData/Local"]) mkdirSync(join(home, d), { recursive: true });
	const exe = join(instDir, `${BRAND.executableName}.exe`);
	let aborted; let watcher; let handlerWatch; let proxy; let page; let child; let sampler;
	const proxyLog = [];
	const direct = new Map();
	const killTree = async () => {
		const table = await processTable();
		const roots = table.filter((p) => p.path && p.path.toLowerCase().startsWith(instDir.toLowerCase() + sep));
		const killed = []; const refused = [];
		for (const r of roots) {
			const { kill, refuse } = verifiedKillList({ table, rootPid: r.pid, testAppDir: instDir, installDir: hostInstall, hostPids });
			for (const p of kill) if (!killed.some((k) => k.pid === p.pid)) killed.push(p);
			for (const p of refuse) refused.push(p);
		}
		for (const p of killed.sort((a, b) => Number(/\\ao\.exe$/i.test(a.path)) - Number(/\\ao\.exe$/i.test(b.path)))) spawnSync("taskkill", ["/PID", String(p.pid), "/F"], { stdio: "ignore" });
		return { killed: killed.map((p) => `${p.pid} ${p.path}`), refused: refused.map((p) => `${p.pid} ${p.path}`) };
	};
	const emergency = async (why) => { aborted ??= why; step(`ABORT: ${why}`); report.aborted = why; await killTree(); };
	process.on("uncaughtException", (e) => { void emergency(String(e?.stack ?? e)).finally(() => process.exit(1)); });
	process.on("unhandledRejection", (e) => { void emergency(String(e)).finally(() => process.exit(1)); });

	try {
		// ---- install the real installer into the temp folder
		report.installerFacts = { ...(await fileFacts(installer)), sha256: createHash("sha256").update(readFileSync(installer)).digest("hex") };
		step(`installing into ${instDir}`);
		const inst = await exec(installer, ["/S", `/D=${instDir}`]);
		check("installer exits cleanly and installs BlueAshAI.exe into the chosen folder", inst.code === 0 && existsSync(exe), { exit: inst.code });
		report.exeFacts = await fileFacts(exe);
		check("installed exe is BlueAshAI (product name, version)", report.exeFacts.ProductName === BRAND.displayName && /^0\.15\.4-blueash\.\d+$/.test(report.exeFacts.ProductVersion), report.exeFacts);
		check("installed exe is unsigned (no production signing credentials exist)", report.exeFacts.Signature === "NotSigned");
		const newKeys = [...(await uninstallKeys())].filter((k) => !uninstallBefore.has(k));
		const entry = newKeys.length === 1 ? await uninstallEntry(newKeys[0]) : null;
		report.registryAfterInstall = { newUninstallKeys: newKeys, entry, newSoftwareKeys: [...(await softwareKeys())].filter((k) => !softwareBefore.has(k)), newClassesKeys: [...(await classesKeys())].filter((k) => !classesBefore.has(k)) };
		check("exactly one new HKCU Uninstall key, named for BlueAshAI, pointing into the temp folder", newKeys.length === 1 && entry?.DisplayName?.startsWith(BRAND.displayName) && String(entry.UninstallString).toLowerCase().includes(work.toLowerCase()), report.registryAfterInstall);
		check("installer registered no URL scheme (HKCU Classes unchanged)", report.registryAfterInstall.newClassesKeys.length === 0);
		check("installed Orchestrator's ao-app handler unchanged by install", (await protocolCommand(LEGACY.protocolScheme)) === handlerBefore.orchestrator);
		check("installed Orchestrator daemon unchanged by install", (await hostIdentity(hostRunFile, hostPort)).pid === hostBefore.pid);

		// ---- pre-launch gates (abort BEFORE launching anything)
		const asar = readFileSync(join(instDir, "resources", "app.asar"));
		const daemon = readFileSync(join(instDir, "resources", "daemon", "ao.exe"));
		const gates = {
			asarBrandCompiled: asar.includes(Buffer.from("ai.blueash.desktop")) && asar.includes(Buffer.from(BRAND.stateDirName)),
			noTestIdentity: asar.includes(Buffer.from("buildIdentity:``")),
			noPinFile: !existsSync(join(instDir, "ao-instance.json")),
			daemonStateDir: daemon.includes(Buffer.from(`brand.StateDirName=${BRAND.stateDirName}`)),
			daemonPort: daemon.includes(Buffer.from(`brand.Port=${BRAND.defaultPort}`)),
			portFree: await portFree(BRAND.defaultPort),
			portNotHost: BRAND.defaultPort !== hostPort && BRAND.defaultPort !== 3011,
			stateDirNotHost: BRAND.stateDirName !== ".ao",
			expectedPipes: ["ao-supervise", "ao-browser", "ao-browser-stream"].map((b) => `${b}-${BRAND.stateDirName.replace(/[^a-zA-Z0-9-]/g, "-")}`),
		};
		gates.expectedPipesFreeAndDistinct = gates.expectedPipes.every((n) => !pipesBefore.has(n) && !["ao-supervise", "ao-browser", "ao-browser-stream--ao"].includes(n));
		report.safety.gates = gates;
		const bad = Object.entries(gates).filter(([k, v]) => typeof v === "boolean" && !v).map(([k]) => k);
		check("pre-launch isolation gates", bad.length === 0, { gates, failed: bad });
		if (bad.length > 0) throw new Error(`unsafe to launch: ${bad.join(", ")}`);

		// ---- guards + launch with defaults
		proxy = await startDenyProxy(proxyLog);
		watcher = watchHost({ hostRunFile, port: hostPort, onChange: (r) => { void emergency(`host daemon changed: ${r}`); } });
		let lastHandler = handlerBefore.orchestrator;
		handlerWatch = setInterval(async () => { const now = await protocolCommand(LEGACY.protocolScheme); if (now !== lastHandler) { lastHandler = now; void emergency(`ao-app handler changed to ${now}`); } }, 2000);
		const proxyUrl = `http://127.0.0.1:${proxy.port}`;
		const cdpPort = 30000 + Math.floor(Math.random() * 20000);
		const env = { ...process.env, USERPROFILE: home, HOME: home, APPDATA: join(home, "AppData", "Roaming"), LOCALAPPDATA: join(home, "AppData", "Local"), HTTPS_PROXY: proxyUrl, HTTP_PROXY: proxyUrl, https_proxy: proxyUrl, http_proxy: proxyUrl, NO_PROXY: "", no_proxy: "", NODE_USE_ENV_PROXY: "1", AO_SKIP_PROTOCOL_REGISTRATION: "1" };
		for (const k of ["AO_RUN_FILE", "AO_PORT", "AO_DATA_DIR", "AO_BRAND", "AO_TEST_IDENTITY"]) delete env[k];
		step("launching installed BlueAshAI with default state/port/pipes");
		child = spawn(exe, [`--proxy-server=${proxyUrl}`, "--proxy-bypass-list=<-loopback>", `--remote-debugging-port=${cdpPort}`], { env, stdio: "ignore", cwd: instDir });
		let sampling = false;
		sampler = setInterval(async () => {
			if (sampling) return; sampling = true;
			try {
				const names = new Map(); const table = await processTable();
				names.set(child.pid, "root");
				let grew = true; while (grew) { grew = false; for (const p of table) if (names.has(p.ppid) && !names.has(p.pid)) { names.set(p.pid, p.name); grew = true; } }
				const ns = (await exec("netstat", ["-ano", "-p", "TCP"])).out.split("\n");
				for (const line of ns) { const m = /^\s*TCP\s+(\S+)\s+(\S+)\s+(\S+)\s+(\d+)\s*$/.exec(line); if (!m || !names.has(Number(m[4]))) continue; if (/^(127\.0\.0\.1|\[::1\]|0\.0\.0\.0|\[::\]):/.test(m[2])) continue; direct.set(`${names.get(Number(m[4]))}(${m[4]}) -> ${m[2]} ${m[3]}`, true); }
			} finally { sampling = false; }
		}, 3000);
		for (let i = 0; i < 70 && !page; i++) { await sleep(1000); try { page = await cdpPage(cdpPort); } catch { /* not yet */ } if (aborted) throw new Error(aborted); }
		check("app started and its renderer is reachable", Boolean(page));
		if (!page) throw new Error("renderer not reached");
		await sleep(15000);

		// ---- identity of the running instance
		const stateDir = join(home, BRAND.stateDirName);
		const runFile = join(stateDir, "running.json");
		let run = {}; try { run = JSON.parse(readFileSync(runFile, "utf8")); } catch { /* missing */ }
		const pipesNow = listPipes().filter((n) => !pipesBefore.has(n) && /^ao-/.test(n));
		report.runtime = { runFile, run: { pid: run.pid, port: run.port, owner: run.owner }, newAoPipes: pipesNow, stateDirEntries: existsSync(stateDir) ? readdirSync(stateDir) : [] };
		check("default run file is <profile>\\.blueashai\\running.json and names the daemon on port 3101", existsSync(runFile) && run.port === BRAND.defaultPort, report.runtime.run);
		check("daemon listens on the BlueAshAI port, not the Orchestrator port", (await exec("netstat", ["-ano", "-p", "TCP"])).out.split("\n").some((l) => l.includes(`127.0.0.1:${BRAND.defaultPort} `) && l.includes("LISTENING")));
		check("only BlueAshAI-suffixed pipes were created; none of the Orchestrator's names", pipesNow.length > 0 && pipesNow.every((n) => n.includes("-blueashai") || n.includes("--blueashai")) && !pipesNow.some((n) => ["ao-supervise", "ao-browser", "ao-browser-chrome", "ao-browser-stream--ao"].includes(n)), pipesNow);
		const winTitle = (await ps(`(Get-Process -Id ${child.pid}).MainWindowTitle`)).out.trim();
		const docTitle = await evalIn(page, "document.title");
		report.titles = { windowTitle: winTitle, documentTitle: docTitle };
		check("visible title is BlueAshAI", /BlueAshAI/.test(winTitle) || /BlueAshAI/.test(String(docTitle)), report.titles);
		const lifecycle = readdirSync(stateDir, { recursive: true }).filter((f) => String(f).endsWith("desktop-lifecycle.jsonl"));
		report.runtime.lifecycleFiles = lifecycle.map(String);

		// ---- updates fail closed
		report.updateCheck = await evalIn(page, "window.ao.updates.check().then(() => 'resolved', (e) => 'rejected: ' + e.message)");
		await sleep(2500);
		report.updateStatus = await evalIn(page, "window.ao.updates.getStatus()");
		report.featureBuilds = await evalIn(page, "window.ao.featureBuilds.list()");
		check("update check fails closed with the 'no valid update server' error", /no valid update server/i.test(JSON.stringify(report.updateStatus)) && report.updateStatus?.state === "error", report.updateStatus);
		check("feature-build list is empty (no origin configured)", Array.isArray(report.featureBuilds) && report.featureBuilds.length === 0);
		page.close();

		// ---- host unchanged while BlueAshAI ran
		const hostNow = await hostIdentity(hostRunFile, hostPort);
		check("installed Orchestrator daemon still the same pid and healthy", hostNow.pid === hostBefore.pid && hostNow.status === 200, hostNow);
		check("installed Orchestrator's own pipes unchanged", ["ao-supervise", "ao-browser", "ao-browser-chrome", "ao-browser-stream--ao"].every((n) => pipesBefore.has(n) === listPipes().includes(n)));
		check("installed Orchestrator's ao-app handler unchanged", (await protocolCommand(LEGACY.protocolScheme)) === handlerBefore.orchestrator);
		check("no blueashai/ao-app URL scheme written to HKCU Classes (registration skipped for the smoke)", [...(await classesKeys())].filter((k) => !classesBefore.has(k)).length === 0);
	} catch (err) {
		report.error = String(err?.stack ?? err);
		step(`FAILED: ${err?.message}`);
	} finally {
		clearInterval(sampler); clearInterval(handlerWatch);
		report.network = {
			proxyEvents: proxyLog.map((e) => `${e.allowed ? "ALLOW" : "DENY"} ${e.kind} ${e.target}`),
			deniedHostnames: [...new Set(proxyLog.filter((e) => !e.allowed).map((e) => String(e.target).replace(/^https?:\/\//, "").replace(/[:/].*$/, "")))],
			directNonLoopbackConnections: [...direct.keys()],
			forwarded: proxyLog.filter((e) => e.allowed && !e.loopback).length,
		};
		report.cleanup = { processes: await killTree() };
		await sleep(1500);
		const unins = existsSync(instDir) ? readdirSync(instDir).filter((f) => /^Uninstall .*\.exe$/i.test(f)) : [];
		if (unins.length === 1 && unins[0].includes(BRAND.executableName)) {
			const r = await exec(join(instDir, unins[0]), ["/S", `_?=${instDir}`]);
			report.cleanup.uninstall = { exit: r.code };
			await sleep(1500);
		} else report.cleanup.uninstall = { skipped: JSON.stringify(unins) };
		const leftover = [...(await uninstallKeys())].filter((k) => !uninstallBefore.has(k));
		report.cleanup.leftoverUninstallKeys = leftover;
		for (const k of leftover) {
			const e = await uninstallEntry(k);
			if (e?.DisplayName?.startsWith(BRAND.displayName) && String(e.UninstallString).toLowerCase().includes(work.toLowerCase())) {
				const r = await ps(`$u='Registry::HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${k}'; $s='Registry::HKEY_CURRENT_USER\\Software\\${k}'; if (@(Get-ChildItem $u).Count -eq 0) { Remove-Item $u; if ((Test-Path $s) -and @(Get-ChildItem $s).Count -eq 0 -and ((Get-ItemProperty $s).InstallLocation -like '*ao-brand-*')) { Remove-Item $s }; 'removed' } else { 'has-subkeys' }`);
				report.cleanup.removedLeftoverKey = { key: k, result: r.out.trim() };
			}
		}
		report.cleanup.uninstallKeysAfter = [...(await uninstallKeys())].filter((k) => !uninstallBefore.has(k));
		report.cleanup.softwareKeysNew = [...(await softwareKeys())].filter((k) => !softwareBefore.has(k));
		report.cleanup.classesKeysNew = [...(await classesKeys())].filter((k) => !classesBefore.has(k));
		try { if (/ao-brand-/.test(work) && resolve(work).toLowerCase().startsWith(resolve(tmpdir()).toLowerCase() + sep)) { rmSync(work, { recursive: true, force: true }); report.cleanup.workDirRemoved = !existsSync(work); } } catch (e) { report.cleanup.workDirRemoved = `failed: ${e.message}`; }
		watcher?.stop();
		try { proxy?.server.close(); } catch { /* closed */ }
		report.safety.hostAfter = { health: await hostIdentity(hostRunFile, hostPort), handler: await protocolCommand(LEGACY.protocolScheme), hostProcessesAlive: `${(await processTable()).filter((p) => hostPids.includes(p.pid)).length}/${hostPids.length}`, watcherSamples: watcher?.samples.length, aborted: aborted ?? null };
		report.safety.hostUnchanged = report.safety.hostAfter.health.pid === hostBefore.pid && report.safety.hostAfter.health.status === 200 && report.safety.hostAfter.handler === handlerBefore.orchestrator;
		report.finishedAt = new Date().toISOString();
		report.passed = !report.error && Object.values(report.checks).every((c) => c.ok) && report.safety.hostUnchanged && report.cleanup.uninstallKeysAfter.length === 0;
		writeFileSync(join(out, "brand-smoke.json"), JSON.stringify(report, null, 2));
	}
	return report;
}

main().then((r) => { process.stdout.write(`${JSON.stringify({ passed: r.passed, error: r.error?.split("\n")[0], hostUnchanged: r.safety?.hostUnchanged }, null, 2)}\n`); process.exit(r.passed ? 0 : 1); }).catch((e) => { process.stderr.write(`${e.stack}\n`); process.exit(1); });
