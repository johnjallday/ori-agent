#!/bin/zsh
# Versioned, Ori-specific adapter. Never installs or selects the companion.
# No errexit: provisioning intentionally preserves legacy best-effort helpers.
setopt pipefail
source "${0:A:h}/lib/devtools-selector.sh" || exit 2
source "${0:A:h}/lib/devtools-project.zsh" || exit 2
root="$(ori_devtools_anchor "${0:A:h:h}")" || exit 2
if [[ "${1:-}" != v1 ]]; then
  print -u2 -- 'Usage: devtools-project.sh v1 provision|demo|pre-pr [arguments]'
  exit 2
fi
action="${2:-}"
shift 2 || exit 2
case "$action" in
  provision)
    [[ $# == 2 ]] || exit 2
    dev="$1" mode="$2"
    case "$mode" in acceptEdits|bypassPermissions) ;; *) print -u2 -- 'Invalid feature permission mode'; exit 2 ;; esac
    if [[ -n "$dev" ]]; then
      dev="$(ori_devtools_directory "$dev")" || exit 2
      common="$(git -C "$root" rev-parse --path-format=absolute --git-common-dir)" || exit 2
      dev_common="$(git -C "$dev" rev-parse --path-format=absolute --git-common-dir)" || exit 2
      [[ "${common:A}" == "${dev_common:A}" ]] || { print -u2 -- 'Provisioning source belongs to a different Git repository'; exit 2; }
    fi
    ori_devtools_provision "$root" "$dev" "$mode"
    ;;
  demo)
    (( $# <= 1 )) || exit 2
    ori_devtools_demo "$root" "${1:-8931}"
    ;;
  pre-pr)
    (( $# == 0 )) || exit 2
    [[ -f "$root/scripts/ci-local.sh" ]] || { print -u2 -- 'Missing target CI gate; refusing pre-PR success'; exit 2; }
    (cd "$root" && make ci-local)
    ;;
  *) print -u2 -- "Unknown project adapter action: $action"; exit 2 ;;
esac
