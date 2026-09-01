#!/usr/bin/env bash
# Archive Ori's dedicated development profile and recreate it as a genuinely
# fresh install using canonical Personal Assistant Foundation onboarding.

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/reset-dev.sh [options]

Archive the complete dedicated Ori development data directory, create an empty
replacement, and launch canonical personal-assistant onboarding.

Options:
  --data-dir DIR  Dedicated dev profile (default: ./ori-data in this worktree).
  --port PORT     Port used by the launch command (default: 8765).
  --start         Launch after resetting (default; retained for explicit scripts).
  --no-start      Create the fresh profile but only print its safe launch command.
  --yes           Skip the interactive RESET confirmation.
  -h, --help      Show this help text.

Environment:
  ORI_DEV_DATA_DIR  Override the default dedicated dev profile.
  ORI_DEV_PORT      Override the default port.

This script never deletes the previous profile. It moves it to a timestamped
sibling backup, including any local credentials it contains. It deliberately
ignores ORI_DATA_DIR so an installed or non-dev profile cannot become the
implicit reset target.
EOF
}

fail() {
	printf 'reset-dev: %s\n' "$*" >&2
	exit 2
}

is_port_listening() {
	local requested_port="$1"
	if command -v lsof >/dev/null 2>&1; then
		lsof -nP -iTCP:"$requested_port" -sTCP:LISTEN >/dev/null 2>&1
		return
	fi
	if command -v curl >/dev/null 2>&1; then
		curl -sS -o /dev/null --max-time 1 "http://127.0.0.1:$requested_port/" 2>/dev/null
		return
	fi
	return 1
}

is_profile_in_use() {
	local profile="$1"
	local candidate resolved
	[[ -d "$profile" ]] || return 1
	if command -v lsof >/dev/null 2>&1; then
		# Ori runs with the worktree as cwd, so inspect every open file under the
		# profile rather than checking process working directories.
		lsof -nP +D "$profile" >/dev/null 2>&1
		return
	fi
	if [[ -d /proc ]]; then
		for candidate in /proc/[0-9]*/cwd; do
			[[ -L "$candidate" ]] || continue
			resolved="$(readlink "$candidate" 2>/dev/null || true)"
			[[ "$resolved" == "$profile" ]] && return 0
		done
	fi
	return 1
}

next_backup_path() {
	local source="$1"
	local timestamp candidate suffix
	timestamp="$(date '+%Y%m%d-%H%M%S')"
	candidate="${source}.backup-${timestamp}"
	suffix=1
	while [[ -e "$candidate" || -L "$candidate" ]]; do
		candidate="${source}.backup-${timestamp}-${suffix}"
		suffix=$((suffix + 1))
	done
	printf '%s\n' "$candidate"
}

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run from an Ori Git worktree"
repo_root="$(CDPATH= cd -- "$repo_root" && pwd -P)"

data_dir="${ORI_DEV_DATA_DIR:-$repo_root/ori-data}"
port="${ORI_DEV_PORT:-8765}"
start_server=true
assume_yes=false

while [[ $# -gt 0 ]]; do
	case "$1" in
	--data-dir)
		[[ $# -ge 2 ]] || fail "--data-dir requires a directory"
		data_dir="$2"
		shift 2
		;;
	--data-dir=*)
		data_dir="${1#*=}"
		shift
		;;
	--port)
		[[ $# -ge 2 ]] || fail "--port requires a number"
		port="$2"
		shift 2
		;;
	--port=*)
		port="${1#*=}"
		shift
		;;
	--start)
		start_server=true
		shift
		;;
	--no-start)
		start_server=false
		shift
		;;
	--yes)
		assume_yes=true
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		fail "unknown option: $1"
		;;
	esac
done

[[ "$port" =~ ^[0-9]+$ ]] || fail "port must be numeric"
((port >= 1 && port <= 65535)) || fail "port must be between 1 and 65535"
[[ -n "${data_dir//[[:space:]]/}" ]] || fail "data directory cannot be empty"

if [[ "$data_dir" != /* ]]; then
	data_dir="$repo_root/$data_dir"
fi

data_parent="$(dirname -- "$data_dir")"
data_name="$(basename -- "$data_dir")"
[[ "$data_name" != "." && "$data_name" != ".." ]] ||
	fail "refusing unsafe data directory name: $data_name"
[[ -d "$data_parent" ]] || fail "data directory parent does not exist: $data_parent"
data_parent="$(CDPATH= cd -- "$data_parent" && pwd -P)"
data_dir="$data_parent/$data_name"

[[ ! -L "$data_dir" ]] || fail "refusing symlink data directory: $data_dir"
[[ ! -e "$data_dir" || -d "$data_dir" ]] || fail "data path is not a directory: $data_dir"

home_root=""
if [[ -n "${HOME:-}" && -d "$HOME" ]]; then
	home_root="$(CDPATH= cd -- "$HOME" && pwd -P)"
fi

case "$data_dir" in
"/" | "$repo_root" | "$repo_root/.git" | "$home_root")
	fail "refusing unsafe data directory: $data_dir"
	;;
esac

# Moving a directory that contains the checkout would move source code too.
case "$repo_root/" in
"$data_dir/"*) fail "refusing data directory that contains this worktree: $data_dir" ;;
esac

if is_port_listening "$port"; then
	fail "port $port is already in use; stop the running dev server or choose --port"
fi
if is_profile_in_use "$data_dir"; then
	fail "data directory is in use by a running process; stop that dev server first: $data_dir"
fi

backup_dir=""
if [[ -d "$data_dir" ]]; then
	backup_dir="$(next_backup_path "$data_dir")"
fi

printf 'Ori development reset plan\n'
printf '  Worktree:  %s\n' "$repo_root"
printf '  Data:      %s\n' "$data_dir"
if [[ -n "$backup_dir" ]]; then
	printf '  Archive:   %s\n' "$backup_dir"
else
	printf '  Archive:   none (the profile does not exist yet)\n'
fi
printf '  Onboarding: personal assistant hiring\n'
printf '  Port:      %s\n' "$port"

if [[ "$assume_yes" != "true" ]]; then
	[[ -t 0 ]] || fail "confirmation required; rerun with --yes in a non-interactive shell"
	printf '\nType RESET to archive this dev profile and create a fresh one: '
	IFS= read -r confirmation
	[[ "$confirmation" == "RESET" ]] || {
		printf 'Reset cancelled.\n'
		exit 1
	}
fi

if [[ -n "$backup_dir" ]]; then
	mv -- "$data_dir" "$backup_dir"
	chmod 0750 "$backup_dir"
fi
install -d -m 0750 "$data_dir"

printf '\nFresh dev profile ready: %s\n' "$data_dir"
if [[ -n "$backup_dir" ]]; then
	printf 'Previous profile archived: %s\n' "$backup_dir"
fi
printf 'The new app_state.json will use canonical personal-assistant onboarding.\n'

if [[ "$start_server" == "true" ]]; then
	printf 'Starting Ori on http://127.0.0.1:%s ...\n\n' "$port"
	exec env ORI_DATA_DIR="$data_dir" make -C "$repo_root" run-dev PORT="$port"
fi

printf '\nLaunch with:\n  '
printf 'ORI_DATA_DIR=%q make -C %q run-dev PORT=%q\n' \
	"$data_dir" "$repo_root" "$port"
