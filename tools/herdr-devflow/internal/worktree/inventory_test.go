package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRepo lays out checkout directories so classifyCheckout can tell the
// source checkout (a .git directory) from linked worktrees (a .git file).
type fakeRepo struct {
	root string
}

func newFakeRepo(t *testing.T) *fakeRepo {
	t.Helper()
	// TempDir returns /var/... on macOS, which is a symlink to /private/var.
	// The inventory canonicalizes every path, so expectations must too.
	root, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temp root: %v", err)
	}
	return &fakeRepo{root: root}
}

func (r *fakeRepo) source(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(r.root, name)
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatalf("create source checkout: %v", err)
	}
	return path
}

func (r *fakeRepo) linked(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(r.root, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create linked worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatalf("write gitfile: %v", err)
	}
	return path
}

func porcelain(entries ...string) string { return strings.Join(entries, "\n\n") + "\n" }

func entry(path, head, branch string) string {
	return "worktree " + path + "\nHEAD " + head + "\nbranch refs/heads/" + branch
}

func runnerFor(output string) Runner {
	return func(context.Context, string, ...string) (string, error) { return output, nil }
}

func listFor(t *testing.T, repo *fakeRepo, output string) Inventory {
	t.Helper()
	inventory, err := ListCheckouts(context.Background(), repo.root, "dev", runnerFor(output), time.Now())
	if err != nil {
		t.Fatalf("ListCheckouts: %v", err)
	}
	return inventory
}

func TestListCheckoutsExcludesBaselinesAsFeatures(t *testing.T) {
	repo := newFakeRepo(t)
	main := repo.source(t, "ori-agent")
	dev := repo.linked(t, "ori-agent-dev")
	feature := repo.linked(t, "downloads-janitor")

	inventory := listFor(t, repo, porcelain(
		entry(main, "aaa", "main"),
		entry(dev, "bbb", "dev"),
		entry(feature, "ccc", "feature/downloads-janitor"),
	))

	if slugs := inventory.Slugs(); len(slugs) != 1 || slugs[0] != "downloads-janitor" {
		t.Fatalf("feature slugs = %v, want only the feature checkout", slugs)
	}
	if inventory.SourcePath != main {
		t.Fatalf("source path = %q, want %q", inventory.SourcePath, main)
	}
	if inventory.BaselinePath != dev {
		t.Fatalf("baseline path = %q, want %q", inventory.BaselinePath, dev)
	}
	if len(inventory.Checkouts) != 3 {
		t.Fatalf("checkouts = %d, want all 3 listed even though baselines are not features", len(inventory.Checkouts))
	}
}

func TestListCheckoutsPrefersBranchSlugAndRetainsPathSlug(t *testing.T) {
	repo := newFakeRepo(t)
	// wt start creates worktrees/wt-herd-feature-overview for the branch
	// feature/wt-herd-feature-overview; a renamed directory must not hide the
	// branch's stronger claim, but must stay visible as a mismatch.
	renamed := repo.linked(t, "old-directory-name")

	inventory := listFor(t, repo, porcelain(entry(renamed, "ddd", "feature/wt-herd-feature-overview")))

	checkouts, ok := inventory.Feature("wt-herd-feature-overview")
	if !ok || len(checkouts) != 1 {
		t.Fatalf("feature lookup = %v, ok=%v", checkouts, ok)
	}
	checkout := checkouts[0]
	if checkout.SlugOrigin != SlugOriginBranch {
		t.Fatalf("slug origin = %q, want branch", checkout.SlugOrigin)
	}
	if checkout.PathSlug != "old-directory-name" {
		t.Fatalf("path slug = %q, want the directory name retained for mismatch reporting", checkout.PathSlug)
	}
}

func TestListCheckoutsRecognizesEveryWorkBranchPrefix(t *testing.T) {
	// Branch prefix records intent, not size: pull requests have landed from
	// fix/ and feat/ branches, and missing those would hide delivered work.
	for _, prefix := range BranchPrefixes {
		repo := newFakeRepo(t)
		checkout := repo.linked(t, "renamed-dir")

		inventory := listFor(t, repo, porcelain(entry(checkout, "eee", prefix+"some-feature")))

		checkouts, ok := inventory.Feature("some-feature")
		if !ok || len(checkouts) != 1 {
			t.Fatalf("prefix %q: feature lookup = %v, ok=%v", prefix, checkouts, ok)
		}
		if checkouts[0].SlugOrigin != SlugOriginBranch {
			t.Fatalf("prefix %q: slug origin = %q, want branch", prefix, checkouts[0].SlugOrigin)
		}
	}
}

func TestListCheckoutsFallsBackToWorktreeBasename(t *testing.T) {
	repo := newFakeRepo(t)
	legacy := repo.linked(t, "legacy-feature")

	// An unrecognized namespace leaves the directory name as the only claim.
	inventory := listFor(t, repo, porcelain(entry(legacy, "eee", "wip/whatever")))

	checkouts, ok := inventory.Feature("legacy-feature")
	if !ok || len(checkouts) != 1 {
		t.Fatalf("feature lookup = %v, ok=%v", checkouts, ok)
	}
	if checkouts[0].SlugOrigin != SlugOriginPath {
		t.Fatalf("slug origin = %q, want path", checkouts[0].SlugOrigin)
	}
}

func TestListCheckoutsPreservesAmbiguousClaims(t *testing.T) {
	repo := newFakeRepo(t)
	first := repo.linked(t, "duplicate-claim")
	second := repo.linked(t, "other-directory")

	inventory := listFor(t, repo, porcelain(
		entry(first, "fff", "feature/duplicate-claim"),
		entry(second, "ggg", "feature/duplicate-claim"),
	))

	checkouts, _ := inventory.Feature("duplicate-claim")
	if len(checkouts) != 2 {
		t.Fatalf("claims = %d, want both preserved rather than one silently chosen", len(checkouts))
	}
	if checkouts[0].Path == checkouts[1].Path {
		t.Fatal("ambiguous claims collapsed to one path")
	}
}

func TestListCheckoutsIgnoresDetachedAndBareEntries(t *testing.T) {
	repo := newFakeRepo(t)
	detached := repo.linked(t, "detached-checkout")

	inventory := listFor(t, repo, porcelain(
		"worktree "+detached+"\nHEAD hhh\ndetached",
		"worktree "+repo.root+"\nbare",
	))

	if len(inventory.Features) != 0 {
		t.Fatalf("features = %v, want none from detached or bare entries", inventory.Features)
	}
	var found bool
	for _, checkout := range inventory.Checkouts {
		if checkout.Path == detached && checkout.Detached {
			found = true
		}
	}
	if !found {
		t.Fatal("detached checkout was dropped instead of listed without a slug")
	}
}

func TestListCheckoutsRejectsNonSlugDirectoryNames(t *testing.T) {
	repo := newFakeRepo(t)
	odd := repo.linked(t, "Not A Slug")

	inventory := listFor(t, repo, porcelain(entry(odd, "iii", "some-unprefixed-branch")))

	if len(inventory.Features) != 0 {
		t.Fatalf("features = %v, want none from a non-canonical directory name", inventory.Features)
	}
}

func TestListCheckoutsDropsSourcePathWhenNotExactlyOne(t *testing.T) {
	repo := newFakeRepo(t)
	first := repo.source(t, "checkout-one")
	second := repo.source(t, "checkout-two")

	inventory := listFor(t, repo, porcelain(
		entry(first, "jjj", "main"),
		entry(second, "kkk", "release"),
	))

	if inventory.SourcePath != "" {
		t.Fatalf("source path = %q, want empty when the layout is ambiguous", inventory.SourcePath)
	}
}

func TestListCheckoutsSharesRepositoryIdentityAcrossCheckouts(t *testing.T) {
	repo := newFakeRepo(t)
	repo.source(t, "ori-agent")

	inventory := listFor(t, repo, porcelain(entry(repo.root, "lll", "main")))
	if inventory.RepositoryID == "" || inventory.GitCommonDir == "" {
		t.Fatalf("identity = %+v, want a stable repository identity", inventory)
	}
	if inventory.RepositoryID != RepositoryID(inventory.GitCommonDir) {
		t.Fatal("repository ID was not derived from the Git common directory")
	}
}

func TestListCheckoutsSurfacesGitFailure(t *testing.T) {
	repo := newFakeRepo(t)
	failing := func(context.Context, string, ...string) (string, error) {
		return "", errors.New("git exploded")
	}
	if _, err := ListCheckouts(context.Background(), repo.root, "dev", failing, time.Now()); err == nil {
		t.Fatal("a failed Git listing was reported as success")
	}
}

func TestListCheckoutsUsesFixedArgumentVector(t *testing.T) {
	repo := newFakeRepo(t)
	var seen []string
	spy := func(_ context.Context, _ string, args ...string) (string, error) {
		seen = args
		return "", nil
	}
	if _, err := ListCheckouts(context.Background(), repo.root, "dev", spy, time.Now()); err != nil {
		t.Fatalf("ListCheckouts: %v", err)
	}
	want := []string{"worktree", "list", "--porcelain"}
	if len(seen) != len(want) {
		t.Fatalf("args = %v, want %v", seen, want)
	}
	for index := range want {
		if seen[index] != want[index] {
			t.Fatalf("args = %v, want %v", seen, want)
		}
	}
}

func TestCheckoutForResolvesCanonicalPathsNotDirectoryNames(t *testing.T) {
	repo := newFakeRepo(t)
	main := repo.source(t, "ori-agent")
	dev := repo.linked(t, "ori-agent-dev")
	feature := repo.linked(t, "downloads-janitor")

	inventory := listFor(t, repo, porcelain(
		entry(main, "aaa", "main"),
		entry(dev, "bbb", "dev"),
		entry(feature, "ccc", "feature/downloads-janitor"),
	))

	cases := []struct {
		name string
		path string
		want string
		slug string
	}{
		{name: "the dev checkout itself", path: dev, want: dev},
		{name: "a directory inside the dev checkout", path: filepath.Join(dev, "tasks"), want: dev},
		{name: "a feature worktree", path: feature, want: feature, slug: "downloads-janitor"},
		{name: "the source checkout", path: main, want: main},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			checkout, found := inventory.CheckoutFor(testCase.path)
			if !found {
				t.Fatalf("CheckoutFor(%q) found nothing", testCase.path)
			}
			if checkout.Path != testCase.want {
				t.Fatalf("CheckoutFor(%q) = %q, want %q", testCase.path, checkout.Path, testCase.want)
			}
			// The dev checkout's basename is a valid slug shape. Resolving by
			// path must never hand back a feature for it.
			if checkout.Slug != testCase.slug {
				t.Fatalf("CheckoutFor(%q) slug = %q, want %q", testCase.path, checkout.Slug, testCase.slug)
			}
		})
	}

	if _, found := inventory.CheckoutFor(filepath.Join(repo.root, "elsewhere")); found {
		t.Fatal("a path outside every checkout resolved to one")
	}
	if _, found := inventory.CheckoutFor(""); found {
		t.Fatal("an empty path resolved to a checkout")
	}
}

// TestCheckoutForPrefersTheDeepestContainingCheckout covers a repository whose
// linked worktrees live inside the source checkout, where a shallow match would
// attribute every feature agent to the source.
func TestCheckoutForPrefersTheDeepestContainingCheckout(t *testing.T) {
	repo := newFakeRepo(t)
	main := repo.source(t, "ori-agent")
	feature := repo.linked(t, filepath.Join("ori-agent", "worktrees", "downloads-janitor"))

	inventory := listFor(t, repo, porcelain(
		entry(main, "aaa", "main"),
		entry(feature, "ccc", "feature/downloads-janitor"),
	))

	checkout, found := inventory.CheckoutFor(filepath.Join(feature, "tools"))
	if !found || checkout.Path != feature || checkout.Slug != "downloads-janitor" {
		t.Fatalf("CheckoutFor(nested) = %+v, %v; want the feature worktree", checkout, found)
	}
}
