#!/usr/bin/env bash
#
# Tests scripts/branch-server.sh and the `go` wrapper in branch-server.zsh
# against throwaway Git checkouts and a stub `go`, with HOME pointed at a
# fixture so no real folder is ever read or emptied.
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
server_script="$script_dir/branch-server.sh"
wrapper="$script_dir/branch-server.zsh"
fixture_parent="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
fixture_root="$(mktemp -d "$fixture_parent/ori-branch-server.XXXXXX")"

cleanup_fixture() {
	case "$fixture_root" in
	"$fixture_parent"/ori-branch-server.*)
		rm -rf "$fixture_root"
		;;
	*)
		printf 'Refusing to remove unexpected test fixture: %s\n' "$fixture_root" >&2
		;;
	esac
}
trap cleanup_fixture EXIT

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq is required"
command -v zsh >/dev/null 2>&1 || fail "zsh is required"

# --- fixtures ---------------------------------------------------------------

# Checkouts: the shared dev worktree, a main checkout, two feature worktrees,
# and a Go project that is not Ori. Unborn branches are enough: the script only
# asks Git for the top level and the branch name.
make_checkout() { # dir, branch, module
	mkdir -p "$1"
	git -C "$1" init -q -b "$2"
	printf 'module %s\n\ngo 1.26\n' "$3" >"$1/go.mod"
}
ori_module="github.com/johnjallday/ori-agent"
dev_wt="$fixture_root/worktrees/ori-agent-dev"
main_wt="$fixture_root/ori-agent"
feature_wt="$fixture_root/worktrees/nav-collapse"
fix_wt="$fixture_root/worktrees/fix-thing"
other_project="$fixture_root/other-go-project"
make_checkout "$dev_wt" dev "$ori_module"
make_checkout "$main_wt" main "$ori_module"
make_checkout "$feature_wt" feature/nav-collapse "$ori_module"
make_checkout "$fix_wt" fix/thing "$ori_module"
make_checkout "$other_project" feature/x example.com/other

# A stub `go` that records how it was called instead of building anything.
stub_bin="$fixture_root/bin"
report="$fixture_root/go-report"
mkdir -p "$stub_bin"
cat >"$stub_bin/go" <<'STUB'
#!/usr/bin/env bash
printf 'args=%s\nORI_DATA_DIR=%s\n' "$*" "${ORI_DATA_DIR:-<unset>}" >"$STUB_GO_REPORT"
STUB
chmod +x "$stub_bin/go"

# A fake home with the real app's state file, so name copying is observable.
home="$fixture_root/home"
mkdir -p "$home/Library/Application Support/OriAgent" "$home/Test"
printf '{"user_name":"Casey","assistant_name":"Pixel","onboarding":{"completed":true}}\n' \
	>"$home/Library/Application Support/OriAgent/app_state.json"
default_root="$(cd "$home/Test" && pwd -P)"

# run_in runs a command from a directory with the fixture environment and
# returns its exit status without tripping set -e.
run_in() { # dir, command...
	local dir="$1"
	shift
	(cd "$dir" && env -u ORI_BRANCH_WORKSPACE_ROOT -u WT_TEST_WORKSPACE_DIR -u ORI_BRANCH_ONBOARDING -u CLAUDECODE \
		HOME="$home" PATH="$stub_bin:$PATH" STUB_GO_REPORT="$report" "$@") && return 0 || return $?
}

report_value() { # key
	grep "^$1=" "$report" | cut -d= -f2-
}

stale_root() { # fill the default root with leftovers from a previous round
	mkdir -p "$home/Test/old-workspace/sub" "$home/Test/.ori-data"
	printf 'stale\n' >"$home/Test/old-workspace/notes.md"
	printf 'stale\n' >"$home/Test/.ori-data/sessions.db"
	printf 'x\n' >"$home/Test/.DS_Store"
}

# --- applies ----------------------------------------------------------------

expect_applies() { # dir, expected status, label
	local status=0
	run_in "$1" "$server_script" applies >/dev/null 2>&1 || status=$?
	[[ "$status" == "$2" ]] || fail "applies in $3: expected exit $2, got $status"
}
expect_applies "$dev_wt" 1 "ori-agent-dev worktree"
expect_applies "$main_wt" 1 "main checkout"
expect_applies "$feature_wt" 0 "feature worktree"
expect_applies "$fix_wt" 0 "fix worktree"
expect_applies "$other_project" 1 "non-Ori Go project"
expect_applies "$fixture_root" 1 "directory outside any checkout"

# --- start: default root, default args, wipe and seed ----------------------

stale_root
rm -f "$report"
run_in "$feature_wt" "$server_script" start 2>/dev/null || fail "start from a feature worktree"
[[ "$(ls -A "$home/Test")" == ".ori-data" ]] || fail "root was not emptied: $(ls -A "$home/Test" | tr '\n' ' ')"
[[ ! -e "$home/Test/.ori-data/sessions.db" ]] || fail "stale data dir survived the wipe"
[[ "$(report_value args)" == "run ./cmd/server" ]] || fail "default go args: $(report_value args)"
[[ "$(report_value ORI_DATA_DIR)" == "$default_root/.ori-data" ]] || fail "ORI_DATA_DIR: $(report_value ORI_DATA_DIR)"
settings="$home/Test/.ori-data/settings.json"
[[ "$(jq -r .workspace_root "$settings")" == "$default_root" ]] || fail "workspace_root: $(jq -r .workspace_root "$settings")"
[[ "$(jq -r .workspace_root_confirmed "$settings")" == "true" ]] || fail "workspace_root_confirmed not true"
state="$home/Test/.ori-data/app_state.json"
[[ "$(jq -r .onboarding.completed "$state")" == "true" ]] || fail "onboarding not completed"
[[ "$(jq -r .user_name "$state")" == "Casey" ]] || fail "user_name not copied: $(jq -r .user_name "$state")"
[[ "$(jq -r .assistant_name "$state")" == "Pixel" ]] || fail "assistant_name not copied"

# --- start: explicit go args, onboarding opt-out, root overrides ------------

run_in "$feature_wt" "$server_script" start run -race ./cmd/server 2>/dev/null || fail "start with go args"
[[ "$(report_value args)" == "run -race ./cmd/server" ]] || fail "go args passthrough: $(report_value args)"

run_in "$feature_wt" env ORI_BRANCH_ONBOARDING=1 "$server_script" start 2>/dev/null || fail "start with onboarding kept"
[[ -f "$home/Test/.ori-data/settings.json" ]] || fail "settings.json missing with onboarding kept"
[[ ! -e "$home/Test/.ori-data/app_state.json" ]] || fail "app_state.json written although onboarding was kept"

alt_root="$fixture_root/alt-root"
run_in "$feature_wt" env ORI_BRANCH_WORKSPACE_ROOT="$alt_root" "$server_script" start 2>/dev/null || fail "start with ORI_BRANCH_WORKSPACE_ROOT"
[[ -f "$alt_root/.ori-data/settings.json" ]] || fail "ORI_BRANCH_WORKSPACE_ROOT was not used"
[[ "$(report_value ORI_DATA_DIR)" == "$(cd "$alt_root" && pwd -P)/.ori-data" ]] || fail "ORI_DATA_DIR under the override root"

wt_root="$fixture_root/wt-test-dir"
run_in "$feature_wt" env WT_TEST_WORKSPACE_DIR="$wt_root" "$server_script" start 2>/dev/null || fail "start with WT_TEST_WORKSPACE_DIR"
[[ -f "$wt_root/.ori-data/settings.json" ]] || fail "WT_TEST_WORKSPACE_DIR was not used as the fallback root"

# --- start: refusals leave everything untouched -----------------------------

expect_refused() { # root, label
	local status=0
	stale_root
	run_in "$feature_wt" env ORI_BRANCH_WORKSPACE_ROOT="$1" "$server_script" start >/dev/null 2>&1 || status=$?
	[[ "$status" == 2 ]] || fail "$2: expected exit 2, got $status"
	[[ -f "$home/Test/old-workspace/notes.md" ]] || fail "$2: default root was emptied anyway"
}
expect_refused "Test" "relative root"
expect_refused "$home" "home itself"
expect_refused "$home/Ori Workspaces" "real workspaces folder"
expect_refused "$home/Projects/ori/x" "folder under Projects"
expect_refused "$home/Library/Application Support/OriAgent" "real data dir"
expect_refused "/nonexistent-outside-home/x" "folder outside HOME and TMPDIR"
[[ ! -e "$home/Projects" ]] || fail "refused root under Projects was created"
mkdir -p "$fixture_root/a-repo/.git"
expect_refused "$fixture_root/a-repo" "git repository"

stale_root
status=0
run_in "$other_project" "$server_script" start >/dev/null 2>&1 || status=$?
[[ "$status" == 2 ]] || fail "start outside an Ori checkout: expected exit 2, got $status"
[[ -f "$home/Test/old-workspace/notes.md" ]] || fail "start outside an Ori checkout emptied the root"

# --- the zsh `go` wrapper ---------------------------------------------------

expect_routed() { # dir, expected ORI_DATA_DIR or <unset>, label, go args...
	local dir="$1" expected="$2" label="$3"
	shift 3
	rm -f "$report"
	run_in "$dir" zsh -c 'source "$1"; shift; go "$@"' zsh "$wrapper" "$@" >/dev/null 2>&1 || fail "$label: wrapper exited non-zero"
	[[ "$(report_value ORI_DATA_DIR)" == "$expected" ]] || fail "$label: ORI_DATA_DIR=$(report_value ORI_DATA_DIR)"
}
expect_routed "$feature_wt" "$default_root/.ori-data" "feature worktree" run ./cmd/server
expect_routed "$feature_wt" "$default_root/.ori-data" "feature worktree with flags" run -race ./cmd/server/
expect_routed "$dev_wt" "<unset>" "ori-agent-dev worktree" run ./cmd/server
expect_routed "$main_wt" "<unset>" "main checkout" run ./cmd/server
expect_routed "$other_project" "<unset>" "non-Ori project" run ./cmd/server
expect_routed "$feature_wt" "<unset>" "other package" run ./cmd/menubar
expect_routed "$feature_wt" "<unset>" "other go command" version
[[ "$(report_value args)" == "version" ]] || fail "other go command args: $(report_value args)"

rm -f "$report"
run_in "$feature_wt" env CLAUDECODE=1 zsh -c 'source "$1"; go run ./cmd/server' zsh "$wrapper" >/dev/null 2>&1 || fail "agent shell: wrapper exited non-zero"
[[ "$(report_value ORI_DATA_DIR)" == "<unset>" ]] || fail "agent shell (CLAUDECODE set) was routed to the throwaway root"

printf 'branch-server tests passed\n'
