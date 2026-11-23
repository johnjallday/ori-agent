#!/usr/bin/env bash
# One disposable journey. Reuses the candidate-build and Chromium entrypoints;
# no live DAW, model calls or changes to the companion sources are needed.
set -euo pipefail
umask 027
case "${1:-}" in picker|both) scenario="$1" ;; *) echo 'usage: music-folder-hierarchy-demo.sh picker|both PORT REAPER_SOURCE MUSIC_SOURCE' >&2; exit 2;; esac
[[ $# == 4 && "$2" =~ ^[0-9]+$ ]] || exit 2
port="$2"; reaper="$3"; music="$4"
cd "$(dirname "$0")/.."
base="http://localhost:$port"
if curl -fsS --max-time 1 "$base/health" >/dev/null 2>&1; then echo 'port in use' >&2; exit 1; fi
sandbox=$(mktemp -d "${TMPDIR:-/tmp}/ori-music-hierarchy.XXXXXX")
echo "SANDBOX=$sandbox"
export OPENAI_API_KEY=disposable-no-inference OPENAI_BASE_URL=http://127.0.0.1:1/v1
export ORI_REAPER_PLUGIN_REF="${ORI_REAPER_PLUGIN_REF:-v0.12.0}"
server=''
stop() { if [[ -n "$server" ]]; then kill -TERM "$server" 2>/dev/null || true; wait "$server" || true; server=''; fi; }
trap stop EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
scripts/reaper-demo.sh serve --port "$port" --reaper-source "$reaper" --music-source "$music" --music-ref "${ORI_MUSIC_PLUGIN_REF:-v0.5.0}" --keep --sandbox "$sandbox" >"$sandbox/staging.log" 2>&1 & server=$!
wait_ready(){
 for _ in $(seq 1 600); do
  if grep -Fq "$1" "$2" && curl -fsS --max-time 1 "$base/health" >/dev/null 2>&1; then return; fi
  kill -0 "$server" 2>/dev/null || { echo "staging failed: $2" >&2; return 1; };sleep 1
 done
 echo "timed out waiting for sandbox: $2" >&2; return 1
}
wait_ready 'Press Ctrl-C' "$sandbox/staging.log"
scripts/folder-agent-demo-setup.sh "$base" --provider openai --model gpt-5-mini
mkdir "$sandbox/fixture"
scripts/folder-agent-fixtures.sh "$sandbox/fixture"
# The oversized, uninspected directory is part of the fixture before hashing.
python3 - "$sandbox/fixture/Ori Music Demo" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1])/'Uninspected'/'Too large';p.mkdir(parents=True)
for n in range(5001): (p/str(n)).touch()
PY
manifest(){ python3 - "$sandbox/fixture/Ori Music Demo" <<'PY'
from pathlib import Path
import hashlib,sys
root=Path(sys.argv[1]);
for p in sorted(root.rglob('*')): print(p.relative_to(root),hashlib.sha256(p.read_bytes()).hexdigest() if p.is_file() else 'directory')
PY
}
manifest >"$sandbox/source-before"
both=0; [[ "$scenario" == picker ]] || both=1
scripts/e2e.sh --port "$port" --env "ORI_MUSIC_HIERARCHY_SANDBOX=$sandbox" --env "ORI_MUSIC_HIERARCHY_BOTH=$both" tests/music-folder-hierarchy.spec.ts -- --workers=1 --output "$sandbox/evidence/intake"
if [[ "$scenario" == both ]]; then
 scripts/e2e.sh --port "$port" --env ORI_MUSIC_FOLDER_MAP=setup tests/music-folder-map.spec.ts -- --workers=1 -g 'choose two' --output "$sandbox/evidence/setup"
 scripts/e2e.sh --port "$port" --env ORI_MUSIC_FOLDER_MAP=setup tests/music-workspace-folders.spec.ts -- --workers=1 --output "$sandbox/evidence/workspace-context"
 stop
 export ORI_REVIEWED_INTEGRATION_DEV_SOURCE="$sandbox/plugin-source/reaper-plugin" ORI_REVIEWED_HOME_PROVIDER_DEV_SOURCE="$sandbox/plugin-source/music-project-management"
 scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/restart.log" 2>&1 & server=$!
 wait_ready 'URL=' "$sandbox/restart.log"
 scripts/e2e.sh --port "$port" --env ORI_MUSIC_FOLDER_MAP=existing tests/music-folder-map.spec.ts -- --workers=1 -g 'populated map' --output "$sandbox/evidence/restart"
 scripts/e2e.sh --port "$port" --env ORI_MUSIC_FOLDER_MAP=existing --env ORI_MUSIC_COMPARE_RESTART=1 tests/music-workspace-folders.spec.ts -- --workers=1 --output "$sandbox/evidence/workspace-context-restart"
fi
manifest >"$sandbox/source-after"
cmp "$sandbox/source-before" "$sandbox/source-after"
if [[ "$scenario" == both ]]; then
 shasum -a 256 "$sandbox/source-before" "$sandbox/source-after" | awk '{print $1, (NR == 1 ? "before" : "after")}' >tasks/evidence-music-workspace-folder-context/source-manifest-sha256.txt
fi
echo 'Source listing and SHA-256 hashes unchanged.'
