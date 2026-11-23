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
