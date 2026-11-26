#!/usr/bin/env bash
#
# demo-filing-sends.sh — an isolated demo server for Filing sending files from one
# workspace to another (filing-sends-to-workspace), with no model configured.
#
# Usage:
#   ./scripts/demo-filing-sends.sh [--port PORT] [--sandbox DIR] [--connect]
#
# 1. A new sandbox under $TMPDIR (or --sandbox, which must be under $TMPDIR) and
#    the demo server (scripts/demo-server.sh: builds this worktree, redirects HOME
#    and ORI_DATA_DIR, disables desktop launches; no browser is opened). No Codex
#    home and no provider API key reach the server, so no model is configured.
# 2. Two seeded folders on the sandbox's one disk:
#      Downloads   loose files of several kinds: invoices, a lease and a tax return
#                  (Documents), screenshots (Images), installers, archives, data
#      Documents   four folders (Finance, Home, Taxes, Work) and one loose file,
#                  W-9-signed.pdf, whose name a Downloads file also has, so a send
#                  of that file stays in Downloads and the receipt says why
# 3. Two workspaces, "Downloads" and "Documents", each with Filing installed and set
#    up on its folder in manual mode, through the same host routes the Playwright
#    suites use. --connect also turns on "Downloads sends to Documents" for the
#    Documents kind and scans Downloads, so Review opens on the picture.
#
# The guided Documents pre-tick (FR 6) is not seeded: creating a guided Documents
# workspace needs the reviewed creation flow. Unit tests cover it.
#
# The server keeps running in the foreground until interrupted; it is stopped on
# exit. The port must be free: nothing already listening is ever stopped. The
# sandbox is left under $TMPDIR (its path is printed) for its logs.

set -euo pipefail

port=8953
sandbox=""
connect=0
while [[ $# -gt 0 ]]; do
	case "$1" in
	--connect)
		connect=1
		shift
		;;
	--port)
		port="${2:-}"
		shift 2
		;;
	--sandbox)
		sandbox="${2:-}"
		shift 2
		;;
	-h | --help)
		echo "usage: $0 [--port PORT] [--sandbox DIR] [--connect]"
		echo "Serves this worktree on an isolated sandbox with two Filing workspaces, Downloads and Documents."
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
	sandbox="$tmp_root/ori-sends-demo-$(date +%Y%m%d-%H%M%S)-$$"
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

# A file written a moment ago is not settled, so every fixture is dated days ago.
fixture() {
	local path="$1" days="$2"
	printf 'fixture %s\n' "$(basename "$path")" >"$path"
	touch -t "$(date -v-"$days"d +%Y%m%d%H%M 2>/dev/null || date -d "-$days days" +%Y%m%d%H%M)" "$path"
}

downloads="$sandbox/Downloads"
documents="$sandbox/Documents"
mkdir -p "$downloads" "$documents"
day=1
for name in Invoice-2026-10-Acme.pdf Invoice-2026-09-Acme.pdf Lease-renewal-signed.pdf \
	2025-tax-return.pdf W-9-signed.pdf Meeting-notes.docx Packing-list.txt \
	Screenshot-1.png Screenshot-2.png Screenshot-3.png Holiday-photo.jpg \
	Installer-1.dmg Installer-2.pkg Backup.zip Old-site.tar.gz \
	Export-2026-10.csv Budget.xlsx; do
	fixture "$downloads/$name" "$((day % 9 + 1))"
	day=$((day + 1))
done
echo "DOWNLOADS=$downloads"
for folder in Finance Home Taxes Work; do
	mkdir -p "$documents/$folder"
	fixture "$documents/$folder/kept-$folder.pdf" 30
done
# The receiver's own file with a name Downloads also has: never replaced.
printf 'the receiver'"'"'s own W-9\n' >"$documents/W-9-signed.pdf"
echo "DOCUMENTS=$documents"

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

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
	curl -fsS -m 30 -X "$1" -H 'Content-Type: application/json' ${3:+--data "$3"} "$base$2"
}
# field reads one dotted field from the JSON on stdin.
field() {
	node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{let v=JSON.parse(s);for(const k of process.argv[1].split("."))v=v==null?v:v[k];console.log(v==null?"":v)})' "$1"
}
api POST /api/onboarding/skip >/dev/null

# filing_workspace NAME FOLDER: a workspace with Filing set up on FOLDER in manual
# mode. Prints "id slug".
filing_workspace() {
	local name="$1" folder="$2" created id slug ref revision
	created="$(api POST /api/workspaces "{\"name\":\"$name\"}")"
	id="$(printf '%s' "$created" | field folder.id)"
	[[ -n "$id" ]] || id="$(printf '%s' "$created" | field workspace.id)"
	slug="$(printf '%s' "$created" | field folder.folder_slug)"
	[[ -n "$slug" ]] || slug="$(printf '%s' "$created" | field workspace.folder_slug)"
	api POST "/api/workspaces/$id/capabilities/file-janitor/install" '{"source":"in-place"}' >/dev/null
	ref="$(api POST "/api/workspaces/$id/directories" "{\"name\":\"$name folder\",\"path\":\"$folder\"}" | field directory.id)"
	revision="$(api POST "/api/workspaces/$id/file-janitor/setup-review" "{\"reference_id\":\"$ref\",\"monitoring\":false}" | field review.review_revision)"
	api POST "/api/workspaces/$id/file-janitor/prepare-review" \
		"{\"reference_id\":\"$ref\",\"monitoring\":false,\"review_revision\":\"$revision\"}" >/dev/null
	echo "$id $slug"
}

read -r downloads_id downloads_slug < <(filing_workspace Downloads "$downloads")
read -r documents_id documents_slug < <(filing_workspace Documents "$documents")
echo "DOWNLOADS_WORKSPACE=$downloads_id"
echo "DOCUMENTS_WORKSPACE=$documents_id"

if [[ "$connect" == 1 ]]; then
	on="$(api POST "/api/workspaces/$downloads_id/file-janitor/connections" \
		"{\"receiver_workspace_id\":\"$documents_id\",\"kinds\":[\"documents\"]}" | field connections.sends_to.0.on)"
	echo "CONNECTED=$on"
	# A scan of Downloads, so Review opens on the picture with its Leaving card.
	api POST "/api/workspaces/$downloads_id/file-janitor/scan" >/dev/null
	echo "SCANNED=Downloads"
fi

echo "URL=$base"
echo "DOWNLOADS_SETTINGS=$base/workspaces/$downloads_slug?view=details&panel=file-janitor&tab=settings"
echo "DOCUMENTS_SETTINGS=$base/workspaces/$documents_slug?view=details&panel=file-janitor&tab=settings"
echo "READY: open Downloads' Filing Settings and Connect a workspace."
wait
