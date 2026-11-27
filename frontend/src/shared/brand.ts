// Product identity profiles.
//
// BlueAshAI is a separate product from the upstream Orchestrator and must be able to run next to an
// installed Orchestrator without claiming its URL scheme, sharing its state, run file, daemon port
// or Windows named pipes, or stopping its daemon. Every such identity value lives in brands.json
// (the single source for this module, forge.config.ts and frontend/scripts/build-daemon.mjs, which
// stamps the Go daemon/CLI through ldflags into backend/internal/brand).
//
// The profile is chosen at BUILD time: `AO_BRAND=blueashai` is the production build setting
// (build-artifacts.yml, local release builds) and is compiled in through Vite `define`
// (vite.brand.ts), so a runtime environment variable cannot switch it. When it is unset (unit tests,
// legacy/dev builds) the legacy Orchestrator values apply, which keeps existing behavior and tests.
//
// Windows named pipes (ao-supervise, ao-browser, ao-browser-stream, ...) need no field here: both the
// Go daemon and the app derive them from the run file's directory NAME, so stateDirName ".blueashai"
// already yields pipes distinct from ".ao" (for example ao-supervise--blueashai).
//
// This module is pure (no node/electron imports) so forge.config.ts, the main process, the preload
// and the renderer can all import it.
import brands from "./brands.json";

export type Brand = {
	id: "legacy" | "blueashai";
	/** Visible product name (window title, About, installer, shortcuts, Apps & Features). */
	displayName: string;
	/** Packaged binary name without .exe; NSIS closes running apps by this name. */
	executableName: string;
	/** electron-packager app name (output folder). */
	packagerName: string;
	/** electron-builder appId; the NSIS install registry GUID is derived from it. */
	appId: string;
	/** Windows AppUserModelID (taskbar grouping, notifications). */
	appUserModelId: string;
	/** NSIS installer file name pattern (electron-builder macros). */
	installerArtifactName: string;
	/** Unversioned Windows installer alias used by `ao start` and the release conductor. */
	windowsAssetName: string;
	/** Folder the per-user NSIS install uses under %LOCALAPPDATA%\\Programs (what `ao start` looks for). */
	windowsInstallDirName: string;
	/** electron-updater cache directory name under %LOCALAPPDATA%. */
	updaterCacheDirName: string;
	/** Per-user state directory under the home folder (run file, daemon data, sign-in). */
	stateDirName: string;
	/** Default daemon API port; AO_PORT still overrides. */
	defaultPort: number;
	/** URL scheme this app registers and handles (never another product's). */
	protocolScheme: string;
	/**
	 * Chrome Native Messaging host name for Chrome mode. Chrome keys the registration by it (an HKCU
	 * value on Windows, a manifest file name elsewhere), so each product needs its own: sharing one
	 * means whichever product ran `ao browser chrome setup` last owns the other's bridge too.
	 * Must match brand.NativeMessagingHost in the Go daemon.
	 */
	nativeMessagingHost: string;
	/**
	 * Prefix of the instance variables (INSTANCE_ENV): "AO" keeps the legacy AO_* names, BlueAshAI uses
	 * "BLUEASH" so its daemon coordinates never share a name with an installed Orchestrator's.
	 */
	envPrefix: string;
	/**
	 * Master switch for every inherited update mechanism: the Electron auto-updater and its first-run
	 * prompt, the desktop version floor, feature builds, the update UI, and the daemon's harness/agent-CLI
	 * "newer version" advisory and its remote lookups. Compile-time only (it comes from this JSON through
	 * the AO_BRAND build profile); there is deliberately no environment or settings override.
	 */
	updatesEnabled: boolean;
	/** Whether remote (PostHog) telemetry may be enabled at all. A separate flag from updatesEnabled so egress attribution stays clean. */
	remoteTelemetryEnabled: boolean;
	/** Whether the daemon may refresh the LiteLLM model price catalog from the network. Separate from updatesEnabled. */
	pricingCatalogSyncEnabled: boolean;
};

export const LEGACY_BRAND = brands.legacy as Brand;
export const BLUEASHAI_BRAND = brands.blueashai as Brand;

export function resolveBrand(id: string | undefined): Brand {
	return id === "blueashai" ? BLUEASHAI_BRAND : LEGACY_BRAND;
}

/** The profile compiled into this build (see the header comment). */
export const BRAND: Brand = resolveBrand(process.env.AO_BRAND);

/** True when this build may update itself or check harness versions. False for BlueAshAI: nothing updates, nothing checks. */
export const UPDATES_ENABLED: boolean = BRAND.updatesEnabled;

/**
 * Instance variables, by their legacy AO_ name: each one locates or identifies ONE product's daemon
 * and install (run file, data directory, port, launch id, private runtime resources). The daemon and
 * every process it starts inherit them, so if BlueAshAI spelled them AO_*, an Orchestrator app started
 * from a BlueAshAI terminal or agent would read BlueAshAI's coordinates, attach to BlueAshAI's daemon
 * and never start its own (and the reverse). A build therefore reads and exports them only under its
 * own prefix, and never honors or passes on another product's spelling. Session-scoped variables
 * (AO_SESSION_ID, AO_PROJECT_ID, ...) name a session, not a daemon, and keep AO_ in every build.
 *
 * Must match instanceEnv in backend/internal/brand/brand.go (brand.test.ts checks it).
 */
export const INSTANCE_ENV = [
	"AO_RUN_FILE",
	"AO_DATA_DIR",
	"AO_PORT",
	"AO_APP_RUN_ID",
	"AO_OWNER",
	"AO_ACP_RUNTIME_DIR",
	"AO_BROWSER_RUNTIME_ADDRESS",
	"AO_BROWSER_STREAM_ADDRESS",
	"AO_BROWSER_RUNTIME_TOKEN",
	"AO_BROWSER_RUNTIME_TOKEN_STDIN",
	"AO_TMUX_BINARY",
	"AO_TMUX_SOCKET_NAME",
	"AO_WAKE_HELPER",
	"AO_APPIMAGE",
] as const;

export type InstanceEnvName = (typeof INSTANCE_ENV)[number];

/** The name a brand uses for an instance variable: unchanged for Orchestrator, BLUEASH_<rest> for BlueAshAI. */
export function brandEnvName(name: InstanceEnvName, brand: Brand = BRAND): string {
	return brand.envPrefix === "AO" ? name : `${brand.envPrefix}${name.slice(2)}`;
}

type InstanceEnvKey = InstanceEnvName extends `AO_${infer Rest}` ? Rest : never;

/** This build's instance-variable names, keyed without the prefix: process.env[ENV.RUN_FILE]. */
export const ENV = Object.fromEntries(INSTANCE_ENV.map((name) => [name.slice(3), brandEnvName(name)])) as Record<
	InstanceEnvKey,
	string
>;

/**
 * Remove another product's instance variables from an environment, in place: the AO_ spellings when
 * this build has its own prefix, nothing for Orchestrator itself. Run on process.env before anything
 * reads it and on every environment handed to the daemon, so a BlueAshAI app started from an
 * Orchestrator terminal neither acts on Orchestrator's coordinates nor passes them to its sessions.
 * Returns the names removed.
 */
export function scrubForeignInstanceEnv(env: Record<string, string | undefined>, brand: Brand = BRAND): string[] {
	if (brand.envPrefix === "AO") return [];
	const foreign = new Set<string>(INSTANCE_ENV);
	const removed: string[] = [];
	// Windows environment names are case-insensitive, so match folded.
	for (const key of Object.keys(env)) {
		if (foreign.has(key.toUpperCase())) {
			delete env[key];
			removed.push(key);
		}
	}
	return removed;
}

/** This build's Chrome Native Messaging host name (see Brand.nativeMessagingHost). */
export const NATIVE_MESSAGING_HOST: string = BRAND.nativeMessagingHost;

/** Visible name for a product id reported by a daemon (running.json / health payload). */
export function productName(product: string): string {
	if (product === BRAND.id) return BRAND.displayName;
	if (product === LEGACY_BRAND.id) return "Orchestrator";
	if (product === BLUEASHAI_BRAND.id) return BLUEASHAI_BRAND.displayName;
	return `another product (${product})`;
}
