#!/usr/bin/env bash
# Verify scripts/e2e.sh argument handling without a running server or a real
# Playwright run. A stub `npx` on PATH records what it was invoked with, so the
# assertions are about how e2e.sh splits specs from flags and dispatches them.
set -euo pipefail

exec </dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-e2e-test.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

stub_bin="$fixture_root/bin"
log="$fixture_root/npx.log"
mkdir -p "$stub_bin"

# The stub writes one line per invocation: the args it received, then the env
# vars the suite cares about. NPX_EXIT lets a case force a non-zero exit so the
# exit-code plumbing can be asserted.
cat >"$stub_bin/npx" <<'STUB'
#!/usr/bin/env bash
printf 'ARGS:%s\n' "$*" >>"$NPX_LOG"
printf 'ENV:FOO=%s\n' "${FOO:-}" >>"$NPX_LOG"
printf 'ENV:BASE=%s\n' "${PLAYWRIGHT_BASE_URL:-}" >>"$NPX_LOG"
exit "${NPX_EXIT:-0}"
STUB
chmod +x "$stub_bin/npx"

export NPX_LOG="$log"
PATH="$stub_bin:$PATH"
export PATH

# e2e.sh resolves its repo via `git rev-parse` against the caller's cwd.
cd "$repo_root"

failures=0

# Runs e2e.sh with a fresh log. Sets `out` and `status` for the caller.
run_e2e() {
	: >"$log"
	set +e
	out="$("$repo_root/scripts/e2e.sh" "$@" 2>&1)"
	status=$?
	set -e
}

check() {
	local label="$1" expected="$2" actual="$3"
	if [[ "$expected" == "$actual" ]]; then
		printf '  ok   %s\n' "$label"
	else
		printf '  FAIL %s\n       expected: %s\n       actual:   %s\n' \
			"$label" "$expected" "$actual"
		failures=$((failures + 1))
	fi
}

check_contains() {
	local label="$1" needle="$2" haystack="$3"
	if [[ "$haystack" == *"$needle"* ]]; then
		printf '  ok   %s\n' "$label"
	else
		printf '  FAIL %s\n       missing: %s\n       in:      %s\n' \
			"$label" "$needle" "$haystack"
		failures=$((failures + 1))
	fi
}

echo "--help"
run_e2e --help
check "exits 0" 0 "$status"
check_contains "documents --each" "--each" "$out"
check_contains "documents --env" "--env KEY=VAL" "$out"

echo "spec/flag split keeps a -g title out of the spec list"
run_e2e --no-check tests/a.spec.ts -- -g "some title"
check "exits 0" 0 "$status"
check_contains "spec is positional" "ARGS:playwright test tests/a.spec.ts -g some title" "$(cat "$log")"
check_contains "Running line lists one spec" "npx playwright test tests/a.spec.ts " "$out"

echo "unrecognised dash flags still pass through without a -- separator"
run_e2e --no-check tests/a.spec.ts --headed
check_contains "flag reaches playwright" "--headed" "$(cat "$log")"
check_contains "flag is not a spec" "npx playwright test tests/a.spec.ts " "$out"

echo "--env"
run_e2e --no-check --env FOO=bar tests/a.spec.ts
check_contains "var reaches the child" "ENV:FOO=bar" "$(cat "$log")"
run_e2e --no-check --env NOTAPAIR tests/a.spec.ts
check "rejects a non KEY=VALUE pair" 2 "$status"

echo "--each"
run_e2e --no-check --each tests/a.spec.ts tests/b.spec.ts
check "exits 0 when all pass" 0 "$status"
check "runs once per spec" 2 "$(grep -c '^ARGS:' "$log")"
check_contains "labels the first spec" "=== tests/a.spec.ts ===" "$out"
check_contains "summarises" "2/2 specs passed" "$out"
run_e2e --no-check --each
check "needs at least one spec" 2 "$status"

echo "--fmt formats before running"
run_e2e --no-check --fmt tests/a.spec.ts
check_contains "prettier ran on the spec" "ARGS:prettier --write tests/a.spec.ts" "$(cat "$log")"
check_contains "playwright still ran" "ARGS:playwright test tests/a.spec.ts" "$(cat "$log")"

echo "exit codes"
# Exported, not `VAR=x func`: a prefix assignment on a *function* call stays a
# plain shell variable in bash and would never reach the stub's environment.
export NPX_EXIT=1
run_e2e --no-check tests/a.spec.ts
check "propagates playwright failure" 1 "$status"
run_e2e --no-check --each tests/a.spec.ts tests/b.spec.ts
check "--each exits with the failed count" 2 "$status"
check_contains "--each reports the failures" "0/2 specs passed" "$out"
unset NPX_EXIT

echo "validation"
run_e2e --no-check --wait abc tests/a.spec.ts
check "rejects a non-numeric --wait" 2 "$status"
run_e2e --no-check --tail abc tests/a.spec.ts
check "rejects a non-numeric --tail" 2 "$status"

echo "--url overrides --port"
run_e2e --no-check --port 9999 --url http://localhost:1234 tests/a.spec.ts
check_contains "base url wins" "ENV:BASE=http://localhost:1234" "$(cat "$log")"

echo "--wait gives up on a server that never arrives"
# Port 1 is reserved and refuses instantly, so the loop spins rather than
# blocking on curl's timeout.
run_e2e --wait 1 --url http://localhost:1/ tests/a.spec.ts
check "exits 2" 2 "$status"
check_contains "names the timeout" "did not come up within 1s" "$out"
check "never reached playwright" 0 "$(grep -c '^ARGS:' "$log" || true)"

if ((failures)); then
	printf '\n%d assertion(s) failed\n' "$failures"
	exit 1
fi
printf '\nall assertions passed\n'
