// @vitest-environment node
import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { classifyHost, dialogsAmong, forbiddenUiText, hostOf, readUpdaterDiagnostics, summarizeNetwork } from "./brand-smoke-checks.mjs";

describe("outbound host attribution", () => {
	it("keeps update, telemetry, pricing and other hosts apart", () => {
		for (const h of ["api.github.com", "github.com", "downloads.claude.ai", "releases.openai.com", "cursor.com", "registry.npmjs.org", "pypi.org", "formulae.brew.sh", "foo.r2.dev", "antigravity-cli-auto-updater-974169037036.us-central1.run.app", "x.ai"]) {
			expect(classifyHost(h), h).toBe("update");
		}
		expect(classifyHost("us.i.posthog.com")).toBe("telemetry");
		expect(classifyHost("o4511955966361600.ingest.us.sentry.io")).toBe("telemetry");
		expect(classifyHost("raw.githubusercontent.com")).toBe("pricing");
		// Account/usage probes are not version checks and must not be hidden inside the update bucket.
		expect(classifyHost("api2.cursor.sh")).toBe("other");
		expect(classifyHost("chatgpt.com")).toBe("other");
		// Look-alikes must not match.
		expect(classifyHost("notgithub.com")).toBe("other");
		expect(classifyHost("github.com.evil.example")).toBe("other");
	});

	it("summarizes proxy events by category, ignoring loopback and the negative control", () => {
		const log = [
			{ target: "api.github.com:443", allowed: false },
			{ target: "us.i.posthog.com:443", allowed: false },
			{ target: "raw.githubusercontent.com:443", allowed: false },
			{ target: "api2.cursor.sh:443", allowed: false },
			{ target: "127.0.0.1:3101", allowed: true, loopback: true },
			{ target: "http://127.0.0.1:3101/x", allowed: false },
			{ target: "api.github.com:443", allowed: false, control: true },
		];
		expect(summarizeNetwork(log)).toEqual({ update: ["api.github.com"], telemetry: ["us.i.posthog.com"], pricing: ["raw.githubusercontent.com"], other: ["api2.cursor.sh"] });
		expect(summarizeNetwork([{ target: "api.github.com:443", control: true }]).update).toEqual([]);
		expect(hostOf("https://Example.COM:8443/a")).toBe("example.com");
	});
});

describe("renderer text and dialogs", () => {
	it("flags every update affordance string and nothing else", () => {
		expect(forbiddenUiText("Settings\nCheck for updates\nRestart to update")).toEqual(["check for updates", "restart to update"]);
		expect(forbiddenUiText("Keep Orchestrator up to date automatically?")).toEqual(["up to date automatically"]);
		expect(forbiddenUiText("Projects  Sessions  Settings  Harness")).toEqual([]);
		expect(forbiddenUiText(undefined)).toEqual([]);
	});

	it("recognizes native message boxes and ignores ordinary windows and hidden ones", () => {
		const windows = [
			{ pid: 1, visible: true, className: "Chrome_WidgetWin_1", title: "BlueAshAI" },
			{ pid: 1, visible: true, className: "#32770", title: "BlueAshAI" },
			{ pid: 1, visible: false, className: "#32770", title: "hidden" },
		];
		expect(dialogsAmong(windows).map((w) => w.title)).toEqual(["BlueAshAI"]);
		expect(dialogsAmong([])).toEqual([]);
	});
});

describe("updater diagnostics reader", () => {
	it("returns [] without a file and parses jsonl otherwise", () => {
		const dir = mkdtempSync(join(tmpdir(), "ao-diag-"));
		try {
			expect(readUpdaterDiagnostics(dir)).toEqual([]);
			mkdirSync(join(dir, "logs"));
			writeFileSync(join(dir, "logs", "updater.jsonl"), `${JSON.stringify({ event: "automatic-updates-started" })}\nnot json\n`);
			expect(readUpdaterDiagnostics(dir).map((e) => e.event)).toEqual(["automatic-updates-started", "unparseable"]);
		} finally {
			rmSync(dir, { recursive: true, force: true });
		}
	});
});
