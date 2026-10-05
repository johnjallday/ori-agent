# Ori-owned project mechanics. Sourced by devtools-project.sh and its unit tests.
# Workflow Git creation, navigation, confirmation and delivery remain external.
function ori_devtools_provision {
  local target="$1" env_source="$2" mode="$3"
  local claude_local="$target/.claude/settings.local.json"
  if [[ ! -f "$claude_local" ]]; then
    echo "Writing feature-worktree Claude profile ($mode)..."
    mkdir -p "$target/.claude"
    cat > "$claude_local" <<JSON
{
  "permissions": {
    "defaultMode": "$mode",
    "deny": [
      "Bash(rm -rf:*)",
      "Bash(git push --force:*)",
      "Bash(git push -f:*)",
      "Bash(./scripts/release.sh:*)",
      "Read(**/.env)",
      "Read(**/*secret*)"
    ]
  }
}
JSON
  fi
  if [[ -f "$target/scripts/build-folder-picker.sh" ]]; then
    echo "Building folder picker in new worktree..."
    (cd "$target" && bash scripts/build-folder-picker.sh)
  fi
  if [[ -n "$env_source" && -f "$env_source/.env" && ! -e "$target/.env" ]]; then
    if cp -p "$env_source/.env" "$target/.env"; then
      echo "Copied .env from $env_source (untracked local secrets)."
    else
      echo "Warning: could not copy .env from $env_source; set secrets in $target/.env by hand."
    fi
  fi
  if [[ -f "$target/package.json" ]] && command -v npm >/dev/null 2>&1; then
    echo "Installing npm dependencies in new worktree..."
    (cd "$target" && npm install)
  fi
  # Preserve legacy best-effort picker/npm behavior, not a new Git rollback.
  return 0
}

# The legacy function name is retained only here for the product-owned tests.
function wt_demo_codex_env {
  typeset -ga WT_DEMO_CODEX_ENV=()
  typeset -g WT_DEMO_CODEX_NOTE=""
  if [[ "${ORI_DEMO_NO_CODEX:-0}" == "1" ]]; then
    WT_DEMO_CODEX_ENV=(-u CODEX_HOME)
    WT_DEMO_CODEX_NOTE="Codex:        off (ORI_DEMO_NO_CODEX=1)"
    return 0
  fi
  local codex_home="${CODEX_HOME:-$HOME/.codex}"
  if [[ -d "$codex_home" ]]; then
    WT_DEMO_CODEX_ENV=("CODEX_HOME=$codex_home")
    WT_DEMO_CODEX_NOTE="Codex:        $codex_home (your Codex login; ORI_DEMO_NO_CODEX=1 isolates it)"
  fi
  return 0
}

function ori_devtools_demo {
  local demo_root="$1" demo_port="${2:-8931}"
  echo "Building $demo_root ..."
  (cd "$demo_root" && go build -o bin/ori-agent ./cmd/server) || return 1
  local demo_parent
  demo_parent="$(cd "${TMPDIR:-/tmp}" 2>/dev/null && pwd -P)" || {
    echo "Could not resolve the demo temporary directory"
    return 1
  }
  local demo_dir
  demo_dir="$(mktemp -d "$demo_parent/ori-demo.XXXXXX")" || return 1
  echo "Demo sandbox: $demo_dir   (removed automatically on exit)"
  echo "Branch:       $(git -C "$demo_root" branch --show-current)"
  echo "URL:          http://localhost:$demo_port   (Ctrl-C to stop)"
  wt_demo_codex_env
  [[ -z "$WT_DEMO_CODEX_NOTE" ]] || echo "$WT_DEMO_CODEX_NOTE"
  local demo_status=0
  {
    if [[ "${ORI_DEMO_OPEN:-0}" == "1" ]]; then
      (cd "$demo_dir" && env -u NO_BROWSER "${WT_DEMO_CODEX_ENV[@]}" HOME="$demo_dir" ORI_DATA_DIR="$demo_dir" PORT="$demo_port" ORI_NO_DESKTOP_OPEN=1 "$demo_root/bin/ori-agent") || demo_status=$?
    else
      (cd "$demo_dir" && env "${WT_DEMO_CODEX_ENV[@]}" HOME="$demo_dir" ORI_DATA_DIR="$demo_dir" PORT="$demo_port" NO_BROWSER=1 ORI_NO_DESKTOP_OPEN=1 "$demo_root/bin/ori-agent") || demo_status=$?
    fi
  } always {
    if [[ "${ORI_KEEP_DEMO_SANDBOX:-0}" == "1" ]]; then
      echo "Demo sandbox preserved: $demo_dir"
    else
      case "$demo_dir" in
        "$demo_parent"/ori-demo.*)
          rm -rf -- "$demo_dir" || {
            echo "Failed to remove demo sandbox: $demo_dir"
            [[ "$demo_status" -ne 0 ]] || demo_status=1
          }
          ;;
        *)
          echo "Refusing to remove unexpected demo sandbox: $demo_dir"
          [[ "$demo_status" -ne 0 ]] || demo_status=1
          ;;
      esac
    fi
  }
  return "$demo_status"
}
