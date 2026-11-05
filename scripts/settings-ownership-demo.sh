#!/usr/bin/env bash
#
# settings-ownership-demo.sh — an isolated demo server seeded for the
# "Where settings come from" inspection (agent-settings-ownership), for its
# browser spec and Demo checkpoints.
#
# Usage:
#   ./scripts/settings-ownership-demo.sh [port]      # default 8947
#
# Run it as a tracked background process; it stays in the foreground serving
# the demo until stopped (./scripts/stop-demo-server.sh <port>).
#
# Workspace copies of an agent are written by the server's startup snapshot
# (and by blueprint/staffing flows), not by attaching an agent through the API.
# So the seed runs in two passes on one sandbox: create the library agent and
# workspaces, restart so startup takes each workspace's own copy, then change
# one copy through the existing workspace model editor. Every write goes to the
# fresh sandbox under $TMPDIR (HOME and ORI_DATA_DIR redirected, desktop
# launches off); nothing touches the real home folder, real credentials or any
# provider.
#
# Seeded (names are fixed; the spec looks them up through the API):
#   agent      "Ownership Writer"  model gpt-4o-mini, prompt "Library prompt."
#   workspace  "Ownership Same"    own copy, equal to the library (untracked)
#   workspace  "Ownership Custom"  own copy, model changed to gpt-4.1
#   workspace  "Ownership Direct"  attached later: no copy, reads the library
#
# The sandbox path is printed as SANDBOX=<path>; remove it with one rm -rf.

set -euo pipefail

port="${1:-8947}"
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $port is already in use; choose another port" >&2
	exit 2
fi

go build -o bin/ori-agent ./cmd/server
binary="$repo_root/bin/ori-agent"
tmp_root="${TMPDIR:-/tmp}"
sandbox="$(mktemp -d "${tmp_root%/}/ori-demo.XXXXXX")"
[[ -d "$sandbox" ]] || {
	echo "could not create a sandbox" >&2
	exit 1
}
printf 'worktree=%s\n' "$(pwd -P)" >"$sandbox/.ori-demo-owner"
echo "SANDBOX=$sandbox"
echo "URL=http://localhost:$port"

base="http://127.0.0.1:$port"
server_pid=""

start_server() {
	(cd "$sandbox" && exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" NO_BROWSER=1 \
		ORI_NO_DESKTOP_OPEN=1 "$binary" >>"$sandbox/server.log" 2>&1) &
	server_pid=$!
	for _ in $(seq 1 240); do
		if curl -fsS -o /dev/null "$base/api/agents" 2>/dev/null; then return 0; fi
		if ! kill -0 "$server_pid" 2>/dev/null; then
			echo "server exited; see $sandbox/server.log" >&2
			exit 1
		fi
		sleep 0.5
	done
	echo "server did not answer on $port" >&2
	exit 1
}

stop_server() {
	if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
		kill "$server_pid"
		wait "$server_pid" 2>/dev/null || true
	fi
	server_pid=""
}
trap stop_server EXIT INT TERM

post() { curl -fsS -X "$1" "$base$2" -H 'content-type: application/json' -d "$3"; }

agent="Ownership Writer"
agent_path="Ownership%20Writer"

start_server
post POST /api/agents "{\"name\":\"$agent\",\"model\":\"gpt-4o-mini\",\"system_prompt\":\"Library prompt.\"}" >/dev/null
same_id="$(post POST /api/workspaces "{\"name\":\"Ownership Same\",\"entry_agent_name\":\"$agent\"}" | jq -r .folder.id)"
custom_id="$(post POST /api/workspaces "{\"name\":\"Ownership Custom\",\"entry_agent_name\":\"$agent\"}" | jq -r .folder.id)"
stop_server

# Second pass: startup snapshots each referenced agent into its workspace.
start_server
post PATCH "/api/workspaces/$custom_id/agents/$agent_path" '{"model":"gpt-4.1","llm_provider":"openai"}' >/dev/null
direct_id="$(post POST /api/workspaces '{"name":"Ownership Direct"}' | jq -r .folder.id)"
post POST "/api/workspaces/$direct_id/agents" "{\"agent_name\":\"$agent\"}" >/dev/null

jq -n --arg agent "$agent" --arg same "$same_id" --arg custom "$custom_id" --arg direct "$direct_id" \
	'{agent:$agent, workspaces:{same:$same, custom:$custom, direct:$direct}}' >"$sandbox/settings-ownership-fixture.json"
echo "FIXTURE=$sandbox/settings-ownership-fixture.json"
echo "READY"

wait "$server_pid"
