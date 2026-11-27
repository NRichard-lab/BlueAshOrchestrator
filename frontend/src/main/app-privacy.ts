import { PRIVACY_PANES, type PrivacyPane } from "./privacy-guide";

// Blue Ash AI's own macOS privacy grants. macOS charges every process in a
// session (the agent CLI and whatever it starts) to Blue Ash AI, so Claude
// Code's built-in computer use in a terminal session works only once
// Blue Ash AI holds Accessibility and Screen Recording, whatever the error
// shown in the session names.

export type AppPrivacyGrants = Record<PrivacyPane, boolean>;

/** Runs a file with arguments and resolves to its stdout. */
export type RunFile = (file: string, args: string[], options: { timeout: number }) => Promise<{ stdout: string }>;

const OSASCRIPT = "/usr/bin/osascript";
// A stalled osascript (waiting on a prompt, a wedged scripting bridge) must
// not leave the settings row spinning and every computer-use action disabled.
export const APP_PRIVACY_TIMEOUT_MS = 5_000;

// Asked in a fresh process every time: a running process keeps the Screen
// Recording answer it first got until it restarts, so Blue Ash AI cannot ask
// itself. osascript is Blue Ash AI's child, so macOS answers for Blue Ash AI.
export const APP_PRIVACY_SCRIPT = [
	"ObjC.import('ApplicationServices');",
	"ObjC.bindFunction('CGPreflightScreenCaptureAccess', ['bool', []]);",
	"JSON.stringify({ accessibility: $.AXIsProcessTrusted(), screenRecording: $.CGPreflightScreenCaptureAccess() });",
].join("\n");

/** Blue Ash AI's current grants; undefined when they cannot be read. */
export async function readAppPrivacy(run: RunFile): Promise<AppPrivacyGrants | undefined> {
	try {
		const { stdout } = await run(OSASCRIPT, ["-l", "JavaScript", "-e", APP_PRIVACY_SCRIPT], { timeout: APP_PRIVACY_TIMEOUT_MS });
		return parseAppPrivacy(stdout);
	} catch {
		return undefined;
	}
}

export function parseAppPrivacy(stdout: string): AppPrivacyGrants | undefined {
	try {
		const value = JSON.parse(stdout.trim()) as Record<string, unknown>;
		if (!value || typeof value !== "object") return undefined;
		if (typeof value.accessibility !== "boolean" || typeof value.screenRecording !== "boolean") return undefined;
		return { accessibility: value.accessibility, screenRecording: value.screenRecording };
	} catch {
		return undefined;
	}
}

export interface AppPrivacyPrompts {
	/** Shows macOS's Accessibility prompt for Blue Ash AI and lists it under Accessibility. */
	accessibility(): void;
	/** Shows macOS's Screen Recording prompt for Blue Ash AI and lists it under Screen Recording. */
	screenRecording(): Promise<void>;
}

/**
 * Asks macOS for each missing grant, which lists Blue Ash AI in that pane.
 * macOS shows each prompt once per app; after that the request only lists it,
 * and the user turns its switch on in System Settings.
 */
export async function requestAppPrivacy(granted: AppPrivacyGrants, prompts: AppPrivacyPrompts): Promise<void> {
	for (const pane of PRIVACY_PANES) {
		if (granted[pane]) continue;
		if (pane === "accessibility") prompts.accessibility();
		else await prompts.screenRecording();
	}
}

/** The .app bundle an executable runs from, or undefined when it is not in one. */
export function appBundlePath(executablePath: string): string | undefined {
	const marker = ".app/Contents/MacOS/";
	const index = executablePath.lastIndexOf(marker);
	return index < 0 ? undefined : executablePath.slice(0, index + ".app".length);
}
