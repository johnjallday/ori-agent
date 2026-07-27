package worktree

import (
	"path/filepath"
	"strings"
)

// Contains reports whether candidate is a feature worktree's root or a path
// inside it.
//
// This is the primitive the bridge binds agents with. It matches on directory
// boundaries, never on string prefixes, so two sibling worktrees whose names
// share a prefix — `feature-a` and `feature-abc` — can never be confused for
// one another. Both values are canonicalized first, because a pane may report
// `/var/...` while the worktree inventory resolved `/private/var/...`, and the
// two must reach the same answer.
//
// Paths that do not exist still resolve: Herdr reports a cwd for a directory
// that has since been removed, and "the agent was in the worktree we just
// deleted" is an answer worth having rather than an error.
func Contains(root, candidate string) bool {
	canonicalRoot, ok := canonicalOrEmpty(root)
	if !ok {
		return false
	}
	canonicalCandidate, ok := canonicalOrEmpty(candidate)
	if !ok {
		return false
	}
	return within(canonicalRoot, canonicalCandidate)
}

// SameRepository reports whether two repository roots refer to one repository.
// It is what stops a same-named worktree in a different clone being attributed
// to this repository's feature.
func SameRepository(left, right string) bool {
	canonicalLeft, ok := canonicalOrEmpty(left)
	if !ok {
		return false
	}
	canonicalRight, ok := canonicalOrEmpty(right)
	if !ok {
		return false
	}
	return canonicalLeft == canonicalRight
}

// canonicalOrEmpty canonicalizes a path, reporting failure instead of an error
// for the two cases callers hit constantly: an empty value, and a value
// carrying control characters.
//
// A control character means the value came from somewhere untrustworthy — pane
// cwd and terminal titles both originate in a terminal — so it must never
// resolve into a match.
func canonicalOrEmpty(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	canonical, err := canonicalPath(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(canonical), true
}
