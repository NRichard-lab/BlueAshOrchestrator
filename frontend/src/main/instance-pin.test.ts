// @vitest-environment node
import path from "node:path";
import { describe, expect, it } from "vitest";
import { applyTestInstancePin, parseInstancePin } from "./instance-pin";

const dir = path.resolve("/apps/ao-test");
const good = {
	version: 1,
	identity: "updt1a",
	runFile: "instance-updt1a/running.json",
	port: 41234,
	skipProtocolRegistration: true,
	telemetryRemote: "off",
};
const text = (o: object) => JSON.stringify(o);

describe("parseInstancePin", () => {
	it("accepts a pin bound to this build, with a unique run dir and a safe port", () => {
		const r = parseInstancePin(text(good), dir, "updt1a");
		expect(r.ok).toBe(true);
		if (r.ok) {
			expect(r.pin.runFile).toBe(path.resolve(dir, "instance-updt1a/running.json"));
			expect(r.pin.port).toBe(41234);
		}
	});

	it.each([
		["wrong identity", { ...good, identity: "other" }],
		["wrong version", { ...good, version: 2 }],
		["protocol registration allowed", { ...good, skipProtocolRegistration: false }],
		["telemetry not off", { ...good, telemetryRemote: "posthog" }],
		["default run dir .ao", { ...good, runFile: "x/.ao/running.json" }],
		["data run dir", { ...good, runFile: "data/running.json" }],
		["dev run dir", { ...good, runFile: "dev/running.json" }],
		["host port", { ...good, port: 3001 }],
		["lan port", { ...good, port: 3011 }],
		["low port", { ...good, port: 80 }],
		["string port", { ...good, port: "41234" }],
		["no run file", { ...good, runFile: "" }],
	])("rejects %s", (_name, pin) => {
		expect(parseInstancePin(text(pin), dir, "updt1a").ok).toBe(false);
	});

	it("rejects malformed JSON", () => {
		expect(parseInstancePin("{", dir, "updt1a").ok).toBe(false);
	});
});

describe("applyTestInstancePin", () => {
	it("is inert in a release build: constant empty, file never read, env untouched", () => {
		const env: NodeJS.ProcessEnv = {};
		let reads = 0;
		const err = applyTestInstancePin({ buildIdentity: "", execDir: dir, env, readText: () => { reads++; return text(good); } });
		expect(err).toBeNull();
		expect(reads).toBe(0);
		expect(env).toEqual({});
	});

	it("pins a test build, overriding inherited defaults", () => {
		const env: NodeJS.ProcessEnv = { AO_RUN_FILE: "C:/Users/u/.ao/running.json", AO_PORT: "3001" };
		const err = applyTestInstancePin({ buildIdentity: "updt1a", execDir: dir, env, readText: () => text(good) });
		expect(err).toBeNull();
		expect(env.AO_RUN_FILE).toBe(path.resolve(dir, "instance-updt1a/running.json"));
		expect(env.AO_PORT).toBe("41234");
		expect(env.AO_SKIP_PROTOCOL_REGISTRATION).toBe("1");
		expect(env.AO_TELEMETRY_REMOTE).toBe("off");
	});

	it("refuses to start a test build without a valid pin, leaving env untouched", () => {
		const env: NodeJS.ProcessEnv = { AO_PORT: "3001" };
		expect(applyTestInstancePin({ buildIdentity: "updt1a", execDir: dir, env, readText: () => { throw new Error("ENOENT"); } })).toMatch(/missing/);
		expect(applyTestInstancePin({ buildIdentity: "updt1a", execDir: dir, env, readText: () => text({ ...good, identity: "x" }) })).toMatch(/rejected/);
		expect(applyTestInstancePin({ buildIdentity: "updt1a", execDir: null, env })).toMatch(/installed location/);
		expect(env).toEqual({ AO_PORT: "3001" });
	});
});
