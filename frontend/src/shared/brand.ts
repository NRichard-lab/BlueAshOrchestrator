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
	/**
	 * Product name as written in UI text (see brand-text.ts). Separate from displayName, which is also the
	 * exe ProductName and the NSIS product name: changing that one changes the installed identity.
	 */
	visibleName: string;
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
