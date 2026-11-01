#!/usr/bin/env bash
#
# second-brain-legacy.sh — make a demo sandbox's second brain look like one set
# up before the fill-in shipped.
#
# Usage:
#   ./scripts/second-brain-legacy.sh <sandbox>
#
# <sandbox> is the SANDBOX= path on the READY line of scripts/second-brain-demo.sh.
# The setup a new install makes is allowed to keep People, projects and the facts
# note up to date from the start. An older setup has no answer on record, and the
# consent card asks the first time there is something to write there. This drops
# the answer, and only the answer, from the sandbox's record, so that card can be
# driven. It touches nothing outside the sandbox and refuses a path that is not
# one of the demo sandboxes.
set -euo pipefail

sandbox="${1:-}"
if [[ -z "$sandbox" || ! -d "$sandbox" ]]; then
	echo "usage: $0 <sandbox from the demo's READY line>" >&2
	exit 2
fi
case "$(basename "$sandbox")" in
ori-demo.*) ;;
*)
	echo "refusing $sandbox: not an ori-demo.* sandbox" >&2
	exit 2
	;;
esac

record="$(find "$sandbox" -name second-brain-v1.json -not -path '*/recovery/*' | head -1)"
if [[ -z "$record" ]]; then
	echo "no second-brain record under $sandbox; set one up first" >&2
	exit 1
fi
if ! jq -e '.sources[0].fill_in' "$record" >/dev/null; then
	echo "the record already has no fill-in answer"
	exit 0
fi
tmp="$record.legacy.$$"
jq 'del(.sources[].fill_in)' "$record" >"$tmp"
chmod 600 "$tmp"
mv "$tmp" "$record"
echo "dropped the fill-in answer from $record"
