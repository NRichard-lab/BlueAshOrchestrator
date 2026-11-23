// Real Windows NSIS update test on an isolated, test-scoped install (plan checkpoint C10, Windows).
//
//   node scripts/update-nsis-harness.mjs --installers <dir with baseline+update Setup exes> \
//        --baseline 0.99.1 --update 0.99.2 --id updt1a --out <report dir> [--corrupt]
//
// What is REAL: the NSIS installers (built by build-test-installers.mjs under a test identity), the
// silent install of the baseline, the in-app manual check + download + hash verification by
// electron-updater against a genuine latest.yml, the app's own quitAndInstall, the NSIS update
// (`--updated /S --force-run`), and the relaunch of the new version.
// What is test-scoped: the update origin is a test hostname served by a loopback HTTPS server with a
// throwaway CA; trust is a launch-flag SPKI pin for the FIRST launch only; installers are UNSIGNED
// (no code-signing credentials are used or exist for tests; production signing is a separate
// release step).
//
// Safety (the host Orchestrator must be untouched; see ledger addenda 1-2):
//   * the test build has its own exe name/appId/product/updater cache (makers/test-identity.ts) and
//     a compile-time-gated ao-instance.json pin for run file, port, protocol and telemetry
//     (src/main/instance-pin.ts), because the post-update relaunch does NOT inherit environment;
//   * install dir is under %TEMP%; before ANY launch the pin is validated against the live host
//     (unique pipes, free port, not the host run file) and the run aborts otherwise;
//   * the host daemon (pid + healthz), the installed ao-app protocol handler, the HKCU Uninstall key
//     set and the host's processes are sampled throughout; any change aborts and cleans up;
//   * processes are only ever terminated if their executable lives under the test install dir.
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, createReadStream, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import https from "node:https";
import { tmpdir } from "node:os";
import { dirname, join, resolve, sep } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { TEST_HOST, makeTestPki, startProxy, UPSTREAM } from "./update-egress-harness.mjs";
import { hostIdentity, isolationViolations, listPipes, derivedPipes, verifiedKillList, watchHost } from "./update-egress-guards.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const step = (m) => process.stderr.write(`[nsis ${new Date().toISOString()}] ${m}\n`);
const arg = (n, d) => { const i = process.argv.indexOf(`--${n}`); return i >= 0 ? process.argv[i + 1] : d; };

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
	const r = await ps("Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine | ConvertTo-Json -Compress");
	try { return JSON.parse(r.out).map((p) => ({ pid: p.ProcessId, ppid: p.ParentProcessId, name: p.Name, path: p.ExecutablePath ?? "", cmd: p.CommandLine ?? "" })); } catch { return []; }
}

// Read-only registry/handler snapshots.
async function protocolCommand() {
	const r = await ps("(Get-ItemProperty 'Registry::HKEY_CURRENT_USER\\Software\\Classes\\ao-app\\shell\\open\\command' -ErrorAction SilentlyContinue).'(default)'");
	return r.out.trim() || null;
}
async function uninstallKeys() {
	const r = await ps("(Get-ChildItem 'Registry::HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall' -ErrorAction SilentlyContinue | ForEach-Object { $_.PSChildName }) -join [char]10");
	return new Set(r.out.split("\n").map((s) => s.trim()).filter(Boolean));
}
async function uninstallEntry(name) {
	const r = await ps(`Get-ItemProperty 'Registry::HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${name}' | Select-Object DisplayName,DisplayVersion,InstallLocation,UninstallString | ConvertTo-Json -Compress`);
	try { return JSON.parse(r.out); } catch { return null; }
}
async function hkcuSoftwareKeys() {
	const r = await ps("(Get-ChildItem 'Registry::HKEY_CURRENT_USER\\Software' -ErrorAction SilentlyContinue | ForEach-Object { $_.PSChildName }) -join [char]10");
	return new Set(r.out.split("\n").map((s) => s.trim()).filter(Boolean));
}
async function runKeyValues() {
	const r = await ps("(Get-ItemProperty 'Registry::HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Run' -ErrorAction SilentlyContinue | Out-String).Trim()");
	return r.out.trim();
}
async function fileFacts(file) {
	const r = await ps(`$i=Get-Item -LiteralPath '${file}'; $s=Get-AuthenticodeSignature -LiteralPath '${file}'; [pscustomobject]@{ProductVersion=$i.VersionInfo.ProductVersion;FileVersion=$i.VersionInfo.FileVersion;Length=$i.Length;Signature=[string]$s.Status} | ConvertTo-Json -Compress`);
	try { return JSON.parse(r.out); } catch { return { error: r.out.trim() }; }
}

function hashFile(file) {
	return new Promise((res, rej) => {
		const sha512 = createHash("sha512");
		const sha256 = createHash("sha256");
		createReadStream(file).on("data", (d) => { sha512.update(d); sha256.update(d); }).on("end", () => res({ sha512: sha512.digest("base64"), sha256: sha256.digest("hex") })).on("error", rej);
	});
}

// ---------- static HTTPS feed (real files, Range + HEAD support, request log) ----------
function startStaticFeed(pki, root, log, corrupt) {
	const server = https.createServer({ key: readFileSync(pki.key), cert: readFileSync(pki.cert) }, (req, res) => {
		const url = decodeURIComponent(req.url.split("?")[0]);
		const file = resolve(join(root, url));
		const entry = { t: Date.now(), method: req.method, url, range: req.headers.range, status: 0, bytes: 0, corrupted: false };
		log.push(entry);
		if (!file.startsWith(resolve(root) + sep) || !existsSync(file) || !statSync(file).isFile()) { entry.status = 404; res.writeHead(404).end(); return; }
		const size = statSync(file).size;
		const isInstaller = /\.exe$/i.test(file);
		let start = 0;
		let end = size - 1;
		let status = 200;
		const m = /^bytes=(\d*)-(\d*)$/.exec(req.headers.range ?? "");
		if (m) { start = m[1] === "" ? size - Number(m[2]) : Number(m[1]); end = m[2] === "" ? size - 1 : Math.min(Number(m[2]), size - 1); status = 206; }
		const headers = { "content-length": end - start + 1, "accept-ranges": "bytes", "content-type": "application/octet-stream" };
		if (status === 206) headers["content-range"] = `bytes ${start}-${end}/${size}`;
		entry.status = status;
		res.writeHead(status, headers);
		if (req.method === "HEAD") { res.end(); return; }
		const stream = createReadStream(file, { start, end });
		// Corrupt payload: flip 16 bytes at a fixed offset in the installer body, same length, same manifest.
		const CORRUPT_AT = 8 * 1024 * 1024;
		let pos = start;
		stream.on("data", (chunk) => {
			if (corrupt && isInstaller && pos <= CORRUPT_AT + 16 && pos + chunk.length > CORRUPT_AT) {
				chunk = Buffer.from(chunk);
				for (let i = 0; i < 16; i++) { const at = CORRUPT_AT + i - pos; if (at >= 0 && at < chunk.length) chunk[at] ^= 0xff; }
				entry.corrupted = true;
			}
			pos += chunk.length;
			entry.bytes += chunk.length;
			if (!res.write(chunk)) { stream.pause(); res.once("drain", () => stream.resume()); }
		});
		stream.on("end", () => res.end());
		stream.on("error", () => res.destroy());
		res.on("close", () => stream.destroy());
	});
	server.on("tlsClientError", () => undefined);
	server.on("clientError", (_e, s) => s.destroy());
	return new Promise((resolveP) => server.listen(0, "127.0.0.1", () => resolveP({ server, port: server.address().port })));
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
async function evalIn(page, expression) {
	const r = await page.call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
	return r.result?.exceptionDetails ? { error: r.result.exceptionDetails.text } : r.result?.result?.value;
}

export async function main() {
	const installers = resolve(arg("installers"));
	const baseline = arg("baseline");
	const update = arg("update");
	const id = arg("id");
	const out = resolve(arg("out"));
	const corrupt = process.argv.includes("--corrupt");
	if (!installers || !baseline || !update || !id || !arg("out")) throw new Error("usage: --installers <dir> --baseline v --update v --id id --out dir [--corrupt]");
	mkdirSync(out, { recursive: true });
	const report = { startedAt: new Date().toISOString(), scenario: corrupt ? "corrupt-payload" : "update", id, baseline, update, steps: [], safety: {} };
	const note = (s, extra) => { report.steps.push({ t: new Date().toISOString(), step: s, ...(extra ? { data: extra } : {}) }); step(s); };

	const product = `AO Update Test ${id}`;
	const setupName = (v) => `${product} Setup ${v}.exe`;
	const baselineExe = join(installers, setupName(baseline));
	const updateExe = join(installers, setupName(update));
	for (const f of [baselineExe, updateExe]) if (!existsSync(f)) throw new Error(`missing installer ${f}`);

	// ---- host discovery + baselines (read-only)
	const realHome = process.env.USERPROFILE;
	const hostRunFile = join(realHome, ".ao", "running.json");
	let hostRun = {};
	try { hostRun = JSON.parse(readFileSync(hostRunFile, "utf8")); } catch { /* none */ }
	const table0 = await processTable();
	const hostProc = table0.find((p) => p.pid === hostRun.pid);
	const hostInstall = hostProc?.path ? resolve(dirname(hostProc.path), "..", "..") : undefined;
	const hostPids = table0.filter((p) => hostInstall && p.path.toLowerCase().startsWith(hostInstall.toLowerCase() + sep) && !p.path.toLowerCase().includes(`${sep}data${sep}worktrees${sep}`)).map((p) => p.pid);
	const host = { port: hostRun.port ?? 3001, runFile: hostRunFile, dataDirs: [join(realHome, ".ao"), ...(hostInstall ? [join(hostInstall, "data")] : [])], installDir: hostInstall };
	const hostBefore = await hostIdentity(hostRunFile, host.port);
	if (hostRun.pid && hostBefore.status !== 200) throw new Error(`host daemon not healthy before the test: ${JSON.stringify(hostBefore)}`);
	const protocolBefore = await protocolCommand();
	const uninstallBefore = await uninstallKeys();
	const softwareBefore = await hkcuSoftwareKeys();
	const runKeyBefore = await runKeyValues();
	const pipesBefore = new Set(listPipes());
	report.safety.hostBefore = { pid: hostRun.pid, port: host.port, health: hostBefore, installDir: hostInstall, protocolHandler: protocolBefore };
	note("host baseline captured", report.safety.hostBefore);

	const work = mkdtempSync(join(tmpdir(), "ao-nsis-"));
	// A wizard install appends the app folder name to the chosen directory and later updates reuse that
	// exact folder, so the baseline is installed the same way (a bare /D= root would be re-nested).
	const instDir = join(work, "inst", `ao-test-${id}`);
	const home = join(work, "home");
	for (const d of ["Downloads", "Documents", "Desktop", "Pictures", "Music", "Videos", "AppData/Roaming", "AppData/Local"]) mkdirSync(join(home, d), { recursive: true });
	let aborted;
	let watcher;
	let protocolWatch;
	const cleanupLog = {};
	const testExe = () => join(instDir, `ao-test-${id}.exe`);

	const killTestTree = async () => {
		const table = await processTable();
		const roots = table.filter((p) => p.path && p.path.toLowerCase().startsWith(instDir.toLowerCase() + sep));
		const killed = [];
		const refused = [];
		for (const r of roots) {
			const { kill, refuse } = verifiedKillList({ table, rootPid: r.pid, testAppDir: instDir, installDir: hostInstall, hostPids });
			for (const p of kill) if (!killed.some((k) => k.pid === p.pid)) killed.push(p);
			for (const p of refuse) refused.push(p);
		}
		const ordered = killed.sort((a, b) => Number(/\\ao\.exe$/i.test(a.path)) - Number(/\\ao\.exe$/i.test(b.path)));
		for (const p of ordered) spawnSync("taskkill", ["/PID", String(p.pid), "/F"], { stdio: "ignore" });
		return { killed: ordered.map((p) => `${p.pid} ${p.path}`), refused: refused.map((p) => `${p.pid} ${p.path}`) };
	};
	const emergency = async (why) => { aborted ??= why; step(`ABORT: ${why}`); report.aborted = why; await killTestTree(); };
	process.on("uncaughtException", (e) => { void emergency(String(e?.stack ?? e)).finally(() => process.exit(1)); });
	process.on("unhandledRejection", (e) => { void emergency(String(e)).finally(() => process.exit(1)); });

	let feed; let proxy; let page; let child;
	const feedLog = [];
	const proxyLog = [];
	try {
		// ---- stage a genuine feed for the update version (real manifest hashes, real blockmap)
		const feedIn = join(work, "feed-in");
		mkdirSync(feedIn);
		copyFileSync(updateExe, join(feedIn, setupName(update)));
		const feedRoot = join(work, "feed");
		const origin = `https://${TEST_HOST}`;
		const stage = await exec(process.execPath, [join(here, "stage-update-tree.mjs"), "--origin", origin, "--channel", "stable", "--version", update, "--in", feedIn, "--out", feedRoot]);
		if (stage.code !== 0) throw new Error(`staging failed: ${stage.out}`);
		const latestYml = readFileSync(join(feedRoot, "desktop", "stable", "latest.yml"), "utf8");
		const manifestSha512 = /^sha512:\s*(.+)$/m.exec(latestYml)?.[1]?.trim();
		const updateHashes = await hashFile(updateExe);
		const baselineHashes = await hashFile(baselineExe);
		note("feed staged", { latestYml, manifestSha512, updateInstaller: { file: setupName(update), ...updateHashes, size: statSync(updateExe).size }, baselineInstaller: { file: setupName(baseline), ...baselineHashes } });
		if (manifestSha512 !== updateHashes.sha512) throw new Error("latest.yml sha512 does not match the installer on disk");
		report.artifacts = { baseline: { ...baselineHashes, ...(await fileFacts(baselineExe)) }, update: { ...updateHashes, ...(await fileFacts(updateExe)) } };

		// ---- install the REAL baseline NSIS installer silently into the temp install dir
		note(`installing baseline ${baseline} (silent) into ${instDir}`);
		const inst = await exec(baselineExe, ["/S", `/D=${instDir}`]);
		report.baselineInstall = { exit: inst.code, out: inst.out.slice(0, 400) };
		if (!existsSync(testExe())) throw new Error(`baseline install produced no ${testExe()}`);
		const uninstallAfterInstall = await uninstallKeys();
		const newKeys = [...uninstallAfterInstall].filter((k) => !uninstallBefore.has(k));
		report.safety.registryAfterBaselineInstall = { newUninstallKeys: newKeys, entries: await Promise.all(newKeys.map(async (k) => ({ key: k, ...(await uninstallEntry(k)) }))) };
		note("baseline installed", { newKeys, version: (await fileFacts(testExe())).ProductVersion, files: readdirSync(instDir).slice(0, 40) });
		if (newKeys.length !== 1) throw new Error(`expected exactly one new HKCU Uninstall key, saw ${JSON.stringify(newKeys)}`);
		const entry = report.safety.registryAfterBaselineInstall.entries[0];
		if (!String(entry.UninstallString ?? "").toLowerCase().includes(work.toLowerCase())) throw new Error(`install key does not point into the test dir: ${entry.UninstallString}`);
		if ((await protocolCommand()) !== protocolBefore) throw new Error("ao-app handler changed during installation");
		if ((await hostIdentity(hostRunFile, host.port)).pid !== hostBefore.pid) throw new Error("host daemon changed during installation");

		// ---- validate the pin that the installer wrote BEFORE any launch
		const pin = JSON.parse(readFileSync(join(instDir, "ao-instance.json"), "utf8"));
		const pinRunFile = resolve(instDir, pin.runFile);
		const plan = { runFile: pinRunFile, dataDir: join(instDir, "data"), port: pin.port };
		const violations = isolationViolations(plan, host, listPipes());
		report.safety.pin = { ...pin, resolvedRunFile: pinRunFile, derivedPipes: derivedPipes(pinRunFile), violations };
		if (pin.identity !== id || pin.skipProtocolRegistration !== true || pin.telemetryRemote !== "off") violations.push("pin identity/opt-outs wrong");
		const portFree = await new Promise((r) => { const s = import("node:net").then(({ default: net }) => { const sv = net.createServer(); sv.once("error", () => r(false)); sv.listen(pin.port, "127.0.0.1", () => sv.close(() => r(true))); }); void s; });
		if (!portFree) violations.push(`pin port ${pin.port} is in use`);
		// The pin is only honored by a build compiled with this identity. Prove the INSTALLED binary has it
		// before it is ever launched (otherwise it would ignore the pin and use the host defaults).
		const asarBytes = readFileSync(join(instDir, "resources", "app.asar"));
		const compiled = asarBytes.includes(Buffer.from(`buildIdentity:\`${id}\``));
		report.safety.installedBuildCompiledWithIdentity = compiled;
		if (!compiled) violations.push("installed app.asar does not carry the compile-time test identity; it would ignore the pin");
		if (violations.length > 0) throw new Error(`unsafe pin, refusing to launch: ${violations.join("; ")}`);
		note("pin validated; launch is isolated", report.safety.pin);

		// ---- servers + guards
		const pki = makeTestPki(work);
		feed = await startStaticFeed(pki, feedRoot, feedLog, corrupt);
		proxy = await startProxy(feed.port, proxyLog, pin.port);
		watcher = watchHost({ hostRunFile, port: host.port, onChange: (r) => { void emergency(`host daemon changed: ${r}`); } });
		let lastProtocol = protocolBefore;
		protocolWatch = setInterval(async () => { const now = await protocolCommand(); if (now !== lastProtocol) { lastProtocol = now; void emergency(`ao-app handler changed to ${now}`); } }, 2000);

		// ---- seed settings in the pinned state dir (manual download; automatic download off)
		mkdirSync(dirname(pinRunFile), { recursive: true });
		writeFileSync(join(dirname(pinRunFile), "update-settings.json"), JSON.stringify({ enabled: false, channel: "latest", nightlyAck: false, feature: null }));

		// ---- launch the installed baseline (first launch gets proxy + test trust via env/flags)
		const proxyUrl = `http://127.0.0.1:${proxy.port}`;
		const cdpPort = 30000 + Math.floor(Math.random() * 20000);
		const env = {
			...process.env, USERPROFILE: home, HOME: home, APPDATA: join(home, "AppData", "Roaming"), LOCALAPPDATA: join(home, "AppData", "Local"),
			HTTPS_PROXY: proxyUrl, HTTP_PROXY: proxyUrl, https_proxy: proxyUrl, http_proxy: proxyUrl, NO_PROXY: "", no_proxy: "",
			NODE_USE_ENV_PROXY: "1", NODE_EXTRA_CA_CERTS: pki.ca,
		};
		note("launching installed baseline");
		child = spawn(testExe(), [`--proxy-server=${proxyUrl}`, "--proxy-bypass-list=<-loopback>", `--ignore-certificate-errors-spki-list=${pki.spki}`, `--remote-debugging-port=${cdpPort}`], { env, stdio: "ignore", cwd: instDir });
		for (let i = 0; i < 60 && !page; i++) { await sleep(1000); try { page = await cdpPage(cdpPort); } catch { /* not yet */ } if (aborted) throw new Error(aborted); }
		if (!page) throw new Error("renderer not reached");
		await sleep(8000);
		const pipesDuring = listPipes().filter((n) => !pipesBefore.has(n));
		report.safety.newPipesWhileBaselineRuns = pipesDuring;
		note("baseline running", { newPipes: pipesDuring });
		const bootLog = readFileSync(join(dirname(pinRunFile), "logs", "desktop-lifecycle.jsonl"), "utf8").trim().split("\n").map((l) => JSON.parse(l));
		const baselineStart = bootLog.filter((e) => e.event === "app_start").at(-1);
		report.baselineRuntimeVersion = baselineStart?.version;
		note("baseline runtime version from its own lifecycle log", { version: baselineStart?.version, pid: baselineStart?.pid });
		if (baselineStart?.version !== baseline) throw new Error(`baseline reports version ${baselineStart?.version}`);

		// ---- manual check, then real download through the updater
		report.statusBeforeCheck = await evalIn(page, "window.ao.updates.getStatus()");
		report.manualCheck = await evalIn(page, "window.ao.updates.check().then(() => 'resolved', (e) => 'rejected: ' + e.message)");
		await sleep(3000);
		report.statusAfterCheck = await evalIn(page, "window.ao.updates.getStatus()");
		note("after manual check", report.statusAfterCheck);
		if (report.statusAfterCheck?.state !== "available" || report.statusAfterCheck?.version !== update) throw new Error(`expected available ${update}, got ${JSON.stringify(report.statusAfterCheck)}`);
		const downloadStarted = Date.now();
		await evalIn(page, "window.ao.updates.download()");
		let status;
		for (let i = 0; i < 240; i++) {
			await sleep(1000);
			status = await evalIn(page, "window.ao.updates.getStatus()");
			if (status?.state === "downloaded" || status?.state === "error") break;
			if (aborted) throw new Error(aborted);
		}
		report.statusAfterDownload = status;
		report.downloadMs = Date.now() - downloadStarted;
		note("download finished", { status, ms: report.downloadMs });
		const cacheRoot = join(home, "AppData", "Local", `ao-test-${id}-updater`);
		const pending = existsSync(join(cacheRoot, "pending")) ? readdirSync(join(cacheRoot, "pending")) : [];
		report.updaterCache = { dir: cacheRoot, pending };
		const cachedExe = pending.find((f) => f.toLowerCase().endsWith(".exe"));
		if (cachedExe) {
			const h = await hashFile(join(cacheRoot, "pending", cachedExe));
			report.updaterCache.cachedInstaller = { file: cachedExe, ...h, matchesManifest: h.sha512 === manifestSha512, matchesSourceInstaller: h.sha256 === updateHashes.sha256 };
		}

		if (corrupt) {
			report.expectation = "download rejected; no installer staged; baseline untouched";
			if (status?.state !== "error") throw new Error(`corrupt payload was NOT rejected: ${JSON.stringify(status)}`);
			report.outcome = "corrupt payload rejected by the updater";
		} else {
			if (status?.state !== "downloaded") throw new Error(`download did not complete: ${JSON.stringify(status)}`);
			if (!report.updaterCache.cachedInstaller?.matchesManifest) throw new Error("cached installer sha512 does not match the manifest");

			// ---- real install: the app quits and the NSIS updater runs, then relaunches the new version
			const before = await fileFacts(testExe());
			const beforeUninstall = await uninstallEntry(newKeys[0]);
			note("requesting install", { before, beforeUninstall });
			const seenInstallerCmd = [];
			const sampler = setInterval(async () => {
				for (const p of await processTable()) if (/--updated/.test(p.cmd) && /Setup|updater|pending/i.test(p.path + p.cmd) && !seenInstallerCmd.some((s) => s.pid === p.pid)) seenInstallerCmd.push({ pid: p.pid, cmd: p.cmd, path: p.path });
			}, 700);
			const installStarted = Date.now();
			report.installRequest = await evalIn(page, "window.ao.updates.install().then((r) => r, (e) => ({ rejected: e.message }))");
			page.close();
			// quitAndInstall(false, true) is NOT silent: the NSIS wizard appears. Find the installer the app
			// started (isolated updater cache, --updated) and drive only the verified pages.
			let wizard;
			for (let i = 0; i < 90 && !wizard; i++) {
				await sleep(1000);
				const hit = (await processTable()).find((p) => /--updated/.test(p.cmd) && p.cmd.toLowerCase().includes(work.toLowerCase()));
				if (hit) wizard = hit;
			}
			if (!wizard) throw new Error("the app did not start an --updated installer from the isolated cache");
			report.installerProcess = { pid: wizard.pid, cmd: wizard.cmd };
			note("installer started by the app; driving wizard", report.installerProcess);
			const transcriptFile = join(out, `nsis-wizard-${report.scenario}.json`);
			const driver = await exec("powershell", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", join(here, "nsis-wizard-driver.ps1"), "-InstallerPid", String(wizard.pid), "-WorkDir", work, "-Identity", id, "-OutFile", transcriptFile]);
			report.wizard = { exit: driver.code, transcriptFile, ...(existsSync(transcriptFile) ? { status: JSON.parse(readFileSync(transcriptFile, "utf8").replace(/^\uFEFF/, "")).status } : {}) };
			note("wizard driver finished", report.wizard);
			if (driver.code !== 0) throw new Error(`wizard driver refused or failed (exit ${driver.code}): ${driver.out.slice(0, 300)}`);
			let relaunch;
			for (let i = 0; i < 240; i++) {
				await sleep(1000);
				const facts = await fileFacts(testExe());
				const table = await processTable();
				const running = table.filter((p) => p.path.toLowerCase() === testExe().toLowerCase());
				const freshStart = existsSync(join(dirname(pinRunFile), "logs", "desktop-lifecycle.jsonl"))
					? readFileSync(join(dirname(pinRunFile), "logs", "desktop-lifecycle.jsonl"), "utf8").trim().split("\n").map((l) => JSON.parse(l)).filter((e) => e.event === "app_start" && e.version === update).at(-1)
					: undefined;
				if (facts.ProductVersion === update && running.length > 0 && freshStart) { relaunch = { facts, runningPids: running.map((p) => p.pid), lifecycle: freshStart, seconds: Math.round((Date.now() - installStarted) / 1000) }; break; }
				if (aborted) throw new Error(aborted);
			}
			clearInterval(sampler);
			report.installerInvocations = seenInstallerCmd;
			report.relaunch = relaunch;
			if (!relaunch) throw new Error("new version was not installed and relaunched in time");
			const afterUninstall = await uninstallEntry(newKeys[0]);
			report.install = { onDiskBefore: before, onDiskAfter: relaunch.facts, uninstallEntryBefore: beforeUninstall, uninstallEntryAfter: afterUninstall };
			note("update installed and relaunched", report.install);
			await sleep(6000);
			const table = await processTable();
			const relaunched = table.filter((p) => p.path.toLowerCase().startsWith(instDir.toLowerCase() + sep));
			report.safety.relaunchedProcesses = relaunched.map((p) => ({ pid: p.pid, ppid: p.ppid, path: p.path }));
			report.safety.newPipesAfterRelaunch = listPipes().filter((n) => !pipesBefore.has(n));
			report.safety.relaunchPinnedPort = (await exec("netstat", ["-ano", "-p", "TCP"])).out.split("\n").filter((l) => l.includes(`:${pin.port} `) && l.includes("LISTENING")).map((l) => l.trim());
			note("relaunch isolation observed", { newPipes: report.safety.newPipesAfterRelaunch, listening: report.safety.relaunchPinnedPort });
			if (JSON.stringify(report.safety.newPipesAfterRelaunch).includes("ao-supervise\"") ) throw new Error("relaunched app created the shared default supervisor pipe");
			report.outcome = "update downloaded, hash-verified, installed and relaunched on the new version";
		}
	} catch (err) {
		report.error = String(err?.stack ?? err);
		step(`FAILED: ${err?.message}`);
	} finally {
		clearInterval(protocolWatch);
		report.feedRequests = feedLog.map((e) => `${e.status} ${e.method} ${e.url}${e.range ? ` [${e.range}]` : ""}${e.corrupted ? " (CORRUPTED by harness)" : ""} bytes=${e.bytes}`);
		const hostsSeen = {};
		for (const e of proxyLog) { const k = `${e.allowed ? "ALLOW" : "DENY"} ${String(e.target).replace(/^https?:\/\//, "").replace(/:\d{4,5}.*$/, "").replace(/^(127\.0\.0\.1|[a-z-]+\.localhost)$/, "<test-daemon-loopback>")}`; hostsSeen[k] = (hostsSeen[k] ?? 0) + 1; }
		report.proxyHostCounts = hostsSeen;
		report.upstreamDeniedHostnames = [...new Set(proxyLog.filter((e) => !e.allowed && UPSTREAM.test(String(e.target).replace(/^https?:\/\//, "").split(/[:/]/)[0])).map((e) => e.target))];
		// Blocked-request control (proves the instrumentation records an upstream attempt and forwards nothing):
		// a Node client using the same env-proxy mechanism deliberately asks for upstream update URLs.
		if (proxy && child) {
			const proxyUrl = `http://127.0.0.1:${proxy.port}`;
			const script = "for (const u of ['https://api.github.com/repos/Untrivial-ai/agent-orchestrator/releases','https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/desktop-version-floor.json']) fetch(u, { signal: AbortSignal.timeout(8000) }).then(r=>console.log(u,r.status),e=>console.log(u,'blocked',String(e.cause?.message||e.message)))";
			const before = proxyLog.length;
			const c = await new Promise((resolveP) => {
				const cp = spawn(process.execPath, ["-e", script], { env: { ...process.env, HTTPS_PROXY: proxyUrl, https_proxy: proxyUrl, NODE_USE_ENV_PROXY: "1" }, stdio: ["ignore", "pipe", "ignore"] });
				let o = "";
				cp.stdout.on("data", (d) => { o += d; });
				cp.on("close", () => resolveP(o));
				setTimeout(() => cp.kill(), 20000).unref();
			});
			report.blockedRequestControl = { clientOutput: c.trim().split("\n"), proxyRecorded: proxyLog.slice(before).map((e) => `${e.allowed ? "ALLOW" : "DENY"} ${e.kind} ${e.target}`) };
		}

		watcher?.stop();
		try { page?.close(); } catch { /* closed */ }
		cleanupLog.processes = await killTestTree();
		await sleep(1500);
		// ---- guarded uninstall of the TEST install only
		const unins = (existsSync(instDir) ? readdirSync(instDir, { withFileTypes: true }) : []).filter((d) => d.isFile() && /^Uninstall .*\.exe$/i.test(d.name)).map((d) => join(instDir, d.name));
		if (unins.length === 1 && unins[0].toLowerCase().startsWith(instDir.toLowerCase() + sep) && unins[0].includes(`ao-test-${id}`)) {
			const r = await exec(unins[0], ["/S", `_?=${instDir}`]);
			cleanupLog.uninstall = { exit: r.code };
			await sleep(1500);
		} else cleanupLog.uninstall = { skipped: `uninstaller not uniquely identified: ${JSON.stringify(unins)}` };
		let leftover = [...(await uninstallKeys())].filter((k) => !uninstallBefore.has(k));
		cleanupLog.leftoverAfterUninstaller = leftover;
		for (const key of leftover) {
			// Only a leaf key that is provably this test's own: its name, its UninstallString inside the
			// temp work dir, and no subkeys. Never a subtree delete, never a pre-existing key.
			const e = await uninstallEntry(key);
			const mine = e && String(e.DisplayName ?? "").startsWith(product) && String(e.UninstallString ?? "").toLowerCase().includes(work.toLowerCase());
			if (!mine) { cleanupLog.leftoverNotMine = [...(cleanupLog.leftoverNotMine ?? []), key]; continue; }
			const r = await ps(`$u='Registry::HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${key}'; $s='Registry::HKEY_CURRENT_USER\\Software\\${key}'; if (@(Get-ChildItem $u).Count -eq 0) { Remove-Item $u; if ((Test-Path $s) -and @(Get-ChildItem $s).Count -eq 0 -and ((Get-ItemProperty $s).InstallLocation -like '*ao-nsis-*')) { Remove-Item $s }; 'removed' } else { 'has-subkeys' }`);
			cleanupLog.removedLeftoverKey = { key, result: r.out.trim() };
		}
		const uninstallAfter = await uninstallKeys();
		cleanupLog.uninstallKeysAfterCleanup = [...uninstallAfter].filter((k) => !uninstallBefore.has(k));
		cleanupLog.softwareKeysNew = [...(await hkcuSoftwareKeys())].filter((k) => !softwareBefore.has(k));
		try { if (resolve(work).toLowerCase().startsWith(resolve(tmpdir()).toLowerCase() + sep) && /ao-nsis-/.test(work)) rmSync(work, { recursive: true, force: true }); cleanupLog.workDirRemoved = !existsSync(work); } catch (e) { cleanupLog.workDirRemoved = `failed: ${e.message}`; }
		report.cleanup = cleanupLog;
		report.safety.hostAfter = { health: await hostIdentity(hostRunFile, host.port), protocolHandler: await protocolCommand(), uninstallKeysUnchanged: cleanupLog.uninstallKeysAfterCleanup.length === 0, runKeyUnchanged: (await runKeyValues()) === runKeyBefore, hostProcessesAlive: (await processTable()).filter((p) => hostPids.includes(p.pid)).length + "/" + hostPids.length, watcherSamples: watcher?.samples.length, aborted: aborted ?? null };
		report.safety.hostUnchanged = report.safety.hostAfter.health.pid === hostBefore.pid && report.safety.hostAfter.health.status === 200 && report.safety.hostAfter.protocolHandler === protocolBefore;
		report.finishedAt = new Date().toISOString();
		writeFileSync(join(out, `nsis-${report.scenario}.json`), JSON.stringify(report, null, 2));
		try { proxy?.server.close(); feed?.server.close(); } catch { /* closed */ }
	}
	return report;
}

if (process.argv[1]?.endsWith("update-nsis-harness.mjs")) {
	main().then((r) => { process.stdout.write(`${JSON.stringify({ outcome: r.outcome, error: r.error, hostUnchanged: r.safety?.hostUnchanged }, null, 2)}\n`); process.exit(r.error ? 1 : 0); }).catch((e) => { process.stderr.write(`${e.stack}\n`); process.exit(1); });
}
