#!/usr/bin/env bash
#
# ci-local.sh — run CI's gates locally, in CI's order, with CI's pins, before a
# PR is opened. `wt pr` runs it first and pushes nothing when a gate fails;
# `make ci-local` runs it on demand.
#
#   ./scripts/ci-local.sh [--quick] [--readme|--no-readme] [--base REF]
#                         [--keep-going] [--no-fetch] [--dry-run]
#
# Gates, in the order CI's jobs would report them:
#   fetch       git fetch origin dev, so the ratchets below compare against the
#               real base (a stale origin/dev lies about "new" findings)
#   gofmt       gofmt -l on the Go files this branch changed
#   vet         go vet on the packages this branch changed
#   wails-modes make check-wails-modes, as CI's Lint
#   lint-new    make lint-new: golangci-lint ratcheted against the base, as CI's Lint
#   unit-tests  the unit suite exactly as CI's Unit Tests job runs it: go test
#               -short -race over scripts/list-unit-packages.sh (not make test,
#               which also runs suites CI does not gate on); --quick runs make
#               test-unit (the same packages without -race) instead
#   test-js     make test-js (npm run test:modules), as CI's Frontend Lint
#   lint-js     npm run lint, as CI's Frontend Lint
#   format-js   npm run format:check, as CI's Frontend Lint
#   character-assets  npm run test:character-assets, as CI's Frontend Lint
#   gosec-new   scripts/smoke.sh gosec-new with gosec pinned to CI's version,
#               installed on first use into a cached tool dir (skipped by --quick)
#   readme      CI's README Contract: npm run readme:test, make readme-check, a
#               disposable capture, and the tracked-README-files check. Runs when
#               the branch touches a path the README scenes photograph (--readme
#               forces it, --no-readme skips it, --quick skips it)
#
# A gate that cannot run (a missing tool) fails loudly with the install command
# rather than being skipped: a skipped gate is how a red CI happens.
#
# Every gate reports pass/fail and its duration; the first failure stops the run
# unless --keep-going is given. --dry-run prints the plan and runs nothing.
set -euo pipefail

# The version CI pins in .github/workflows/ci.yml (the securego/gosec action's
# "pinned action/image" comment). scripts/ci-local.test.sh keeps them equal.
GOSEC_VERSION="2.29.0"

# Paths whose change means the README scenes may photograph something new.
CI_LOCAL_README_PATHS=(
  'internal/web/'
  'internal/server/routes.go'
  'README.md'
  'docs/images/'
  'docs/readme-screenshots.json'
  'tests/readme-capture.spec.ts'
  'scripts/readme'
  'scripts/readme-refresh.sh'
)

ci_local_usage() {
  sed -n '2,32p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

# ci_local_readme_needed reports (exit 0) whether any of the given changed
# paths falls under a README-photographed path.
ci_local_readme_needed() {
  local path prefix
  for path in "$@"; do
    for prefix in "${CI_LOCAL_README_PATHS[@]}"; do
      if [[ "$path" == "$prefix"* ]]; then
        return 0
      fi
    done
  done
  return 1
}

# ci_local_tools_dir is where pinned tools are cached between runs.
ci_local_tools_dir() {
  if [[ -n "${ORI_TOOLS_DIR:-}" ]]; then
    printf '%s\n' "$ORI_TOOLS_DIR"
  elif [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    printf '%s/ori-tools\n' "$XDG_CACHE_HOME"
  elif [[ -n "${HOME:-}" && -w "$HOME" ]]; then
    printf '%s/.cache/ori-tools\n' "$HOME"
  else
    printf '%s/ori-tools\n' "${TMPDIR:-/tmp}"
  fi
}

# ci_local_gosec_bin prints the path of gosec at CI's version, installing it
# on first use. Prints nothing (and fails) when it cannot be installed.
ci_local_gosec_bin() {
  local dir bin
  dir="$(ci_local_tools_dir)/gosec-${GOSEC_VERSION}"
  bin="$dir/gosec"
  if [[ ! -x "$bin" ]]; then
    echo "Installing gosec ${GOSEC_VERSION} (CI's pin) into $dir …" >&2
    mkdir -p "$dir"
    if ! GOBIN="$dir" GOFLAGS=-mod=mod go install "github.com/securego/gosec/v2/cmd/gosec@v${GOSEC_VERSION}" >&2; then
      echo "Could not install gosec ${GOSEC_VERSION}. Install it by hand: GOBIN=$dir go install github.com/securego/gosec/v2/cmd/gosec@v${GOSEC_VERSION}" >&2
      return 1
    fi
  fi
  printf '%s\n' "$bin"
}

ci_local_main() {
  local quick=0 readme="auto" base="origin/dev" keep_going=0 fetch=1 dry_run=0 arg
  for arg in "$@"; do
    case "$arg" in
      --quick) quick=1 ;;
      --readme) readme="yes" ;;
      --no-readme) readme="no" ;;
      --base=*) base="${arg#--base=}" ;;
      --keep-going) keep_going=1 ;;
      --no-fetch) fetch=0 ;;
      --dry-run) dry_run=1 ;;
      -h | --help)
        ci_local_usage
        return 0
        ;;
      *)
        echo "ci-local: unknown argument $arg" >&2
        ci_local_usage >&2
        return 2
        ;;
    esac
  done
  if [[ "$base" == "--base" ]]; then
    echo "ci-local: use --base=REF" >&2
    return 2
  fi

  local repo_root
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
    echo "ci-local: not in a git checkout" >&2
    return 2
  }
  cd "$repo_root"

  if (( fetch )) && (( ! dry_run )); then
    git fetch -q origin "${base#origin/}" 2>/dev/null || echo "ci-local: could not fetch ${base}; ratcheting against the local ref" >&2
  fi

  local -a changed go_files go_packages
  changed=()
  while IFS= read -r line; do
    [[ -n "$line" ]] && changed+=("$line")
  done < <(git diff --name-only --diff-filter=ACMR "$base"...HEAD 2>/dev/null || true)
  go_files=()
  for line in "${changed[@]:-}"; do
    [[ "$line" == *.go ]] && go_files+=("$line")
  done
  go_packages=()
  if (( ${#go_files[@]} )); then
    while IFS= read -r line; do
      [[ -n "$line" && -d "$line" ]] && go_packages+=("./$line")
    done < <(printf '%s\n' "${go_files[@]}" | xargs -n1 dirname | sort -u)
  fi

  local readme_reason="" run_readme=0
  case "$readme" in
    yes) run_readme=1; readme_reason="--readme" ;;
    no) readme_reason="--no-readme" ;;
    auto)
      if (( quick )); then
        readme_reason="--quick"
      elif ci_local_readme_needed "${changed[@]:-}"; then
        run_readme=1
        readme_reason="the branch touches a README-photographed path"
      else
        readme_reason="no README-photographed path changed vs $base"
      fi
      ;;
  esac
  if (( quick )) && [[ "$readme" == "yes" ]]; then
    run_readme=1
  fi

  # The plan: one line per gate, in order.
  local -a plan
  plan=("gofmt" "vet" "wails-modes" "lint-new")
  if (( quick )); then plan+=("test-unit"); else plan+=("unit-tests"); fi
  plan+=("test-js" "lint-js" "format-js" "character-assets")
  if (( ! quick )); then plan+=("gosec-new"); fi
  if (( run_readme )); then plan+=("readme"); fi

  echo "ci-local: base $base · ${#changed[@]} changed file(s) · mode $([[ $quick == 1 ]] && echo quick || echo full)"
  # The gates read the branch as committed, which is what a PR carries.
  local dirty
  dirty="$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')"
  if [[ "$dirty" != "0" ]]; then
    echo "ci-local: note: $dirty uncommitted change(s) in the tree are not part of the branch these gates read"
  fi
  echo "ci-local: gates: ${plan[*]}"
  if (( run_readme )); then
    echo "ci-local: readme gate runs ($readme_reason)"
  else
    echo "ci-local: readme gate skipped ($readme_reason)"
  fi
  if (( dry_run )); then
    echo "ci-local: dry run, nothing executed"
    return 0
  fi

  local -a results
  results=()
  local failed=0 gate started elapsed status
  for gate in "${plan[@]}"; do
    started=$(date +%s)
    status="pass"
    echo
    echo "── ci-local: $gate ──"
    if ! ci_local_run_gate "$gate" "$base" "${go_files[@]:-}" -- "${go_packages[@]:-}"; then
      status="FAIL"
      failed=1
    fi
    elapsed=$(( $(date +%s) - started ))
    results+=("$(printf '%-10s %-4s %4ss' "$gate" "$status" "$elapsed")")
    if [[ "$status" == "FAIL" ]] && (( ! keep_going )); then
      break
    fi
  done

  echo
  echo "ci-local summary"
  printf '  %s\n' "${results[@]}"
  if (( failed )); then
    echo "ci-local: FAILED — fix the gate above and run again (wt pr --skip-checks bypasses this gate)"
    return 1
  fi
  echo "ci-local: all gates passed"
  return 0
}

# ci_local_run_gate runs one gate. Arguments: gate, base, changed Go files…,
# "--", changed Go packages….
ci_local_run_gate() {
  local gate="$1" base="$2"
  shift 2
  local -a go_files go_packages
  go_files=()
  while (( $# )) && [[ "$1" != "--" ]]; do
    [[ -n "$1" ]] && go_files+=("$1")
    shift
  done
  [[ "${1:-}" == "--" ]] && shift
  go_packages=()
  while (( $# )); do
    [[ -n "$1" ]] && go_packages+=("$1")
    shift
  done

  case "$gate" in
    gofmt)
      if (( ! ${#go_files[@]} )); then
        echo "no Go files changed"
        return 0
      fi
      local unformatted
      unformatted="$(gofmt -l "${go_files[@]}" 2>&1 || true)"
      if [[ -n "$unformatted" ]]; then
        echo "gofmt would change:"
        printf '  %s\n' $unformatted
        return 1
      fi
      echo "gofmt clean"
      ;;
    vet)
      if (( ! ${#go_packages[@]} )); then
        echo "no Go packages changed"
        return 0
      fi
      go vet "${go_packages[@]}"
      ;;
    wails-modes)
      make check-wails-modes
      ;;
    character-assets)
      [[ -d node_modules ]] || npm ci
      npm run test:character-assets
      ;;
    lint-new)
      command -v golangci-lint >/dev/null || {
        echo "golangci-lint is not installed: https://golangci-lint.run/usage/install/" >&2
        return 1
      }
      make lint-new
      ;;
    unit-tests)
      # CI's command (minus -v and the coverage profile): the unit packages,
      # -short, under the race detector, with CI's per-package timeout.
      local -a unit_packages
      unit_packages=()
      while IFS= read -r line; do
        [[ -n "$line" ]] && unit_packages+=("$line")
      done < <(./scripts/list-unit-packages.sh)
      (( ${#unit_packages[@]} )) || {
        echo "scripts/list-unit-packages.sh listed no packages" >&2
        return 1
      }
      ./scripts/run-test-command.sh go test -short -race -timeout 30m "${unit_packages[@]}"
      ;;
    test-unit)
      make test-unit
      ;;
    test-js)
      make test-js
      ;;
    lint-js)
      [[ -d node_modules ]] || npm ci
      npm run lint
      ;;
    format-js)
      [[ -d node_modules ]] || npm ci
      npm run format:check
      ;;
    gosec-new)
      local gosec_bin
      gosec_bin="$(ci_local_gosec_bin)" || return 1
      GOSEC_BIN="$gosec_bin" ./scripts/smoke.sh gosec-new "$base"
      ;;
    readme)
      [[ -d node_modules ]] || npm ci
      if ! npx --no-install playwright --version >/dev/null 2>&1; then
        echo "Playwright is not installed; the README capture needs it: npm ci && npx playwright install chromium" >&2
        return 1
      fi
      npm run readme:test
      make readme-check
      local run_id="ci-local-$(date +%s)"
      local captured=0
      if bash scripts/readme-refresh.sh capture --run-id "$run_id"; then
        captured=1
      fi
      bash scripts/readme-refresh.sh cleanup --run-id "$run_id" >/dev/null 2>&1 || true
      (( captured )) || return 1
      git diff --exit-code -- README.md docs/images docs/readme-screenshots.json
      ;;
    *)
      echo "ci-local: unknown gate $gate" >&2
      return 2
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  ci_local_main "$@"
fi
