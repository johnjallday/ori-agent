#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_root="$(mktemp -d)"
trap 'rm -rf "$temporary_root"' EXIT

# make_fixture <dir> <wails version in go.mod> <installed CLI version | none>
#
# The fixture is a throwaway repo layout with a fake `wails` (build + version)
# and a fake `go` whose `install` drops that fake CLI into $HOME/go/bin at the
# requested version and logs every call to $HOME/go.log.
make_fixture() {
    local fixture="$1" gomod_version="$2" installed_version="$3"
    rm -rf "$fixture"
    mkdir -p \
        "$fixture/scripts" \
        "$fixture/bin" \
        "$fixture/cmd/folder-picker/frontend/wailsjs/go/main" \
        "$fixture/cmd/folder-picker/frontend/wailsjs/runtime" \
        "$fixture/home/go/bin"

    cp "$repo_root/scripts/build-folder-picker.sh" "$fixture/scripts/build-folder-picker.sh"

    # The webview2 line shares the "wailsapp" prefix: the script must match the
    # exact module path, not the first line that mentions Wails.
    printf 'module example.com/fixture\n\ngo 1.26\n\nrequire (\n\tgithub.com/wailsapp/go-webview2 v1.0.23 // indirect\n\tgithub.com/wailsapp/wails/v2 %s\n)\n' \
        "$gomod_version" > "$fixture/go.mod"

    for generated_file in \
        go/main/App.d.ts \
        go/main/App.js \
        go/models.ts \
        runtime/package.json \
        runtime/runtime.d.ts \
        runtime/runtime.js; do
        printf 'generated fixture: %s\n' "$generated_file" \
            > "$fixture/cmd/folder-picker/frontend/wailsjs/$generated_file"
    done

    # Real `wails version` prints the version first, then a sponsor banner.
    cat > "$fixture/home/wails-stub" <<'FAKE_WAILS'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "version" ]]; then
    cat "$HOME/go/bin/.wails-version"
    printf 'If Wails is useful to you, please consider sponsoring the project\n'
    exit 0
fi
find frontend/wailsjs -type f -exec chmod 0755 {} +
exit "${FAKE_WAILS_EXIT:-0}"
FAKE_WAILS
    chmod 0755 "$fixture/home/wails-stub"

    cat > "$fixture/bin/go" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$HOME/go.log"
[[ "${1:-}" == "install" ]] || exit 0
if [[ "${FAKE_GO_EXIT:-0}" -ne 0 ]]; then
    exit "$FAKE_GO_EXIT"
fi
cp "$HOME/wails-stub" "$HOME/go/bin/wails"
printf '%s\n' "${2##*@}" > "$HOME/go/bin/.wails-version"
FAKE_GO
    chmod 0755 "$fixture/bin/go"

    if [[ "$installed_version" != "none" ]]; then
        cp "$fixture/home/wails-stub" "$fixture/home/go/bin/wails"
        printf '%s\n' "$installed_version" > "$fixture/home/go/bin/.wails-version"
    fi
}

# run_build <fixture> [ENV=value ...]: prints nothing, returns the script's status.
run_build() {
    local fixture="$1"
    shift
    env HOME="$fixture/home" PATH="$fixture/bin:/usr/bin:/bin" "$@" \
        bash "$fixture/scripts/build-folder-picker.sh" >/dev/null 2>"$fixture/stderr.log"
}

fail() {
    printf '%s\n' "$*" >&2
    exit 1
}

assert_bindings_are_regular_files() {
    local fixture="$1" generated_file
    while IFS= read -r -d '' generated_file; do
        if [[ -x "$generated_file" ]]; then
            fail "generated binding remained executable: $generated_file"
        fi
    done < <(find "$fixture/cmd/folder-picker/frontend/wailsjs" -type f -print0)
}

assert_installed_version() {
    local fixture="$1" want="$2" got
    got="$(cat "$fixture/home/go/bin/.wails-version")"
    [[ "$got" == "$want" ]] || fail "installed Wails CLI is $got, want $want"
}

assert_go_log() {
    local fixture="$1" want="$2" got=""
    [[ ! -f "$fixture/home/go.log" ]] || got="$(cat "$fixture/home/go.log")"
    [[ "$got" == "$want" ]] || fail "go was called with [$got], want [$want]"
}

# 1. Matching CLI: built, bindings normalized, nothing reinstalled.
fixture="$temporary_root/matching"
make_fixture "$fixture" v2.17.0 v2.17.0
run_build "$fixture"
assert_bindings_are_regular_files "$fixture"
assert_go_log "$fixture" ""

# 2. Stale CLI (go.mod moved ahead): reinstalled at the go.mod version.
fixture="$temporary_root/stale"
make_fixture "$fixture" v2.17.0 v2.16.0
run_build "$fixture"
assert_installed_version "$fixture" v2.17.0
assert_go_log "$fixture" "install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0"
assert_bindings_are_regular_files "$fixture"

# 3. No CLI at all: installed at the go.mod version.
fixture="$temporary_root/missing"
make_fixture "$fixture" v2.17.0 none
run_build "$fixture"
assert_installed_version "$fixture" v2.17.0
assert_go_log "$fixture" "install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0"

# 4. Stale CLI and the update fails (offline): keep building with what exists.
fixture="$temporary_root/update-fails"
make_fixture "$fixture" v2.17.0 v2.16.0
run_build "$fixture" FAKE_GO_EXIT=1
assert_installed_version "$fixture" v2.16.0
grep -q 'continuing with v2.16.0' "$fixture/stderr.log" ||
    fail "failed CLI update did not warn and continue"
assert_bindings_are_regular_files "$fixture"

# 5. No CLI and the install fails: nothing to build with, so fail.
fixture="$temporary_root/missing-install-fails"
make_fixture "$fixture" v2.17.0 none
set +e
run_build "$fixture" FAKE_GO_EXIT=1
status=$?
set -e
[[ "$status" -ne 0 ]] || fail "build succeeded without a Wails CLI"

# 6. A failed Wails build keeps its exit status and still normalizes bindings.
fixture="$temporary_root/failure"
make_fixture "$fixture" v2.17.0 v2.17.0
set +e
run_build "$fixture" FAKE_WAILS_EXIT=23
status=$?
set -e
[[ "$status" -eq 23 ]] || fail "failed Wails build returned $status, want 23"
assert_bindings_are_regular_files "$fixture"

printf 'Folder picker build keeps the Wails CLI on the go.mod version and normalizes binding modes\n'
