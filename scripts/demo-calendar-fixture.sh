#!/usr/bin/env bash
#
# demo-calendar-fixture.sh — "connect a calendar" on an isolated demo server.
#
# Builds the in-repo fake calendar MCP server (tests/fixtures/fake-calendar-mcp),
# registers and connects it as `fake-calendar`, creates a Calendar Ops workspace
# (or reuses the one the server already resolves), binds the connector, and
# saves the same mapping tests/calendar-ops.spec.ts uses. Afterwards Home, Today,
# and the Daily Brief read the fixture's today-relative meetings.
#
# Usage:
#   ./scripts/demo-calendar-fixture.sh <port>
#   ./scripts/demo-calendar-fixture.sh --build-only
#
# --build-only builds the fixture binary and prints its path, touching no
# server: tests/personal-assistant-meetings.spec.ts registers it itself from
# FAKE_CALENDAR_MCP_BIN.
#
# The port is required so the script can never fall through to the default app
# port of a real, non-sandboxed Ori. Start the server with `wt demo` (or
# ./scripts/demo-server.sh) first, and finish or skip onboarding: the script
# refuses to run while onboarding is still pending, like the Calendar Ops spec,
# whose beforeAll gets onboarding out of the way before it creates anything.
#
# Output (stdout, one per line, so a caller can `eval` or grep it):
#   FAKE_CALENDAR_MCP_BIN=<path>
#   CALENDAR_WORKSPACE_ID=<id>
#   CALENDAR_WORKSPACE_SLUG=<slug>
#   CALENDAR_DISPLAY_TZ=<IANA zone>
#
# Any non-2xx response exits non-zero with the response body on stderr.

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
bin_dir="$tmp_root/ori-fake-calendar-mcp"
binary="$bin_dir/fake-calendar-mcp"

build_fixture() {
	mkdir -p "$bin_dir"
	(cd "$repo_root" && go build -o "$binary" ./tests/fixtures/fake-calendar-mcp)
	echo "FAKE_CALENDAR_MCP_BIN=$binary"
}

if [[ "${1:-}" == "--build-only" ]]; then
	build_fixture
	exit 0
fi

port="${1:-}"
if [[ ! "$port" =~ ^[0-9]+$ ]]; then
	echo "usage: $0 <port>   (the demo server's port, e.g. 8931)" >&2
	echo "       $0 --build-only" >&2
	exit 2
fi
base="http://localhost:$port"
server_name="fake-calendar"

command -v jq >/dev/null || {
	echo "jq is required" >&2
	exit 2
}

# The fixture builds its events in the server process's local time (it runs as
# the server's child), so the display timezone must be that zone or "today"
# lands on a different calendar day. CALENDAR_DISPLAY_TZ overrides it: set it
# to match a server started with TZ=..., or to pin a zone (the Calendar Ops
# spec pins America/New_York).
display_tz="${CALENDAR_DISPLAY_TZ:-}"
if [[ -z "$display_tz" ]]; then
	localtime="$(readlink /etc/localtime 2>/dev/null || true)"
	display_tz="${localtime##*/zoneinfo/}"
	[[ -n "$localtime" && "$display_tz" != "$localtime" ]] || display_tz="UTC"
fi

# call METHOD PATH [JSON] — prints the body; exits on transport failure or non-2xx.
call() {
	local method="$1" path="$2" data="${3:-}" out status
	out="$(mktemp "$tmp_root/ori-calendar-fixture.XXXXXX")"
	if [[ -n "$data" ]]; then
		status="$(curl -sS -o "$out" -w '%{http_code}' -X "$method" "$base$path" \
			-H 'Content-Type: application/json' -d "$data")" || {
			rm -f "$out"
			echo "request failed: $method $path (is a server running on port $port?)" >&2
			exit 1
		}
	else
		status="$(curl -sS -o "$out" -w '%{http_code}' -X "$method" "$base$path")" || {
			rm -f "$out"
			echo "request failed: $method $path (is a server running on port $port?)" >&2
			exit 1
		}
	fi
	if [[ "$status" != 2* ]]; then
		echo "$method $path -> HTTP $status" >&2
		cat "$out" >&2
		echo >&2
		rm -f "$out"
		exit 1
	fi
	cat "$out"
	rm -f "$out"
}

# status_of PATH — prints only the HTTP status of a GET, for existence checks.
status_of() {
	curl -sS -o /dev/null -w '%{http_code}' "$base$1" || echo 000
}

onboarding="$(call GET /api/onboarding/status)"
if [[ "$(jq -r '.needs_onboarding' <<<"$onboarding")" == "true" ]]; then
	echo "onboarding is still pending on $base; finish it or skip it first:" >&2
	echo "  curl -X POST $base/api/onboarding/skip" >&2
	exit 1
fi

build_fixture

if [[ "$(status_of "/api/mcp/servers/$server_name/status")" == 2* ]]; then
	echo "reusing registered MCP server $server_name" >&2
else
	call POST /api/mcp/servers "$(jq -cn --arg name "$server_name" --arg cmd "$binary" \
		'{name: $name, transport: "stdio", command: $cmd, enabled: true}')" >/dev/null
fi
# Connect starts a stdio server in the background and can answer "stopped"
# before that start has begun, so poll the status until it is running.
call POST "/api/mcp/servers/$server_name/connect" >/dev/null
mcp_status=""
for _ in $(seq 1 20); do
	mcp_status="$(call GET "/api/mcp/servers/$server_name/status" | jq -r '.status')"
	[[ "$mcp_status" == "running" ]] && break
	sleep 0.5
done
if [[ "$mcp_status" != "running" ]]; then
	echo "$server_name did not start (status=$mcp_status)" >&2
	exit 1
fi

# Reuse the Calendar Ops workspace the server already resolves (FR49's
# ActiveWorkspace), so re-running this script never piles up duplicates.
portal="$(call GET /api/calendar-ops/home-portal-summary)"
workspace_id=""
if [[ "$(jq -r '.has_workspace' <<<"$portal")" == "true" ]]; then
	workspace_id="$(jq -r '.workspace_id' <<<"$portal")"
	workspace_slug="$(jq -r '.workspace_slug' <<<"$portal")"
	echo "reusing Calendar Ops workspace $workspace_slug" >&2
else
	created="$(call POST /api/workspaces \
		'{"name":"Calendar Ops","description":"","template_id":"calendar-ops","create_template_agents":true}')"
	workspace_id="$(jq -r '.folder.id // empty' <<<"$created")"
	workspace_slug="$(jq -r '.folder.folder_slug // empty' <<<"$created")"
	[[ -n "$workspace_id" ]] || {
		echo "workspace create returned no id: $created" >&2
		exit 1
	}
fi

call POST /api/calendar-ops/setup/connector "$(jq -cn --arg ws "$workspace_id" --arg name "$server_name" \
	'{workspace_id: $ws, server_name: $name}')" >/dev/null

# The mapping and selection are tests/calendar-ops.spec.ts's bindAndMapConnector;
# only the display timezone differs (see display_tz above).
saved="$(call POST /api/calendar-ops/setup/save "$(jq -cn --arg ws "$workspace_id" --arg tz "$display_tz" '{
	workspace_id: $ws,
	mapping: {
		capability: "calendar",
		operations: {
			list_calendars: {
				tool: "calendars_list",
				result_collection: "/items",
				fields: {id: "/id", name: "/summary"}
			},
			list_events: {
				tool: "events_list",
				result_collection: "/items",
				fields: {
					id: "/id", title: "/summary",
					start_time: "/start/dateTime", end_time: "/end/dateTime",
					location: "/location", description: "/description",
					all_day: "/allDay", private: "/private"
				},
				arguments: {calendar_id: "/calendarId", start_time: "/timeMin", end_time: "/timeMax"}
			},
			create_event: {
				tool: "events_insert",
				fields: {id: "/id", title: "/summary", start_time: "/start/dateTime", end_time: "/end/dateTime"},
				arguments: {
					calendar_id: "/calendarId", title: "/summary",
					start_time: "/start/dateTime", end_time: "/end/dateTime",
					time_zone: "/start/timeZone", location: "/location", description: "/description"
				}
			}
		}
	},
	selected_calendar_ids: ["primary", "team"],
	display_time_zone: $tz
}')")"
state="$(jq -r '.state' <<<"$saved")"
if [[ "$state" != "ready" ]]; then
	echo "Calendar Ops setup saved but is not ready (state=$state): $saved" >&2
	exit 1
fi

echo "CALENDAR_WORKSPACE_ID=$workspace_id"
echo "CALENDAR_WORKSPACE_SLUG=$workspace_slug"
echo "CALENDAR_DISPLAY_TZ=$display_tz"
