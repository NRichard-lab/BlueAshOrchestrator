// @vitest-environment node
import { describe, it, expect, vi } from "vitest";
import { join } from "node:path";
import { derivedPipes, isolationViolations, pipeSuffixFromRunFile, planIsolation, verifiedKillList, watchHost } from "./update-egress-guards.mjs";

const host = {
	port: 3001,
	runFile: "C:\\Users\\u\\.ao\\running.json",
	dataDirs: ["C:\\Users\\u\\.ao", "D:\\Apps\\Orchestrator\\data"],
	installDir: "D:\\Apps\\Orchestrator",
};
const hostPipes = ["ao-supervise", "ao-browser", "ao-browser-chrome", "ao-browser-stream--ao"];

describe("pipe derivation mirrors the daemon and app", () => {
	it("maps default/installed directories to the shared host pipe names", () => {
		expect(derivedPipes("C:\\Users\\u\\.ao\\running.json")).toEqual(hostPipes);
		expect(pipeSuffixFromRunFile("D:\\Apps\\Orchestrator\\data\\running.json")).toBe("");
		expect(pipeSuffixFromRunFile("C:\\t\\aotest-ab12\\running.json")).toBe("-aotest-ab12");
	});
});

describe("isolationViolations", () => {
	it("REGRESSION: rejects the configuration that stopped the host daemon (<tmp>/home/.ao/running.json)", () => {
		const old = { runFile: "C:\\tmp\\ao-egress-X\\home\\.ao\\running.json", dataDir: "C:\\tmp\\ao-egress-X\\home\\.ao\\data", port: 3001 };
		const v = isolationViolations(old, host, hostPipes);
		expect(v.join("\n")).toMatch(/maps to the default pipe names/);
		expect(v.join("\n")).toMatch(/equals the host daemon port/);
		expect(v.join("\n")).toMatch(/pipe ao-supervise already exists/);
	});

	it("accepts a unique run dir, separate port, and no pipe collision", () => {
		const plan = planIsolation("C:\\tmp\\work", 34567);
		expect(plan.id).toMatch(/^aotest-[0-9a-f]{8}$/);
		expect(isolationViolations(plan, host, hostPipes)).toEqual([]);
		expect(planIsolation("C:\\tmp\\work", 34567).id).not.toBe(plan.id);
	});

	it("rejects ports outside the safe range, the LAN port, state inside host paths, and a taken pipe", () => {
		const base = planIsolation("C:\\tmp\\work", 34567);
		expect(isolationViolations({ ...base, port: 3011 }, host)).not.toEqual([]);
		expect(isolationViolations({ ...base, port: 80 }, host)).not.toEqual([]);
		const inside = { ...base, runFile: "D:\\Apps\\Orchestrator\\data\\aotest-1\\running.json", dataDir: "D:\\Apps\\Orchestrator\\data\\aotest-1\\data" };
		expect(isolationViolations(inside, host).join()).toMatch(/protected host path/);
		expect(isolationViolations(base, host, [`ao-supervise-${base.id}`]).join()).toMatch(/already exists/);
		expect(isolationViolations({ ...base, runFile: host.runFile }, host).join()).toMatch(/host run file/);
	});
});

describe("verifiedKillList", () => {
	const testDir = "C:\\tmp\\out\\App-win32-x64";
	const table = [
		{ pid: 10, ppid: 1, path: `${testDir}\\agent-orchestrator.exe` },
		{ pid: 11, ppid: 10, path: `${testDir}\\resources\\daemon\\ao.exe` },
		{ pid: 12, ppid: 11, path: "C:\\Windows\\System32\\cmd.exe" },
		{ pid: 20, ppid: 1, path: "D:\\Apps\\Orchestrator\\agent-orchestrator.exe" },
		{ pid: 21, ppid: 20, path: "D:\\Apps\\Orchestrator\\resources\\daemon\\ao.exe" },
		{ pid: 30, ppid: 10, path: "D:\\Apps\\Orchestrator\\resources\\daemon\\ao.exe" },
	];

	it("kills only the test app's own executables and refuses everything else, including host look-alikes in its tree", () => {
		const { kill, refuse } = verifiedKillList({ table, rootPid: 10, testAppDir: testDir, installDir: "D:\\Apps\\Orchestrator", hostPids: [20, 21] });
		expect(kill.map((p) => p.pid).sort()).toEqual([10, 11]);
		expect(refuse.map((p) => p.pid).sort()).toEqual([12, 30]);
	});

	it("never selects the host tree even if asked to root there", () => {
		const { kill } = verifiedKillList({ table, rootPid: 20, testAppDir: testDir, installDir: "D:\\Apps\\Orchestrator", hostPids: [20, 21] });
		expect(kill).toEqual([]);
	});

	it("matches paths case-insensitively and not by prefix alone", () => {
		const t = [{ pid: 1, ppid: 0, path: `${testDir.toUpperCase()}\\a.exe` }, { pid: 2, ppid: 1, path: `${testDir}-evil\\a.exe` }];
		const { kill, refuse } = verifiedKillList({ table: t, rootPid: 1, testAppDir: testDir, installDir: "D:\\Apps\\Orchestrator" });
		expect(kill.map((p) => p.pid)).toEqual([1]);
		expect(refuse.map((p) => p.pid)).toEqual([2]);
	});
});

describe("verifiedKillList with the test build nested inside the host install folder", () => {
	const install = "D:\\Apps\\Orchestrator";
	const testDir = `${install}\\data\\worktrees\\w4\\out\\App-win32-x64`;
	it("still kills the test app and refuses the host executables", () => {
		const table = [
			{ pid: 10, ppid: 1, path: `${testDir}\\agent-orchestrator.exe` },
			{ pid: 11, ppid: 10, path: `${testDir}\\resources\\daemon\\ao.exe` },
			{ pid: 12, ppid: 10, path: `${install}\\resources\\daemon\\ao.exe` },
			{ pid: 20, ppid: 1, path: `${install}\\agent-orchestrator.exe` },
		];
		const { kill, refuse } = verifiedKillList({ table, rootPid: 10, testAppDir: testDir, installDir: install, hostPids: [20] });
		expect(kill.map((p) => p.pid).sort()).toEqual([10, 11]);
		expect(refuse.map((p) => p.pid)).toEqual([12]);
		expect(verifiedKillList({ table, rootPid: 20, testAppDir: testDir, installDir: install, hostPids: [20] }).kill).toEqual([]);
	});
});

describe("watchHost", () => {
	it("trips when the host daemon pid changes or healthz stops answering, and not before", async () => {
		vi.useFakeTimers();
		try {
			const answers = [{ pid: 7, status: 200 }, { pid: 7, status: null }, { pid: 7, status: null }];
			let i = 0;
			const onChange = vi.fn();
			const w = watchHost({ hostRunFile: "x", port: 1, intervalMs: 100, onChange, identity: async () => answers[Math.min(i++, answers.length - 1)] });
			await vi.advanceTimersByTimeAsync(0);
			await vi.advanceTimersByTimeAsync(100);
			expect(onChange).not.toHaveBeenCalled(); // one bad sample is not enough
			await vi.advanceTimersByTimeAsync(100);
			expect(onChange).toHaveBeenCalledTimes(1);
			expect(onChange.mock.calls[0][0]).toMatch(/host daemon changed/);
			w.stop();
			expect(w.samples.length).toBeGreaterThanOrEqual(3);
		} finally {
			vi.useRealTimers();
		}
	});

	it("trips on a different pid with healthy status", async () => {
		const seq = [{ pid: 1, status: 200 }, { pid: 2, status: 200 }];
		let i = 0;
		const onChange = vi.fn();
		const w = watchHost({ hostRunFile: "x", port: 1, intervalMs: 5, onChange, identity: async () => seq[Math.min(i++, 1)] });
		await new Promise((r) => setTimeout(r, 60));
		w.stop();
		expect(onChange).toHaveBeenCalledTimes(1);
	});
});

void join;
