// @vitest-environment node
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm, writeFile, readdir, readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
	macDifferentialUpdatesEnabled,
	disableUpdateSettingsOnDisk,
	readUpdateSettings,
	updateUpdateSettings,
	writeUpdateSettings,
	UPDATE_SETTINGS_FILE_NAME,
} from "./update-settings";

describe("update-settings", () => {
	let dir: string;
	beforeEach(async () => {
		dir = await mkdtemp(path.join(os.tmpdir(), "ao-update-settings-"));
	});
	afterEach(async () => {
		await rm(dir, { recursive: true, force: true });
	});

	it("returns safe defaults when no file exists", async () => {
		expect(await readUpdateSettings(dir)).toEqual({
			enabled: false,
			channel: "latest",
			nightlyAck: false,
			feature: null,
			macDifferentialUpdates: false,
		});
	});

	it.each([
		["darwin nightly Developer Mode", "darwin", { channel: "nightly", feature: null, macDifferentialUpdates: true }, true],
		["normal nightly", "darwin", { channel: "nightly", feature: null, macDifferentialUpdates: false }, false],
		["legacy nightly", "darwin", { channel: "nightly", feature: null }, false],
		["stable", "darwin", { channel: "latest", feature: null, macDifferentialUpdates: true }, false],
		["feature pin", "darwin", { channel: "nightly", feature: { pr: 3288 }, macDifferentialUpdates: true }, false],
		["Windows", "win32", { channel: "nightly", feature: null, macDifferentialUpdates: true }, false],
		["Linux", "linux", { channel: "nightly", feature: null, macDifferentialUpdates: true }, false],
	] as const)("enables macOS differential updates only for %s", (_label, platform, settings, expected) => {
		expect(macDifferentialUpdatesEnabled({ platform, settings })).toBe(expected);
	});

	it("round-trips written settings", async () => {
		await writeUpdateSettings(dir, {
			enabled: true,
			channel: "nightly",
			nightlyAck: true,
			feature: null,
			macDifferentialUpdates: true,
		});
		expect(await readUpdateSettings(dir)).toEqual({
			enabled: true,
			channel: "nightly",
			nightlyAck: true,
			feature: null,
			macDifferentialUpdates: true,
		});
	});

	it("falls back to defaults on garbage", async () => {
		await writeFile(path.join(dir, UPDATE_SETTINGS_FILE_NAME), "{not json", "utf8");
		expect(await readUpdateSettings(dir)).toEqual({
			enabled: false,
			channel: "latest",
			nightlyAck: false,
			feature: null,
			macDifferentialUpdates: false,
		});
	});

	it.each(["true", 1, null, {}, []])("fails closed for malformed mirror %j", async (value) => {
		await writeFile(
			path.join(dir, UPDATE_SETTINGS_FILE_NAME),
			JSON.stringify({
				enabled: true,
				channel: "nightly",
				nightlyAck: true,
				feature: null,
				macDifferentialUpdates: value,
			}),
			"utf8",
		);
		expect((await readUpdateSettings(dir)).macDifferentialUpdates).toBe(false);
	});

	it.each([{}, "pr3288", { pr: "invalid" }])("disables the mirror with malformed feature data %j", async feature => {
		await writeFile(path.join(dir, UPDATE_SETTINGS_FILE_NAME), JSON.stringify({
			channel: "nightly", feature, macDifferentialUpdates: true,
		}));
		expect(macDifferentialUpdatesEnabled({ platform: "darwin", settings: await readUpdateSettings(dir) })).toBe(false);
	});

	it("coerces an unknown channel back to latest", async () => {
		await writeFile(
			path.join(dir, UPDATE_SETTINGS_FILE_NAME),
			JSON.stringify({ enabled: true, channel: "weird", nightlyAck: false }),
			"utf8",
		);
		expect((await readUpdateSettings(dir)).channel).toBe("latest");
	});

	it("legacy file without feature key defaults feature to null", async () => {
		await writeFile(
			path.join(dir, UPDATE_SETTINGS_FILE_NAME),
			JSON.stringify({ enabled: true, channel: "nightly", nightlyAck: true }),
			"utf8",
		);
		const settings = await readUpdateSettings(dir);
		expect(settings.feature).toBeNull();
		expect(settings.macDifferentialUpdates).toBe(false);
		// channel is preserved as-is
		expect(settings.channel).toBe("nightly");
	});

	it("round-trips nightly + feature pin without clobbering channel", async () => {
		await writeUpdateSettings(dir, { enabled: true, channel: "nightly", nightlyAck: true, feature: { pr: 2270 } });
		const settings = await readUpdateSettings(dir);
		expect(settings).toEqual({
			enabled: true,
			channel: "nightly",
			nightlyAck: true,
			feature: { pr: 2270 },
			macDifferentialUpdates: false,
		});
		// Sanity: channel must remain the home channel value, not a feature string.
		expect(settings.channel).toBe("nightly");
	});

	it("coerces a malformed feature value to null", async () => {
		await writeFile(
			path.join(dir, UPDATE_SETTINGS_FILE_NAME),
			JSON.stringify({ enabled: true, channel: "latest", nightlyAck: false, feature: { pr: "not-a-number" } }),
			"utf8",
		);
		expect((await readUpdateSettings(dir)).feature).toBeNull();
	});

	it("atomic write leaves no temp file behind", async () => {
		await writeUpdateSettings(dir, { enabled: true, channel: "latest", nightlyAck: false, feature: null });
		const entries = await readdir(dir);
		expect(entries).toEqual([UPDATE_SETTINGS_FILE_NAME]);
	});

	it("serializes a read-modify-write with later settings writes", async () => {
		await writeUpdateSettings(dir, {
			enabled: false,
			channel: "latest",
			nightlyAck: false,
			feature: { pr: 2709 },
		});
		let releaseMutation!: () => void;
		const mutationBlocked = new Promise<void>((resolve) => {
			releaseMutation = resolve;
		});
		let mutationStarted!: () => void;
		const started = new Promise<void>((resolve) => {
			mutationStarted = resolve;
		});

		const mutation = updateUpdateSettings(dir, async (current) => {
			mutationStarted();
			await mutationBlocked;
			return { ...current, feature: null };
		});
		await started;
		const newer = { enabled: true, channel: "nightly" as const, nightlyAck: true, feature: { pr: 2710 } };
		const laterWrite = writeUpdateSettings(dir, newer);

		releaseMutation();
		await Promise.all([mutation, laterWrite]);

		expect(await readUpdateSettings(dir)).toEqual({ ...newer, macDifferentialUpdates: false });
	});
});

describe("disableUpdateSettingsOnDisk (BlueAshAI one-time migration)", () => {
	let dir: string;
	beforeEach(async () => {
		dir = await mkdtemp(path.join(os.tmpdir(), "ao-update-migrate-"));
	});
	afterEach(async () => {
		await rm(dir, { recursive: true, force: true });
	});
	const file = () => path.join(dir, UPDATE_SETTINGS_FILE_NAME);

	it("creates an explicit disabled file on an empty profile so the first-run prompt can never appear", async () => {
		expect(await disableUpdateSettingsOnDisk(dir)).toBe("created");
		expect(JSON.parse(await readFile(file(), "utf8"))).toMatchObject({ enabled: false, channel: "latest", feature: null });
	});

	it("clears a previously enabled profile, including a nightly channel pin and a feature build", async () => {
		await writeFile(file(), JSON.stringify({ enabled: true, channel: "nightly", nightlyAck: true, feature: { pr: 12 }, macDifferentialUpdates: true }));
		expect(await disableUpdateSettingsOnDisk(dir)).toBe("cleared");
		expect(await readUpdateSettings(dir)).toEqual({ enabled: false, channel: "latest", nightlyAck: false, feature: null, macDifferentialUpdates: false });
	});

	it("leaves an already disabled file untouched and is idempotent", async () => {
		await writeFile(file(), JSON.stringify({ enabled: false, channel: "latest", nightlyAck: false, feature: null }));
		const before = await readFile(file(), "utf8");
		expect(await disableUpdateSettingsOnDisk(dir)).toBe("unchanged");
		expect(await readFile(file(), "utf8")).toBe(before);
		expect(await disableUpdateSettingsOnDisk(dir)).toBe("unchanged");
	});

	it("repairs a corrupt file rather than leaving defaults implicit", async () => {
		await writeFile(file(), "{not json");
		expect(await disableUpdateSettingsOnDisk(dir)).toBe("cleared");
		expect(JSON.parse(await readFile(file(), "utf8")).enabled).toBe(false);
	});
});
