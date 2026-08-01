package overview

import (
	"fmt"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// SelectorKind names what a status selector narrowed the snapshot to.
type SelectorKind string

const (
	// SelectorAll is the whole repository: no narrowing was requested.
	SelectorAll SelectorKind = "all"
	// SelectorFeature is one exact feature and its agents.
	SelectorFeature SelectorKind = "feature"
	// SelectorRepository is a checkout that implements no feature — the source
	// checkout or a baseline dev/main worktree. Standing in one is a request to
	// see the repository's active work, not a feature named after the directory.
	SelectorRepository SelectorKind = "repository"
)

// Selector is the resolved narrowing for one render. Every surface resolves it
// the same way, against the same snapshot, so `wt status`, JSON, and the board
// can never disagree about which checkout the operator is standing in.
type Selector struct {
	Kind SelectorKind
	// Feature is the exact slug when Kind is SelectorFeature.
	Feature string
	// CheckoutPath is the canonical checkout the selector resolved into, empty
	// when the selector was not derived from a path.
	CheckoutPath string
	// Detail is a sanitized, operator-facing description of the resolution.
	Detail string
}

// SelectAll is the unnarrowed selector.
func SelectAll() Selector { return Selector{Kind: SelectorAll} }

// SelectFeature narrows to one exact slug, as `--feature` does.
func SelectFeature(slug string) Selector {
	if slug == "" {
		return SelectAll()
	}
	return Selector{Kind: SelectorFeature, Feature: slug}
}

// ResolveSelector maps a working directory onto this snapshot.
//
// Resolution compares canonical paths against the collected checkouts. It never
// treats a directory basename as an identity: `ori-agent-dev` has the shape of
// a feature slug, and reading it as one is why standing in the dev checkout
// used to report that no feature by that name existed.
func (s Snapshot) ResolveSelector(path string) (Selector, error) {
	if path == "" {
		return SelectAll(), nil
	}
	checkout, found := s.CheckoutFor(path)
	if !found {
		return Selector{}, fmt.Errorf("%s is not a checkout of this repository", identityPath(path))
	}
	if checkout.Feature != "" {
		return Selector{
			Kind:         SelectorFeature,
			Feature:      checkout.Feature,
			CheckoutPath: checkout.Path,
			Detail:       "resolved from the " + checkout.Feature + " worktree",
		}, nil
	}
	return Selector{
		Kind:         SelectorRepository,
		CheckoutPath: checkout.Path,
		Detail:       "resolved from " + checkoutDescription(checkout) + ", which implements no feature",
	}, nil
}

// CheckoutFor resolves the checkout containing path, deepest match first.
func (s Snapshot) CheckoutFor(path string) (Checkout, bool) {
	best, found := Checkout{}, false
	for _, checkout := range s.Checkouts {
		if !worktree.Contains(checkout.Path, path) {
			continue
		}
		if !found || len(checkout.Path) > len(best.Path) {
			best, found = checkout, true
		}
	}
	return best, found
}

// Narrow returns the snapshot restricted to what the selector selected.
//
// Narrowing is a view, not a second collection: every retained value is the one
// already observed, and repository-scoped facts (sources, findings, repository
// identity) are kept intact so a narrowed view still says what it could not see.
func (s Snapshot) Narrow(selector Selector) Snapshot {
	switch selector.Kind {
	case SelectorFeature:
		narrowed := s
		narrowed.Features = nil
		if found, ok := s.Feature(selector.Feature); ok {
			narrowed.Features = []Feature{found}
		}
		narrowed.Agents = agentsForFeature(s.Agents, selector.Feature)
		return narrowed
	case SelectorRepository:
		// Standing in a checkout that implements no feature is a request for
		// the repository's active work: every feature still in flight, plus the
		// agents that belong to no feature at all. History is what `wt status`
		// without a selector is for.
		narrowed := s
		narrowed.Features = nil
		for _, feature := range s.Features {
			if !feature.Phase.Phase.Terminal() {
				narrowed.Features = append(narrowed.Features, feature)
			}
		}
		narrowed.Agents = nil
		for _, agent := range s.Agents {
			if agent.Scope == AgentScopeRepository {
				narrowed.Agents = append(narrowed.Agents, agent)
				continue
			}
			if _, retained := findFeature(narrowed.Features, agent.Feature); retained {
				narrowed.Agents = append(narrowed.Agents, agent)
			}
		}
		return narrowed
	default:
		return s
	}
}

func agentsForFeature(agents []Agent, slug string) []Agent {
	var matched []Agent
	for _, agent := range agents {
		if agent.Feature == slug {
			matched = append(matched, agent)
		}
	}
	return matched
}

func findFeature(features []Feature, slug string) (Feature, bool) {
	if slug == "" {
		return Feature{}, false
	}
	for _, feature := range features {
		if feature.Slug == slug {
			return feature, true
		}
	}
	return Feature{}, false
}

// checkoutDescription names a checkout the way an operator refers to it.
func checkoutDescription(checkout Checkout) string {
	switch {
	case checkout.Branch != "":
		return "the " + checkout.Branch + " checkout"
	case checkout.Detached:
		return "a detached checkout"
	default:
		return "a checkout of this repository"
	}
}
