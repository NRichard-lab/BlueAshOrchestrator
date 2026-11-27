// @vitest-environment node
import { describe, expect, it } from "vitest";
import { appCatalogs } from "../renderer/i18n/all-messages";
import { createAppI18n } from "../renderer/i18n/instance";
import { APP_LOCALES } from "./ui-locale";
import { BLUEASHAI_BRAND, LEGACY_BRAND } from "./brand";
import { DE_PRODUCT_OCCURRENCES, DE_ROLE_KEYS, KEPT_KEYS, ROLE_KEYS, brandCatalog, brandText, productText, productTextFor } from "./brand-text";

const blue = (value: string, key = "some.key", locale = "en") => brandText(value, key, locale, BLUEASHAI_BRAND);
const baseKey = (key: string) => key.replace(/_(?:zero|one|two|few|many|other)$/, "");
// A few catalog values are string arrays; none names the product, and brandCatalog maps over them.
const catalogs = Object.fromEntries(
	Object.entries(appCatalogs).map(([locale, catalog]) => [
		locale,
		Object.fromEntries(Object.entries(catalog).filter((entry): entry is [string, string] => typeof entry[1] === "string")),
	]),
) as Record<string, Record<string, string>>;
const en = catalogs.en;

/** The English value for a key, falling back across plural forms English does not have (ru: few, many). */
function englishFor(key: string): string | undefined {
	return en[key] ?? en[`${baseKey(key)}_other`] ?? en[`${baseKey(key)}_one`];
}

describe("brandText", () => {
	it("leaves every value alone for the legacy profile", () => {
		expect(brandText("Quit Orchestrator?", "quitConfirm.title", "en", LEGACY_BRAND)).toBe("Quit Orchestrator?");
		expect(brandCatalog(appCatalogs.en, "en", LEGACY_BRAND)).toBe(appCatalogs.en);
	});

	it("uses the profile's visible name, not its packaging name", () => {
		expect(BLUEASHAI_BRAND.visibleName).toBe("Blue Ash AI");
		expect(LEGACY_BRAND.visibleName).toBe("Orchestrator");
		expect(blue("Quit Orchestrator?")).toBe("Quit Blue Ash AI?");
	});

	it("rewrites the product name with the words that depend on it", () => {
		expect(blue("Open Orchestrator's permissions")).toBe("Open Blue Ash AI's permissions");
		expect(blue("Welcome to Agent Orchestrator")).toBe("Welcome to Blue Ash AI");
		expect(blue("About Orchestrator.inc")).toBe("About Blue Ash AI");
		expect(blue("This address is not an Orchestrator host.")).toBe("This address is not a Blue Ash AI host.");
		expect(blue("An Orchestrator host")).toBe("A Blue Ash AI host");
		expect(blue("Run `ao stop`, then reopen Orchestrator.")).toBe("Run `ao stop`, then reopen Blue Ash AI.");
		expect(blue("Orchestrator, then Orchestrator")).toBe("Blue Ash AI, then Blue Ash AI");
	});

	it("keeps upstream's own Cloud service and Browser Bridge extension by name", () => {
		expect(blue("Sign in to Orchestrator Cloud to use teams.")).toBe("Sign in to Orchestrator Cloud to use teams.");
		expect(blue("Lokale Orchestrator-Cloud-Anmeldung", "cloudLocalAuth.title", "de")).toBe("Lokale Orchestrator-Cloud-Anmeldung");
		expect(blue("Orchestrator Cloud에 로그인", "shell.signInToAOCloud", "ko")).toBe("Orchestrator Cloud에 로그인");
		expect(blue("through the Orchestrator Browser Bridge extension so Chrome can reach Orchestrator")).toBe(
			"through the Orchestrator Browser Bridge extension so Chrome can reach Blue Ash AI",
		);
	});

	it("keeps identifiers and interpolation variables", () => {
		expect(blue("{{orchestrators}}/{{orchestratorSlots}} via Orchestrator")).toBe("{{orchestrators}}/{{orchestratorSlots}} via Blue Ash AI");
		expect(blue("KindOrchestrator and OrchestratorSession and Orchestrator_x")).toBe("KindOrchestrator and OrchestratorSession and Orchestrator_x");
	});

	it("keeps the session role and things still named Orchestrator elsewhere", () => {
		expect(blue("Orchestrator agent", "settings.project.defaultOrchestrator")).toBe("Orchestrator agent");
		expect(blue("Orchestrator", "shell.orchestrator")).toBe("Orchestrator");
		expect(blue("Orchestrator restart failed: {{error}}", "settings.project.restartFailed")).toBe("Orchestrator restart failed: {{error}}");
		expect(blue("Get Orchestrator on Google Play", "mobile.androidHint")).toBe("Get Orchestrator on Google Play");
		expect(blue("Sign in to Orchestrator.inc to open shared sessions.", "shareLink.signInFirst")).toBe("Sign in to Orchestrator.inc to open shared sessions.");
		// The same word under a product key is the product.
		expect(blue("Orchestrator", "shell.memoryGroupApp")).toBe("Blue Ash AI");
	});

	it("applies exceptions to every plural form of a key", () => {
		expect(blue("Die Orchestrator-Ersetzung ist fehlgeschlagen.", "orchestratorReplacement.morePending_one", "de")).toBe("Die Orchestrator-Ersetzung ist fehlgeschlagen.");
		expect(blue("Orchestrator will import this workspace", "createProject.workspaceReposExcluded_many", "ru")).toBe("Blue Ash AI will import this workspace");
	});

	it("undoes French elisions before the new name", () => {
		expect(blue("L'hôte d'Orchestrator pour cette session", "k", "fr")).toBe("L'hôte de Blue Ash AI pour cette session");
		expect(blue("le temps qu’Orchestrator crée l’espace", "k", "fr")).toBe("le temps que Blue Ash AI crée l’espace");
		expect(blue("D'Orchestrator", "k", "fr")).toBe("De Blue Ash AI");
	});

	it("handles German: capitalized role, compounds and genitive", () => {
		expect(blue("Orchestrator-Agenten öffnen", "shell.openOrchestrator", "de")).toBe("Orchestrator-Agenten öffnen");
		expect(blue("Orchestrator-Daemon ist nicht erreichbar", "daemon.title.unreachable", "de")).toBe("Blue Ash AI-Daemon ist nicht erreichbar");
		expect(blue("Orchestrators Desktop-Fenster", "shell.memoryCategoryHint.app", "de")).toBe("Blue Ash AIs Desktop-Fenster");
		expect(
			blue(
				"Dieser Orchestrator hat keine Sitzung. Sie können einen neuen Orchestrator erstellen; Orchestrator bewahrt Workspace-Daten.",
				"restoreUnavailable.orchestratorBody",
				"de",
			),
		).toBe("Dieser Orchestrator hat keine Sitzung. Sie können einen neuen Orchestrator erstellen; Blue Ash AI bewahrt Workspace-Daten.");
	});

	it("leaves particles attached in Korean and Japanese", () => {
		expect(blue("Orchestrator가 저장소를 복제합니다", "k", "ko")).toBe("Blue Ash AI가 저장소를 복제합니다");
		expect(blue("Orchestratorの権限を開く", "k", "ja")).toBe("Blue Ash AIの権限を開く");
	});
});

describe("brandCatalog over the real catalogs", () => {
	it("does not mutate the source catalog", () => {
		const before = en["quitConfirm.title"];
		const branded = brandCatalog(en, "en", BLUEASHAI_BRAND);
		expect(branded).not.toBe(en);
		expect(en["quitConfirm.title"]).toBe(before);
		expect(branded["quitConfirm.title"]).toBe("Quit Blue Ash AI?");
	});

	it("serves the rewritten name through an i18n instance", () => {
		const i18n = createAppI18n("fr", { en: brandCatalog(en, "en", BLUEASHAI_BRAND), fr: brandCatalog(catalogs.fr, "fr", BLUEASHAI_BRAND) });
		expect(i18n.t("quitConfirm.title")).toContain("Blue Ash AI");
		expect(i18n.t("quitConfirm.title")).not.toContain("Orchestrator");
		expect(i18n.t("shell.orchestrator", { lng: "en" })).toBe("Orchestrator");
	});

	it.each(APP_LOCALES)("%s: every remaining \"Orchestrator\" is a listed exception", (locale) => {
		const unexplained: string[] = [];
		for (const [key, value] of Object.entries(brandCatalog(catalogs[locale], locale, BLUEASHAI_BRAND))) {
			if (!value.includes("Orchestrator")) continue;
			const base = baseKey(key);
			if (ROLE_KEYS.has(base) || KEPT_KEYS.has(base)) continue;
			if (locale === "de" && (DE_ROLE_KEYS.has(base) || base in DE_PRODUCT_OCCURRENCES)) continue;
			const rest = value.replace(/Orchestrator[ -](?:Cloud|Browser[ -]Bridge)/g, "");
			if (rest.includes("Orchestrator")) unexplained.push(`${key}: ${value}`);
		}
		expect(unexplained).toEqual([]);
	});

	// German writes the session role with a capital too. A translated value that would be rewritten
	// although its English source has no product name is such a role use: list it in DE_ROLE_KEYS
	// (or DE_PRODUCT_OCCURRENCES when it names both).
	it.each(APP_LOCALES.filter((locale) => locale !== "en"))("%s: only rewrites values whose English source names the product", (locale) => {
		const suspicious: string[] = [];
		for (const [key, value] of Object.entries(catalogs[locale])) {
			if (blue(value, key, locale) === value) continue;
			const source = englishFor(key);
			if (source === undefined || blue(source, key, "en") === source) suspicious.push(`${key}: ${value}`);
		}
		expect(suspicious).toEqual([]);
	});

	it.each(APP_LOCALES)("%s: never rewrites more names than the English source does", (locale) => {
		// A translation may phrase around the product name, so fewer is fine. More means the extra one is
		// probably the role (see German above).
		const count = (before: string, after: string) => (before.match(/Orchestrator/g)?.length ?? 0) - (after.match(/Orchestrator/g)?.length ?? 0);
		const mismatched: string[] = [];
		for (const [key, value] of Object.entries(catalogs[locale])) {
			const source = englishFor(key);
			if (source === undefined) continue;
			if (count(value, blue(value, key, locale)) > count(source, blue(source, key, "en"))) mismatched.push(`${key}: ${value}`);
		}
		expect(mismatched).toEqual([]);
	});

	it.each(APP_LOCALES)("%s: keeps interpolation variables intact", (locale) => {
		const variables = (text: string) => (text.match(/\{\{[^}]+\}\}/g) ?? []).join(" ");
		for (const [key, value] of Object.entries(catalogs[locale])) {
			expect(variables(blue(value, key, locale)), key).toBe(variables(value));
		}
	});

	it("de: \"an Orchestrator\" only occurs in untranslated English values", () => {
		// "an" becomes "a" before the new name. A German sentence with the preposition "an" before the
		// product name would be damaged by that; reword it or teach leadFor about it.
		const german = Object.entries(catalogs.de).filter(([key, value]) => /an Orchestrator/i.test(value) && value !== en[key]);
		expect(german).toEqual([]);
		expect(blue(catalogs.de["settings.cliLink.foreign"], "settings.cliLink.foreign", "de")).toContain("not a Blue Ash AI link");
	});

	it("maps over array values", () => {
		expect(brandCatalog({ list: ["Open Orchestrator", "Orchestrator Cloud"] }, "en", BLUEASHAI_BRAND)).toEqual({ list: ["Open Blue Ash AI", "Orchestrator Cloud"] });
		const arrays = Object.values(appCatalogs).flatMap((catalog) => Object.values(catalog).filter((value) => typeof value !== "string"));
		expect(JSON.stringify(arrays)).not.toContain("Orchestrator");
	});

	it("lists only keys that exist, with valid German positions", () => {
		const known = new Set(Object.keys(en).map(baseKey));
		for (const key of [...ROLE_KEYS, ...KEPT_KEYS, ...DE_ROLE_KEYS, ...Object.keys(DE_PRODUCT_OCCURRENCES)]) expect(known.has(key), key).toBe(true);
		for (const [key, positions] of Object.entries(DE_PRODUCT_OCCURRENCES)) {
			const occurrences = catalogs.de[key].match(/Orchestrator/g)?.length ?? 0;
			expect(Math.max(...positions), key).toBeLessThan(occurrences);
			expect(positions.length, key).toBeLessThan(occurrences);
		}
	});
});

describe("productText", () => {
	const blueText = productTextFor(BLUEASHAI_BRAND);

	it("returns the text as written for the legacy profile, which is what source and test builds compile", () => {
		const reason = "version";
		expect(productText("Restart Orchestrator to retry.")).toBe("Restart Orchestrator to retry.");
		expect(productText`the Orchestrator daemon refused (${reason})`).toBe("the Orchestrator daemon refused (version)");
	});

	it("rewrites a plain string", () => {
		expect(blueText("Orchestrator")).toBe("Blue Ash AI");
		expect(blueText("Could not stage Orchestrator's bundled tmux under the Orchestrator data directory")).toBe(
			"Could not stage Blue Ash AI's bundled tmux under the Blue Ash AI data directory",
		);
		expect(blueText("An older Orchestrator daemon is already running.")).toBe("An older Blue Ash AI daemon is already running.");
		expect(blueText("An Orchestrator daemon is already running, but it is not ready yet.")).toBe("A Blue Ash AI daemon is already running, but it is not ready yet.");
		expect(blueText("This Orchestrator-managed session link is malformed.")).toBe("This Blue Ash AI-managed session link is malformed.");
	});

	it("keeps Orchestrator Cloud", () => {
		expect(blueText("Sign in to Orchestrator Cloud to use remote hosts.")).toBe("Sign in to Orchestrator Cloud to use remote hosts.");
	});

	it("as a template tag, rewrites the literal parts and never the values", () => {
		const actual = "/Applications/Agent Orchestrator.app";
		const title = "Orchestrator notes";
		expect(blueText`Another Orchestrator daemon is already running from ${actual}; expected ${title}.`).toBe(
			"Another Blue Ash AI daemon is already running from /Applications/Agent Orchestrator.app; expected Orchestrator notes.",
		);
		expect(blueText`The command at ${"/usr/local/bin/ao"} is not an Orchestrator link.`).toBe("The command at /usr/local/bin/ao is not a Blue Ash AI link.");
		expect(blueText`Orchestrator v${"1.2.3"} is no longer supported.`).toBe("Blue Ash AI v1.2.3 is no longer supported.");
		expect(blueText`${3} of ${4}`).toBe("3 of 4");
	});
});
