import { describe, expect, it } from "vitest";
import { resolveTestIdentity } from "./test-identity";
import { resolveAppUpdateYml } from "./update-feed";

describe("test identity", () => {
	it("is absent for production builds", () => {
		expect(resolveTestIdentity({})).toBeUndefined();
		expect(resolveTestIdentity({ AO_TEST_IDENTITY: "  " })).toBeUndefined();
	});

	it("derives a distinct executable, appId, product, installer and updater cache", () => {
		const id = resolveTestIdentity({ AO_TEST_IDENTITY: "upd1a2" });
		expect(id).toMatchObject({
			executableName: "ao-test-upd1a2",
			appId: "dev.agent-orchestrator.test.upd1a2",
			productName: "AO Update Test upd1a2",
			updaterCacheDirName: "ao-test-upd1a2-updater",
		});
		for (const v of [id!.executableName, id!.appId, id!.productName, id!.packagerName, id!.updaterCacheDirName]) {
			expect(v.toLowerCase()).not.toBe("agent-orchestrator");
			expect(v.toLowerCase()).not.toBe("dev.agent-orchestrator.desktop");
			expect(v.toLowerCase()).not.toBe("orchestrator.inc");
			expect(v).not.toBe("agent-orchestrator-updater");
		}
		expect(id!.installerArtifactName).toContain("${version}");
	});

	it("rejects malformed ids and any combination with publishing", () => {
		for (const bad of ["a", "Upper1", "1abc", "has-dash", "toolongidentity1"]) {
			expect(() => resolveTestIdentity({ AO_TEST_IDENTITY: bad })).toThrow(/AO_TEST_IDENTITY/);
		}
		expect(() => resolveTestIdentity({ AO_TEST_IDENTITY: "upd1a2", AO_RELEASE_REPO: "x/y" })).toThrow(/publish/);
		expect(() => resolveTestIdentity({ AO_TEST_IDENTITY: "upd1a2", AO_PUBLISHED_BUILD: "1" })).toThrow(/publish/);
	});

	it("bakes the identity's updater cache dir into app-update.yml, production unchanged", () => {
		const origin = "https://updates.example.test";
		expect(resolveAppUpdateYml({ AO_UPDATE_ORIGIN: origin, AO_TEST_IDENTITY: "upd1a2" })).toContain("updaterCacheDirName: ao-test-upd1a2-updater");
		expect(resolveAppUpdateYml({ AO_UPDATE_ORIGIN: origin })).toContain("updaterCacheDirName: agent-orchestrator-updater");
	});
});

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { instancePinFor, testInstallerInclude } from "./test-identity";
import { parseInstancePin } from "../src/main/instance-pin";
import viteMain from "../vite.main.config";

describe("test installer include and compile-time gating", () => {
	const production = readFileSync(join(__dirname, "..", "resources", "installer.nsh"), "utf8");
	const id = resolveTestIdentity({ AO_TEST_IDENTITY: "upd1a2" })!;

	it("adds the pin file only inside customInstall, leaving the production include otherwise intact", () => {
		const out = testInstallerInclude(id, production);
		expect(out).toContain('ao-instance.json');
		expect(production).not.toContain("ao-instance.json");
		const customInstall = out.slice(out.indexOf("!macro customInstall"), out.indexOf("!macroend", out.indexOf("!macro customInstall")));
		expect(customInstall).toContain("ao-instance.json");
		expect(customInstall).toContain('"identity": "upd1a2"');
		expect(customInstall).toContain('"skipProtocolRegistration": true');
		// Everything outside the macro is byte-identical to the production include.
		expect(out.replace(customInstall, "")).toBe(production.replace(production.slice(production.indexOf("!macro customInstall"), production.indexOf("!macroend", production.indexOf("!macro customInstall"))), ""));
	});

	it("derives a pin the app's own validator accepts, unique per identity", () => {
		const pin = instancePinFor(id);
		const json = JSON.stringify({ version: 1, identity: id.id, runFile: pin.runFile, port: pin.port, skipProtocolRegistration: true, telemetryRemote: "off" });
		expect(parseInstancePin(json, "C:/inst", id.id).ok).toBe(true);
		const other = resolveTestIdentity({ AO_TEST_IDENTITY: "zzzz9" })!;
		expect(instancePinFor(other).runFile).not.toBe(pin.runFile);
	});

	it("compiles an EMPTY identity into release builds (the pin code is inert there)", () => {
		const define = (viteMain as { define?: Record<string, string> }).define ?? {};
		expect(define["process.env.AO_TEST_BUILD_IDENTITY"]).toBe(process.env.AO_TEST_IDENTITY ? JSON.stringify(process.env.AO_TEST_IDENTITY) : '""');
	});
});
