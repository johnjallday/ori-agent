#!/usr/bin/env bash
# Install/remove the narrow passwordless pmset capability used by wt away.
set -euo pipefail

readonly rule_path="/etc/sudoers.d/com.ori.wt-away-pmset"
readonly owner="com.ori.wt-away-tick"

if [[ "$(uname -s)" != "Darwin" ]]; then
  printf '%s\n' "Away wake permission is supported only on macOS." >&2
  exit 1
fi

if [[ "${1:-}" == "--remove" ]]; then
  [[ "$#" -eq 1 ]] || { printf '%s\n' "Usage: $0 [--remove]" >&2; exit 2; }
  printf 'Remove %s (administrator approval required).\n' "$rule_path"
  /usr/bin/sudo -k -- /bin/rm -f -- "$rule_path"
  printf '%s\n' "Removed Away Dispatcher pmset permission."
  exit 0
fi
[[ "$#" -eq 0 ]] || { printf '%s\n' "Usage: $0 [--remove]" >&2; exit 2; }

user_name="$(id -un)"
if [[ ! "$user_name" =~ ^[A-Za-z0-9._-]+$ ]]; then
  printf 'Refusing unsafe local user name: %s\n' "$user_name" >&2
  exit 1
fi

temporary_rule="$(mktemp "${TMPDIR:-/tmp}/ori-away-sudoers.XXXXXX")"
trap 'rm -f -- "$temporary_rule"' EXIT
chmod 600 "$temporary_rule"
cat > "$temporary_rule" <<RULE
# Managed by Ori's Away Dispatcher. Allows only fixed-owner one-shot wakes.
Cmnd_Alias ORI_WT_AWAY_PMSET = /usr/bin/pmset schedule wakeorpoweron * $owner, /usr/bin/pmset schedule cancel wakeorpoweron * $owner
$user_name ALL=(root) NOPASSWD: ORI_WT_AWAY_PMSET
RULE

/usr/sbin/visudo -cf "$temporary_rule"
printf '%s\n' "Install the following fixed permission (administrator approval required):"
printf '  %s\n' "$rule_path"
/usr/bin/sudo -k -- /usr/bin/install -o root -g wheel -m 0440 "$temporary_rule" "$rule_path"

# `sudo -l` may itself require authentication under a site's listpw policy,
# even for a NOPASSWD command. Invalidate the fresh installation timestamp,
# then prove the capability through the same bounded schedule/read-back/exact-
# cancel sequence the dispatcher relies on. Without `sudo -k`, the installer's
# cached administrator timestamp can create a false positive.
/usr/bin/sudo -k
if /usr/bin/pmset -g sched | /usr/bin/grep -Fq "by '$owner'"; then
  printf 'A pre-existing %s wake is present; refusing to disturb it.\n' "$owner" >&2
  /usr/bin/sudo -- /bin/rm -f -- "$rule_path"
  exit 1
fi
self_test_date="$(/bin/date -v+5M '+%m/%d/%y %H:%M:%S')"
self_test_four_digit="$(/bin/date -j -f '%m/%d/%y %H:%M:%S' \
  "$self_test_date" '+%m/%d/%Y %H:%M:%S')"
if ! /usr/bin/sudo -n -- /usr/bin/pmset schedule wakeorpoweron \
    "$self_test_date" "$owner"; then
  printf '%s\n' "The installed rule is not active without a cached sudo timestamp." >&2
  printf '%s\n' "Use the fixed-purpose LaunchDaemon fallback (it also removes this stale rule):" >&2
  printf '  %s/away/install-pmset-helper.sh\n' "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)" >&2
  exit 1
fi

self_test_verified=1
if ! /usr/bin/pmset -g sched | /usr/bin/grep -Fq \
    "wakeorpoweron at $self_test_four_digit by '$owner'"; then
  self_test_verified=0
  printf '%s\n' "pmset did not confirm the exact self-test wake; attempting exact cleanup." >&2
fi
if ! /usr/bin/sudo -n -- /usr/bin/pmset schedule cancel wakeorpoweron \
    "$self_test_date" "$owner"; then
  printf '%s\n' "Could not exactly cancel the self-test wake; preserving the rule for recovery." >&2
  printf 'Recovery: /usr/bin/sudo -- /usr/bin/pmset schedule cancel wakeorpoweron %q %q\n' \
    "$self_test_date" "$owner" >&2
  exit 1
fi
if /usr/bin/pmset -g sched | /usr/bin/grep -Fq "by '$owner'"; then
  printf '%s\n' "The fixed-owner self-test wake remained after cancellation; preserving the rule for recovery." >&2
  exit 1
fi
if [[ "$self_test_verified" -ne 1 ]]; then
  printf '%s\n' "The self-test wake was cleaned up but never verified; removing the rule." >&2
  /usr/bin/sudo -- /bin/rm -f -- "$rule_path"
  exit 1
fi

printf '%s\n' "Installed Away Dispatcher pmset permission; schedule/read-back/exact-cancel self-test passed."
printf 'Remove it later with: %s --remove\n' "$0"
