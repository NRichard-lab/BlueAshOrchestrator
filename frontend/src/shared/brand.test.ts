// @vitest-environment node
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { BLUEASHAI_BRAND, BRAND, LEGACY_BRAND, resolveBrand } from "./brand";
import { supervisorPipeFromRunFile } from "../main/daemon-run-file";
import { brandDefine } from "../../vite.brand";

describe("product profiles", () => {
	it("source/unit-test builds are legacy Orchestrator (no AO_BRAND compiled in)", () => {
		expect(BRAND).toBe(LEGACY_BRAND);
		expect(resolveBrand(undefined)).toBe(LEGACY_BRAND);
		expect(resolveBrand("")).toBe(LEGACY_BRAND);
		expect(resolveBrand("something-else")).toBe(LEGACY_BRAND);
		expect(resolveBrand("blueashai")).toBe(BLUEASHAI_BRAND);
	});

	it("BlueAshAI shares no identity value with Orchestrator, so the two can coexist", () => {
		const b = BLUEASHAI_BRAND;
		const l = LEGACY_BRAND;
		for (const key of [
			"displayName", "executableName", "packagerName", "appId", "appUserModelId", "installerArtifactName",
			"windowsAssetName", "windowsInstallDirName", "updaterCacheDirName", "stateDirName", "defaultPort", "protocolScheme",
		] as const) {
			expect(b[key], key).not.toBe(l[key]);
		}
		expect(b.protocolScheme).not.toBe("ao-app");
		expect(b.stateDirName.startsWith(".")).toBe(true);
		expect(b.defaultPort).not.toBe(3001);
		expect(b.defaultPort).not.toBe(3011);
		expect(b.executableName.toLowerCase()).not.toBe("agent-orchestrator");
	});

	it("BlueAshAI visible names and installer/asset names follow the product name", () => {
		const b = BLUEASHAI_BRAND;
		expect(b.displayName).toBe("BlueAshAI");
		expect(b.executableName).toBe("BlueAshAI");
		expect(b.installerArtifactName).toBe("BlueAshAI Setup ${version}.${ext}");
		expect(b.windowsAssetName).toBe("BlueAshAI-win32-x64.exe");
		expect(b.windowsInstallDirName).toBe("BlueAshAI");
	});

	it("a BlueAshAI default run file yields Windows pipes distinct from the installed Orchestrator's", () => {
		const legacyPipe = "\\\\.\\pipe\\ao-supervise";
		const run = (home: string, dir: string) => `${home}\\${dir}\\running.json`;
		if (process.platform === "win32") {
			expect(supervisorPipeFromRunFile(run("C:\\Users\\u", LEGACY_BRAND.stateDirName))).toBe(legacyPipe);
			const blue = supervisorPipeFromRunFile(run("C:\\Users\\u", BLUEASHAI_BRAND.stateDirName));
			expect(blue).toBe("\\\\.\\pipe\\ao-supervise--blueashai");
			expect(blue).not.toBe(legacyPipe);
		}
	});

	it("is chosen at build time only: the Vite define compiles an empty or blueashai constant", () => {
		expect(brandDefine({ AO_BRAND: "blueashai" } as NodeJS.ProcessEnv)).toEqual({ "process.env.AO_BRAND": '"blueashai"' });
		expect(brandDefine({} as NodeJS.ProcessEnv)).toEqual({ "process.env.AO_BRAND": '""' });
		expect(brandDefine({ AO_BRAND: "evil" } as NodeJS.ProcessEnv)).toEqual({ "process.env.AO_BRAND": '""' });
	});

	it("the Go ldflags stamped by build-daemon.mjs name the same values (single JSON source)", () => {
		const script = readFileSync(path.join(__dirname, "..", "..", "scripts", "build-daemon.mjs"), "utf8");
		for (const field of ["StateDirName", "Port", "WindowsAssetName", "WindowsInstallDirName", "WindowsExeName"]) {
			expect(script).toContain(`internal/brand`);
			expect(script).toContain(`.${field}=`);
		}
		expect(script).toContain("brands.json");
		const json = JSON.parse(readFileSync(path.join(__dirname, "brands.json"), "utf8"));
		expect(json.blueashai.stateDirName).toBe(BLUEASHAI_BRAND.stateDirName);
	});
});
