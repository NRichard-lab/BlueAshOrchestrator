// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
	alreadyLooking,
	dockBounceType,
	osToastChimeControllable,
	shouldFlashFrame,
	shouldPlaySound,
	shouldReplaceBounce,
	shouldSignalAttention,
	shouldToast,
	toastSilent,
	type NotificationType,
} from "./notification-signals";

const ALL_TYPES: NotificationType[] = ["needs_input", "ready_to_merge", "pr_merged", "pr_closed_unmerged"];

describe("shouldToast", () => {
	it("fires a toast for every backend notification type", () => {
		for (const type of ALL_TYPES) {
			expect(shouldToast({ title: `${type} title` }, true)).toBe(true);
		}
	});

	it("does not toast without a title or when notifications are unsupported", () => {
		expect(shouldToast({ title: "" }, true)).toBe(false);
		expect(shouldToast({}, true)).toBe(false);
		expect(shouldToast({ title: "needs input" }, false)).toBe(false);
	});
});

describe("alreadyLooking", () => {
	it("only counts the renderer's watched report", () => {
		expect(alreadyLooking({ watched: true })).toBe(true);
		expect(alreadyLooking({ watched: false })).toBe(false);
		expect(alreadyLooking({})).toBe(false);
	});
});

describe("shouldPlaySound", () => {
	// The regression: working in Orchestrator on one session while another
	// blocks on input left the waiting agent with no sound at all, because any
	// window focus counted as "already looking".
	it("plays for an unwatched actionable notification", () => {
		for (const platform of ["win32", "darwin"] as NodeJS.Platform[]) {
			expect(shouldPlaySound(platform, "needs_input", true, false)).toBe(true);
			expect(shouldPlaySound(platform, "ready_to_merge", true, false)).toBe(true);
		}
	});

	it("stays quiet only for the prompt the user is actually watching", () => {
		expect(shouldPlaySound("win32", "needs_input", true, true)).toBe(false);
		expect(shouldPlaySound("darwin", "needs_input", true, true)).toBe(false);
	});

	it("ignores the looking guess on Linux, where focus is unreliable", () => {
		expect(shouldPlaySound("linux", "needs_input", true, true)).toBe(true);
	});

	it("respects the preference and the actionable-type allowlist", () => {
		expect(shouldPlaySound("win32", "needs_input", false, false)).toBe(false);
		expect(shouldPlaySound("linux", "needs_input", false, true)).toBe(false);
		expect(shouldPlaySound("win32", "pr_merged", true, false)).toBe(false);
		expect(shouldPlaySound("win32", undefined, true, false)).toBe(false);
	});
});

describe("shouldFlashFrame", () => {
	it("flashes an unfocused window for the actionable types", () => {
		for (const platform of ["win32", "linux"] as NodeJS.Platform[]) {
			expect(shouldFlashFrame(platform, "needs_input", false)).toBe(true);
			expect(shouldFlashFrame(platform, "ready_to_merge", false)).toBe(true);
			expect(shouldFlashFrame(platform, "pr_merged", false)).toBe(false);
		}
	});

	// Flashing a focused window is a no-op on Windows, and the clearing
	// listener fires only on the next focus, so the flag would latch.
	it("does not flash a focused window", () => {
		expect(shouldFlashFrame("win32", "needs_input", true)).toBe(false);
		expect(shouldFlashFrame("linux", "needs_input", true)).toBe(false);
	});

	it("leaves macOS to the dock bounce", () => {
		expect(shouldFlashFrame("darwin", "needs_input", false)).toBe(false);
	});
});

describe("shouldSignalAttention", () => {
	it("flashes the taskbar for the actionable types", () => {
		expect(shouldSignalAttention("needs_input")).toBe(true);
		expect(shouldSignalAttention("ready_to_merge")).toBe(true);
	});

	it("does not flash the taskbar for informational PR outcomes", () => {
		expect(shouldSignalAttention("pr_merged")).toBe(false);
		expect(shouldSignalAttention("pr_closed_unmerged")).toBe(false);
	});

	it("does not signal for unknown or missing types", () => {
		expect(shouldSignalAttention("some_future_type")).toBe(false);
		expect(shouldSignalAttention(undefined)).toBe(false);
	});
});

describe("shouldReplaceBounce", () => {
	it("replaces no bounce or a pending informational bounce", () => {
		expect(shouldReplaceBounce(null)).toBe(true);
		expect(shouldReplaceBounce({ critical: false })).toBe(true);
	});

	it("never replaces a pending critical bounce, so a blocked agent stays loud", () => {
		expect(shouldReplaceBounce({ critical: true })).toBe(false);
	});
});

describe("osToastChimeControllable", () => {
	// Platform contract: Electron's mac and win presenters read `silent`;
	// the Linux libnotify presenter does not (no suppress-sound hint).
	it.each(["darwin", "win32"] as const)("is controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(true);
	});

	it.each(["linux", "freebsd", "openbsd"] as const)("is not controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(false);
	});
});

describe("toastSilent", () => {
	const HONOURING: NodeJS.Platform[] = ["darwin", "win32"];

	it.each(HONOURING)("on %s mutes the toast whenever sound notifications are off", (platform) => {
		expect(toastSilent(platform, false, false)).toBe(true);
		expect(toastSilent(platform, false, true)).toBe(true);
	});

	it.each(HONOURING)("on %s mutes the toast when AO plays its own sound", (platform) => {
		expect(toastSilent(platform, true, true)).toBe(true);
	});

	it.each(HONOURING)("on %s keeps the native chime for an informational toast when sound is on", (platform) => {
		expect(toastSilent(platform, true, false)).toBe(false);
	});

	// On Linux the value would be ignored, so no combination may claim a mute.
	it.each([
		[false, false],
		[false, true],
		[true, false],
		[true, true],
	])("on linux never claims control (enabled=%s, playsSound=%s)", (enabled, playsSound) => {
		expect(toastSilent("linux", enabled, playsSound)).toBeUndefined();
	});
});

describe("dockBounceType", () => {
	it("bounces critically for a blocked agent waiting on the user", () => {
		expect(dockBounceType("needs_input")).toBe("critical");
	});

	it("bounces once for the other backend types", () => {
		for (const type of ALL_TYPES.filter((t) => t !== "needs_input")) {
			expect(dockBounceType(type)).toBe("informational");
		}
	});

	it("bounces once for unknown or missing types", () => {
		expect(dockBounceType("some_future_type")).toBe("informational");
		expect(dockBounceType(undefined)).toBe("informational");
	});
});
