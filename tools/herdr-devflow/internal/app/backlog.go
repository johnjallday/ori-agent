package app

import (
	"context"
)

// This file owns the `./scripts/backlog.sh` command surface.
//
// The Issue surface it used to own moved to issue.go when `./scripts/issue.sh`
// was split out. For this one commit `backlog` simply forwards there, so the
// extraction is provably behaviour-preserving: the same parser, the same
// renderers, the same JSON. The board reader replaces this forwarder in the
// next commit, and the retired Issue subcommands grow their own rejections
// after that.
func (a *App) backlog(ctx context.Context, opts options, args []string) int {
	return a.issue(ctx, opts, args)
}
