import { constants, readFileSync } from "node:fs";
import {
	access,
	copyFile,
	cp,
	mkdir,
	readdir,
	readFile,
	rename,
	rm,
	stat,
	writeFile,
} from "node:fs/promises";
import path from "node:path";

/** Marker the Windows installer (and settings) leave beside the packaged exe. */
export const INSTALL_HOME_MARKER = "ao-home.json";

export type InstallHomeRecord = {
	version: 1;
	/** Absolute data directory (SQLite, worktrees, artifacts). */
	dataDir: string;
	/** Absolute Electron userData directory (Chromium profile). */
	electronDir: string;
	/** Absolute worker workspace root; defaults to `<dataDir>/worktrees`. */
	workspaceDir?: string;
};

export type InstallHomePaths = {
	dataDir: string;
	electronDir: string;
	workspaceDir: string;
	/** Directory that holds the marker (install root). */
	installDir: string;
	source: "env" | "marker" | "default";
};

type MarkerFile = {
	version?: number;
	dataDir?: string;
	electronDir?: string;
	workspaceDir?: string;
};

function isAbsolutePath(value: string): boolean {
	return path.isAbsolute(value);
}

function resolveBeside(installDir: string, value: string): string {
	return isAbsolutePath(value) ? path.normalize(value) : path.resolve(installDir, value);
}

export function parseInstallHomeMarker(raw: string, installDir: string): InstallHomeRecord | null {
	let parsed: MarkerFile;
	try {
		parsed = JSON.parse(raw) as MarkerFile;
	} catch {
		return null;
	}
	if (parsed.version !== 1) return null;
	const dataDir = typeof parsed.dataDir === "string" ? parsed.dataDir.trim() : "";
	const electronDir = typeof parsed.electronDir === "string" ? parsed.electronDir.trim() : "";
	if (!dataDir || !electronDir) return null;
	const workspaceDir = typeof parsed.workspaceDir === "string" ? parsed.workspaceDir.trim() : "";
	return {
		version: 1,
		dataDir: resolveBeside(installDir, dataDir),
		electronDir: resolveBeside(installDir, electronDir),
		...(workspaceDir ? { workspaceDir: resolveBeside(installDir, workspaceDir) } : {}),
	};
}

export function installHomeMarkerContents(record: InstallHomeRecord, installDir: string): string {
	const relativeOrAbs = (target: string): string => {
		const rel = path.relative(installDir, target);
		if (rel && !rel.startsWith("..") && !path.isAbsolute(rel)) {
			return rel.split(path.sep).join("/");
		}
		return target;
	};
	const body: MarkerFile = {
		version: 1,
		dataDir: relativeOrAbs(record.dataDir),
		electronDir: relativeOrAbs(record.electronDir),
	};
	if (record.workspaceDir) body.workspaceDir = relativeOrAbs(record.workspaceDir);
	return `${JSON.stringify(body, null, 2)}\n`;
}

/** Default layout when the installer ties state to the chosen install folder. */
export function defaultInstallHomeRecord(installDir: string): InstallHomeRecord {
	const dataDir = path.join(installDir, "data");
	return {
		version: 1,
		dataDir,
		electronDir: path.join(installDir, "electron"),
		workspaceDir: path.join(dataDir, "worktrees"),
	};
}

export async function readInstallHomeMarker(installDir: string): Promise<InstallHomeRecord | null> {
	const markerPath = path.join(installDir, INSTALL_HOME_MARKER);
	try {
		const raw = await readFile(markerPath, "utf8");
		return parseInstallHomeMarker(raw, installDir);
	} catch {
		return null;
	}
}

/** Synchronous marker read for pre-ready Electron path pinning. */
export function readInstallHomeMarkerSync(installDir: string): InstallHomeRecord | null {
	const markerPath = path.join(installDir, INSTALL_HOME_MARKER);
	try {
		return parseInstallHomeMarker(readFileSync(markerPath, "utf8"), installDir);
	} catch {
		return null;
	}
}

export async function writeInstallHomeMarker(installDir: string, record: InstallHomeRecord): Promise<void> {
	const markerPath = path.join(installDir, INSTALL_HOME_MARKER);
	const temporary = `${markerPath}.${process.pid}.tmp`;
	await writeFile(temporary, installHomeMarkerContents(record, installDir), { encoding: "utf8", mode: 0o600 });
	await rename(temporary, markerPath);
}

/**
 * Resolve where packaged desktop state lives.
 *
 * Precedence: AO_DATA_DIR env → ao-home.json beside the exe → ~/.ao defaults.
 * When the installer marker is present, Electron userData and worker worktrees
 * stay under the chosen install location instead of the user profile.
 */
export function resolveInstallHomePaths(input: {
	env: Record<string, string | undefined>;
	homeDir: string;
	launchWorkingDirectory: string;
	isPackaged: boolean;
	execDir?: string | null;
	marker?: InstallHomeRecord | null;
}): InstallHomePaths {
	const configured = input.env.AO_DATA_DIR?.trim();
	if (configured) {
		const dataDir = path.resolve(input.launchWorkingDirectory, configured);
		return {
			dataDir,
			electronDir: path.join(dataDir, "electron"),
			workspaceDir: path.join(dataDir, "worktrees"),
			installDir: path.dirname(dataDir),
			source: "env",
		};
	}

	const installDir = input.execDir?.trim() || "";
	const marker = input.marker ?? null;
	if (input.isPackaged && installDir && marker) {
		return {
			dataDir: marker.dataDir,
			electronDir: marker.electronDir,
			workspaceDir: marker.workspaceDir ?? path.join(marker.dataDir, "worktrees"),
			installDir,
			source: "marker",
		};
	}

	const root = path.resolve(input.homeDir, ".ao", input.isPackaged ? "" : "dev");
	const dataDir = path.join(root, "data");
	return {
		dataDir,
		electronDir: path.join(root, "electron"),
		workspaceDir: path.join(dataDir, "worktrees"),
		installDir: root,
		source: "default",
	};
}

export type MigrateDataDirResult = {
	copied: string[];
	skipped: string[];
	workspaceDir: string;
};

const SKIP_NAMES = new Set(["running.json", "daemon.sock", "daemon.lock", "datadir.lock"]);

/**
 * Copy durable AO state from one data directory into another.
 *
 * Safe for tests and first-run migration: refuses to overwrite a destination
 * that already has an ao.db, never deletes the source, and skips live lock /
 * socket files. Callers must pass a copied fixture when verifying — never the
 * active install directory.
 */
export async function migrateDataDir(sourceDir: string, destinationDir: string): Promise<MigrateDataDirResult> {
	const source = path.resolve(sourceDir);
	const destination = path.resolve(destinationDir);
	if (source === destination) {
		throw new Error("source and destination data directories are the same");
	}
	await access(source, constants.R_OK);
	await mkdir(destination, { recursive: true, mode: 0o700 });

	const destDb = path.join(destination, "ao.db");
	try {
		await access(destDb, constants.F_OK);
		throw new Error(`destination already has ao.db at ${destDb}; refusing to overwrite`);
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
	}

	const copied: string[] = [];
	const skipped: string[] = [];
	const entries = await readdir(source, { withFileTypes: true });
	for (const entry of entries) {
		if (SKIP_NAMES.has(entry.name) || entry.name.endsWith(".tmp") || entry.name.startsWith(".")) {
			skipped.push(entry.name);
			continue;
		}
		const from = path.join(source, entry.name);
		const to = path.join(destination, entry.name);
		if (entry.isDirectory()) {
			await cp(from, to, { recursive: true, force: false, errorOnExist: true });
			copied.push(entry.name);
			continue;
		}
		if (entry.isFile()) {
			await copyFile(from, to);
			copied.push(entry.name);
			continue;
		}
		skipped.push(entry.name);
	}

	const workspaceDir = path.join(destination, "worktrees");
	await mkdir(workspaceDir, { recursive: true, mode: 0o700 });
	return { copied, skipped, workspaceDir };
}

export async function ensureInstallHomeLayout(paths: InstallHomePaths): Promise<void> {
	await mkdir(paths.dataDir, { recursive: true, mode: 0o700 });
	await mkdir(paths.electronDir, { recursive: true, mode: 0o700 });
	await mkdir(paths.workspaceDir, { recursive: true, mode: 0o700 });
}

export async function pathExists(target: string): Promise<boolean> {
	try {
		await stat(target);
		return true;
	} catch {
		return false;
	}
}

/** Remove a directory tree only when it looks like a migration fixture. */
export async function removeFixtureTree(target: string): Promise<void> {
	await rm(target, { recursive: true, force: true });
}
