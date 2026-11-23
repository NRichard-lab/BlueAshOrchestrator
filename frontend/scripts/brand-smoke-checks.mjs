// Pure helpers for the BlueAshAI isolated smoke (scripts/brand-smoke.mjs): outbound-host attribution,
// native dialog detection, and the strings the renderer must no longer show. Kept separate so they can be
// unit-tested without installing anything (scripts/brand-smoke-checks.test.mjs).
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Hosts that can only be an update/version check of the app or of an agent CLI (f3). A request to any of
// these from the BlueAshAI process tree means an inherited update path is still alive. `*.x` matches any
// sub-domain of x. api2.cursor.sh and chatgpt.com are account/usage probes, not version checks, so they are
// deliberately NOT listed: they are reported as "other" and are not claimed to be absent.
export const UPDATE_HOSTS = [
	"github.com",
	"api.github.com",
	"objects.githubusercontent.com",
	"github-releases.githubusercontent.com",
	"downloads.claude.ai",
	"releases.openai.com",
	"x.ai",
	"cursor.com",
	"registry.npmjs.org",
	"pypi.org",
	"formulae.brew.sh",
	"static.ampcode.com",
	"static.devin.ai",
	"opencode.ai",
	"api.meta.ai",
	"prod.download.cli.kiro.dev",
	"code.kimi.com",
	"releases.fx.sh",
	"*.r2.dev",
	"antigravity-cli-auto-updater-*.run.app",
];
// Remote analytics / crash reporting (separate switch: remoteTelemetryEnabled).
export const TELEMETRY_HOSTS = ["us.i.posthog.com", "us-assets.i.posthog.com", "*.posthog.com", "*.ingest.us.sentry.io", "*.ingest.sentry.io", "*.sentry.io"];
// The LiteLLM model price catalog (separate switch: pricingCatalogSyncEnabled). The product's only use of
// raw.githubusercontent.com; a CONNECT shows no path, so the host is attributed to pricing and a nonzero
// count would still be a failure of one of the two switches.
export const PRICING_HOSTS = ["raw.githubusercontent.com"];

function matches(host, pattern) {
	const h = host.toLowerCase();
	const p = pattern.toLowerCase();
	if (p.startsWith("*.")) return h === p.slice(2) || h.endsWith(p.slice(1));
	if (p.includes("*")) return new RegExp(`^${p.split("*").map((s) => s.replace(/[.+?^${}()|[\]\\]/g, "\\$&")).join("[a-z0-9.-]*")}$`).test(h);
	return h === p || h.endsWith(`.${p}`);
}

/** "update" | "telemetry" | "pricing" | "other" for one hostname (no port). */
export function classifyHost(host) {
	if (PRICING_HOSTS.some((p) => matches(host, p))) return "pricing";
	if (TELEMETRY_HOSTS.some((p) => matches(host, p))) return "telemetry";
	if (UPDATE_HOSTS.some((p) => matches(host, p))) return "update";
	return "other";
}

export function hostOf(target) {
	return String(target).replace(/^[a-z]+:\/\//i, "").replace(/[:/].*$/, "").toLowerCase();
}

/** Attribution of every proxy event (controls excluded) by category. */
export function summarizeNetwork(proxyLog) {
	const hosts = { update: new Set(), telemetry: new Set(), pricing: new Set(), other: new Set() };
	for (const e of proxyLog) {
		if (e.control) continue;
		const host = hostOf(e.target);
		if (e.loopback || host === "127.0.0.1" || host === "localhost" || host.endsWith(".localhost")) continue;
		hosts[classifyHost(host)].add(host);
	}
	return Object.fromEntries(Object.entries(hosts).map(([k, v]) => [k, [...v].sort()]));
}

// Text the renderer must not contain once updates are compiled out (f5). Matched case-insensitively
// against document.body.innerText on every screen the smoke reaches.
export const FORBIDDEN_UI_TEXT = [
	"check for updates",
	"checking for updates",
	"update available",
	"restart to update",
	"restart to install",
	"updates are failing",
	"automatic updates",
	"release channel",
	"harness update",
	"up to date automatically",
];

export function forbiddenUiText(text) {
	const lower = String(text ?? "").toLowerCase();
	return FORBIDDEN_UI_TEXT.filter((s) => lower.includes(s));
}

// Lists visible top-level windows owned by a set of process ids (native dialogs such as
// dialog.showMessageBox are top-level windows of class "#32770" or a TaskDialog). The first-run prompt
// "Keep Orchestrator up to date automatically?" is exactly such a window.
const ENUM_WINDOWS_PS1 = `
param([string]$Pids)
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System; using System.Text; using System.Collections.Generic; using System.Runtime.InteropServices;
public static class SmokeWin {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc p, IntPtr l);
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
  public static List<string> List(HashSet<uint> pids) {
    var r = new List<string>();
    EnumWindows((h, l) => {
      uint pid; GetWindowThreadProcessId(h, out pid);
      if (!pids.Contains(pid)) return true;
      var t = new StringBuilder(512); var c = new StringBuilder(256);
      GetWindowText(h, t, 512); GetClassName(h, c, 256);
      r.Add(pid + "|" + (IsWindowVisible(h) ? "1" : "0") + "|" + c + "|" + t);
      return true;
    }, IntPtr.Zero);
    return r;
  }
}
'@
$set = New-Object 'System.Collections.Generic.HashSet[uint32]'
foreach ($p in $Pids.Split(',')) { if ($p) { [void]$set.Add([uint32]$p) } }
[SmokeWin]::List($set) | ForEach-Object { $_ }
`;

/** Visible windows of the given pids: [{ pid, visible, className, title }]. */
export function listWindows(pids) {
	const dir = mkdtempSync(join(tmpdir(), "ao-smoke-win-"));
	const script = join(dir, "enum.ps1");
	writeFileSync(script, ENUM_WINDOWS_PS1);
	return new Promise((resolve) => {
		const c = spawn("powershell", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-Pids", [...pids].join(",")], { stdio: ["ignore", "pipe", "pipe"] });
		let out = "";
		c.stdout.on("data", (d) => { out += d; });
		c.stderr.on("data", (d) => { out += d; });
		c.on("error", () => { rmSync(dir, { recursive: true, force: true }); resolve({ error: "powershell failed", windows: [] }); });
		c.on("close", () => {
			rmSync(dir, { recursive: true, force: true });
			const windows = out.split(/\r?\n/).map((l) => l.trim()).filter((l) => /^\d+\|[01]\|/.test(l)).map((l) => {
				const [pid, visible, className, ...title] = l.split("|");
				return { pid: Number(pid), visible: visible === "1", className, title: title.join("|") };
			});
			resolve({ windows, raw: windows.length === 0 && out.trim() ? out.trim().slice(0, 400) : undefined });
		});
	});
}

/** Native message boxes / task dialogs among the windows (what a first-run prompt looks like). */
export function dialogsAmong(windows) {
	return windows.filter((w) => w.visible && (w.className === "#32770" || /TaskDialog|DirectUIHWND/i.test(w.className)));
}

/** Events recorded by the updater's local diagnostics file (<state>/logs/updater.jsonl), [] when absent. */
export function readUpdaterDiagnostics(stateDir) {
	const file = join(stateDir, "logs", "updater.jsonl");
	if (!existsSync(file)) return [];
	return readFileSync(file, "utf8").split(/\r?\n/).filter(Boolean).map((l) => { try { return JSON.parse(l); } catch { return { event: "unparseable" }; } });
}
