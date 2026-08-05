package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// Everything two GitHub-backed commands need in common: `issue`, which reads
// and writes GitHub Issues, and `backlog`, which reads the project board those
// Issues are ranked on. Shared rather than duplicated because a client built
// two different ways is a client that authenticates two different ways, and
// that difference would only ever surface as one command working while the
// other mysteriously does not.

// githubClient builds the GitHub client for the checkout a command was run
// from.
//
// The bridge configuration is read for its bounds when it is present, but a
// missing or unreadable one is not fatal here. Reading a repository's Issues or
// its board has nothing to do with Herdr, and refusing to answer because an
// unrelated file is malformed would be an odd way to find that out.
func (a *App) githubClient(opts options) (*github.Client, error) {
	lookup := a.withOverrides(opts)
	repoRoot := opts.repoRoot
	if repoRoot == "" {
		if value, ok := lookup(worktree.RepoOverrideEnv); ok && strings.TrimSpace(value) != "" {
			repoRoot = value
		}
	}
	if repoRoot == "" {
		cwd, err := a.getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		// Any linked worktree resolves here, and all of them share one
		// repository, so the answer is the same from every checkout.
		repoRoot, err = worktree.FindRepoRoot(cwd)
		if err != nil {
			return nil, fmt.Errorf("must run inside a Git checkout: %w", err)
		}
	}

	settings := config.Default()
	if paths, err := worktree.Resolve(repoRoot, lookup); err == nil {
		if loaded, err := config.Load(paths.ConfigPath, lookup); err == nil {
			settings = loaded
		}
		repoRoot = paths.RepoRoot
	}
	return github.New(github.Options{
		Dir: repoRoot,
		// Run is nil in production, which selects the real `gh` binary.
		Run:        a.githubRunner,
		Timeout:    settings.GitHubTimeout(),
		IssueLimit: github.DefaultIssueLimit,
	}), nil
}

// writeGitHubError reports a GitHub failure with the one action most likely to
// fix it. The classified error already carries nothing but text this repository
// wrote, so it is safe to print as-is.
//
// schemaVersion and command are supplied by the caller because the two command
// surfaces version their JSON independently and report under their own names.
func (a *App) writeGitHubError(err error, asJSON bool, schemaVersion int, command string) {
	var remoteErr *github.Error
	if !errors.As(err, &remoteErr) {
		a.writeError(err, asJSON)
		return
	}
	if asJSON {
		a.writeResult(true, map[string]any{
			"schema_version": schemaVersion,
			"error": map[string]string{
				"code":     string(remoteErr.Kind),
				"message":  remoteErr.Detail,
				"recovery": remoteErr.Recovery(),
			},
		})
		return
	}
	a.errf("%s: %s\n", command, remoteErr.Detail)
	if recovery := remoteErr.Recovery(); recovery != "" {
		a.errf("Recovery: %s\n", recovery)
	}
}

// out, outln and errf write one piece of command output.
//
// They exist to make one argument once instead of thirty times: the write error
// is deliberately discarded. Every call here is rendering to a terminal or a
// pipe the caller already owns, and if that destination has gone away there is
// nothing useful left to do about it — reporting the failure would need the
// same writer that just failed. The alternative, checking each call
// individually, adds thirty branches that can only ever be dead.
func (a *App) out(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stdout, format, args...)
}

func (a *App) outln(args ...any) {
	_, _ = fmt.Fprintln(a.stdout, args...)
}

func (a *App) errf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, format, args...)
}

// ghTimestamp renders one time as UTC RFC 3339, or as an empty string when the
// remote did not supply it. An absent timestamp is stated as absent rather than
// encoded as the zero year, which reads like a real date from 1 CE.
func ghTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// relativeAge says how long ago something happened in the coarsest useful
// unit. A backlog is read to judge staleness, and "3 days ago" answers that
// faster than a timestamp does.
func relativeAge(moment, now time.Time) string {
	if moment.IsZero() || now.IsZero() || moment.After(now) {
		return ""
	}
	elapsed := now.Sub(moment)
	switch {
	case elapsed < time.Hour:
		return "updated just now"
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("updated %dh ago", int(elapsed.Hours()))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("updated %dd ago", int(elapsed.Hours()/24))
	default:
		return "updated " + moment.UTC().Format("2006-01-02")
	}
}

func orPlaceholder(value, placeholder string) string {
	if strings.TrimSpace(value) == "" {
		return placeholder
	}
	return value
}

// listStyle holds the escape sequences for one invocation, or empty strings
// when the destination is not a terminal.
//
// Piped and redirected output must stay plain: these listings are read by
// `grep`, by `less`, and by whatever a script does with them, and an escape
// sequence none of them asked for is corruption of the data, not decoration.
type listStyle struct {
	bold  string
	dim   string
	cyan  string
	reset string
}

func (a *App) listStyle() listStyle {
	if !a.statusColorEnabled(false) {
		return listStyle{}
	}
	return listStyle{
		bold:  "\x1b[1m",
		dim:   "\x1b[2m",
		cyan:  "\x1b[36m",
		reset: "\x1b[0m",
	}
}
