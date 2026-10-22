#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The gosec pin in ci-local.sh must be the one CI's action is pinned to, or
# the local gate scans with a different analyzer set than the PR gate.
script_pin="$(sed -n 's/^GOSEC_VERSION="\([0-9.]*\)"$/\1/p' "$repo_root/scripts/ci-local.sh")"
ci_pin="$(grep -o 'pinned action/image: gosec [0-9.]*' "$repo_root/.github/workflows/ci.yml" | head -n1 | grep -o '[0-9.]*$')"
if [[ -z "$script_pin" || -z "$ci_pin" || "$script_pin" != "$ci_pin" ]]; then
  echo "gosec pin drift: scripts/ci-local.sh has '$script_pin', .github/workflows/ci.yml has '$ci_pin'" >&2
  exit 1
fi

# The README gate is selected from the paths the README scenes photograph.
# shellcheck source=scripts/ci-local.sh
source "$repo_root/scripts/ci-local.sh"
if ! ci_local_readme_needed "internal/web/static/js/modules/home.js"; then
  echo "a Home module change did not select the README gate" >&2
  exit 1
fi
if ! ci_local_readme_needed "docs/readme-screenshots.json"; then
  echo "a manifest change did not select the README gate" >&2
  exit 1
fi
if ci_local_readme_needed "internal/folderdigest/scan.go" "scripts/smoke.sh"; then
  echo "a backend-only change selected the README gate" >&2
  exit 1
fi
if ci_local_readme_needed; then
  echo "no changes selected the README gate" >&2
  exit 1
fi

# A dry run prints the plan in CI's order and executes nothing.
full="$(cd "$repo_root" && bash scripts/ci-local.sh --dry-run --no-fetch --readme)"
grep -q 'ci-local: gates: gofmt vet wails-modes lint-new unit-tests devtools test-js lint-js format-js character-assets gosec-new readme$' <<<"$full" || {
  echo "unexpected full plan:" >&2
  printf '%s\n' "$full" >&2
  exit 1
}
grep -q 'ci-local: dry run, nothing executed' <<<"$full"

quick="$(cd "$repo_root" && bash scripts/ci-local.sh --dry-run --no-fetch --quick)"
grep -q 'ci-local: gates: gofmt vet wails-modes lint-new test-unit devtools test-js lint-js format-js character-assets$' <<<"$quick" || {
  echo "unexpected quick plan:" >&2
  printf '%s\n' "$quick" >&2
  exit 1
}
grep -q 'readme gate skipped (--quick)' <<<"$quick"

skipped="$(cd "$repo_root" && bash scripts/ci-local.sh --dry-run --no-fetch --no-readme)"
grep -q 'readme gate skipped (--no-readme)' <<<"$skipped"
grep -q 'ci-local: gates: gofmt vet wails-modes lint-new unit-tests devtools test-js lint-js format-js character-assets gosec-new$' <<<"$skipped"

if (cd "$repo_root" && bash scripts/ci-local.sh --dry-run --bogus >/dev/null 2>&1); then
  echo "an unknown flag was accepted" >&2
  exit 1
fi

# The full gate delegates to CI's canonical coverage-enabled runner, rather
# than duplicating flags that can drift. Inherited hosted scope must not shard it.
unit_sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-ci-unit-contract.XXXXXX")"
holder=""
trap '[[ -z "$holder" ]] || kill "$holder" 2>/dev/null || true; rm -rf "$unit_sandbox"' EXIT
mkdir "$unit_sandbox/scripts" "$unit_sandbox/locks"
cat > "$unit_sandbox/scripts/run-unit-tests.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ $# == 0 && -z "${RUNNER_OS:-}${ORI_UNIT_SHARD:-}${ORI_UNIT_SHARD_COUNT:-}" ]]
printf 'canonical full unit runner invoked\n'
printf 'parallel=%s\n' "${ORI_UNIT_TEST_PARALLEL:-}"
printf 'lock=%s\n' "$(readlink "$ORI_CI_LOCAL_LOCK_DIR/ori-ci-local-unit-tests.lock" || true)"
touch "$UNIT_RAN"
exit "${UNIT_STATUS:-0}"
STUB
chmod 700 "$unit_sandbox/scripts/run-unit-tests.sh"
# The tests own their lock directory, so a real gate running on this machine
# neither blocks them nor sees their locks, and a caller's queue or package
# settings do not leak in.
unset ORI_CI_LOCAL_LOCK ORI_UNIT_TEST_PARALLEL
export ORI_CI_LOCAL_LOCK_DIR="$unit_sandbox/locks" ORI_CI_LOCAL_LOCK_POLL=0.1 UNIT_RAN="$unit_sandbox/ran"
lock="$ORI_CI_LOCAL_LOCK_DIR/ori-ci-local-unit-tests.lock"
run_unit_gate() {
  rm -f "$UNIT_RAN"
  (cd "$unit_sandbox" && ci_local_run_gate unit-tests origin/dev)
}
fail() {
  echo "$1" >&2
  [[ -z "${2:-}" ]] || printf '%s\n' "$2" >&2
  exit 1
}

unit_result="$(
  export RUNNER_OS=macOS ORI_UNIT_SHARD=2 ORI_UNIT_SHARD_COUNT=4
  run_unit_gate
)"
grep -qx 'canonical full unit runner invoked' <<<"$unit_result" ||
  fail "full unit gate did not invoke the unsharded canonical runner" "$unit_result"

# Half the CPUs by default, the caller's number when given.
grep -qx "parallel=$(ci_local_unit_parallel)" <<<"$unit_result" || fail "unit gate did not pass the default parallelism" "$unit_result"
[[ "$(ci_local_unit_parallel)" -ge 2 ]] || fail "default parallelism below two"
unit_result="$(ORI_UNIT_TEST_PARALLEL=7 run_unit_gate)"
grep -qx 'parallel=7' <<<"$unit_result" || fail "ORI_UNIT_TEST_PARALLEL was not passed through" "$unit_result"

# The runner holds the lock, recorded as this process, and leaves none behind.
grep -q "^lock=$$|" <<<"$unit_result" || fail "the runner did not hold the unit-test lock" "$unit_result"
[[ ! -L "$lock" ]] || fail "the unit-test lock outlived the gate"

# A failing run releases the lock and keeps its status.
status=0
UNIT_STATUS=3 run_unit_gate >/dev/null || status=$?
[[ "$status" -eq 3 ]] || fail "a failing unit gate returned $status, not 3"
[[ ! -L "$lock" ]] || fail "a failing unit gate left its lock"

# Another worktree's live gate holds the lock: this one queues, names it, and
# runs once it is released.
sleep 30 &
holder=$!
ln -sn "$holder|$(ci_local_process_started "$holder")|0|09:00|feature/other|/x/worktrees/other" "$lock"
run_unit_gate > "$unit_sandbox/queued.log" 2>&1 &
queued=$!
for ((i = 0; i < 100; i++)); do
  grep -q 'queued behind' "$unit_sandbox/queued.log" && break
  sleep 0.05
done
grep -q 'ci-local: unit tests queued behind other (feature/other), running since 09:00' "$unit_sandbox/queued.log" ||
  fail "a queued gate did not name the holder" "$(cat "$unit_sandbox/queued.log")"
sleep 0.3
[[ ! -e "$UNIT_RAN" ]] || fail "a queued gate ran while another held the lock"
rm -f "$lock"
wait "$queued" || fail "the queued gate failed after the lock was released" "$(cat "$unit_sandbox/queued.log")"
[[ -e "$UNIT_RAN" ]] || fail "the queued gate never ran"
grep -q 'waited .* for the unit-test lock' "$unit_sandbox/queued.log" || fail "the queued gate did not say how long it waited"

# With the queue turned off, a held lock is neither waited for nor touched.
ln -sn "$holder|$(ci_local_process_started "$holder")|0|09:00|feature/other|/x/worktrees/other" "$lock"
held="$(readlink "$lock")"
ORI_CI_LOCAL_LOCK=0 run_unit_gate >/dev/null || fail "the unit gate failed with the queue turned off"
[[ -e "$UNIT_RAN" && "$(readlink "$lock")" == "$held" ]] || fail "ORI_CI_LOCAL_LOCK=0 still used the lock"
rm -f "$lock"

# A lock whose gate is gone, or whose pid now belongs to another process, is
# cleared rather than waited on forever.
kill "$holder"
wait "$holder" 2>/dev/null || true
ln -sn "$holder|$(date)|0|09:00|feature/gone|/x/worktrees/gone" "$lock"
holder=""
unit_result="$(run_unit_gate)"
grep -q 'cleared a unit-test lock left by gone (feature/gone)' <<<"$unit_result" || fail "a dead gate's lock was not cleared" "$unit_result"
ln -sn "$$|Mon Jan  1 00:00:00 2001|0|09:00|feature/reused|/x/worktrees/reused" "$lock"
unit_result="$(run_unit_gate)"
grep -q 'cleared a unit-test lock left by reused (feature/reused)' <<<"$unit_result" || fail "a reused pid kept its lock" "$unit_result"
[[ ! -L "$lock" ]] || fail "the unit-test lock outlived the gate"

# A lock that cannot be created never hangs the gate.
unit_result="$(ORI_CI_LOCAL_LOCK_DIR="$unit_sandbox/missing" run_unit_gate)" || fail "the unit gate failed without a lock directory" "$unit_result"
grep -q 'unit tests run without the machine-wide lock' <<<"$unit_result" || fail "a missing lock directory was not reported" "$unit_result"
[[ -e "$UNIT_RAN" ]] || fail "the unit gate did not run without a lock directory"

printf 'ci-local pins gosec to CI, selects the README gate, plans in CI order, uses the full canonical unit runner, and queues unit runs behind one machine-wide lock\n'
