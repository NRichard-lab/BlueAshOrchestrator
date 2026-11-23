#!/usr/bin/env bash
# Fresh Ubuntu x64 entry point. The lower-level installer owns AO releases and service setup.
set -euo pipefail

# Braced so bash reads the whole script before running any of it: a download
# cut off mid-way by `curl ... | bash` runs nothing.
{
# Prints the normalized update origin (https://host[:port], no trailing slash) or
# fails. Mirrors frontend/scripts/update-origin-core.mjs and
# backend/internal/cli/update_origin.go: https only, a public DNS name (no IP,
# no localhost, no credentials/path/query), and never an upstream host.
validate_update_origin() {
	local raw="${1%/}" host suffix
	if [[ -z "$raw" ]]; then
		printf '%s\n' 'AO_UPDATE_ORIGIN is not set. Set it to your BlueAsh update server origin (https://<host>).' >&2
		return 1
	fi
	raw="${raw,,}"
	if [[ ! "$raw" =~ ^https://([a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+)(:[0-9]{1,5})?$ ]]; then
		printf 'AO_UPDATE_ORIGIN must be a bare https origin on a public DNS name, got: %s\n' "$1" >&2
		return 1
	fi
	host="${BASH_REMATCH[1]}"
	if [[ "$host" =~ ^[0-9.]+$ || "$host" == localhost || "$host" == *.localhost ]]; then
		printf 'AO_UPDATE_ORIGIN must be a public DNS name, not an address or localhost: %s\n' "$1" >&2
		return 1
	fi
	for suffix in github.com githubusercontent.com githubassets.com github.io orchestrator.inc orchestratorusercontent.com expo.dev expo.io; do
		if [[ "$host" == "$suffix" || "$host" == *".$suffix" ]]; then
			printf 'AO_UPDATE_ORIGIN must not be an upstream Orchestrator or GitHub host: %s\n' "$1" >&2
			return 1
		fi
	done
	printf '%s\n' "$raw"
}

usage() {
	printf '%s\n' 'Usage: bootstrap-self-hosted.sh [--nightly | --source-ref BRANCH] [--lan]'
	printf '%s\n' 'Installs prerequisites, then installs released Orchestrator or builds a source branch.'
	printf '%s\n' 'Downloads stable by default; --nightly installs the newest published nightly.'
	printf '%s\n' 'Releases come from the update server named by AO_UPDATE_ORIGIN (https://<host>); there is no default.'
	printf '%s\n' 'Source builds clone AO_SOURCE_REPO_URL (default: the BlueAsh repository).'
}

source_ref=""
nightly=false
tunnel=true
while (($#)); do
	case "$1" in
		--nightly) nightly=true; shift ;;
		--source-ref) source_ref="${2:?--source-ref needs a branch}"; shift 2 ;;
		--lan) tunnel=false; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; exit 2 ;;
	esac
done
if "$nightly" && [[ -n "$source_ref" ]]; then
	printf '%s\n' '--nightly cannot be combined with --source-ref.' >&2; exit 2
fi

[[ "$(id -u)" != 0 ]] || { printf '%s\n' 'Run as the host user, not root.' >&2; exit 1; }
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]] || { printf '%s\n' 'This bootstrap supports Ubuntu x64 only.' >&2; exit 1; }
command -v apt-get >/dev/null || { printf '%s\n' 'This bootstrap requires Ubuntu apt-get.' >&2; exit 1; }
[[ -z "$source_ref" || "$source_ref" =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ ]] || {
	printf '%s\n' 'Invalid source branch.' >&2; exit 2;
}

sudo apt-get update
sudo apt-get install -y ca-certificates curl gh git python3
sudo loginctl enable-linger "$(id -un)"
systemctl --user show-environment >/dev/null || {
	printf '%s\n' 'A systemd user session is required for an always-on Orchestrator host.' >&2; exit 1;
}

if "$tunnel" && ! command -v cloudflared >/dev/null; then
	sudo install -d -m 0755 /usr/share/keyrings
	curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
	printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared noble main' |
		sudo tee /etc/apt/sources.list.d/cloudflared.list >/dev/null
	sudo apt-get update
	sudo apt-get install -y cloudflared
fi

stage="$(mktemp -d)"
trap 'rm -rf -- "$stage"' EXIT
installer="$stage/setup-self-hosted.sh"
if [[ -z "$source_ref" ]]; then
	origin="$(validate_update_origin "${AO_UPDATE_ORIGIN:-}")" || exit 1
	export AO_UPDATE_ORIGIN="$origin"
	curl -fsSL "$origin/selfhost/setup-self-hosted.sh" -o "$installer"
else
	sudo apt-get install -y build-essential pkg-config xz-utils
	tools="$HOME/.local/ao-build-tools"
	mkdir -p "$tools"
	# Replace a cached Go older than the one backend/go.mod requires; keep a newer one.
	go_version=1.27.4
	cached_go="$(GOTOOLCHAIN=local "$tools/go/bin/go" env GOVERSION 2>/dev/null || true)"
	if [[ "$(printf '%s\n' "$go_version" "${cached_go#go}" | sort -V | head -n1)" != "$go_version" ]]; then
		rm -rf "$tools/go"
		curl -fsSL "https://go.dev/dl/go${go_version}.linux-amd64.tar.gz" -o "$stage/go.tar.gz"
		printf '%s  %s\n' 7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840 "$stage/go.tar.gz" | sha256sum -c -
		tar -xzf "$stage/go.tar.gz" -C "$tools"
	fi
	if [[ ! -x "$tools/node-v22.23.5-linux-x64/bin/node" ]]; then
		curl -fsSL https://nodejs.org/dist/v22.23.5/node-v22.23.5-linux-x64.tar.xz -o "$stage/node.tar.xz"
		curl -fsSL https://nodejs.org/dist/v22.23.5/SHASUMS256.txt -o "$stage/SHASUMS256.txt"
		expected="$(awk '$2 == "node-v22.23.5-linux-x64.tar.xz" { print $1 }' "$stage/SHASUMS256.txt")"
		[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { printf '%s\n' 'Node checksum missing.' >&2; exit 1; }
		printf '%s  %s\n' "$expected" "$stage/node.tar.xz" | sha256sum -c -
		tar -xJf "$stage/node.tar.xz" -C "$tools"
	fi
	export PATH="$tools/go/bin:$tools/node-v22.23.5-linux-x64/bin:$PATH"
	source_repo="${AO_SOURCE_REPO_URL:-https://github.com/NRichard-lab/BlueAshOrchestrator.git}"
	[[ "$source_repo" =~ ^https://[A-Za-z0-9._/-]+$ ]] || { printf 'AO_SOURCE_REPO_URL must be a plain https URL: %s\n' "$source_repo" >&2; exit 2; }
	if [[ "${source_repo,,}" =~ (untrivial-ai|orchestratorinc|agentwrapper|aoagents) ]]; then
		printf 'AO_SOURCE_REPO_URL must not point at the upstream Orchestrator project: %s\n' "$source_repo" >&2; exit 2
	fi
	git clone --depth 1 --single-branch --branch "$source_ref" "$source_repo" "$stage/source"
	(
		cd "$stage/source/frontend"
		export GOMAXPROCS=2 NODE_OPTIONS=--max-old-space-size=1536
		npm ci
		npm run build:daemon
		npm run build:tmux
		npm run build:acp-runtime
		npm run build:host
	)
	installer="$stage/source/scripts/setup-self-hosted.sh"
	bundle="$stage/source/frontend/dist-host/ao-host-linux-x64.tar.gz"
fi

args=()
"$nightly" && args+=(--nightly)
[[ -z "$source_ref" ]] || args+=(--bundle "$bundle")
if "$tunnel"; then
	args+=(--tunnel)
fi
bash "$installer" "${args[@]}"

printf '\nDay-two commands: %s\n' "$HOME/.ao/host/current/resources/daemon/ao remote-host status"
printf 'Service logs: journalctl --user -u ao-self-hosted.service -n 100 --no-pager\n'
}
