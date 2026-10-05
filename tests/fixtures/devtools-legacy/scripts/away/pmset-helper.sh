#!/usr/bin/env bash
# Root-only fixed-purpose launchd helper for Away Dispatcher pmset requests.
# Installed under /Library/PrivilegedHelperTools by install-pmset-helper.sh.
set -uo pipefail

readonly owner="com.ori.wt-away-tick"
readonly ori_support_root="/Library/Application Support/Ori"
readonly request_root="$ori_support_root/AwayDispatcher"

[[ "$(id -u)" -eq 0 ]] || exit 1
[[ "$#" -eq 1 && "$1" =~ ^[1-9][0-9]*$ ]] || exit 1
readonly expected_uid="$1"
readonly request_dir="$request_root/$expected_uid"
readonly request_file="$request_dir/request"
readonly response_file="$request_dir/response"

# launchd may invoke us again when the response changes the watched directory.
[[ -e "$request_file" ]] || exit 0
[[ ! -L "$ori_support_root" && -d "$ori_support_root" ]] || exit 1
[[ "$(/usr/bin/stat -f '%u' "$ori_support_root" 2>/dev/null || true)" == "0" ]] || exit 1
[[ "$(/usr/bin/stat -f '%Lp' "$ori_support_root" 2>/dev/null || true)" == "755" ]] || exit 1
[[ ! -L "$request_root" && -d "$request_root" ]] || exit 1
[[ "$(/usr/bin/stat -f '%u' "$request_root" 2>/dev/null || true)" == "0" ]] || exit 1
[[ "$(/usr/bin/stat -f '%Lp' "$request_root" 2>/dev/null || true)" == "755" ]] || exit 1
[[ ! -L "$request_dir" && -d "$request_dir" ]] || exit 1
[[ "$(/usr/bin/stat -f '%u' "$request_dir" 2>/dev/null || true)" == "$expected_uid" ]] || exit 1
[[ "$(/usr/bin/stat -f '%Lp' "$request_dir" 2>/dev/null || true)" == "700" ]] || exit 1
[[ ! -L "$request_file" && -f "$request_file" ]] || exit 1
[[ "$(/usr/bin/stat -f '%u' "$request_file" 2>/dev/null || true)" == "$expected_uid" ]] || exit 1
[[ "$(/usr/bin/stat -f '%Lp' "$request_file" 2>/dev/null || true)" == "600" ]] || exit 1
[[ "$(/usr/bin/stat -f '%l' "$request_file" 2>/dev/null || true)" == "1" ]] || exit 1

version_line=""
operation_line=""
date_line=""
nonce_line=""
extra_line=""
exec 3< "$request_file" || exit 1
IFS= read -r version_line <&3 || exit 1
IFS= read -r operation_line <&3 || exit 1
IFS= read -r date_line <&3 || exit 1
IFS= read -r nonce_line <&3 || exit 1
if IFS= read -r extra_line <&3; then
  exit 1
fi
exec 3<&-

[[ "$version_line" == "version=1" ]] || exit 1
operation="${operation_line#operation=}"
pmset_date="${date_line#date=}"
nonce="${nonce_line#nonce=}"
[[ "$operation_line" == "operation=$operation" ]] || exit 1
[[ "$date_line" == "date=$pmset_date" ]] || exit 1
[[ "$nonce_line" == "nonce=$nonce" ]] || exit 1
[[ "$operation" == "schedule" || "$operation" == "cancel" ]] || exit 1
[[ "$pmset_date" =~ ^(0[1-9]|1[0-2])/(0[1-9]|[12][0-9]|3[01])/[0-9]{2}[[:space:]](0[0-9]|1[0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]$ ]] || exit 1
[[ "$nonce" =~ ^[A-Za-z0-9_-]{16,64}$ ]] || exit 1
normalized_date="$(/bin/date -j -f '%m/%d/%y %H:%M:%S' "$pmset_date" '+%m/%d/%y %H:%M:%S' 2>/dev/null || true)"
[[ "$normalized_date" == "$pmset_date" ]] || exit 1

# Unlink the validated request before mutation so a directory notification can
# never replay it. The parsed values above are the only inputs used below.
/bin/rm -f -- "$request_file" || exit 1
pmset_status=0
if [[ "$operation" == "schedule" ]]; then
  /usr/bin/pmset schedule wakeorpoweron "$pmset_date" "$owner" || pmset_status=$?
else
  /usr/bin/pmset schedule cancel wakeorpoweron "$pmset_date" "$owner" || pmset_status=$?
fi

response_status="ok"
[[ "$pmset_status" -eq 0 ]] || response_status="pmset-failed"
temporary_response="$(/usr/bin/mktemp "$request_dir/.response.XXXXXX")" || exit 1
trap '/bin/rm -f -- "$temporary_response"' EXIT
/bin/chmod 0644 "$temporary_response" || exit 1
printf 'version=1\nnonce=%s\nstatus=%s\n' "$nonce" "$response_status" > "$temporary_response" || exit 1
/bin/mv -f -- "$temporary_response" "$response_file" || exit 1
trap - EXIT
[[ "$pmset_status" -eq 0 ]]
