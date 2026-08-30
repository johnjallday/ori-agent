#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_root="$(mktemp -d)"
trap 'rm -rf "$temporary_root"' EXIT

make_fixture() {
    local fixture="$1"
    rm -rf "$fixture"
    mkdir -p \
        "$fixture/scripts" \
        "$fixture/cmd/folder-picker/frontend/wailsjs/go/main" \
        "$fixture/cmd/folder-picker/frontend/wailsjs/runtime" \
        "$fixture/home/go/bin"

    cp "$repo_root/scripts/build-folder-picker.sh" "$fixture/scripts/build-folder-picker.sh"

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

    cat > "$fixture/home/go/bin/wails" <<'FAKE_WAILS'
#!/usr/bin/env bash
set -euo pipefail
find frontend/wailsjs -type f -exec chmod 0755 {} +
exit "${FAKE_WAILS_EXIT:-0}"
FAKE_WAILS
    chmod 0755 "$fixture/home/go/bin/wails"
}

assert_bindings_are_regular_files() {
    local fixture="$1" generated_file
    while IFS= read -r -d '' generated_file; do
        if [[ -x "$generated_file" ]]; then
            printf 'generated binding remained executable: %s\n' "$generated_file" >&2
            return 1
        fi
    done < <(find "$fixture/cmd/folder-picker/frontend/wailsjs" -type f -print0)
}

success_fixture="$temporary_root/success"
make_fixture "$success_fixture"
HOME="$success_fixture/home" PATH="/usr/bin:/bin" \
    bash "$success_fixture/scripts/build-folder-picker.sh" >/dev/null
assert_bindings_are_regular_files "$success_fixture"

failure_fixture="$temporary_root/failure"
make_fixture "$failure_fixture"
set +e
HOME="$failure_fixture/home" PATH="/usr/bin:/bin" FAKE_WAILS_EXIT=23 \
    bash "$failure_fixture/scripts/build-folder-picker.sh" >/dev/null 2>&1
failure_status=$?
set -e

if [[ "$failure_status" -ne 23 ]]; then
    printf 'failed Wails build returned %s, want 23\n' "$failure_status" >&2
    exit 1
fi
assert_bindings_are_regular_files "$failure_fixture"

printf 'Folder picker build normalizes generated binding modes\n'
