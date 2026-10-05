#!/usr/bin/env bash
# Install/remove the fixed-purpose root launchd helper used by wt away.
set -euo pipefail

readonly label="com.ori.wt-away-pmset"
readonly helper_path="/Library/PrivilegedHelperTools/$label"
readonly plist_path="/Library/LaunchDaemons/$label.plist"
readonly ori_support_root="/Library/Application Support/Ori"
readonly request_root="$ori_support_root/AwayDispatcher"
readonly current_uid="$(id -u)"
readonly current_gid="$(id -g)"
readonly request_dir="$request_root/$current_uid"
readonly stale_rule="/etc/sudoers.d/com.ori.wt-away-pmset"
readonly owner="com.ori.wt-away-tick"
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/../.." && pwd)"
helper_source="$script_dir/pmset-helper.sh"

usage() {
  printf 'Usage: %s [--remove]\n' "$0" >&2
}

[[ "$(uname -s)" == "Darwin" ]] || { printf '%s\n' "Away wake helper is supported only on macOS." >&2; exit 1; }
[[ "$current_uid" =~ ^[1-9][0-9]*$ && "$current_gid" =~ ^[0-9]+$ ]] || { printf '%s\n' "Unsafe local uid/gid." >&2; exit 1; }
[[ -f "$helper_source" ]] || { printf 'Missing helper source: %s\n' "$helper_source" >&2; exit 1; }

remove_helper() {
  /usr/bin/sudo -- /bin/launchctl bootout "system/$label" 2>/dev/null || true
  /usr/bin/sudo -- /bin/rm -f -- "$plist_path"
  /usr/bin/sudo -- /bin/rm -f -- "$helper_path"
  /usr/bin/sudo -- /bin/rm -f -- "$stale_rule"
  /usr/bin/sudo -- /bin/rm -f -- "$request_dir/request"
  /usr/bin/sudo -- /bin/rm -f -- "$request_dir/response"
  /usr/bin/sudo -- /usr/bin/find "$request_dir" -depth -type d -empty -delete 2>/dev/null || true
  /usr/bin/sudo -- /usr/bin/find "$request_root" -depth -type d -empty -delete 2>/dev/null || true
}

if [[ "${1:-}" == "--remove" ]]; then
  [[ "$#" -eq 1 ]] || { usage; exit 2; }
  if /usr/bin/pmset -g sched | /usr/bin/grep -Fq "by '$owner'"; then
    printf '%s\n' "An Away Dispatcher wake is still scheduled; run wt away disarm before removing the helper." >&2
    exit 1
  fi
  printf '%s\n' "Remove the fixed-purpose Away Dispatcher wake helper (administrator approval required)."
  /usr/bin/sudo -k
  /usr/bin/sudo -v
  remove_helper
  printf '%s\n' "Removed the Away Dispatcher wake helper and stale sudoers rule."
  exit 0
fi
[[ "$#" -eq 0 ]] || { usage; exit 2; }

[[ ! -L "$ori_support_root" && ! -L "$request_root" && ! -L "$request_dir" ]] || {
  printf '%s\n' "Refusing a symlink in the privileged helper request path." >&2
  exit 1
}
unexpected="$(/usr/bin/find "$request_dir" -mindepth 1 -maxdepth 1 \
  ! -name request ! -name response 2>/dev/null || true)"
if [[ -n "$unexpected" ]]; then
  printf 'Refusing request directory with unexpected entries: %s\n' "$request_dir" >&2
  exit 1
fi

temporary_plist="$(mktemp "${TMPDIR:-/tmp}/ori-away-helper-plist.XXXXXX")"
trap '/bin/rm -f -- "$temporary_plist"' EXIT
/bin/chmod 600 "$temporary_plist"
cat > "$temporary_plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>$helper_path</string>
    <string>$current_uid</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>WatchPaths</key>
  <array>
    <string>$request_dir</string>
  </array>
  <key>ProcessType</key>
  <string>Background</string>
  <key>ThrottleInterval</key>
  <integer>1</integer>
</dict>
</plist>
PLIST
/usr/bin/plutil -lint "$temporary_plist" >/dev/null
/bin/bash -n "$helper_source"

cat <<EOF
Install a root-owned helper with one fixed protocol:
  helper: $helper_path
  daemon: $plist_path
  requests: $request_dir

It accepts only schedule/cancel for wakeorpoweron, an exact validated timestamp,
and owner $owner. It cannot run a caller-supplied executable or pmset action.
Administrator approval is required once.
EOF
/usr/bin/sudo -k
/usr/bin/sudo -v
/usr/bin/sudo -- /bin/mkdir -p -- "/Library/PrivilegedHelperTools"
/usr/bin/sudo -- /bin/mkdir -p -- "$ori_support_root"
/usr/bin/sudo -- /bin/test ! -L "$ori_support_root"
/usr/bin/sudo -- /usr/sbin/chown root:wheel "$ori_support_root"
/usr/bin/sudo -- /bin/chmod 0755 "$ori_support_root"
/usr/bin/sudo -- /bin/mkdir -p -- "$request_root"
/usr/bin/sudo -- /bin/test ! -L "$request_root"
/usr/bin/sudo -- /usr/sbin/chown root:wheel "$request_root"
/usr/bin/sudo -- /bin/chmod 0755 "$request_root"
/usr/bin/sudo -- /bin/mkdir -p -- "$request_dir"
/usr/bin/sudo -- /bin/test ! -L "$request_dir"
/usr/bin/sudo -- /usr/sbin/chown "$current_uid:$current_gid" "$request_dir"
/usr/bin/sudo -- /bin/chmod 0700 "$request_dir"
/usr/bin/sudo -- /bin/rm -f -- "$request_dir/request"
/usr/bin/sudo -- /bin/rm -f -- "$request_dir/response"
/usr/bin/sudo -- /usr/bin/install -o root -g wheel -m 0755 "$helper_source" "$helper_path"
/usr/bin/sudo -- /usr/bin/install -o root -g wheel -m 0644 "$temporary_plist" "$plist_path"
/usr/bin/sudo -- /bin/rm -f -- "$stale_rule"
/usr/bin/sudo -- /bin/launchctl bootout "system/$label" 2>/dev/null || true
/usr/bin/sudo -- /bin/launchctl bootstrap system "$plist_path"

# Prove the complete unprivileged request -> root helper -> read-back -> exact
# cancel chain. No sudo timestamp is used for either pmset mutation.
/usr/bin/sudo -k
AWAY_DISPATCH_SOURCE_ONLY=1 source "$repo_root/scripts/away-dispatch.sh"
self_test_date="$(/bin/date -v+5M '+%m/%d/%y %H:%M:%S')"
self_test_four_digit="$(/bin/date -j -f '%m/%d/%y %H:%M:%S' \
  "$self_test_date" '+%m/%d/%Y %H:%M:%S')"
if ! away_pmset_mutate schedule wakeorpoweron "$self_test_date" "$owner"; then
  printf '%s\n' "The installed helper failed its unprivileged schedule test." >&2
  exit 1
fi
if ! /usr/bin/pmset -g sched | /usr/bin/grep -Fq \
    "wakeorpoweron at $self_test_four_digit by '$owner'"; then
  printf '%s\n' "pmset did not confirm the helper's exact self-test wake." >&2
  exit 1
fi
if ! away_pmset_mutate schedule cancel wakeorpoweron "$self_test_date" "$owner"; then
  printf '%s\n' "The installed helper could not exactly cancel its self-test wake." >&2
  exit 1
fi
if /usr/bin/pmset -g sched | /usr/bin/grep -Fq "by '$owner'"; then
  printf '%s\n' "The helper self-test wake remained after exact cancellation." >&2
  exit 1
fi

printf '%s\n' "Installed fixed-purpose Away Dispatcher wake helper; unprivileged schedule/read-back/exact-cancel passed."
printf 'Remove it later with: %s --remove\n' "$0"
