#!/usr/bin/env bash
#
# second-brain-age.sh — make a demo sandbox's second brain look like it was set
# up some days ago, so the first-week recap row can be driven without waiting.
#
# Usage:
#   ./scripts/second-brain-age.sh <sandbox> [days]
#
# <sandbox> is the SANDBOX= path on the READY line of scripts/second-brain-demo.sh.
# days defaults to 8: past the seventh local day whatever the time zone, and well
# inside the week the recap row waits to be seen. Only the source's connected_at
# is rewritten, in the sandbox's own record; it touches nothing outside the
# sandbox and refuses a path that is not one of the demo sandboxes.
set -euo pipefail

sandbox="${1:-}"
days="${2:-8}"
if [[ -z "$sandbox" || ! -d "$sandbox" ]]; then
	echo "usage: $0 <sandbox from the demo's READY line> [days]" >&2
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
	echo "no second-brain record under $sandbox; set one up first" >&2
	exit 1
fi
tmp="$record.aged.$$"
jq --argjson days "$days" '.sources[0].connected_at = ((now - ($days * 86400)) | strftime("%Y-%m-%dT%H:%M:%SZ"))' "$record" >"$tmp"
chmod 600 "$tmp"
mv "$tmp" "$record"
echo "set up $days days ago in $record"
