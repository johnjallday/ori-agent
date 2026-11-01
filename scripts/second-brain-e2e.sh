#!/usr/bin/env bash
#
# second-brain-e2e.sh — run the second-brain Playwright specs, each against the
# isolated sandbox it needs, and stop every server it started.
#
# Usage:
#   ./scripts/second-brain-e2e.sh [stage ...]
#
# Stages (all of them when none is named):
#   vault        tests/second-brain.spec.ts         ./scripts/second-brain-demo.sh
#   setup        tests/second-brain-setup.spec.ts   ./scripts/second-brain-demo.sh --create
#   first-week   tests/second-brain-first-week.spec.ts, the cases for a second
#                brain Ori set up                    ./scripts/second-brain-demo.sh --create
#   downloads    tests/second-brain-first-week.spec.ts, the Downloads case
#                                                    ./scripts/demo-inbox-folder.sh --recurring-names
#
# Each stage gets a fresh sandbox (the specs leave state behind, and a second
# run on the same sandbox is not what they are for). Ports start at
# SECOND_BRAIN_E2E_PORT (default 8947) and go up by one per stage; a port with
# anything listening is refused, never taken over. Nothing is written outside the
# demo sandboxes under $TMPDIR, and each demo script redirects HOME and the data
# dir. Needs node, jq, lsof and a built Playwright browser (npx playwright
# install chromium). Exits 1 if any stage failed, after running the rest.
set -uo pipefail

cd "$(dirname "$0")/.."

base_port="${SECOND_BRAIN_E2E_PORT:-8947}"
if ! [[ "$base_port" =~ ^[0-9]+$ ]]; then
	echo "SECOND_BRAIN_E2E_PORT must be a number: $base_port" >&2
	exit 2
fi

stages=("$@")
if [[ ${#stages[@]} -eq 0 ]]; then
	stages=(vault setup first-week downloads)
fi
for stage in "${stages[@]}"; do
	case "$stage" in
	vault | setup | first-week | downloads) ;;
	*)
		echo "unknown stage: $stage (vault, setup, first-week, downloads)" >&2
		exit 2
		;;
	esac
done

logdir="${TMPDIR:-/tmp}/second-brain-e2e.$$"
mkdir -p "$logdir"
pids=()
ports=()

cleanup() {
	local port
	for port in "${ports[@]}"; do
		./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
	done
	local pid
	for pid in "${pids[@]}"; do
		wait "$pid" 2>/dev/null || true
	done
}
trap cleanup EXIT

# field NAME LINE prints NAME=value out of a READY line. VAULT is the line's last
# field and its path has a space in it ("Second Brain"), so it takes the rest of
# the line; every other field is one word.
field() {
	if [[ "$1" == VAULT ]]; then
		sed -n 's/.* VAULT=//p' <<<"$2"
	else
		tr ' ' '\n' <<<"$2" | sed -n "s/^$1=//p"
	fi
}

# start PORT LOGNAME COMMAND... runs a demo script in the background.
start() {
	local port="$1" name="$2"
	shift 2
	if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
		echo "port $port is in use; set SECOND_BRAIN_E2E_PORT to a free range" >&2
		return 1
	fi
	"$@" >"$logdir/$name.log" 2>&1 &
	pids+=("$!")
	ports+=("$port")
}

failed=()
index=0
for stage in "${stages[@]}"; do
	port=$((base_port + index))
	index=$((index + 1))
	echo "=== $stage (port $port)"
	case "$stage" in
	vault)
		start "$port" "$stage" ./scripts/second-brain-demo.sh "$port" || { failed+=("$stage"); continue; }
		ready="$(./scripts/wait-demo-ready.sh "$logdir/$stage.log" 400)" || { failed+=("$stage"); continue; }
		PLAYWRIGHT_BASE_URL="http://localhost:$port" SECOND_BRAIN_VAULT="$(field VAULT "$ready")" \
			npx playwright test tests/second-brain.spec.ts --project=chromium --workers=1 || failed+=("$stage")
		;;
	setup | first-week)
		start "$port" "$stage" ./scripts/second-brain-demo.sh --create "$port" || { failed+=("$stage"); continue; }
		ready="$(./scripts/wait-demo-ready.sh "$logdir/$stage.log" 400)" || { failed+=("$stage"); continue; }
		spec=tests/second-brain-setup.spec.ts
		only="" # a title filter; the first-week spec shares a file with the Downloads case
		if [[ "$stage" == first-week ]]; then
			spec=tests/second-brain-first-week.spec.ts
			only="in a second brain Ori set up"
		fi
		PLAYWRIGHT_BASE_URL="http://localhost:$port" SECOND_BRAIN_SANDBOX="$(field SANDBOX "$ready")" \
			SECOND_BRAIN_DOCUMENTS="$(field DOCUMENTS "$ready")" SECOND_BRAIN_FOLLOW_UPS="$(field FOLLOW_UPS "$ready")" \
			SECOND_BRAIN_PEOPLE="$(field PEOPLE "$ready")" \
			npx playwright test "$spec" --grep "$only" --project=chromium --workers=1 || failed+=("$stage")
		;;
	downloads)
		start "$port" "$stage" ./scripts/demo-inbox-folder.sh --port "$port" --recurring-names || { failed+=("$stage"); continue; }
		./scripts/wait-demo-server.sh "$port" 400 >/dev/null || { failed+=("$stage"); continue; }
		# The seeded folders are ready once the server answers; the script goes on to
		# hire the assistant and build Personal HQ before it prints its READY line.
		deadline=$((SECONDS + 400))
		until grep -q '^READY' "$logdir/$stage.log" 2>/dev/null; do
			if ((SECONDS >= deadline)); then
				echo "the inbox demo never printed READY" >&2
				failed+=("$stage")
				continue 2
			fi
			sleep 2
		done
		PLAYWRIGHT_BASE_URL="http://localhost:$port" FIRST_WEEK_DOWNLOADS_URL="http://localhost:$port" \
			npx playwright test tests/second-brain-first-week.spec.ts -g "Downloads" --project=chromium --workers=1 || failed+=("$stage")
		;;
	esac
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
done

echo
if ((${#failed[@]})); then
	echo "FAILED: ${failed[*]} (server logs in $logdir)"
	exit 1
fi
echo "all stages passed: ${stages[*]}"
rm -rf "$logdir"
