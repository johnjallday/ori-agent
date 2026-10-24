#!/usr/bin/env bash
#
# demo-inbox-folder.sh — an isolated demo server for the inbox-folder hire
# (folder-inbox-hire), with no model configured and folders seeded where the
# sandboxed folder chips reach them.
#
# Usage:
#   ./scripts/demo-inbox-folder.sh [--port PORT] [--sandbox DIR]
#
# 1. A new sandbox under $TMPDIR (or --sandbox, which must be under $TMPDIR),
#    the demo server (scripts/demo-server.sh: builds this worktree, redirects
#    HOME and ORI_DATA_DIR, sets ORI_NO_DESKTOP_OPEN=1). No Codex home and no
#    provider API key is passed, so no model provider is configured.
# 2. Seeded folders, every one a disposable fixture of loose files:
#      Downloads   a dump: 25 loose files of 6 kinds, no project marker
#      Documents   a mixed folder: one Go module ("widget") beside 22 loose files
#      Desktop     an empty folder: one loose file
# 3. The personal assistant and its HQ, through the same host routes the
#    Playwright suites use, so Home's Hire an agent and the composer's Add folder
#    are both available at once.
#
# The server keeps running in the foreground until interrupted; it is stopped on
# exit. The port must be free: nothing already listening is ever stopped. The
# sandbox is left under $TMPDIR (its path is printed) for its logs.

set -euo pipefail

port=8952
sandbox=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--port)
		port="${2:-}"
		shift 2
		;;
	--sandbox)
		sandbox="${2:-}"
		shift 2
		;;
	-h | --help)
		echo "usage: $0 [--port PORT] [--sandbox DIR]"
		echo "Serves this worktree on an isolated sandbox with no model and seeded inbox folders."
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
done
[[ "$port" =~ ^[0-9]+$ ]] || {
	echo "--port must be a number, got '$port'" >&2
	exit 2
}

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}
cd "$repo_root"

if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $port is in use; pick another with --port" >&2
	exit 2
fi

tmp_root="${TMPDIR:-/tmp}"
tmp_root="$(cd "${tmp_root%/}" && pwd -P)"
if [[ -z "$sandbox" ]]; then
	sandbox="$tmp_root/ori-inbox-demo-$(date +%Y%m%d-%H%M%S)-$$"
fi
mkdir -p "$sandbox"
sandbox="$(cd "$sandbox" && pwd -P)"
# An empty sandbox would start the server in the worktree; one outside $TMPDIR
# could be a real home folder.
case "$sandbox/" in
"$tmp_root"/*/) ;;
*)
	echo "refusing $sandbox: it is not under \$TMPDIR ($tmp_root)" >&2
	exit 2
	;;
esac
if [[ -n "${HOME:-}" && "$sandbox" == "$(cd "$HOME" && pwd -P)" ]]; then
	echo "refusing the real home folder" >&2
	exit 2
fi
echo "SANDBOX=$sandbox"

# seed_loose writes N files named <stem>-<i>.<ext> into a folder, cycling through
# the given extensions, each dated a few days apart.
seed_loose() {
	local dir="$1" count="$2" stem="$3"
	shift 3
	local extensions=("$@") i ext
	mkdir -p "$dir"
	for ((i = 1; i <= count; i++)); do
		ext="${extensions[$(((i - 1) % ${#extensions[@]}))]}"
		printf 'fixture %d\n' "$i" >"$dir/$stem-$i.$ext"
		touch -t "$(date -v-"$((i % 9))"d +%Y%m%d%H%M)" "$dir/$stem-$i.$ext" 2>/dev/null || true
	done
}

seed_loose "$sandbox/Downloads" 25 download pdf png zip dmg csv mov
echo "DOWNLOADS=$sandbox/Downloads"

seed_loose "$sandbox/Documents" 22 note docx pdf txt xlsx png
widget="$sandbox/Documents/widget"
mkdir -p "$widget"
printf 'module example.com/widget\n\ngo 1.22\n' >"$widget/go.mod"
for name in main util handler store config; do
	printf 'package main\n' >"$widget/$name.go"
done
echo "DOCUMENTS=$sandbox/Documents"

mkdir -p "$sandbox/Desktop"
printf 'fixture\n' >"$sandbox/Desktop/todo.txt"
echo "DESKTOP=$sandbox/Desktop"

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

# No model provider reaches the server: no Codex home and no provider API key,
# so the card and its Hire and start are shown working with no model at all.
env -u CODEX_HOME -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY -u USE_OLLAMA \
	./scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/server.log" 2>&1 &
base="http://localhost:$port"
for _ in $(seq 1 300); do
	if curl -fsS -o /dev/null -m 2 "$base/" 2>/dev/null; then break; fi
	sleep 1
done
curl -fsS -o /dev/null -m 2 "$base/" || {
	echo "the server did not answer on port $port; see $sandbox/server.log" >&2
	exit 1
}

api() {
	curl -fsS -m 20 -X "$1" -H 'Content-Type: application/json' ${3:+--data "$3"} "$base$2"
}
api POST /api/onboarding/skip >/dev/null
api POST /api/settings/workspace-root '{"workspace_root":""}' >/dev/null
version="$(api GET /api/personal-assistant | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const a=JSON.parse(s).personal_assistant;console.log(a.state+" "+(a.state_version||0))})')"
state="${version% *}"
version="${version#* }"
if [[ "$state" != "active" && "$state" != "paused" && "$state" != "needs_hq" ]]; then
	version="$(api POST /api/personal-assistant/hire "{\"request_id\":\"inbox-demo-hire\",\"if_version\":$version,\"display_name\":\"Atlas\",\"mandate\":\"Help me keep my folders tidy.\",\"focus_areas\":[\"plan_my_day\"]}" |
		node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>console.log(JSON.parse(s).personal_assistant.state_version))')"
fi
if [[ "$state" != "active" && "$state" != "paused" ]]; then
	api POST /api/personal-assistant/hq "{\"request_id\":\"inbox-demo-hq\",\"if_version\":$version,\"name\":\"My HQ\",\"timezone\":\"UTC\"}" >/dev/null
fi
echo "URL=$base"
echo "READY: Home's Hire an agent and the composer's Add folder can attach Downloads, Documents or Desktop."
wait
