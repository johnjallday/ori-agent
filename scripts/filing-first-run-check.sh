#!/usr/bin/env bash
# Credential-free acceptance checks over the existing disposable e2e harness.
# Usage: scripts/filing-first-run-check.sh matrix|upgrade [--port PORT] [--base REV]
set -euo pipefail

mode="${1:-matrix}"
if (($#)); then shift; fi
port=8947
# Frozen pre-Filing dev build: the upgrade test must retain old saved names.
base=6a32e09c32913dcbbabab0d04f69297d1a19e595
while (($#)); do
	case "$1" in
		--port) port="${2:?missing port}"; shift 2 ;;
		--base) base="${2:?missing revision}"; shift 2 ;;
		*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
done
root="$(git rev-parse --show-toplevel)"
cd "$root"
fresh() {
	env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY -u USE_OLLAMA -u CODEX_HOME \
		bash scripts/e2e-fresh.sh --port "$port" --tail 60 "$@"
}
case "$mode" in
	matrix)
		for entry in home composer; do
			for folder in Downloads 'Loose Files'; do
				theme=light
				if [[ "$entry:$folder" == 'home:Loose Files' || "$entry:$folder" == composer:Downloads ]]; then theme=dark; fi
				label="$entry-${folder// /-}-$theme"
				ORI_DEMO_SPLIT_PROFILE=1 fresh --sandbox-env ORI_FILING_SANDBOX \
					--env "ORI_FILING_CASE=$label" --env "ORI_FILING_ENTRY=$entry" \
					--env "ORI_FILING_FOLDER=$folder" --env "ORI_FILING_THEME=$theme" \
					tests/filing-first-run.spec.ts -- --workers=1 -g 'a fed'
			done
		done
		;;
	upgrade)
		log="$(mktemp "${TMPDIR:-/tmp}/ori-filing-upgrade.XXXXXX")"
		trap 'rm -f "$log"' EXIT
		fresh --rev "$base" --sandbox-env ORI_FILING_UPGRADE_SANDBOX \
			--env ORI_FILING_UPGRADE_PHASE=seed tests/filing-upgrade.spec.ts -- --workers=1 | tee "$log"
		sandbox="$(awk '/^   sandbox / {sub(/^   sandbox /, ""); print; exit}' "$log")"
		[[ -n "$sandbox" ]] || { echo 'upgrade seed did not name its sandbox' >&2; exit 1; }
		fresh --sandbox "$sandbox" --sandbox-env ORI_FILING_UPGRADE_SANDBOX \
			--env ORI_FILING_UPGRADE_PHASE=verify tests/filing-upgrade.spec.ts -- --workers=1
		;;
	*) echo 'usage: scripts/filing-first-run-check.sh matrix|upgrade [--port PORT] [--base REV]' >&2; exit 2 ;;
esac
