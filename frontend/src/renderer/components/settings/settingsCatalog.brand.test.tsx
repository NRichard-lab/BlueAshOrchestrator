import { afterEach, describe, expect, it, vi } from "vitest";

const context = {
	cloudEnabled: false,
	teams: "none",
	selfServeBilling: false,
	customCoder: false,
	diagnostics: true,
} as never;

describe("settings catalog by product profile", () => {
	afterEach(() => {
		vi.unstubAllEnvs();
		vi.resetModules();
	});

	it("legacy Orchestrator keeps the Updates page", async () => {
		const mod = await import("./settingsCatalog");
		expect(mod.visibleGlobalSettings(context).some((item) => item.id === "updates")).toBe(true);
	}, 90_000);

	it("BlueAshAI has no Updates page, and asking for it lands on the first page instead", async () => {
		vi.resetModules();
		vi.stubEnv("AO_BRAND", "blueashai");
		const mod = await import("./settingsCatalog");
		expect(mod.visibleGlobalSettings(context).some((item) => item.id === "updates")).toBe(false);
		expect(mod.globalSettingsItem("updates", context).id).not.toBe("updates");
	}, 90_000);
});
