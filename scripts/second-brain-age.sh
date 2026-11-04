#!/usr/bin/env bash
#
# second-brain-age.sh — make a demo sandbox's second brain look like it was set
# up some days ago, so the first-week recap row can be driven without waiting;
# or, with --ask, make the ask's "Not now" look that old, so the re-offer can.
#
# Usage:
#   ./scripts/second-brain-age.sh <sandbox> [days]
#   ./scripts/second-brain-age.sh --ask <sandbox> [days]
#
# <sandbox> is the SANDBOX= path on the READY line of scripts/second-brain-demo.sh.
# days defaults to 8: past the seventh local day whatever the time zone, and well
# inside the week the recap row waits to be seen. Only the source's connected_at
# (or, with --ask, the ask's decided_at) is rewritten, in the sandbox's own
# record; it touches nothing outside the sandbox and refuses a path that is not
# one of the demo sandboxes.
set -euo pipefail

field="connected"
if [[ "${1:-}" == "--ask" ]]; then
	field="ask"
	shift
fi
sandbox="${1:-}"
days="${2:-8}"
if [[ -z "$sandbox" || ! -d "$sandbox" ]]; then
	echo "usage: $0 [--ask] <sandbox from the demo's READY line> [days]" >&2
	exit 2
fi
case "$(basename "$sandbox")" in
ori-demo.*) ;;
*)
	echo "refusing $sandbox: not an ori-demo.* sandbox" >&2
	exit 2
	;;
esac
if ! [[ "$days" =~ ^[0-9]+$ ]]; then
	echo "days must be a whole number, got: $days" >&2
	exit 2
fi

record="$(find "$sandbox" -name second-brain-v1.json -not -path '*/recovery/*' | head -1)"
if [[ -z "$record" ]]; then
	echo "no second-brain record under $sandbox; set one up (or answer the ask) first" >&2
	exit 1
fi
if [[ "$field" == "ask" ]]; then
	if ! jq -e '.ask.decision == "later" and .ask.decided_at != null' "$record" >/dev/null; then
		echo "the ask in $record was not answered with Not now; press Not now first" >&2
		exit 1
	fi
	filter='.ask.decided_at = ((now - ($days * 86400)) | strftime("%Y-%m-%dT%H:%M:%SZ"))'
else
	filter='.sources[0].connected_at = ((now - ($days * 86400)) | strftime("%Y-%m-%dT%H:%M:%SZ"))'
fi
tmp="$record.aged.$$"
jq --argjson days "$days" "$filter" "$record" >"$tmp"
chmod 600 "$tmp"
mv "$tmp" "$record"
if [[ "$field" == "ask" ]]; then
	echo "Not now was $days days ago in $record"
else
	echo "set up $days days ago in $record"
fi
