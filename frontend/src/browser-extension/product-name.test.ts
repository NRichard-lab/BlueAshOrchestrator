import { afterEach, expect, it, vi } from "vitest";
import { groupTitle } from "./sessions";

afterEach(() => vi.unstubAllGlobals());

it.each([
	["Orchestrator Browser Bridge", "Orchestrator"],
	["BlueAshAI Browser Bridge", "Blue Ash AI"],
	["Blue Ash AI Browser Bridge", "Blue Ash AI"],
])("uses the display spelling for %s", (name, displayName) => {
	vi.stubGlobal("chrome", { runtime: { getManifest: () => ({ name }) } });
	expect(groupTitle("Orchestrator")).toBe(`${displayName} · Orchestrator`);
});
