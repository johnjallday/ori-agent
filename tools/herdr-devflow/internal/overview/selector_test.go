package overview

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveSelectorFromTheBaselineCheckoutIsRepositoryScoped is the reported
// `--current` failure: the dev checkout's directory name has the shape of a
// feature slug, and reading it as one asked for a feature that never existed.
// FR10, FR11.
func TestResolveSelectorFromTheBaselineCheckoutIsRepositoryScoped(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	for _, path := range []string{scenario.Primary, filepath.Join(scenario.Primary, "tasks"), scenario.Source} {
		selector, err := snapshot.ResolveSelector(path)
		if err != nil {
			t.Fatalf("ResolveSelector(%q): %v", path, err)
		}
		if selector.Kind != SelectorRepository {
			t.Fatalf("ResolveSelector(%q) = %+v, want a repository selector", path, selector)
		}
		if selector.Feature != "" {
			t.Fatalf("ResolveSelector(%q) invented feature %q from a directory name", path, selector.Feature)
		}
	}
}

// TestResolveSelectorFromAFeatureWorktreeSelectsThatFeature covers FR9, and the
// symlink case that a basename comparison would also have got wrong.
func TestResolveSelectorFromAFeatureWorktreeSelectsThatFeature(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	selector, err := snapshot.ResolveSelector(scenario.Managed)
	if err != nil {
		t.Fatalf("ResolveSelector: %v", err)
	}
	if selector.Kind != SelectorFeature || selector.Feature != scenarioManagedFeature {
		t.Fatalf("selector = %+v, want the managed feature", selector)
	}

	// A path reached through a symlink is the same checkout.
	link := filepath.Join(t.TempDir(), "linked-managed")
	if err := os.Symlink(scenario.Managed, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linked, err := snapshot.ResolveSelector(filepath.Join(link, "tasks"))
	if err != nil {
		t.Fatalf("ResolveSelector(symlink): %v", err)
	}
	if linked.Kind != SelectorFeature || linked.Feature != scenarioManagedFeature {
		t.Fatalf("symlinked selector = %+v, want the managed feature", linked)
	}
}

// TestResolveSelectorRejectsPathsOutsideTheRepository keeps a removed worktree
// and an unrelated directory explicit failures rather than silently widening to
// the whole repository. FR11, FR127.
func TestResolveSelectorRejectsPathsOutsideTheRepository(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	for _, path := range []string{scenario.Removed, t.TempDir()} {
		if _, err := snapshot.ResolveSelector(path); err == nil {
			t.Fatalf("ResolveSelector(%q) accepted a path that is not a checkout of this repository", path)
		}
	}
}

// TestNarrowRepositoryKeepsActiveWorkAndUnscopedAgents locks in what standing in
// the dev checkout must show: every feature still in flight, and every agent
// that belongs to no feature. FR10.
func TestNarrowRepositoryKeepsActiveWorkAndUnscopedAgents(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	selector, err := snapshot.ResolveSelector(scenario.Primary)
	if err != nil {
		t.Fatalf("ResolveSelector: %v", err)
	}
	narrowed := snapshot.Narrow(selector)

	for _, feature := range snapshot.Features {
		_, retained := narrowed.Feature(feature.Slug)
		if feature.Phase.Phase.Terminal() == retained {
			t.Fatalf("feature %q terminal=%v retained=%v; active work must be kept and history dropped",
				feature.Slug, feature.Phase.Phase.Terminal(), retained)
		}
	}
	for _, pane := range []string{"w-dev:p1", "w-main:p1"} {
		if _, found := scenarioAgentByPane(narrowed, pane); !found {
			t.Fatalf("repository agent %q was dropped by narrowing: %+v", pane, narrowed.Agents)
		}
	}
	// The repository view is still a view of the whole repository: sources and
	// checkouts stay intact so it can say what it could not see.
	if len(narrowed.Sources) != len(snapshot.Sources) || len(narrowed.Checkouts) != len(snapshot.Checkouts) {
		t.Fatalf("narrowing dropped repository-scoped evidence: %+v", narrowed)
	}
}

// TestNarrowFeatureKeepsOnlyThatFeaturesAgents covers the detail view.
func TestNarrowFeatureKeepsOnlyThatFeaturesAgents(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	narrowed := snapshot.Narrow(SelectFeature(scenarioManagedFeature))
	if len(narrowed.Features) != 1 || narrowed.Features[0].Slug != scenarioManagedFeature {
		t.Fatalf("narrowed features = %+v, want only the managed feature", narrowed.Features)
	}
	if len(narrowed.Agents) != 2 {
		t.Fatalf("narrowed agents = %+v, want the feature's two agents", narrowed.Agents)
	}
	for _, agent := range narrowed.Agents {
		if agent.Feature != scenarioManagedFeature {
			t.Fatalf("narrowing to a feature kept a stranger's agent: %+v", agent)
		}
	}
}

// TestResolveSelectorWithoutAPathSelectsEverything keeps the default behavior
// of `wt status` unchanged.
func TestResolveSelectorWithoutAPathSelectsEverything(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	selector, err := snapshot.ResolveSelector("")
	if err != nil || selector.Kind != SelectorAll {
		t.Fatalf("ResolveSelector(\"\") = %+v, %v; want the unnarrowed selector", selector, err)
	}
	narrowed := snapshot.Narrow(selector)
	if len(narrowed.Features) != len(snapshot.Features) || len(narrowed.Agents) != len(snapshot.Agents) {
		t.Fatalf("the unnarrowed selector changed the snapshot: %+v", narrowed)
	}
}
