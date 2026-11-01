#!/usr/bin/env bash
#
# second-brain-demo.sh — serve the current worktree from an isolated sandbox
# with a fixture notes vault, ready for the "Connect your second brain" ask.
#
# Usage:
#   ./scripts/second-brain-demo.sh [--flavour music|software|research] [--no-vault-list] [--create|--empty] [--codex] [port]
#
# It refuses a port that is already in use (default 8947, not wt demo's 8931),
# writes a fixture vault under <sandbox>/Documents and, unless --no-vault-list,
# Obsidian's own vault list under the sandbox HOME, so the server detects that
# vault and never the real one. It then starts scripts/demo-server.sh on the
# sandbox, completes onboarding, hires the assistant and builds Personal HQ by
# API, and prints one line:
#
#   READY SANDBOX=<dir> ORI_URL=http://localhost:<port> VAULT=<path>
#
# --create is for "Set one up for me": <sandbox>/Documents is left empty, there
# is no vault list (so the ask offers no vault), and the sandbox gets three
# open follow-ups and two approved People entries for the setup to seed. The
# line then reads VAULT= (empty) DOCUMENTS=<sandbox>/Documents FOLLOW_UPS=3
# PEOPLE=2. --empty is --create without those: Ori knows nothing about the
# user yet, so the first look is the empty-seed card (FOLLOW_UPS=0 PEOPLE=0).
# To see the consent card an older setup gets, set one up and then run
# ./scripts/second-brain-legacy.sh <SANDBOX>.
#
# The server stays in the foreground; stop it with
# ./scripts/stop-demo-server.sh <port>. --codex passes your Codex home through
# (as wt demo does) and sets the system model to codex/gpt-5.6-luna, so the
# consent card names a model and the headline can be written by it. Without it
# the demo is fully isolated and the look is rules-only.
set -euo pipefail

flavour="music"
vault_list=1
create=0
empty=0
codex=0
port="8947"
while (($#)); do
	case "$1" in
	--flavour)
		flavour="${2:-}"
		shift 2
		;;
	--no-vault-list)
		vault_list=0
		shift
		;;
	--create)
		create=1
		shift
		;;
	--empty)
		# Like --create, but Ori knows nothing about the user yet: no follow-ups
		# and no People, so the first look is the empty-seed card.
		create=1
		empty=1
		shift
		;;
	--codex)
		codex=1
		shift
		;;
	-h | --help)
		sed -n '3,32p' "$0"
		exit 0
		;;
	*)
		port="$1"
		shift
		;;
	esac
done
if ! [[ "$port" =~ ^[0-9]+$ ]]; then
	echo "usage: $0 [--flavour music|software|research] [--no-vault-list] [--create|--empty] [--codex] [port]" >&2
	exit 2
fi
if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $port is already in use; choose another (another session's demo may be on it)" >&2
	exit 1
fi

repo="$(cd "$(dirname "$0")/.." && pwd)"
tmp_root="${TMPDIR:-/tmp}"
sandbox="${tmp_root%/}/ori-demo.second-brain-$port-$$"
mkdir -p "$sandbox"
if [[ ! -d "$sandbox" ]]; then
	echo "could not create the sandbox $sandbox" >&2
	exit 1
fi

vault=""
if ((create)); then
	# Nothing to read yet: an empty Documents, and no vault list for the ask to find.
	mkdir -p "$sandbox/Documents"
else
	fixture_args=(-flavours "$flavour")
	if ((vault_list)); then fixture_args+=(-home "$sandbox"); fi
	(cd "$repo" && go run ./internal/testutil/vaultfixture/cmd/vaultfixture "${fixture_args[@]}" "$sandbox/Documents") >"$sandbox/fixture.json"
	vault="$(sed -n 's/^ *"path": "\(.*\)",$/\1/p' "$sandbox/fixture.json" | head -1)"
fi

if ((codex)); then
	export CODEX_HOME="${CODEX_HOME:-$HOME/.codex}"
else
	unset CODEX_HOME
fi
"$repo/scripts/demo-server.sh" "$port" "$sandbox" >"$sandbox/server.log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' INT TERM
"$repo/scripts/wait-demo-server.sh" "$port" 180 >/dev/null

url="http://localhost:$port"
post() {
	curl -fsS -X POST -H "Origin: $url" -H 'Content-Type: application/json' -d "$2" "$url$1"
}
post /api/onboarding/complete '{}' >/dev/null
if ((create)); then
	# The HQ form confirms the Workspace Directory before it builds; without it
	# Set one up for me has no default place.
	mkdir -p "$sandbox/Ori Workspaces"
	post /api/settings/workspace-root "{\"workspace_root\":\"$sandbox/Ori Workspaces\"}" >/dev/null
fi
if ((codex)); then
	post /api/settings/system-model '{"provider":"codex","model":"gpt-5.6-luna"}' >/dev/null
fi
post /api/personal-assistant/hire '{"request_id":"demo-hire","if_version":0,"display_name":"Atlas","mandate":"Keep my projects moving and remember what I am working on.","focus_areas":["keep_projects_moving"]}' >/dev/null
version="$(curl -fsS "$url/api/personal-assistant" | sed -n 's/.*"state_version":\([0-9]*\).*/\1/p' | head -1)"
post /api/personal-assistant/hq "{\"request_id\":\"demo-hq\",\"if_version\":${version:-2},\"name\":\"Personal HQ\",\"timezone\":\"Asia/Seoul\",\"schedule_days\":[\"mon\",\"tue\",\"wed\",\"thu\",\"fri\"],\"schedule_time\":\"08:00\",\"scope\":\"all\",\"include_future_workspaces\":true,\"notify_on_ready\":false}" >/dev/null

if ((create && empty)); then
	echo "READY SANDBOX=$sandbox ORI_URL=$url VAULT= DOCUMENTS=$sandbox/Documents FOLLOW_UPS=0 PEOPLE=0"
elif ((create)); then
	# What the setup seeds: open follow-ups the user confirmed (manual ones are
	# active, not candidates) and approved People entries.
	for title in "Call the distributor" "Renew the studio lease" "Send the invoice to Sam"; do
		post /api/personal-hq/followups "{\"category\":\"i_owe\",\"direction\":\"outbound\",\"title\":\"$title\"}" >/dev/null
	done
	version="$(curl -fsS "$url/api/personal-assistant" | sed -n 's/.*"state_version":\([0-9]*\).*/\1/p' | head -1)"
	n=0
	for text in "Dana Lee is my distributor." "Sam Ortiz mixes my records."; do
		n=$((n + 1))
		post /api/personal-assistant/knowledge/explicit "{\"request_id\":\"demo-person-$n\",\"state_version\":${version:-3},\"category\":\"people\",\"text\":\"$text\"}" >/dev/null
	done
	echo "READY SANDBOX=$sandbox ORI_URL=$url VAULT= DOCUMENTS=$sandbox/Documents FOLLOW_UPS=3 PEOPLE=2"
else
	echo "READY SANDBOX=$sandbox ORI_URL=$url VAULT=$vault"
fi
wait "$server"
