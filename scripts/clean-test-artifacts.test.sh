#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
cleanup_script="$script_dir/clean-test-artifacts.sh"
fixture_parent="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
fixture_root="$(mktemp -d "$fixture_parent/ori-clean-test-artifacts.XXXXXX")"

cleanup_fixture() {
	case "$fixture_root" in
	"$fixture_parent"/ori-clean-test-artifacts.*)
		rm -rf "$fixture_root"
		;;
	*)
		printf 'Refusing to remove unexpected test fixture: %s\n' "$fixture_root" >&2
		;;
	esac
}
trap cleanup_fixture EXIT

mkdir -p \
	"$fixture_root/ori-test-12345/workspaces" \
	"$fixture_root/ori-vault-files-12345/vaults" \
	"$fixture_root/ori-agent-test-12345" \
	"$fixture_root/ori-db-test-12345" \
	"$fixture_root/ori-db-migration-030-12345" \
	"$fixture_root/ori-wt-herd.ABC123" \
	"$fixture_root/ori-wt-backlog.ABC123" \
	"$fixture_root/ori-e2e-demo.ABC123" \
	"$fixture_root/ori-readme-capture.ABC123" \
	"$fixture_root/ori-clean-test-artifacts.ABC123" \
	"$fixture_root/ori-demo.keep" \
	"$fixture_root/unrelated-project"
touch \
	"$fixture_root/ori-test-server-12345.log" \
	"$fixture_root/ori-e2e.ABC123" \
	"$fixture_root/unrelated-notes.txt"

preview="$("$cleanup_script" --temp-dir "$fixture_root")"
[[ "$preview" == *"Dry run: found 12 Ori test artifact(s)"* ]]
[[ -f "$fixture_root/ori-test-server-12345.log" ]]
[[ -d "$fixture_root/ori-vault-files-12345" ]]

"$cleanup_script" --delete --temp-dir "$fixture_root"

[[ ! -e "$fixture_root/ori-test-server-12345.log" ]]
[[ ! -e "$fixture_root/ori-test-12345" ]]
[[ ! -e "$fixture_root/ori-vault-files-12345" ]]
[[ ! -e "$fixture_root/ori-agent-test-12345" ]]
[[ ! -e "$fixture_root/ori-db-test-12345" ]]
[[ ! -e "$fixture_root/ori-db-migration-030-12345" ]]
[[ ! -e "$fixture_root/ori-wt-herd.ABC123" ]]
[[ ! -e "$fixture_root/ori-wt-backlog.ABC123" ]]
[[ ! -e "$fixture_root/ori-e2e.ABC123" ]]
[[ ! -e "$fixture_root/ori-e2e-demo.ABC123" ]]
[[ ! -e "$fixture_root/ori-readme-capture.ABC123" ]]
[[ ! -e "$fixture_root/ori-clean-test-artifacts.ABC123" ]]
[[ -d "$fixture_root/ori-demo.keep" ]]
[[ -d "$fixture_root/unrelated-project" ]]
[[ -f "$fixture_root/unrelated-notes.txt" ]]

mkdir -p \
	"$fixture_root/ori-test-old" \
	"$fixture_root/ori-test-recent"
touch -t 202001010000 "$fixture_root/ori-test-old"

aged_output="$("$cleanup_script" --delete --quiet --older-than-hours 24 --temp-dir "$fixture_root")"
[[ "$aged_output" == *"Removed 1 of 1 Ori test artifact(s)"* ]]
[[ "$aged_output" == *"Skipped 1 recent artifact(s)"* ]]
[[ ! -e "$fixture_root/ori-test-old" ]]
[[ -d "$fixture_root/ori-test-recent" ]]

if "$cleanup_script" --delete --temp-dir / >/dev/null 2>&1; then
	printf 'Expected cleanup script to reject the filesystem root.\n' >&2
	exit 1
fi

printf 'clean-test-artifacts tests passed\n'
