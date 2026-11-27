// @vitest-environment node
//
// The packaging identity of a product: the macOS bundle and its identifier, the Linux artifact and
// desktop entry, and the Chrome Native Messaging host. Two products that share any of them collide
// on the same machine: the same /Applications bundle gets replaced or trashed, `ao start` downloads
// and opens the other product, the NSIS/electron-builder registry key is the same install, and
// whichever product ran `ao browser chrome setup` last owns the other's browser bridge.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { BLUEASHAI_BRAND, LEGACY_BRAND } from "../src/shared/brand";

const frontend = join(__dirname, "..");
const read = (rel: string) => readFileSync(join(frontend, rel), "utf8");

describe("macOS and Linux packaging identity", () => {
	it("BlueAshAI shares no bundle name, identifier, artifact or host name with Orchestrator", () => {
		expect(BLUEASHAI_BRAND.appId).toBe("ai.blueash.desktop");
		expect(BLUEASHAI_BRAND.appId).not.toBe(LEGACY_BRAND.appId);
		expect(`${BLUEASHAI_BRAND.packagerName}.app`).toBe("BlueAshAI.app");
		expect(BLUEASHAI_BRAND.packagerName).not.toBe(LEGACY_BRAND.packagerName);
		expect(BLUEASHAI_BRAND.nativeMessagingHost).not.toBe(LEGACY_BRAND.nativeMessagingHost);
		// `ao start`'s asset names are built from executableName (backend/internal/cli/start.go).
		expect(BLUEASHAI_BRAND.executableName).toBe("BlueAshAI");
		expect(BLUEASHAI_BRAND.executableName).not.toBe(LEGACY_BRAND.executableName);
	});

	it("the release workflow renames every artifact to this product's asset names", () => {
		const wf = read("../.github/workflows/build-artifacts.yml");
		for (const name of [
			`${BLUEASHAI_BRAND.windowsAssetName}`,
			`${BLUEASHAI_BRAND.executableName}-$PLATFORM.zip`,
			`${BLUEASHAI_BRAND.executableName}-linux-x64.AppImage`,
			`${BLUEASHAI_BRAND.executableName}-linux-x64.deb`,
			`${BLUEASHAI_BRAND.executableName}-linux-x64.rpm`,
		]) {
			expect(wf, name).toContain(`name="${name}"`);
		}
		const live = wf.split("\n").filter((line) => !line.trim().startsWith("#")).join("\n");
		expect(live).not.toMatch(/name="agent-orchestrator-/);
	});

	it("the self-hosted installer downloads this product's bootstrap assets", () => {
		// A standalone script (no repo, no node), so it writes the names out; they must still match.
		const script = read("../scripts/setup-self-hosted.sh");
		const exe = BLUEASHAI_BRAND.executableName;
		for (const asset of [`${exe}-darwin-arm64.zip`, `${exe}-darwin-x64.zip`, `${exe}-linux-x64.AppImage`]) {
			expect(script, asset).toContain(`asset='${asset}'`);
		}
		expect(script).not.toMatch(/asset='agent-orchestrator-/);
	});

	// The bare-node build scripts (a "#!" line and no type declarations) are asserted in
	// scripts/brand-packaging.node-test.mjs and build-mac-repair-installer.node-test.mjs. Here:
	// that each one reads the profile rather than a literal.
	it("the macOS install window and repair installer take their names from the profile", () => {
		const dmg = read("scripts/build-mac-dmg.mjs");
		expect(dmg).toContain("brandProfile()");
		expect(dmg).toContain("BRAND.packagerName");
		expect(dmg).toContain("BRAND.appId");
		expect(dmg).toContain("BRAND.displayName");
		const repair = read("scripts/build-mac-repair-installer.mjs");
		expect(repair).toContain("brandProfile()");
		expect(repair).toContain("BRAND.packagerName");
		expect(repair).toContain("BRAND.appId");
		const guard = read("native/repair-installer/guard.sh");
		for (const placeholder of ["@@APP@@", "@@ID@@", "@@NAME@@", "@@APP_RE@@", "@@EXE_RE@@", "@@ID_RE@@"]) {
			expect(guard, placeholder).toContain(placeholder);
		}
	});

	it("the Chrome extension connects to its own product's host", () => {
		// The host name is compiled into the extension bundle from the profile, like the app's.
		expect(read("src/browser-extension/protocol.ts")).toContain("NATIVE_MESSAGING_HOST");
		const script = read("scripts/build-browser-extension.mjs");
		expect(script).toContain("define: brandDefine()");
		// Chrome accepts only lowercase letters, digits, underscore and dot in a host name.
		for (const brand of [LEGACY_BRAND, BLUEASHAI_BRAND]) {
			expect(brand.nativeMessagingHost).toMatch(/^[a-z0-9_]+(\.[a-z0-9_]+)*$/);
		}
	});
});

// The frontend counterpart of backend/internal/brand's TestProductNamesAreOnlyWrittenInThisPackage:
// one of these literals in a shipped file is a value that keeps saying "Orchestrator" in a
// BlueAshAI build. brands.json is the source of truth, and the two template files below carry the
// values their build step replaces.
describe("no product-identity literal outside the profile", () => {
	const FORBIDDEN = /dev\.agent-orchestrator\.desktop|Agent Orchestrator\.app|dev\.ao\.browser_bridge|agent-orchestrator-(darwin|linux|win32)|agent-orchestrator\.AppImage|agent-orchestrator-ao-app\.desktop/;
	const ALLOWED = new Set([
		"src/shared/brands.json", // the profile itself
		"native/dictation-helper/Info.plist", // template; build-dictation-helper.mjs rewrites it
		"scripts/build-dictation-helper.mjs", // names the template values it replaces
		"scripts/stage-update-tree.mjs", // a conductor tool that accepts either product's assets
	]);
	const SEARCH = ["makers", "native", "scripts", "src/main", "src/shared", "src/browser-extension"];

	function sources(dir: string): string[] {
		const out: string[] = [];
		for (const entry of readdirSync(join(frontend, dir))) {
			const rel = `${dir}/${entry}`;
			if (statSync(join(frontend, rel)).isDirectory()) {
				if (entry === "node_modules" || entry === "icons" || entry === "pages") continue;
				out.push(...sources(rel));
				continue;
			}
			if (/\.(test|node-test|spec)\.[cm]?[jt]sx?$/.test(entry) || entry.endsWith(".test.mjs")) continue;
			if (!/\.(ts|tsx|mjs|js|json|sh|swift|plist|nsh)$/.test(entry)) continue;
			out.push(rel);
		}
		return out;
	}

	it("holds for every shipped source file", () => {
		const offenders: string[] = [];
		const files = [...SEARCH.flatMap((dir) => sources(dir)), "forge.config.ts"];
		// A guard that searched nothing would pass: prove it reached the packaging sources.
		expect(files.length).toBeGreaterThan(50);
		for (const expected of ["forge.config.ts", "src/shared/brands.json", "makers/maker-dmg.ts", "scripts/build-mac-dmg.mjs"]) {
			expect(files, expected).toContain(expected);
		}
		{
			for (const file of files) {
				if (ALLOWED.has(file)) continue;
				const lines = readFileSync(join(frontend, file), "utf8").split("\n");
				lines.forEach((line, index) => {
					const trimmed = line.trim();
					// Comments may still name Orchestrator's values as the example they are.
					if (/^(\/\/|\*|#|<!--)/.test(trimmed)) return;
					if (FORBIDDEN.test(line)) offenders.push(`${file}:${index + 1}: ${trimmed}`);
				});
			}
		}
		expect(offenders, offenders.join("\n")).toEqual([]);
	});
});
