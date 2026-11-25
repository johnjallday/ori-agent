#!/usr/bin/env bash
#
# my-notes-demo-source.sh — switch an isolated demo server's notes connection,
# for My Notes demos and manual tests.
#
# Usage:
#   ./scripts/my-notes-demo-source.sh <port> ori <sandbox>
#       Disconnect, then set up a new Ori notebook "Demo Notes" (or "Demo
#       Notes 2", … on a rerun) in <sandbox>/Documents with a House project
#       note and one Inbox capture.
#   ./scripts/my-notes-demo-source.sh <port> external <sandbox>
#       Disconnect, then connect <sandbox>/Documents/Field Notebook, an Obsidian
#       vault with differently named folders and notes.
#
# <sandbox> is the SANDBOX= path `wt demo` or `scripts/demo-server.sh` printed.
# The script refuses any directory without the `.ori-demo-owner` marker those
# scripts write, so it can only touch a disposable demo. It talks to the server
# through the same API the page uses (picker select-path, create/connect,
# capture); it never edits notes on disk beyond seeding the external fixture.
# Assistant and Personal HQ must already exist (see tests/my-notes-home.spec.ts).

set -euo pipefail

usage() {
	sed -n '3,20p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
}

[[ $# -eq 3 ]] || usage
port="$1"
mode="$2"
sandbox="$3"
[[ "$port" =~ ^[0-9]+$ ]] || {
	echo "port must be a number: $port" >&2
	exit 2
}
[[ "$mode" == ori || "$mode" == external ]] || usage
if [[ ! -f "$sandbox/.ori-demo-owner" ]]; then
	echo "refusing: $sandbox is not a demo sandbox (no .ori-demo-owner)" >&2
	exit 2
fi
sandbox="$(cd "$sandbox" && pwd -P)"

base="http://localhost:$port"
endpoint="$base/api/personal-assistant/second-brain"
headers=(-H "Origin: $base" -H "Content-Type: application/json")
# Request ids must differ per run: the server replays a reused id as a no-op.
stamp="$(date +%s)$$${RANDOM}"

field() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))' "$1"; }
api() { curl -sf "${headers[@]}" "$@"; }
pick() { api -X POST "$base/api/folder-picker/select-path" -d "{\"path\":\"$1\"}" | field "d['selection_token']"; }

view="$(api "$endpoint?view=connection")"
status="$(printf '%s' "$view" | field "d['second_brain']['status']")"
if [[ "$status" != not_connected ]]; then
	version="$(printf '%s' "$view" | field "d['second_brain']['version']")"
	api -X POST "$endpoint/disconnect" -d "{\"request_id\":\"demo-disconnect-$stamp\",\"if_version\":$version}" >/dev/null
	echo "disconnected ($status)"
fi

if [[ "$mode" == ori ]]; then
	mkdir -p "$sandbox/Documents"
	# Ori sets up only an empty or new folder; a rerun gets the next free name.
	name="Demo Notes"
	for n in 2 3 4 5 6 7 8 9; do
		[[ -e "$sandbox/Documents/$name" ]] || break
		name="Demo Notes $n"
	done
	parent="$(pick "$sandbox/Documents")"
	api -X POST "$endpoint/create" -d "{\"selection_token\":\"$parent\",\"name\":\"$name\",\"request_id\":\"demo-create-$stamp\",\"project\":{\"name\":\"House\",\"answer\":\"Repaint the hallway and fix the porch light before winter.\"}}" >/dev/null
	api -X POST "$endpoint/capture" -d "{\"text\":\"Pick up paint samples on Saturday\",\"request_id\":\"demo-capture-$stamp\"}" >/dev/null
	echo "set up $name"
else
	root="$sandbox/Documents/Field Notebook"
	mkdir -p "$root/.obsidian" "$root/Expeditions" "$root/Field Log 2026"
	printf 'Not an Ori note.\n' >"$root/Expeditions/Canyon.md"
	printf 'Weather notes.\n' >"$root/Field Log 2026/Week 47.md"
	printf 'Welcome to the field notebook.\n' >"$root/Welcome.md"
	token="$(pick "$root")"
	api -X POST "$endpoint/connect" -d "{\"selection_token\":\"$token\",\"request_id\":\"demo-connect-$stamp\"}" >/dev/null
	echo "connected Field Notebook"
fi
api "$endpoint?view=connection" |
	field "' · '.join([d['second_brain']['status'], d['second_brain']['source']['name'], d['second_brain']['source']['origin'], d['second_brain']['source']['kind']])"
