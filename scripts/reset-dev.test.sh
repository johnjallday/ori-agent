#!/usr/bin/env bash

set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
reset_script="$script_dir/reset-dev.sh"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd -P)"
fixture_parent="$(CDPATH= cd -- "${TMPDIR:-/tmp}" && pwd -P)"
fixture_root="$(mktemp -d "$fixture_parent/ori-reset-dev.XXXXXX")"

cleanup_fixture() {
	case "$fixture_root" in
	"$fixture_parent"/ori-reset-dev.*) rm -rf -- "$fixture_root" ;;
	*) printf 'Refusing to remove unexpected fixture: %s\n' "$fixture_root" >&2 ;;
	esac
}
trap cleanup_fixture EXIT

fail() {
	printf 'reset-dev test: %s\n' "$*" >&2
	exit 1
}

[[ -x "$reset_script" ]] || fail "script is not executable: $reset_script"

# An existing profile is moved intact to one timestamped sibling and replaced
# by an empty directory. The launch command must preserve spaces safely.
profile_parent="$fixture_root/profile parent"
profile="$profile_parent/dev data"
mkdir -p "$profile/workspaces"
printf 'legacy-state\n' >"$profile/app_state.json"
printf 'workspace-state\n' >"$profile/workspaces/state"
output="$($reset_script --data-dir "$profile" --port 65431 --no-start --yes)"
[[ -d "$profile" ]] || fail "fresh profile was not created"
[[ -z "$(find "$profile" -mindepth 1 -maxdepth 1 -print -quit)" ]] ||
	fail "fresh profile is not empty"
shopt -s nullglob
backups=("$profile".backup-*)
shopt -u nullglob
[[ ${#backups[@]} -eq 1 ]] || fail "expected one profile backup, found ${#backups[@]}"
[[ "$(cat "${backups[0]}/app_state.json")" == "legacy-state" ]] ||
	fail "backup did not preserve app state"
[[ "$(cat "${backups[0]}/workspaces/state")" == "workspace-state" ]] ||
	fail "backup did not preserve workspace state"
[[ "$output" == *"run-dev"* ]] ||
	fail "launch command does not use the canonical dev target"
[[ "$output" == *"PORT=65431"* ]] || fail "launch command does not preserve the port"

# A non-interactive invocation must not mutate anything without --yes.
unconfirmed="$fixture_root/unconfirmed"
mkdir -p "$unconfirmed"
printf 'keep-me\n' >"$unconfirmed/app_state.json"
status=0
"$reset_script" --data-dir "$unconfirmed" --port 65432 </dev/null \
	>"$fixture_root/unconfirmed.out" 2>"$fixture_root/unconfirmed.err" || status=$?
[[ "$status" -eq 2 ]] || fail "unconfirmed reset exited $status, want 2"
[[ "$(cat "$unconfirmed/app_state.json")" == "keep-me" ]] ||
	fail "unconfirmed reset changed the profile"
[[ -z "$(find "$fixture_root" -maxdepth 1 -name 'unconfirmed.backup-*' -print -quit)" ]] ||
	fail "unconfirmed reset created a backup"
grep -Fq -- 'rerun with --yes' "$fixture_root/unconfirmed.err" ||
	fail "unconfirmed reset did not explain the confirmation requirement"

# Source roots, HOME, and symlink targets are never eligible reset targets.
for unsafe in "$repo_root" "$HOME"; do
	status=0
	"$reset_script" --data-dir "$unsafe" --port 65433 --yes \
		>"$fixture_root/unsafe.out" 2>"$fixture_root/unsafe.err" || status=$?
	[[ "$status" -eq 2 ]] || fail "unsafe path $unsafe exited $status, want 2"
	grep -Fq -- 'refusing unsafe data directory' "$fixture_root/unsafe.err" ||
		fail "unsafe path $unsafe was not clearly refused"
done
real_profile="$fixture_root/real-profile"
linked_profile="$fixture_root/linked-profile"
mkdir -p "$real_profile"
ln -s "$real_profile" "$linked_profile"
status=0
"$reset_script" --data-dir "$linked_profile" --port 65434 --yes \
	>"$fixture_root/symlink.out" 2>"$fixture_root/symlink.err" || status=$?
[[ "$status" -eq 2 ]] || fail "symlink path exited $status, want 2"
grep -Fq -- 'refusing symlink data directory' "$fixture_root/symlink.err" ||
	fail "symlink path was not clearly refused"

# --start transports the exact fresh root, worktree, and port to make. A stub
# keeps this test deterministic and avoids starting Ori.
fake_bin="$fixture_root/fake-bin"
start_profile="$fixture_root/start-profile"
start_capture="$fixture_root/start-capture"
mkdir -p "$fake_bin" "$start_profile"
printf 'old\n' >"$start_profile/app_state.json"
cat >"$fake_bin/make" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$ORI_DATA_DIR" "$*" >"$RESET_DEV_CAPTURE"
EOF
chmod +x "$fake_bin/make"
PATH="$fake_bin:$PATH" RESET_DEV_CAPTURE="$start_capture" \
	"$reset_script" --data-dir "$start_profile" --port 65435 --yes \
	>"$fixture_root/start.out"
started_data="$(sed -n '1p' "$start_capture")"
started_args="$(sed -n '2p' "$start_capture")"
[[ "$started_data" == "$start_profile" ]] || fail "--start passed the wrong data directory"
[[ "$started_args" == "-C $repo_root run-dev PORT=65435" ]] ||
	fail "--start passed unexpected make arguments: $started_args"

# A listening port fails before confirmation or profile mutation.
busy_bin="$fixture_root/busy-bin"
busy_profile="$fixture_root/busy-profile"
mkdir -p "$busy_bin" "$busy_profile"
printf 'still-live\n' >"$busy_profile/app_state.json"
cat >"$busy_bin/lsof" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$busy_bin/lsof"
status=0
PATH="$busy_bin:$PATH" "$reset_script" --data-dir "$busy_profile" --port 65436 --yes \
	>"$fixture_root/busy.out" 2>"$fixture_root/busy.err" || status=$?
[[ "$status" -eq 2 ]] || fail "busy port exited $status, want 2"
[[ "$(cat "$busy_profile/app_state.json")" == "still-live" ]] ||
	fail "busy-port refusal changed the profile"
grep -Fq -- 'port 65436 is already in use' "$fixture_root/busy.err" ||
	fail "busy-port refusal was not clear"

# Choosing another port must not allow an active profile to be moved out from
# under a server that is already using it.
in_use_bin="$fixture_root/in-use-bin"
in_use_profile="$fixture_root/in-use-profile"
mkdir -p "$in_use_bin" "$in_use_profile"
printf 'still-open\n' >"$in_use_profile/app_state.json"
cat >"$in_use_bin/lsof" <<EOF
#!/usr/bin/env bash
case "\$*" in
*'-iTCP:65437'*) exit 1 ;;
*'$in_use_profile'*) exit 0 ;;
*) exit 1 ;;
esac
EOF
chmod +x "$in_use_bin/lsof"
status=0
PATH="$in_use_bin:$PATH" "$reset_script" --data-dir "$in_use_profile" --port 65437 --yes \
	>"$fixture_root/in-use.out" 2>"$fixture_root/in-use.err" || status=$?
[[ "$status" -eq 2 ]] || fail "in-use profile exited $status, want 2"
[[ "$(cat "$in_use_profile/app_state.json")" == "still-open" ]] ||
	fail "in-use profile refusal changed the profile"
grep -Fq -- 'data directory is in use by a running process' "$fixture_root/in-use.err" ||
	fail "in-use profile refusal was not clear"

printf 'reset-dev tests passed\n'
