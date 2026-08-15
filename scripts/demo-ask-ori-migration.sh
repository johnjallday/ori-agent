#!/usr/bin/env bash
#
# Demo/verification for the Ask Ori identity migration (Issue #350, task 1.12).
#
# Boots a real server twice against each of four isolated on-disk fixtures and
# prints what actually landed in the agent store, so migration can be judged on
# persisted data rather than on unit-test doubles.
#
# Everything lives under $TMPDIR. The server is launched from *inside* the
# sandbox because settings.json is resolved relative to the working directory,
# and HOME is overridden so "Ori Workspaces" cannot touch the real tree.
#
# Usage: ./scripts/demo-ask-ori-migration.sh [base-port]

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/ori-agent"
BASE_PORT="${1:-8950}"
ROOT="${TMPDIR:-/tmp}/ask-ori-migration-$$"

if [[ ! -x "$BIN" ]]; then
  echo "Server binary missing at $BIN — run ./scripts/build-server.sh first." >&2
  exit 1
fi

mkdir -p "$ROOT"
echo "Sandbox root: $ROOT"
echo

# write_agent <fixture-dir> <agent-name> <system-prompt> <model>
write_agent() {
  local dir="$1" name="$2" prompt="$3" model="$4"
  mkdir -p "$dir/agents/$name"
  cat > "$dir/agents/$name/agent_settings.json" <<JSON
{
  "type": "general",
  "role": "orchestrator",
  "Settings": {
    "model": "$model",
    "provider": "anthropic",
    "system_prompt": "$prompt",
    "temperature": 0.25
  },
  "status": "active",
  "metadata": {
    "description": "seeded fixture",
    "tags": ["mine", "custom"],
    "favorite": true
  }
}
JSON
}

# write_sidecars seeds the per-agent state that lives outside agent_settings.json
# — exactly what the old SetAgent+DeleteAgent migration destroyed.
write_sidecars() {
  local dir="$1" name="$2"
  mkdir -p "$dir/agents/$name/skills/reaper-control"
  printf 'installed skill body\n' > "$dir/agents/$name/skills/reaper-control/SKILL.md"
  cat > "$dir/agents/$name/skills_state.json" <<'JSON'
{
  "skills": {
    "reaper-control": { "enabled": true, "trusted": true }
  }
}
JSON
}

write_settings() {
  local dir="$1"
  cat > "$dir/settings.json" <<'JSON'
{
  "system_provider": "claude_code",
  "system_model": "sonnet"
}
JSON
}

boot() {
  local dir="$1" port="$2" label="$3"
  (
    cd "$dir" || exit 1
    HOME="$dir" ORI_DATA_DIR="$dir" PORT="$port" "$BIN" > "$dir/server-$label.log" 2>&1 &
    echo $! > "$dir/server.pid"
    wait
  ) &
  local outer=$!

  local pid=""
  for _ in $(seq 1 50); do
    [[ -f "$dir/server.pid" ]] && pid="$(cat "$dir/server.pid")" && break
    sleep 0.2
  done

  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/" -o /dev/null 2>/dev/null; then
      break
    fi
    sleep 0.2
  done

  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null
  fi
  wait "$outer" 2>/dev/null
  sleep 0.3
}

report() {
  local dir="$1"
  echo "  agents on disk:"
  if [[ -d "$dir/agents" ]]; then
    for agent_dir in "$dir/agents"/*/; do
      [[ -d "$agent_dir" ]] || continue
      local name
      name="$(basename "$agent_dir")"
      local prompt model marker skill state
      prompt="$(grep -o '"system_prompt": *"[^"]*"' "$agent_dir/agent_settings.json" 2>/dev/null | head -1 | cut -d'"' -f4)"
      model="$(grep -o '"model": *"[^"]*"' "$agent_dir/agent_settings.json" 2>/dev/null | head -1 | cut -d'"' -f4)"
      marker="no"
      grep -q 'ori:system-assistant' "$agent_dir/agent_settings.json" 2>/dev/null && marker="YES"
      skill="missing"
      [[ -f "$agent_dir/skills/reaper-control/SKILL.md" ]] && skill="present"
      state="missing"
      [[ -f "$agent_dir/skills_state.json" ]] && state="present"
      printf '    - %-22s protected=%-3s model=%-16s prompt=%-22s skills/=%s skills_state=%s\n' \
        "$name" "$marker" "${model:-–}" "${prompt:-–}" "$skill" "$state"
    done
  else
    echo "    (none)"
  fi
}

run_fixture() {
  local label="$1" port="$2"
  local dir="$ROOT/$label"
  mkdir -p "$dir"
  write_settings "$dir"

  case "$label" in
    fresh-install)
      ;;
    workspace-manager)
      write_agent "$dir" "Workspace Manager" "my own prompt" "claude-opus-5"
      write_sidecars "$dir" "Workspace Manager"
      ;;
    skipped-version)
      write_agent "$dir" "Ori" "ancient prompt" "gpt-4o"
      write_sidecars "$dir" "Ori"
      ;;
    collision)
      write_agent "$dir" "Workspace Manager" "the real assistant" "claude-opus-5"
      write_sidecars "$dir" "Workspace Manager"
      write_agent "$dir" "Ask Ori" "MINE - user authored" "gpt-4o-mini"
      ;;
  esac

  echo "=== fixture: $label (port $port) ==="
  echo "  before:"
  report "$dir"

  boot "$dir" "$port" "first"
  echo "  after first boot:"
  report "$dir"

  boot "$dir" "$((port + 1))" "second"
  echo "  after second boot (idempotency):"
  report "$dir"
  echo
}

run_fixture "fresh-install"     "$BASE_PORT"
run_fixture "workspace-manager" "$((BASE_PORT + 10))"
run_fixture "skipped-version"   "$((BASE_PORT + 20))"
run_fixture "collision"         "$((BASE_PORT + 30))"

echo "Sandbox preserved for inspection: $ROOT"
echo "Remove it with: rm -rf \"$ROOT\""
