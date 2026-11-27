// Product name in UI text.
//
// The locale catalogs (src/renderer/i18n/*.json) are shared with upstream and name the product
// "Orchestrator". A build with another product profile (see brand.ts) rewrites that name when a
// catalog is loaded, instead of editing ~1,900 strings in nine files that every upstream merge
// would then conflict with. The legacy profile returns each catalog untouched.
//
// Not every "Orchestrator" is the product:
//   - The orchestrator *session* (the agent that plans work and spawns workers) keeps its name.
//     English and most locales write it in lowercase or translate it, so only the keys in
//     ROLE_KEYS use the capitalized word for it. German capitalizes every noun, so it needs its
//     own list (DE_ROLE_KEYS) and, for three values that name both, the positions of the product.
//   - Things that still exist under the upstream name outside this app keep it too: Orchestrator
//     Cloud, the Orchestrator Browser Bridge extension, the Orchestrator GitHub App, the phone
//     app in the stores, and the Orchestrator.inc sign-in used for share links (KEPT_KEYS).
//
// brand-text.test.ts checks every catalog against these lists, so a new upstream string that
// uses the word in a way the lists do not cover fails there rather than shipping mislabelled.
//
// English text written in code instead of a catalog (dialogs, toasts, error messages) goes through
// productText, which applies the same rewrite to a literal.
//
// This module is pure so the renderer and the main process can both import it.
import { BRAND, LEGACY_BRAND, type Brand } from "./brand";

/** Catalog values are strings, a few string arrays, and (in the tray's partial catalog) absent. */
type Catalog = Record<string, string | string[] | undefined>;

/** Keys whose capitalized "Orchestrator" is the session role, in every locale. Plural suffixes are ignored. */
export const ROLE_KEYS: ReadonlySet<string> = new Set([
	"createProject.orchestratorAgent",
	"orchestratorReplacement.title",
	"settings.billing.freeOrchestrator",
	"settings.billing.runningDetail",
	"settings.cloudProject.orchestratorInstructions",
	"settings.models.orchestratorMode",
	"settings.models.orchestratorModel",
	"settings.models.orchestratorRole",
	"settings.project.defaultOrchestrator",
	"settings.project.restartFailed",
	"shell.orchestrator",
	"shell.orchestratorBoard",
	"shell.orchestratorWithActivity",
]);

/** German only: keys where English writes the role in lowercase and German capitalizes it. */
export const DE_ROLE_KEYS: ReadonlySet<string> = new Set([
	"board.empty.body",
	"chat.automation.orchestratorReplaced",
	"command.openOrchestrator",
	"command.orchestratorRestarting",
	"createProject.selectOrchestrator",
	"newTask.sameAsOrchestrator",
	"orchestratorReplacement.fallback",
	"orchestratorReplacement.morePending",
	"orchestratorReplacement.openCurrent",
	"restoreUnavailable.configureOrchestrator",
	"restoreUnavailable.createFailed",
	"restoreUnavailable.createOrchestrator",
	"settings.cloudProject.orchestratorInstructionsHint",
	"settings.project.agentsRequired",
	"settings.project.replaceOrchestratorFailed",
	"settings.project.selectOrchestrator",
	"share.accessStartSessionsDescription",
	"shell.couldNotSpawn",
	"shell.memoryRowOrchestrator",
	"shell.openOrchestrator",
	"shell.openProjectOrchestrator",
	"shell.provisioning",
	"shell.spawnOrchestrator",
	"shell.spawnOrchestratorLower",
	"shell.spawnProjectOrchestrator",
	"shortcut.open-orchestrator",
]);

/**
 * German only: values that name the role and the product. The numbers are the zero-based positions,
 * among the value's "Orchestrator" occurrences, of the ones that are the product.
 */
export const DE_PRODUCT_OCCURRENCES: Readonly<Record<string, readonly number[]>> = {
	"restoreUnavailable.orchestratorBody": [2],
	"terminal.preparingOrchestrator": [1],
};

/** Keys about something that is still called Orchestrator outside this app. Left as written. */
export const KEPT_KEYS: ReadonlySet<string> = new Set([
	// The phone app, as listed in the App Store and Google Play.
	"mobile.androidHint",
	"mobile.androidSignupAria",
	"mobile.iosHint",
	"mobile.iosStoreAria",
	"mobile.lan.step2",
	"mobile.scanHint",
	// The GitHub App that co-authors commits and pushes cloud workspaces.
	"cloudWorkspace.pushError.pushForbidden",
	"cloudWorkspace.pushError.workflowsRefused",
	"settings.project.coAuthorCommitsDescription",
	"settings.project.coAuthorCommitsToggle",
	// The earlier upstream install that projects and chats are imported from.
	"migration.title",
	"settings.migration.description",
	// The Orchestrator.inc account that share links sign in to.
	"share.confirm.writeProject",
	"share.confirm.writeSession",
	"share.general.anyoneOption",
	"share.general.anyoneProjectWatch",
	"share.general.anyoneProjectWrite",
	"share.general.anyoneSessionWatch",
	"share.general.anyoneSessionWrite",
	"shareLink.signInFirst",
]);

const UPSTREAM_NAME = "Orchestrator";

// One product-name occurrence, with the neighbours that have to change with it:
//   1: a leading "an ", a French elision (d', qu') or the "Agent " of the old two-word name
//   2: the ".inc" of the upstream display name
// Not matched: the word inside a longer identifier, and the names of upstream's own Cloud service
// and Browser Bridge extension. A trailing "s" (German genitive) stays in place after the name.
const PRODUCT_NAME = /(?<![A-Za-z0-9_])(\b[Aa]n |(?:[Dd]|[Qq]u)['’]|Agent )?Orchestrator(\.inc\b)?(?=s?(?![A-Za-z0-9_]))(?![ -](?:Cloud|Browser[ -]Bridge))/g;

const PLURAL_SUFFIX = /_(?:zero|one|two|few|many|other)$/;

function leadFor(prefix: string | undefined): string {
	if (!prefix || prefix === "Agent ") return "";
	// "an Orchestrator" becomes "a Blue Ash AI". Applied in every locale, because catalogs carry
	// untranslated English values. German "an" is a preposition, but no German value puts it before
	// the product name; brand-text.test.ts fails if one ever does.
	if (/^an $/i.test(prefix)) return `${prefix[0] === "A" ? "A" : "a"} `;
	// d'Orchestrator, qu'Orchestrator: the new name starts with a consonant, so the elision goes.
	return `${prefix.slice(0, -1)}e `;
}

/** Rewrite the product name in one catalog value. `key` and `locale` select the exceptions above. */
export function brandText(value: string, key: string, locale: string, brand: Brand = BRAND): string {
	const name = brand.visibleName;
	if (name === UPSTREAM_NAME || !value.includes(UPSTREAM_NAME)) return value;
	const base = key.replace(PLURAL_SUFFIX, "");
	if (ROLE_KEYS.has(base) || KEPT_KEYS.has(base)) return value;
	if (locale === "de" && DE_ROLE_KEYS.has(base)) return value;
	const only = locale === "de" ? DE_PRODUCT_OCCURRENCES[base] : undefined;
	if (only) {
		// Count every occurrence of the word, matched or not, so the positions stay stable.
		let index = -1;
		return value.replace(/Orchestrator/g, (word) => {
			index += 1;
			return only.includes(index) ? name : word;
		});
	}
	return value.replace(PRODUCT_NAME, (_match, prefix: string | undefined) => leadFor(prefix) + name);
}

/**
 * A catalog with the product name rewritten for `brand`. The legacy profile gets the same object
 * back; any other profile gets a copy, so the imported JSON module is never mutated.
 */
export function brandCatalog<T extends Catalog>(catalog: T, locale: string, brand: Brand = BRAND): T {
	if (brand.visibleName === LEGACY_BRAND.visibleName) return catalog;
	const out: Catalog = {};
	for (const [key, value] of Object.entries(catalog)) {
		if (typeof value === "string") out[key] = brandText(value, key, locale, brand);
		else out[key] = value?.map((item) => brandText(item, key, locale, brand));
	}
	return out as T;
}

type ProductText = {
	(text: string): string;
	(strings: TemplateStringsArray, ...values: unknown[]): string;
};

/** productText for a given profile. Exported for tests; code uses productText. */
export function productTextFor(brand: Brand): ProductText {
	const rewrite = (text: string) => brandText(text, "", "en", brand);
	return (first: string | TemplateStringsArray, ...values: unknown[]): string => {
		if (typeof first === "string") return rewrite(first);
		return first.reduce((out, part, index) => out + rewrite(part) + (index < values.length ? String(values[index]) : ""), "");
	};
}

/**
 * English text written in code, with this build's product name. Keep the upstream wording inside:
 *
 *   productText("Restart Orchestrator to retry.")
 *   productText`Could not restart the Orchestrator daemon: ${message}`
 *
 * As a template tag it rewrites only the literal parts, so a value such as a path
 * (/Applications/Agent Orchestrator.app) or a session title is never changed. The legacy profile
 * returns the text as written. Do not wrap the orchestrator session role, text addressed to agents,
 * or identifiers such as a User-Agent or a commit author name.
 */
export const productText: ProductText = productTextFor(BRAND);
