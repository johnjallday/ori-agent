#!/usr/bin/env bash

set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
snapshot="$script_dir/feature-explore-snapshot.sh"
fixture_parent="$(CDPATH= cd -- "${TMPDIR:-/tmp}" && pwd -P)"
fixture_root="$(mktemp -d "$fixture_parent/ori-feature-explore.XXXXXX")"

cleanup_fixture() {
	case "$fixture_root" in
	"$fixture_parent"/ori-feature-explore.*) rm -rf -- "$fixture_root" ;;
	*) printf 'Refusing to remove unexpected fixture: %s\n' "$fixture_root" >&2 ;;
	esac
}
trap cleanup_fixture EXIT

fail() {
	printf 'feature-explore-snapshot test: %s\n' "$*" >&2
	exit 1
}

[[ -x "$snapshot" ]] || fail "script is not executable: $snapshot"
bash -n "$snapshot" || fail "script does not parse"

# A small checkout on dev with a fake origin/dev one commit behind, an older
# report and one written today, an open checklist, an archived plan and one
# internal package. Two feature worktrees: one whose PR merged, one whose
# directory was deleted without pruning. Nothing reads the real repository
# or the network.
today="$(date +%F)"
repo="$fixture_root/repo"
mkdir -p "$repo/docs/feature-discovery/reports" "$repo/tasks" "$repo/internal/alpha"
git -C "$repo" init -q
git -C "$repo" symbolic-ref HEAD refs/heads/dev
git -C "$repo" config user.email test@example.com
git -C "$repo" config user.name test
git -C "$repo" config commit.gpgsign false
printf '# Feature Discovery — 2026-01-01\n' >"$repo/docs/feature-discovery/reports/2026-01-01.md"
printf '# Feature Discovery — 2026-01-01 (finish)\n' >"$repo/docs/feature-discovery/reports/2026-01-01-finish.md"
printf '# Feature Discovery — today\n' >"$repo/docs/feature-discovery/reports/$today.md"
printf 'package alpha\n' >"$repo/internal/alpha/alpha.go"
git -C "$repo" add -A
git -C "$repo" commit -q -m "seed"
git -C "$repo" update-ref refs/remotes/origin/dev HEAD
printf -- '- [x] 1.1 done\n- [ ] 1.2 open\n' >"$repo/tasks/tasks-demo.md"
printf '# shipped\n' >"$repo/tasks/prd-5-old (done #9).md"
printf '# notes\n' >"$repo/tasks/findings-demo.md"
printf 'package alpha // changed\n' >"$repo/internal/alpha/alpha.go"
git -C "$repo" add -A
git -C "$repo" commit -q -m "ahead of dev"
git -C "$repo" worktree add -q -b feature/123-thing "$fixture_root/wt-thing"
git -C "$repo" worktree add -q -b feature/gone "$fixture_root/wt-gone"
rm -rf -- "$fixture_root/wt-gone"

# A stubbed gh answers the calls the snapshot makes; GH_STUB_FAIL=1 makes
# every call fail the way an expired token would.
stub_bin="$fixture_root/bin"
mkdir -p "$stub_bin"
cat >"$stub_bin/gh" <<'EOF'
#!/usr/bin/env bash
if [[ "${GH_STUB_FAIL:-0}" == 1 ]]; then
	echo "stub: GitHub unreachable" >&2
	exit 1
fi
args=" $* "
case "$1 $2" in
"pr list")
	if [[ "$args" == *" --head feature/123-thing "* ]]; then
		echo "#7 MERGED"
	elif [[ "$args" == *" --head "* ]]; then
		:
	elif [[ "$args" == *" --state open "* ]]; then
		echo "#9  feature/x  Open PR  https://example.test/pr/9  updated=2026-10-01"
	elif [[ "$args" == *" --state merged "* ]]; then
		echo "#8  2026-10-02  Merged PR  feature/y"
	fi
	;;
"issue list")
	if [[ "$args" == *" --json number,title,body "* ]]; then
		printf -- '--- #123 Stub issue ---\nBody text\n'
	else
		echo "#123  [backlog]  Stub issue  https://example.test/issues/123  created=2026-09-01 updated=2026-09-02"
	fi
	;;
*)
	echo "stub gh: unexpected call: $*" >&2
	exit 1
	;;
esac
EOF
chmod +x "$stub_bin/gh"

run() {
	PATH="$stub_bin:$PATH" "$snapshot" --repo "$repo" "$@"
}

# Usage errors exit 2. The stub stays on PATH so no case reaches the real gh.
for bad in "--since yesterday" "--bogus"; do
	set +e
	# shellcheck disable=SC2086 # split the flag pair on purpose
	run $bad >/dev/null 2>&1
	status=$?
	set -e
	[[ $status -eq 2 ]] || fail "'$bad' should exit 2, got $status"
done

# A complete run, every section in the documented order with status last.
output="$(run)" || fail "complete run exited $?"
expect() {
	[[ "$output" == *"$1"* ]] || fail "missing in output: $1"
}
refute() {
	[[ "$output" != *"$1"* ]] || fail "unexpected in output: $1"
}
# Today's report is listed but never the previous one; that day's full run
# beats its scoped run, and its date sets the window.
reports="$repo/docs/feature-discovery/reports"
expect "dir: $reports"
expect "today: $reports/$today.md"
expect "previous: $reports/2026-01-01.md"
expect "since: 2026-01-01"
# The planning checkout keeps its open checklist with the next item and its
# other planning files; the merged feature carries its PR state and its
# commits; the deleted worktree is reported, not fatal.
expect "branch=dev  role=planning"
expect "pr=n/a"
expect "tasks/tasks-demo.md  ticked=1/2  next: 1.2 open"
expect "also: findings-demo.md"
expect "branch=feature/123-thing  role=feature  last_commit=$today  ahead_of_base=1  modified=0  untracked=0  pr=#7 MERGED"
expect "    commit: "
expect " ahead of dev"
expect "branch=feature/gone  role=feature  MISSING"
expect "feature/123-thing"
expect "prd-5-old (done #9).md"
refute "tasks/prd-5-old"
expect "#9  feature/x"
expect "#8  2026-10-02"
expect "#123  [backlog]"
expect " seed"
expect "internal/alpha"
expect "--- #123 Stub issue ---"
expect "complete: yes"
sections="$(printf '%s\n' "$output" | grep '^== ' | tr '\n' '|')"
expected_sections="== snapshot ==|== reports ==|== worktrees ==|== branches carrying an Issue number ==|== archived plans in the planning checkout's tasks/ ==|== open PRs to dev ==|== merged PRs to dev since 2026-01-01 ==|== open Issues ==|== commits on origin/dev since 2026-01-01 ==|== most-changed paths since 2026-01-01 ==|== longest-untouched internal packages ==|== open Issue bodies (800 characters each) ==|== status ==|"
[[ "$sections" == "$expected_sections" ]] || fail "unexpected section order: $sections"

# GitHub down: exit 1, every gh-backed line says so, the run is incomplete.
set +e
output="$(GH_STUB_FAIL=1 run 2>/dev/null)"
status=$?
set -e
[[ $status -eq 1 ]] || fail "GitHub failure should exit 1, got $status"
expect "pr=UNAVAILABLE"
expect "UNAVAILABLE: stub: GitHub unreachable"
expect "complete: no"

# --no-github never calls gh (the failing stub proves it) and exits 0.
output="$(GH_STUB_FAIL=1 run --no-github)" || fail "--no-github exited $?"
expect "pr=skipped"
expect "skipped (--no-github)"
expect "github: skipped"

# --since overrides the previous report's date.
output="$(run --since 2026-02-02)" || fail "--since run exited $?"
expect "since: 2026-02-02"
expect "== merged PRs to dev since 2026-02-02 =="

# Run from a feature worktree, reports and archived plans still come from the
# planning checkout, because they are untracked and live only there.
output="$(PATH="$stub_bin:$PATH" "$snapshot" --repo "$fixture_root/wt-thing")" || fail "feature-worktree run exited $?"
expect "branch: feature/123-thing"
expect "dir: $reports"
expect "previous: $reports/2026-01-01.md"
expect "prd-5-old (done #9).md"

# The snapshot wrote nothing into the checkout.
[[ -z "$(git -C "$repo" status --porcelain)" ]] || fail "snapshot modified the checkout"

echo "feature-explore-snapshot: ok"
