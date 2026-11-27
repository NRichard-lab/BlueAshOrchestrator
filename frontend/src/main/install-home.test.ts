import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
	defaultInstallHomeRecord,
	installHomeMarkerContents,
	migrateDataDir,
	parseInstallHomeMarker,
	removeFixtureTree,
	resolveInstallHomePaths,
	writeInstallHomeMarker,
} from "./install-home";

const fixtures: string[] = [];

afterEach(async () => {
	await Promise.all(fixtures.splice(0).map((dir) => removeFixtureTree(dir)));
});

async function fixtureDir(label: string): Promise<string> {
	const dir = await mkdtemp(path.join(os.tmpdir(), `ao-install-home-${label}-`));
	fixtures.push(dir);
	return dir;
}

describe("install home marker", () => {
	it("round-trips relative paths against the install directory", () => {
		const installDir = "D:\\Apps\\Orchestrator";
		const record = defaultInstallHomeRecord(installDir);
		const raw = installHomeMarkerContents(record, installDir);
		expect(JSON.parse(raw)).toMatchObject({
			version: 1,
			dataDir: "data",
			electronDir: "electron",
			workspaceDir: "data/worktrees",
		});
		expect(parseInstallHomeMarker(raw, installDir)).toEqual(record);
	});

	it("keeps absolute overrides absolute", () => {
		const installDir = "/opt/ao";
		const raw = installHomeMarkerContents(
			{
				version: 1,
				dataDir: "/var/ao/data",
				electronDir: "/var/ao/electron",
				workspaceDir: "/work/ao",
			},
			installDir,
		);
		expect(parseInstallHomeMarker(raw, installDir)).toEqual({
			version: 1,
			dataDir: path.normalize("/var/ao/data"),
			electronDir: path.normalize("/var/ao/electron"),
			workspaceDir: path.normalize("/work/ao"),
		});
	});
});

describe("resolveInstallHomePaths", () => {
	it("prefers AO_DATA_DIR over an install marker", () => {
		const paths = resolveInstallHomePaths({
			env: { AO_DATA_DIR: "custom-data" },
			homeDir: "/home/ao",
			launchWorkingDirectory: "/work",
			isPackaged: true,
			execDir: "/opt/ao",
			marker: defaultInstallHomeRecord("/opt/ao"),
		});
		expect(paths.source).toBe("env");
		expect(paths.dataDir).toBe(path.resolve("/work/custom-data"));
		expect(paths.workspaceDir).toBe(path.resolve("/work/custom-data/worktrees"));
	});

	it("uses the install marker when packaged", () => {
		const marker = defaultInstallHomeRecord("/opt/Orchestrator");
		const paths = resolveInstallHomePaths({
			env: {},
			homeDir: "/home/ao",
			launchWorkingDirectory: "/work",
			isPackaged: true,
			execDir: "/opt/Orchestrator",
			marker,
		});
		expect(paths).toMatchObject({
			source: "marker",
			dataDir: marker.dataDir,
			electronDir: marker.electronDir,
			workspaceDir: marker.workspaceDir,
			installDir: "/opt/Orchestrator",
		});
	});

	// Every terminal and agent the app starts inherits the data-dir variable the app gave its daemon.
	// Launching the app again from one of them must resolve the SAME Electron profile as a normal
	// launch, or the single-instance lock (tied to the profile folder) is missed and a second app opens.
	it("treats an inherited variable naming the install's own data folder as the marker, not an override", () => {
		const marker = defaultInstallHomeRecord("/opt/BlueAshAI");
		const normal = resolveInstallHomePaths({
			env: {},
			homeDir: "/home/ao",
			launchWorkingDirectory: "/work",
			isPackaged: true,
			execDir: "/opt/BlueAshAI",
			marker,
		});
		for (const inherited of [marker.dataDir, `${marker.dataDir}${path.sep}`, marker.dataDir.toUpperCase()]) {
			const fromSession = resolveInstallHomePaths({
				env: { AO_DATA_DIR: inherited },
				homeDir: "/home/ao",
				launchWorkingDirectory: "/some/worktree",
				isPackaged: true,
				execDir: "/opt/BlueAshAI",
				marker,
			});
			expect(fromSession).toEqual(normal);
			expect(fromSession.source).toBe("marker");
			expect(fromSession.electronDir).toBe(marker.electronDir);
		}
	});

	it("falls back to the profile defaults without a marker", () => {
		expect(
			resolveInstallHomePaths({
				env: {},
				homeDir: "/home/ao",
				launchWorkingDirectory: "/work",
				isPackaged: true,
			}),
		).toMatchObject({
			source: "default",
			dataDir: path.resolve("/home/ao/.ao/data"),
			electronDir: path.resolve("/home/ao/.ao/electron"),
			workspaceDir: path.resolve("/home/ao/.ao/data/worktrees"),
		});
	});
});

describe("migrateDataDir", () => {
	it("copies a fixture data directory without touching the source", async () => {
		const root = await fixtureDir("migrate");
		const source = path.join(root, "source");
		const destination = path.join(root, "destination");
		await mkdir(path.join(source, "worktrees", "demo"), { recursive: true });
		await writeFile(path.join(source, "ao.db"), "db-bytes");
		await writeFile(path.join(source, "ao.db-wal"), "wal");
		await writeFile(path.join(source, "running.json"), "{}");
		await writeFile(path.join(source, "worktrees", "demo", "README"), "hi");

		const result = await migrateDataDir(source, destination);
		expect(result.copied.sort()).toEqual(["ao.db", "ao.db-wal", "worktrees"]);
		expect(result.skipped).toContain("running.json");
		expect(await readFile(path.join(destination, "ao.db"), "utf8")).toBe("db-bytes");
		expect(await readFile(path.join(destination, "worktrees", "demo", "README"), "utf8")).toBe("hi");
		expect(await readFile(path.join(source, "ao.db"), "utf8")).toBe("db-bytes");
	});

	it("refuses to overwrite a destination that already has ao.db", async () => {
		const root = await fixtureDir("refuse");
		const source = path.join(root, "source");
		const destination = path.join(root, "destination");
		await mkdir(source, { recursive: true });
		await mkdir(destination, { recursive: true });
		await writeFile(path.join(source, "ao.db"), "new");
		await writeFile(path.join(destination, "ao.db"), "old");
		await expect(migrateDataDir(source, destination)).rejects.toThrow(/refusing to overwrite/);
		expect(await readFile(path.join(destination, "ao.db"), "utf8")).toBe("old");
	});
});

describe("writeInstallHomeMarker", () => {
	it("writes a marker the resolver can read back", async () => {
		const installDir = await fixtureDir("marker");
		const record = defaultInstallHomeRecord(installDir);
		await writeInstallHomeMarker(installDir, record);
		const raw = await readFile(path.join(installDir, "ao-home.json"), "utf8");
		expect(parseInstallHomeMarker(raw, installDir)).toEqual(record);
	});
});
