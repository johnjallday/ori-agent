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

# A demo sandbox records the worktree that made it, so `wt done` can offer to
# remove that worktree's leftovers (a killed `wt demo`, or any
# scripts/demo-server.sh run, which never removes its own) without touching
# another session's. A sandbox that already has a record keeps it.
typeset -g ORI_DEMO_OWNER_FILE=.ori-demo-owner

function ori_devtools_mark_demo_sandbox {
  local sandbox="$1" owner="$2" record="$1/$ORI_DEMO_OWNER_FILE"
  [[ -e "$record" || -L "$record" ]] && return 0
  print -r -- "worktree=$owner" > "$record"
}

# The owner a sandbox records: the worktree path, "" when it has no record, or
# "?" when the record is not a plain file this code wrote.
function ori_devtools_demo_sandbox_owner {
  local record="$1/$ORI_DEMO_OWNER_FILE" line
  if [[ ! -e "$record" && ! -L "$record" ]]; then
    print -r -- ""
    return 0
  fi
  if [[ -L "$record" || ! -f "$record" ]]; then
    print -r -- "?"
    return 0
  fi
  IFS= read -r line < "$record" || [[ -n "$line" ]] || line=""
  if [[ "$line" == worktree=/* ]]; then
    print -r -- "${line#worktree=}"
  else
    print -r -- "?"
  fi
}

# `list` prints one "<kind><TAB><path>" line per ori-demo.* directory directly
# in $TMPDIR:
#   owned     made by this worktree, and no process is running from it
#   in-use    made by this worktree, but some process's working directory is
#             inside it (a demo server still running), or that cannot be told
#   unmarked  no owner record (made before sandboxes had one), and idle
# Another worktree's sandboxes, symlinks and malformed records are never
# listed. `remove` deletes exactly the `owned` ones, checking each again, and
# prints "removed" or "failed" lines. Nothing else is ever removed.
function ori_devtools_demo_sandboxes {
  local root="$1" mode="$2" parent
  [[ "$mode" == list || "$mode" == remove ]] || return 2
  parent="$(cd "${TMPDIR:-/tmp}" 2>/dev/null && pwd -P)" || {
    print -u2 -- "Could not resolve the demo temporary directory"
    return 1
  }
  [[ "$parent" == /?* ]] || {
    print -u2 -- "Refusing to scan unsafe temporary directory: $parent"
    return 1
  }

  # A demo server runs with its sandbox as its working directory, so the
  # working directories of every process show which sandboxes are in use.
  # Without lsof, or with no answer from it, none can be shown idle.
  local -a cwds
  local line known=0
  if command -v lsof >/dev/null 2>&1; then
    for line in "${(@f)$(lsof -w -d cwd -Fn 2>/dev/null)}"; do
      [[ "$line" == n/* ]] && cwds+=("${line#n}")
    done
    (( ${#cwds} )) && known=1
  fi

  local dir owner cwd busy failed=0
  for dir in "$parent"/ori-demo.*(N/); do
    [[ -L "$dir" ]] && continue
    owner="$(ori_devtools_demo_sandbox_owner "$dir")"
    [[ "$owner" == "$root" || -z "$owner" ]] || continue
    busy=$(( ! known ))
    for cwd in "${cwds[@]}"; do
      if [[ "$cwd" == "$dir" || "$cwd" == "$dir"/* ]]; then
        busy=1
        break
      fi
    done
    if [[ -z "$owner" ]]; then
      if [[ "$mode" == list ]] && (( ! busy )); then
        print -r -- $'unmarked\t'"$dir"
      fi
      continue
    fi
    if (( busy )); then
      [[ "$mode" == list ]] && print -r -- $'in-use\t'"$dir"
      continue
    fi
    if [[ "$mode" == list ]]; then
      print -r -- $'owned\t'"$dir"
    elif rm -rf -- "$dir"; then
      print -r -- $'removed\t'"$dir"
    else
      print -r -- $'failed\t'"$dir"
      failed=1
    fi
  done
  return "$failed"
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
  ori_devtools_mark_demo_sandbox "$demo_dir" "$demo_root" ||
    echo "Warning: could not record this worktree in $demo_dir; wt done will not offer to remove it."
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
