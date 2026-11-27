# BlueAsh update server

BlueAsh builds update from **one operator-chosen HTTPS origin**. There is no
default origin, no production domain in the source tree, and no fallback to the
upstream Orchestrator release plane (`Untrivial-ai/*`, `OrchestratorInc/*`,
`aoagents/*`, `orchestrator.inc`, `raw.githubusercontent.com` floor, Expo project
`2d7dec61-…`). When the origin is missing or invalid, every surface **fails
closed**: the update plane is disabled instead of pointed somewhere else.

This document is the contract for that server and for the code that talks to it.
The migration plan and audit it implements are
`blueash-update-server-migration-plan.md` and `update-source-audit-a4eaa4bd.md`
(session artifacts; not part of this tree).

## Configuring the origin

`{ORIGIN}` is a bare origin such as `https://updates.<your-domain>`:
https only, public DNS name, no credentials, path, query or fragment, not an IP
or `localhost`, and not an upstream host. The same rules are enforced in four
places that must stay in lockstep:

| Surface | Where | How the origin is supplied |
| --- | --- | --- |
| Desktop app | `frontend/scripts/update-origin-core.mjs` (shared), `frontend/src/main/update-origin.ts` | Baked at package time: `AO_UPDATE_ORIGIN` → `app-update.yml` (`provider: generic`, `url: {ORIGIN}/desktop/stable/`). Runtime reads only that file. |
| `ao start` (Go CLI) | `backend/internal/cli/update_origin.go` | Stamped with `-ldflags "-X github.com/aoagents/agent-orchestrator/backend/internal/cli.updateOrigin=https://<host>"`; else `AO_UPDATE_ORIGIN` at runtime. A stamped value always wins and an invalid one never falls through to the environment. |
| Self-host scripts | `scripts/setup-self-hosted.sh`, `scripts/bootstrap-self-hosted.sh` | `AO_UPDATE_ORIGIN` (required for release installs). Source builds clone `AO_SOURCE_REPO_URL` (default: the BlueAsh repository). |
| Docs site | `frontend/src/docs/src/components/mdx.tsx` | `NEXT_PUBLIC_AO_UPDATE_ORIGIN` at docs build time; unset ⇒ asset names without links. |
| Mobile | `packages/mobile/app.config.js` | `AO_EXPO_PROJECT_ID` (+ optional `AO_EXPO_OWNER`, `AO_IOS_BUNDLE_ID`, `AO_ANDROID_PACKAGE`). No project id ⇒ OTA disabled. |

CI reads the origin from the repository variable `AO_UPDATE_ORIGIN`
(`build-artifacts.yml`); the build fails if it is unset.

Packaging without a valid origin fails (`frontend/makers/update-feed.ts`). For a
local build that intentionally has updates disabled, set
`AO_ALLOW_UNCONFIGURED_UPDATE_ORIGIN=1`; the baked `app-update.yml` then contains
no feed and the app reports "no valid update server configured".
`AO_RELEASE_REPO` is now only an **optional GitHub publisher target** (mirror);
it never selects where clients update from and may not name an upstream owner.

## BlueAshAI product profile and local preview build

BlueAshAI is its own product and must run next to an installed Orchestrator. Every identity value
that could collide is in one build-time profile, `frontend/src/shared/brands.json` (selected by
`AO_BRAND=blueashai`, the production build setting; unset keeps the legacy Orchestrator values so
existing tests and builds are unchanged):

| | Orchestrator (legacy) | BlueAshAI |
| --- | --- | --- |
| Visible name / exe / installer | Orchestrator.inc / `agent-orchestrator.exe` / `Agent Orchestrator Setup <v>.exe` | BlueAshAI / `BlueAshAI.exe` / `BlueAshAI Setup <v>.exe` |
| Windows asset alias (`ao start`, conductor) | `agent-orchestrator-win32-x64.exe` | `BlueAshAI-win32-x64.exe` |
| macOS/Linux asset aliases (`ao start`) | `agent-orchestrator-darwin-<arch>.zip`, `agent-orchestrator-linux-x64.AppImage` | `BlueAshAI-darwin-<arch>.zip`, `BlueAshAI-linux-x64.AppImage` |
| macOS bundle, Linux AppImage and desktop entry | `Agent Orchestrator.app`, `agent-orchestrator.AppImage`, `agent-orchestrator-ao-app.desktop` | `BlueAshAI.app`, `BlueAshAI.AppImage`, `BlueAshAI-blueashai.desktop` |
| macOS bundle identifier (app, dictation helper, repair installer) | `dev.agent-orchestrator.desktop` | `ai.blueash.desktop` |
| Chrome Native Messaging host | `dev.ao.browser_bridge` | `ai.blueash.browser_bridge` |
| NSIS appId (install key GUID) | `dev.agent-orchestrator.desktop` (`d06535a3-…`) | `ai.blueash.desktop` (`7ebb2fd8-9d4d-5b06-b9b3-8cce0c92e0db`) |
| AppUserModelID | `dev.agent-orchestrator.desktop` | `ai.blueash.desktop` |
| Per-user state dir (run file, daemon data, sign-in) | `~/.ao` | `~/.blueashai` |
| Default daemon port | 3001 | 3101 |
| Windows pipes (derived from the state dir name) | `ao-supervise`, `ao-browser`, … | `ao-supervise--blueashai`, `ao-browser--blueashai`, … |
| URL scheme | `ao-app` | `blueashai` (never `ao-app`) |
| Updater cache dir | `agent-orchestrator-updater` | `blueashai-updater` |
| Instance variables (run file, data dir, port, launch id, runtime resources) | `AO_RUN_FILE`, `AO_DATA_DIR`, `AO_PORT`, … | `BLUEASH_RUN_FILE`, `BLUEASH_DATA_DIR`, `BLUEASH_PORT`, … |
| Product id in `running.json` and `/healthz`, `/readyz` | `legacy` | `blueashai` |

The Go daemon/CLI get the same values through ldflags (`frontend/scripts/build-daemon.mjs` →
`backend/internal/brand`). The mobile LAN bridge default (3011) is not rebranded; a second daemon
falls back to another port.

**Instance variables are per product.** The variables that locate or identify one product's daemon
(the list is `instanceEnv` in `backend/internal/brand/brand.go`, mirrored by `INSTANCE_ENV` in
`frontend/src/shared/brand.ts`) are inherited by every terminal, agent and session the daemon starts.
If both products spelled them `AO_*`, an Orchestrator started from a BlueAshAI terminal would read
BlueAshAI's run file and data dir, attach to BlueAshAI's daemon instead of starting its own, and sit
in an error loop (and the reverse). So BlueAshAI reads and exports them only as `BLUEASH_*`
(`BLUEASH_PORT`, `BLUEASH_RUN_FILE` and `BLUEASH_DATA_DIR` are the overrides), ignores the `AO_*`
spellings, and removes them from its own environment at start so they are not handed down. Session
variables (`AO_SESSION_ID`, `AO_PROJECT_ID`, …) name a session, not a daemon, and keep the `AO_`
prefix in both products. In code, name an instance variable through `brand.Env("AO_…")` (Go) or
`ENV.…` (TypeScript); a test fails on a bare literal.

**A daemon names its product.** `running.json` and both health probes carry `product`. The desktop
app and the CLI refuse a daemon that reports another product's id (status code `foreign_product`)
instead of attaching to it, replacing it or stopping it. A daemon that reports no id predates the
field (upstream Orchestrator, BlueAshAI up to 0.15.4-blueash.4); for those the executable-path check
still decides.

**BlueAshAI has no update plane at all (compiled out).** `brands.json` carries three separate
compile-time switches, all `false` for BlueAshAI and `true` for the legacy profile; they are
compiled into the app (Vite `define` of `AO_BRAND`) and stamped into the Go daemon with ldflags
(`brand.UpdatesEnabled`, `brand.RemoteTelemetryEnabled`, `brand.PricingCatalogSyncEnabled`).
No environment variable or settings file can turn them back on.

| Switch | What it removes |
| --- | --- |
| `updatesEnabled` | the first-run "Keep Orchestrator up to date automatically?" prompt, the Electron updater and every timer, the version-floor check, feature builds, the Updates settings page, the sidebar update row, the restart-to-update dialog, and the daemon's harness/agent-CLI "newer version" advisory with all its remote lookups (npm, PyPI, Homebrew, vendor release feeds, GitHub). The `update-advisory` route stays and answers `unknown / disabled`; a harness `update` operation is refused with `UPDATES_DISABLED`. Install and verify are unchanged. |
| `remoteTelemetryEnabled` | PostHog export, the renderer analytics client and both Sentry DSNs. Local event recording is unchanged. |
| `pricingCatalogSyncEnabled` | the LiteLLM model price download. Usage cost then comes only from a catalog already on disk and from the user's own price overrides. |

Build consequences: the profile always bakes a feed-less `app-update.yml`, and `forge.config.ts` /
`makers/update-feed.ts` refuse `AO_UPDATE_ORIGIN` and `AO_RELEASE_REPO` for it. `build-artifacts.yml`
no longer needs an origin variable. Guards: `node scripts/verify-update-origin.mjs brand src/shared/brands.json blueashai`
and `... app <dir> --no-feed --require-updates-off` (daemon stamps present, no update manifest shipped).

Agent CLIs are asked, not forced: with updates compiled out AO launches Claude Code with
`DISABLE_AUTOUPDATER=1`, Cursor with `--disable-auto-update`, and Codex already gets
`-c check_for_update_on_startup=false`. AO cannot control the vendors' own programs, so this is not a
guarantee of zero vendor traffic; a CLI run outside AO, or one without a documented switch, can still
check for updates by itself.

A previously enabled `update-settings.json` (a profile that pressed "Enable auto-updates" in an
earlier preview) is rewritten to disabled on the first launch, and a missing one is created disabled.

**Local unsigned preview** (no signing credentials exist yet):

```bash
# Node 22 + Go on PATH; from frontend/. No update origin, no opt-out variable: updates are compiled out.
AO_BRAND=blueashai npm run make -- --platform win32 --arch x64
node scripts/verify-update-origin.mjs app "out/BlueAshAI-win32-x64" --no-feed --require-updates-off
node scripts/brand-smoke.mjs --installer "out/make/BlueAshAI Setup <version>.exe" --out <report dir>
```

`brand-smoke.mjs` refuses to run while any BlueAshAI install is registered for the user: the installer
keeps one NSIS GUID, so installing it (even silently into a temp folder) first runs the uninstaller of
an existing install and would remove it. Pass `--control-prompt` with an older installer to prove the
first-run dialog detector sees the prompt that this build no longer shows.

Still missing for a real release: Windows Authenticode signing (`WIN_CSC_LINK`/`WIN_CSC_KEY_PASSWORD`
or a store certificate) and a publisher-name decision, a BlueAsh-owned auth/Cloud endpoint (the
`blueashai://` deep-link flows the upstream sign-in uses will not work against the upstream service),
rebranded visible strings inside the renderer UI and i18n catalogs (the window, installer, shortcuts
and About use the new name; many in-app strings still say "Orchestrator"), the macOS/Linux packagers,
and a store/brew story (all deferred; Windows only). If BlueAshAI ever needs to update itself, that
is a new, deliberate build profile with a production `AO_UPDATE_ORIGIN`, not a flag flip here.

## First release scope: Windows x64 only

The first BlueAsh release ships **Windows x64 only**. macOS and Linux **desktop**
testing is deferred; their build definitions, makers, feed names (`latest-mac.yml`,
`latest-linux.yml`) and the bootstrap asset names all remain, so nothing was removed.
The existing Linux **self-host** harness evidence (21 of 21 modes, Ubuntu 24.04) is
retained.

- `build-artifacts.yml` has a `platforms` dispatch input. Empty (the default) builds
  `win32-x64` only; `all` or a comma list (for example `win32-x64,linux-x64`) builds
  the others. The catalog and selection logic live in
  `frontend/scripts/select-release-platforms.mjs` (unit tested).
- Publish only the Windows channel files (`latest.yml`, `nightly.yml`, `pr{N}.yml`)
  and `agent-orchestrator-win32-x64.exe` for the first release. `ao start` on macOS
  or Linux will find no bootstrap asset on the server and report a download error;
  that is the intended, fail-closed behaviour until those platforms ship.
- Windows installers for this release are **unsigned unless production signing
  credentials are supplied** (`WIN_CSC_LINK` etc., see `makers/maker-nsis.ts`).
  Local test builds are unsigned or test-signed only; electron-updater verifies the
  sha512 in the feed manifest, and checks the installer signature only if the build
  declares a publisher name, which it currently does not.

## Local real-installer update test (Windows)

`frontend/scripts/update-nsis-harness.mjs` exercises the real NSIS update path on this machine,
against a loopback HTTPS feed, without touching an installed Orchestrator:

```bash
# Node 22 + Go on PATH. Builds two REAL NSIS installers under a test identity (own exe name,
# appId, product name, updater cache, no shortcuts; unsigned). Restores package.json afterwards.
AO_TEST_IDENTITY=updt1a node scripts/build-test-installers.mjs --id updt1a \
  --origin https://updates.blueash-test.example --versions 0.99.1,0.99.2 --out <installers dir>
# Install 0.99.1 into a temp folder, update it to 0.99.2 through the app, verify, clean up.
node scripts/update-nsis-harness.mjs --installers <installers dir> --baseline 0.99.1 --update 0.99.2 \
  --id updt1a --out <report dir>            # add --corrupt to serve a tampered installer
```

What makes it safe, and what it asserts, is described at the top of the harness. Points that
matter for the real release:

- The app calls `quitAndInstall(false, true)`: the update is **not silent**. The NSIS wizard
  ("Choose Installation Options", Installing, Finish) appears and the app is relaunched through
  the shell, which does **not** inherit environment variables (verified with a probe installer).
  Test builds therefore compile their identity in and read an `ao-instance.json` written only by
  the test installer (`src/main/instance-pin.ts`); release builds compile an empty identity and
  never read that file.
- Updates reuse the folder recorded by the install (a wizard install appends the app folder
  name to the chosen directory). A first install made with a bare `/D=<dir>` is re-nested one
  level deeper by a later wizard update, so test baselines install to `<dir>\<app folder>`.
- The updater verified the manifest's sha512 for the downloaded installer; a tampered payload is
  rejected (`sha512 checksum mismatch`) and nothing is staged.
- Test installers are **unsigned**; electron-updater does not check an installer signature
  unless the build declares a publisher name. Production releases need real Authenticode
  signing (`WIN_CSC_LINK`, etc.) and a decision on whether to declare a publisher name.

## Endpoint tree

```text
{ORIGIN}/desktop/stable/            latest.yml, latest-mac.yml, installers, digests.json
{ORIGIN}/desktop/nightly/           nightly.yml, nightly-mac.yml, installers, digests.json
{ORIGIN}/desktop/feature/pr{N}/     pr{N}.yml, pr{N}-mac.yml, installers, digests.json
{ORIGIN}/desktop/feature/index.json {"builds":[{pr,title,base,sha,slug,buildId,publishedAt}]}
{ORIGIN}/desktop/meta/healthz       200 "ok"
{ORIGIN}/policy/desktop-version-floor.json   {"min","latest","downloadUrl"}
{ORIGIN}/bootstrap/latest/          agent-orchestrator-{darwin-arm64,darwin-x64}.zip,
                                    agent-orchestrator-win32-x64.exe,
                                    agent-orchestrator-linux-x64.AppImage, manifest.json
{ORIGIN}/selfhost/                  latest.json, nightly.json, setup-self-hosted.sh,
                                    bootstrap-self-hosted.sh
{ORIGIN}/download/                  human download page (floor default link)
```

Client behavior pinned by tests:

- **Channels** use electron-updater's generic provider; the channel selects the
  directory (stable `latest*.yml`, nightly `nightly*.yml`, feature `pr{N}*.yml`).
  No GitHub provider, release discovery, or atom feed is used.
- **Floor** (`desktop-version-floor.ts`): fetched from `{ORIGIN}/policy/…`; a
  `downloadUrl` is honored only if it is https on exactly `{ORIGIN}`, otherwise
  `{ORIGIN}/download/`. Fetch failure skips the check (never forces an upgrade).
- **Feature picker** (`feature-builds.ts`): lists `{ORIGIN}/desktop/feature/index.json`
  (entries older than 7 days are dropped; the server prunes closed PRs). No
  baked origin ⇒ empty list; a pinned build is kept on any fetch error.
- **Packaged build with no valid baked origin**: every check refuses before any
  network I/O (`assertUpdateServerConfigured`), including builds that still carry
  a legacy `provider: github` `app-update.yml`.
- **`ao start`** downloads only `{ORIGIN}/bootstrap/latest/{asset}` and refuses
  redirects to another host. It prints the host, not a GitHub repo.
- **Self-host release installs** read `{ORIGIN}/selfhost/latest.json` (or
  `nightly.json`: a list of releases) shaped like the former release JSON
  (`tag_name`, `assets[{name,digest:"sha256:…",browser_download_url}]`) and
  require every asset URL to be under `{ORIGIN}/`.

## Staging and verifying a tree (local, no network)

```bash
cd frontend
# lay out installers into the endpoint tree + digests + bootstrap set (+ floor)
node scripts/stage-update-tree.mjs --origin https://updates.<host> --channel stable \
  --version <semver> --in <dir with installers> --out <staging dir> --git-sha <sha>
# gate: baked feed + denied upstream strings in a packaged/unpacked app
node scripts/verify-update-origin.mjs app <unpacked app dir> --origin https://updates.<host>
node scripts/verify-update-origin.mjs yml <path>/app-update.yml --origin https://updates.<host>
# gate: release-critical source paths (also CI: update-plane-guard.yml)
node scripts/verify-update-origin.mjs source ..
```

Publish ordering (external step, **not performed by anything in this tree**):
upload all artifacts and `digests.json` for the version first, verify signatures
and digests, flip channel heads (`latest*.yml`, bootstrap `manifest.json`, floor)
last, update the download page after that, and never raise the floor `min` before
the matching artifacts are live. Rollback keeps clients on `allowDowngrade:
false`, so republish a higher version instead of lowering a head.

## Dispositions (not part of the update plane)

| Item | Disposition |
| --- | --- |
| macOS differential v2 (`mac-differential-v2-*`) | Inert: `scripts/mac-differential-rollout.json` is `enabled: false` and payloads must be signed by the upstream trust keys. It still names the upstream repository; **do not enable** until it is re-keyed and repointed. Not reachable from the generic feed. |
| Auth (`auth.orchestrator.inc`), Cloud control plane (`api.aoagents.dev`, `ao-cloud` submodule), Sentry | Product services, not update traffic. Own, disable or knowingly keep (plan D8). |
| cloudflared download | Vendor release host, pinned version + checksum; unrelated to AO self-update (plan D9). |
| Bug triage CLI (`triage*.go`) | Still files issues against the upstream tracker; retarget separately (needs Go test run). |
| npm launcher `@aoagents/ao`, Go module path `github.com/aoagents/...`, bundle id `dev.agent-orchestrator.desktop` | Identity debt, not HTTP feeds. A launcher built with `-ldflags …updateOrigin=` or run with `AO_UPDATE_ORIGIN` follows this server. |
| Homebrew cask (external tap) | Not in this tree. Do not publish a cask whose URL redirects upstream; create a BlueAsh cask once `{ORIGIN}/download/` exists, or drop Brew (plan D6). |
| Mobile store listing / bundle ids | `aoagents.*` ids remain the committed defaults until a BlueAsh listing exists; override with `AO_IOS_BUNDLE_ID` / `AO_ANDROID_PACKAGE`. |
| `docs/ao-start-bootstrapper-and-npm-deprecation.md` | Historical spec for the removed GitHub bootstrap; do not implement from it. |

## Checkpoint ledger (this implementation)

Status after the local implementation. Commit SHAs and evidence output are
recorded in the session artifact `blueash-update-migration-ledger.md` because a
SHA cannot be committed inside the commit it names.

| ID | Checkpoint | Status |
| --- | --- | --- |
| C2 | Origin decision + validated configurable origin | **Code done**; the domain/hosting/publisher decision is **blocked (operator)**. No domain is invented. |
| C3 | Stand up update server + healthz | **Blocked (infrastructure)**. `desktop/meta/healthz` is staged by `stage-update-tree.mjs`. |
| C4 | Publish one BlueAsh stable set | **Blocked (signing + publish)**. Staging tool done and exercised on fixtures. |
| C5 | Client defaults: forge, updater, floor, feature builds | **Done**, unit-tested; packaged run blocked (see below). |
| C6 | CLI `ao start` | **Code + tests written**; Go tests not run locally (no Go toolchain). |
| C7 | Self-host scripts, README, docs | **Done** (scripts syntax-checked; origin validation tested; full `setup-self-hosted.test.sh` needs Linux). Brew/npm per dispositions. |
| C8 | CI conductor and guards | **Done** in-tree (`build-artifacts`, `mac-update-e2e`, `version-floor-guard`, `release-latest-guard`, new `update-plane-guard`); running them needs the `AO_UPDATE_ORIGIN` repo variable. |
| C9 | Package static-scan gate | **Done** (`verify-update-origin.mjs app|yml`, wired into `build-artifacts`); never run against a real package yet. |
| C10 | Runtime egress matrix | **Blocked**: needs packaged apps on Windows/macOS/Linux and a live origin. |
| C11 | Mobile | **Done** as fail-closed + configurable; new Expo project / store ids **blocked (operator)**. |
| C12 | Remove upstream constants | **Done** for release-critical paths (`verify-update-origin.mjs source`, CI-enforced); dispositions above. |
| C13 | Implementation complete | **Done** except the blocked external items. |
| C14 | Verification complete | **Partial**: focused unit/script tests pass; packaged, egress and Go verification are blocked as listed. |
