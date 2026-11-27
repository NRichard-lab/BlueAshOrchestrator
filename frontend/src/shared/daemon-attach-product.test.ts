import { describe, expect, it, vi } from "vitest";
import {
	DAEMON_SERVICE_NAME,
	type DaemonProbe,
	type DaemonProber,
	foreignProductError,
	foreignProductStatus,
	parseDaemonProbe,
	resolveDaemonFromPort,
	resolveDaemonFromRunFile,
} from "./daemon-attach";

// The unit-test build is the legacy (Orchestrator) profile, so "blueashai" is the foreign product
// here. A BlueAshAI build runs the same code with the roles swapped.
const FOREIGN = "blueashai";
const OWN = "legacy";

function probeOf(endpoint: "healthz" | "readyz", pid: number, product?: string): DaemonProbe {
	return {
		status: endpoint === "healthz" ? "ok" : "ready",
		service: DAEMON_SERVICE_NAME,
		pid,
		executablePath: "D:\\Program Files\\BlueAshAI\\resources\\daemon\\ao.exe",
		...(product ? { product } : {}),
	};
}

function fakeProbe(port: number, pid: number, product?: string): { probe: DaemonProber; calls: string[] } {
	const calls: string[] = [];
	const probe: DaemonProber = (askedPort, endpoint) => {
		calls.push(`${askedPort}:${endpoint}`);
		return Promise.resolve(askedPort === port ? probeOf(endpoint, pid, product) : null);
	};
	return { probe, calls };
}

describe("parseDaemonProbe product", () => {
	it("carries the product id a daemon reports", () => {
		const parsed = parseDaemonProbe("healthz", { status: "ok", service: DAEMON_SERVICE_NAME, pid: 7, product: " blueashai " });
		expect(parsed?.product).toBe("blueashai");
	});

	it("leaves it unset for a daemon that predates the field", () => {
		const parsed = parseDaemonProbe("healthz", { status: "ok", service: DAEMON_SERVICE_NAME, pid: 7 });
		expect(parsed).not.toBeNull();
		expect(parsed && "product" in parsed).toBe(false);
	});
});

describe("foreignProductError", () => {
	it("accepts this product and daemons that report none", () => {
		expect(foreignProductError({ pid: 7, product: OWN })).toBeNull();
		expect(foreignProductError({ pid: 7 })).toBeNull();
	});

	it("names both products, the daemon and the settings to check", () => {
		const message = foreignProductError({ pid: 9600, product: FOREIGN }, 3101);
		expect(message).toContain("port 3101");
		expect(message).toContain("pid 9600");
		expect(message).toContain("belongs to BlueAshAI");
		expect(message).toContain("not Orchestrator.inc");
		expect(message).toContain("AO_RUN_FILE");
		expect(message).toContain("will not attach to it or stop it");
	});
});

describe("attach decisions for a daemon from another product", () => {
	// The reproduced failure: the run file names the other product's daemon (inherited coordinates).
	it("run-file path: reports foreign_product without waiting on readiness or the path identity check", async () => {
		const { probe, calls } = fakeProbe(3101, 9600, FOREIGN);
		const identityError = vi.fn(() => "path mismatch");
		const status = await resolveDaemonFromRunFile({
			runFileContents: JSON.stringify({ pid: 9600, port: 3101, startedAt: "2026-11-27T02:49:54Z" }),
			isProcessAlive: () => true,
			probe,
			identityError,
		});
		expect(status).toMatchObject({ state: "error", code: "foreign_product", port: 3101, pid: 9600 });
		expect(status?.message).toContain("belongs to BlueAshAI");
		expect(calls).toEqual(["3101:healthz"]);
		expect(identityError).not.toHaveBeenCalled();
	});

	it("port path: a foreign daemon on the expected port is reported, not attached", async () => {
		const { probe, calls } = fakeProbe(3001, 4242, FOREIGN);
		const status = await resolveDaemonFromPort({ expectedPort: 3001, probe, identityError: () => null });
		expect(status).toMatchObject({ state: "error", code: "foreign_product", port: 3001, pid: 4242 });
		expect(calls).toEqual(["3001:healthz"]);
	});

	it("still attaches to this product's daemon", async () => {
		const { probe } = fakeProbe(3001, 4242, OWN);
		const status = await resolveDaemonFromPort({ expectedPort: 3001, probe, identityError: () => null });
		expect(status).toMatchObject({ state: "ready", port: 3001, pid: 4242 });
	});

	it("a daemon that reports no product falls through to the existing identity check", async () => {
		const { probe } = fakeProbe(3001, 4242);
		const ok = await resolveDaemonFromPort({ expectedPort: 3001, probe, identityError: () => null });
		expect(ok).toMatchObject({ state: "ready" });
		const mismatch = await resolveDaemonFromPort({ expectedPort: 3001, probe, identityError: () => "not our binary" });
		expect(mismatch).toMatchObject({ state: "error", code: "identity_mismatch", message: "not our binary" });
	});

	it("foreignProductStatus is null for own and unreported products", () => {
		expect(foreignProductStatus(3001, probeOf("healthz", 1, OWN))).toBeNull();
		expect(foreignProductStatus(3001, probeOf("healthz", 1))).toBeNull();
	});
});
