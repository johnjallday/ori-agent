# branch-server.zsh — source from ~/.zshrc (after scripts/wt.sh):
#
#   [ -r ~/Projects/ori/worktrees/ori-agent-dev/scripts/branch-server.zsh ] && \
#     source ~/Projects/ori/worktrees/ori-agent-dev/scripts/branch-server.zsh
#
# Routes `go run ./cmd/server` through branch-server.sh when the current
# directory is an Ori checkout other than ori-agent-dev or one on dev/main, so a
# branch under test gets the throwaway workspace directory (~/Test by default,
# EMPTIED on every start) instead of real data. Every other `go` call, and every
# call from a Claude Code shell (CLAUDECODE set), runs the real `go` untouched.

ORI_BRANCH_SERVER="${${(%):-%x}:A:h}/branch-server.sh"

# True when one of the arguments names the server package.
function _ori_branch_server_target {
  local arg
  for arg in "$@"; do
    case "$arg" in
    ./cmd/server | ./cmd/server/ | cmd/server | cmd/server/ | ./cmd/server/main.go) return 0 ;;
    esac
  done
  return 1
}

function go {
  if [[ "${1:-}" == run && -z "${CLAUDECODE:-}" ]] \
     && _ori_branch_server_target "${@:2}" \
     && "$ORI_BRANCH_SERVER" applies; then
    "$ORI_BRANCH_SERVER" start "$@"
    return $?
  fi
  command go "$@"
}
