# Release-candidate testing and promotion for devops.sh.
#
# These actions drive the existing RC lifecycle (docs/RELEASE_CHECKLIST.md);
# nothing here tags, merges or publishes. Promotion runs the same read-only
# check as the Promote Release workflow, then dispatches that workflow through
# release.sh. GitHub validates again and holds the stable tag behind the
# `release` environment reviewer on github.com, which devops never approves.
# macOS bash 3.2 compatible.

devops_rc_tag_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.([1-9][0-9]*)$'
devops_stable_tag_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

rc_tag=""
rc_state=""
rc_stable=""
rc_selected_tag=""
rc_release_prerelease=""
rc_release_url=""
rc_installer_digest=""
rc_server_pid=""

# Equal-width digits, so plain string comparison orders versions numerically.
rc_sort_key() {
  printf '%06d%06d%06d%06d' "$1" "$2" "$3" "$4"
}

# The active candidate is the newest vX.Y.Z-rc.N for a version with no stable
# release yet. Drafts count: a newer draft RC supersedes an older published one,
# and the promotion check would refuse the older tag.
load_rc_status() {
  local listing tag draft prerelease key stable_key="" best_key=""

  rc_tag=""
  rc_state=""
  rc_stable=""
  listing="$(gh release list --limit 50 --json tagName,isDraft,isPrerelease \
    --template '{{range .}}{{printf "%s\t%v\t%v\n" .tagName .isDraft .isPrerelease}}{{end}}')" || return $?

  while IFS=$'\t' read -r tag draft prerelease; do
    if [[ "$draft" == false && "$prerelease" == false && "$tag" =~ $devops_stable_tag_pattern ]]; then
      key="$(rc_sort_key "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" 0)"
      if [[ -z "$stable_key" || "$key" > "$stable_key" ]]; then
        stable_key="$key"
        rc_stable="$tag"
      fi
    fi
  done <<< "$listing"

  while IFS=$'\t' read -r tag draft prerelease; do
    if [[ ! "$tag" =~ $devops_rc_tag_pattern ]]; then
      continue
    fi
    key="$(rc_sort_key "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" 0)"
    if [[ -n "$stable_key" && ! "$key" > "$stable_key" ]]; then
      continue
    fi
    key="$(rc_sort_key "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[4]}")"
    if [[ -z "$best_key" || "$key" > "$best_key" ]]; then
      best_key="$key"
      rc_tag="$tag"
      if [[ "$draft" == true ]]; then
        rc_state=draft
      else
        rc_state=prerelease
      fi
    fi
  done <<< "$listing"
}

rc_status_summary() {
  case "$rc_state" in
    prerelease) printf '%s is published for testing.' "$rc_tag" ;;
    draft) printf '%s is still a draft (building, or its Release run failed).' "$rc_tag" ;;
    *) printf 'No release candidate in testing.' ;;
  esac
}

# Sets rc_selected_tag to an explicit exact tag, or to the active candidate once
# it is a published prerelease.
resolve_rc_tag() {
  local requested="$1"

  rc_selected_tag=""
  if [[ -n "$requested" ]]; then
    if [[ ! "$requested" =~ $devops_rc_tag_pattern ]]; then
      printf 'expected an exact release-candidate tag such as v0.0.114-rc.1, got: %s\n' "$requested" >&2
      return 2
    fi
    rc_selected_tag="$requested"
    return 0
  fi

  load_rc_status || return $?
  if [[ -z "$rc_tag" ]]; then
    printf 'No release candidate is in testing%s.\n' "${rc_stable:+ (latest stable: $rc_stable)}" >&2
    return 1
  fi
  if [[ "$rc_state" == draft ]]; then
    printf '%s is still a draft: its Release workflow is building or failed.\n' "$rc_tag" >&2
    printf 'Inspect it with: gh run list --workflow release.yml --branch %s\n' "$rc_tag" >&2
    return 1
  fi
  rc_selected_tag="$rc_tag"
}

rc_stdin_is_terminal() {
  [[ -t 0 ]]
}

rc_platform() {
  printf '%s:%s' "$(uname -s)" "$(uname -m)"
}

# Only macOS DMGs are fetched and launched from here. Other platforms still get
# the test card and the release link.
rc_installer_asset() {
  local version="${1#v}"
  case "$(rc_platform)" in
    Darwin:arm64) printf 'OriAgent-%s-arm64.dmg' "$version" ;;
    Darwin:x86_64) printf 'OriAgent-%s-amd64.dmg' "$version" ;;
  esac
}

rc_kit_dir() {
  local root="${ORI_RC_DIR:-$HOME/Downloads/ori-rc}"
  printf '%s/%s' "${root%/}" "$1"
}

# One read returns publication state, the release page, and GitHub's own
# SHA-256 digest per asset. checksums.txt omits the DMGs and the MSI.
load_rc_release() {
  local tag="$1" installer="$2" output name digest

  rc_release_prerelease=""
  rc_release_url=""
  rc_installer_digest=""
  output="$(gh api "repos/{owner}/{repo}/releases/tags/$tag" \
    --jq '"\(.prerelease)\t\(.html_url)", (.assets[] | "\(.name)\t\(.digest // "")")')" || return $?
  IFS=$'\t' read -r rc_release_prerelease rc_release_url <<< "$output"
  while IFS=$'\t' read -r name digest; do
    if [[ -n "$installer" && "$name" == "$installer" ]]; then
      rc_installer_digest="${digest#sha256:}"
    fi
  done <<< "$output"
}

# --skip-existing keeps a card or installer that is already in the kit.
rc_download() {
  local tag="$1" kit="$2" pattern
  local -a args=(release download "$tag" --dir "$kit" --skip-existing)
  shift 2
  for pattern in "$@"; do
    args+=(--pattern "$pattern")
  done
  gh "${args[@]}"
}

rc_file_sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

test_rc_action() {
  local requested="" argument tag kit installer report actual reply

  for argument in "$@"; do
    case "$argument" in
      -*)
        printf 'test-rc takes at most one exact release-candidate tag\n' >&2
        return 2
        ;;
      *)
        if [[ -n "$requested" ]]; then
          printf 'test-rc takes at most one exact release-candidate tag\n' >&2
          return 2
        fi
        requested="$argument"
        ;;
    esac
  done

  resolve_rc_tag "$requested" || return $?
  tag="$rc_selected_tag"
  installer="$(rc_installer_asset "$tag")"
  report="rc-test-report-$tag.md"
  kit="$(rc_kit_dir "$tag")"

  if ! load_rc_release "$tag" "$installer"; then
    printf 'Could not read the published release %s. Drafts cannot be tested; wait for its Release workflow.\n' "$tag" >&2
    return 1
  fi
  if [[ "$rc_release_prerelease" != true ]]; then
    printf '%s is not a published prerelease; refusing to treat it as a candidate.\n' "$tag" >&2
    return 1
  fi
  if [[ -n "$installer" && -z "$rc_installer_digest" ]]; then
    printf '%s has no %s asset with a GitHub digest to verify against.\n' "$tag" "$installer" >&2
    return 1
  fi

  mkdir -p "$kit" || return 1
  printf 'Downloading %s into %s ...\n' "$tag" "$kit"
  if [[ -n "$installer" ]]; then
    rc_download "$tag" "$kit" "$report" "$installer" || return $?
    actual="$(rc_file_sha256 "$kit/$installer")" || return 1
    if [[ "$actual" != "$rc_installer_digest" ]]; then
      printf 'SHA-256 mismatch for %s\n  GitHub: %s\n  local:  %s\n' \
        "$installer" "$rc_installer_digest" "$actual" >&2
      printf 'Delete %s and run test-rc again.\n' "$kit/$installer" >&2
      return 1
    fi
  else
    rc_download "$tag" "$kit" "$report" || return $?
  fi

  printf '\nRelease candidate %s\n' "$tag"
  printf '  Release    %s\n' "$rc_release_url"
  if [[ -n "$installer" ]]; then
    printf '  Installer  %s\n' "$kit/$installer"
    printf '  SHA-256    %s (matches GitHub)\n' "$actual"
  else
    printf '  Installer  download yours from the release page (only macOS DMGs are fetched here)\n'
  fi
  printf '  Test card  %s\n' "$kit/$report"
  printf '  Protocol   %s\n' "$repo_root/docs/RC_TEST_PROTOCOL.md"
  printf '\nInstall it in a disposable OS user or VM for installer and native-app checks.\n'
  printf 'Save your results as rc-test-results-%s-<you>.md; keep the card unchanged.\n' "$tag"
  printf 'After an APPROVE decision: ./scripts/devops.sh promote %s\n' "$tag"

  if [[ -z "$installer" ]] || ! rc_stdin_is_terminal; then
    return 0
  fi
  printf "\nLaunch this DMG's server now in an isolated local profile for web checks? [y/N] "
  IFS= read -r reply || return 0
  if [[ "$reply" == y || "$reply" == Y ]]; then
    rc_launch_server "$tag" "$kit" "$installer"
  fi
}

# Copy the app out of the DMG once per kit so the image is never left mounted.
rc_extract_app() {
  local kit="$1" dmg="$2" mount="$1/.dmg-mount" app="$1/OriAgent.app" status=0

  if [[ -x "$app/Contents/Resources/ori-agent" ]]; then
    return 0
  fi
  mkdir -p "$mount" || return 1
  if ! hdiutil attach "$kit/$dmg" -mountpoint "$mount" -nobrowse -readonly -quiet; then
    rmdir "$mount" 2>/dev/null
    return 1
  fi
  # A partial copy would make cp -R nest the bundle inside it.
  rm -rf -- "$app"
  cp -R "$mount/OriAgent.app" "$app" || status=1
  hdiutil detach "$mount" -quiet || status=1
  rmdir "$mount" 2>/dev/null || true
  if [[ "$status" -ne 0 || ! -x "$app/Contents/Resources/ori-agent" ]]; then
    printf 'Could not extract ori-agent from %s.\n' "$dmg" >&2
    return 1
  fi
}

# An occupied port is dangerous, not just busy: ori-agent stops any other
# ori-agent it finds listening on its port.
rc_free_port() {
  python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

rc_stop_server() {
  local waited=0

  if [[ -z "$rc_server_pid" ]]; then
    return 0
  fi
  kill "$rc_server_pid" 2>/dev/null || true
  while kill -0 "$rc_server_pid" 2>/dev/null && [[ "$waited" -lt 100 ]]; do
    sleep 0.1
    waited=$((waited + 1))
  done
  if kill -0 "$rc_server_pid" 2>/dev/null; then
    kill -9 "$rc_server_pid" 2>/dev/null || true
  fi
  wait "$rc_server_pid" 2>/dev/null || true
  rc_server_pid=""
}

# Server-only checks per RC_TEST_PROTOCOL.md: the downloaded binary, with HOME,
# ORI_DATA_DIR and the working directory all inside the kit, on an unused port.
# The profile is kept between launches so save/restart checks can be repeated.
rc_launch_server() {
  local tag="$1" kit="$2" dmg="$3" sandbox="$2/sandbox" binary port url version reply waited=0

  rc_extract_app "$kit" "$dmg" || return 1
  binary="$kit/OriAgent.app/Contents/Resources/ori-agent"

  if [[ -d "$sandbox" ]]; then
    printf 'Reuse the RC profile from earlier launches (keeps its data for restart checks)? [Y/n] '
    IFS= read -r reply || return 1
    if [[ "$reply" == n || "$reply" == N ]]; then
      rm -rf -- "$sandbox"
    fi
  fi
  mkdir -p "$sandbox/home" "$sandbox/data" "$sandbox/work" || return 1
  port="$(rc_free_port)" || return 1
  url="http://127.0.0.1:$port"

  # Only OS essentials cross into the RC: no API keys or ORI_* overrides from
  # this shell. Add a test provider key in the RC's own Settings if needed.
  (
    cd "$sandbox/work" || exit 1
    exec env -i PATH="$PATH" LANG="${LANG:-en_US.UTF-8}" TMPDIR="${TMPDIR:-/tmp}" USER="${USER:-}" \
      HOME="$sandbox/home" ORI_DATA_DIR="$sandbox/data" NO_BROWSER=1 \
      "$binary" --port="$port" --no-browser
  ) > "$sandbox/server.log" 2>&1 &
  rc_server_pid=$!
  trap 'rc_stop_server; exit 130' INT TERM

  printf 'Starting %s on %s ...\n' "$tag" "$url"
  while ! curl -fsS --noproxy '*' --max-time 1 "$url/health" > /dev/null 2>&1; do
    if ! kill -0 "$rc_server_pid" 2>/dev/null || [[ "$waited" -ge 450 ]]; then
      printf 'The RC server did not become healthy. Last lines of %s:\n' "$sandbox/server.log" >&2
      tail -n 20 "$sandbox/server.log" >&2
      rc_stop_server
      trap - INT TERM
      return 1
    fi
    sleep 0.1
    waited=$((waited + 1))
  done

  version="$(curl -fsS --noproxy '*' --max-time 5 "$url/api/updates/version" |
    python3 -c 'import json, sys; print(json.load(sys.stdin).get("version", ""))' 2>/dev/null)"
  if [[ "${version#v}" != "${tag#v}" ]]; then
    printf 'Stopping: the server reports version %s, not %s.\n' "${version:-unknown}" "$tag" >&2
    rc_stop_server
    trap - INT TERM
    return 1
  fi

  open "$url" > /dev/null 2>&1 || true
  printf '\n%s is running at %s (exact version verified).\n' "$tag" "$url"
  printf 'Profile: %s\n' "$sandbox"
  printf 'This is web-behavior evidence only, not installer or native-app coverage.\n'
  printf 'Press Enter to stop the RC server. '
  IFS= read -r reply || true
  rc_stop_server
  trap - INT TERM
  printf 'Stopped. The profile stays at %s for the next launch.\n' "$sandbox"
}

release_candidate_check() {
  python3 "$script_dir/release-candidate.py" check-promotion --rc "$1"
}

release_dispatch_promote() {
  bash "$script_dir/release.sh" promote "$1" --yes
}

promote_rc_action() {
  local requested="" assume_yes=0 argument tag typed actions_url

  for argument in "$@"; do
    case "$argument" in
      --yes) assume_yes=1 ;;
      -*)
        printf 'promote takes at most one exact release-candidate tag, plus --yes\n' >&2
        return 2
        ;;
      *)
        if [[ -n "$requested" ]]; then
          printf 'promote takes at most one exact release-candidate tag, plus --yes\n' >&2
          return 2
        fi
        requested="$argument"
        ;;
    esac
  done

  # Refusals that need no network come first, so they never contact GitHub.
  if [[ -n "$requested" && ! "$requested" =~ $devops_rc_tag_pattern ]]; then
    printf 'expected an exact release-candidate tag such as v0.0.114-rc.1, got: %s\n' "$requested" >&2
    return 2
  fi
  if [[ "$assume_yes" -ne 1 ]] && ! rc_stdin_is_terminal; then
    printf 'refusing to promote without a terminal; pass --yes to confirm\n' >&2
    return 2
  fi
  resolve_rc_tag "$requested" || return $?
  tag="$rc_selected_tag"

  printf 'Checking %s the way Promote Release will (exact RC, branch head, CI and installers)...\n' "$tag"
  if ! release_candidate_check "$tag"; then
    printf 'Nothing was dispatched.\n' >&2
    return 1
  fi

  printf '\nPromoting attests that you installed and tested exactly %s, completed its\n' "$tag"
  printf 'test card (docs/RC_TEST_PROTOCOL.md) and recorded APPROVE.\n'
  if [[ "$assume_yes" -ne 1 ]]; then
    printf 'Type %s to dispatch the promotion (anything else cancels): ' "$tag"
    IFS= read -r typed || return 1
    if [[ "$typed" != "$tag" ]]; then
      printf 'Cancelled; nothing was dispatched.\n'
      return 0
    fi
  fi

  release_dispatch_promote "$tag" || return $?

  actions_url="$(gh repo view --json url --template '{{.url}}' 2>/dev/null || true)"
  printf '\nNext: GitHub checks %s again, then the Promote Release run waits for the\n' "$tag"
  printf '`release` environment. Approve it there with Review deployments, and paste\n'
  printf 'your completed test-card link in the approval comment:\n'
  if [[ -n "$actions_url" ]]; then
    printf '  %s/actions/workflows/promote-release.yml\n' "$actions_url"
  else
    printf '  gh run list --workflow promote-release.yml\n'
  fi
  printf 'After approval, main and the stable tag advance, stable installers are checked\n'
  printf 'and published, and a merge-back PR to dev opens (use a merge commit).\n'
}
