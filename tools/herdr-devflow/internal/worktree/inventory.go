package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// FeatureBranchPrefix is the branch namespace `wt start` creates.
	FeatureBranchPrefix = "feature/"
	// MaxCheckouts bounds how many linked worktrees are inventoried.
	MaxCheckouts = 200
	// listTimeout bounds the single Git command this inventory runs.
	listTimeout = 10 * time.Second
)

// baselineBranches are integration branches that are never feature
// implementations, however they are checked out or named.
var baselineBranches = map[string]struct{}{
	"main":    {},
	"master":  {},
	"dev":     {},
	"develop": {},
}

// featureSlug is the exact slug shape shared by branches, worktree basenames,
// and planning filenames.
var featureSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

// ValidSlug reports whether value is an exact, canonical feature slug.
func ValidSlug(value string) bool { return featureSlug.MatchString(value) }

// Runner executes a Git command and returns its standard output. Injecting it
// keeps the inventory testable without a real repository and keeps every
// invocation a fixed argument vector.
type Runner func(ctx context.Context, dir string, args ...string) (string, error)

// SlugOrigin records how a checkout's feature slug was derived. A branch is
// stronger evidence than a directory name, and disagreement between the two is
// preserved rather than resolved by guessing.
type SlugOrigin string

const (
	// SlugFromBranch means the slug came from a feature/<slug> branch.
	SlugFromBranch SlugOrigin = "branch"
	// SlugFromPath means the slug came from the worktree basename because the
	// branch was not in the feature namespace.
	SlugFromPath SlugOrigin = "path"
	// SlugUnresolved means no exact slug could be derived.
	SlugUnresolved SlugOrigin = "unresolved"
)

// Checkout is one linked worktree or source checkout of the repository.
type Checkout struct {
	// Path is the canonical worktree root.
	Path string
	// Branch is the checked-out branch without refs/heads/, empty if detached.
	Branch string
	// Head is the resolved commit of this checkout.
	Head string
	// Slug is the exact feature slug this checkout implements, empty when the
	// checkout is a baseline or no exact slug could be derived.
	Slug string
	// SlugOrigin records how Slug was derived.
	SlugOrigin SlugOrigin
	// PathSlug is the basename-derived slug, retained even when the branch
	// supplied Slug so a name mismatch stays detectable.
	PathSlug string
	// Baseline is true for main/dev-style integration checkouts, which are
	// never treated as feature implementations.
	Baseline bool
	// Source is true for the repository's normal checkout, which owns a .git
	// directory rather than a .git file.
	Source bool
	// Detached is true when the checkout has no branch.
	Detached bool
	// Bare is true for a bare repository entry in Git's listing.
	Bare bool
}

// Inventory is the read-only result of listing a repository's checkouts.
type Inventory struct {
	// RepositoryID is the stable identity shared by every linked worktree.
	RepositoryID string
	// GitCommonDir is the canonical directory that identity derives from.
	GitCommonDir string
	// SourcePath is the repository's normal checkout, when exactly one exists.
	SourcePath string
	// BaselinePath is the checkout of the baseline branch, when one exists.
	BaselinePath string
	// Checkouts are every listed checkout, including baselines and the source.
	Checkouts []Checkout
	// Features maps exact slug to the feature checkouts claiming it. More than
	// one entry is an ambiguity to report, never to silently resolve.
	Features map[string][]Checkout
	// Truncated is true when MaxCheckouts capped the listing.
	Truncated bool
	// ObservedAt is when Git was consulted.
	ObservedAt time.Time
}

// Feature returns the checkouts claiming an exact slug.
func (i Inventory) Feature(slug string) ([]Checkout, bool) {
	checkouts, ok := i.Features[slug]
	return checkouts, ok
}

// Slugs returns the discovered feature slugs in sorted order.
func (i Inventory) Slugs() []string {
	slugs := make([]string, 0, len(i.Features))
	for slug := range i.Features {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// GitRunner is the default Runner. It uses a fixed argument vector and never
// passes user input through a shell.
func GitRunner(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 -- dir is a canonical repository path and args are fixed Git
	// plumbing arguments composed by this package, never user input.
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// ListCheckouts inventories every checkout of the repository containing
// repoRoot. It runs one read-only Git command and mutates nothing.
//
// Baseline checkouts are listed but never carry a feature slug: `dev` is where
// features are planned and archived, not where they are implemented.
func ListCheckouts(ctx context.Context, repoRoot, baseline string, run Runner, now time.Time) (Inventory, error) {
	if run == nil {
		run = GitRunner
	}
	if strings.TrimSpace(baseline) == "" {
		baseline = "dev"
	}
	canonicalRoot, err := canonicalPath(repoRoot)
	if err != nil {
		return Inventory{}, fmt.Errorf("canonicalize repository root: %w", err)
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	output, err := run(listCtx, canonicalRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return Inventory{}, fmt.Errorf("list linked Git worktrees: %w", err)
	}

	commonDir := gitCommonDir(canonicalRoot)
	inventory := Inventory{
		RepositoryID: RepositoryID(commonDir),
		GitCommonDir: commonDir,
		Features:     map[string][]Checkout{},
		ObservedAt:   now,
	}

	sources := 0
	for _, record := range parsePorcelain(output) {
		if len(inventory.Checkouts) >= MaxCheckouts {
			inventory.Truncated = true
			break
		}
		checkout, ok := classifyCheckout(record, baseline)
		if !ok {
			continue
		}
		if checkout.Source {
			sources++
			inventory.SourcePath = checkout.Path
		}
		if checkout.Baseline && checkout.Branch == baseline {
			inventory.BaselinePath = checkout.Path
		}
		inventory.Checkouts = append(inventory.Checkouts, checkout)
		if checkout.Slug != "" {
			inventory.Features[checkout.Slug] = append(inventory.Features[checkout.Slug], checkout)
		}
	}
	// More than one normal checkout means the repository layout is not what
	// the bridge assumes; leave the field empty rather than pick one.
	if sources != 1 {
		inventory.SourcePath = ""
	}
	return inventory, nil
}

// record is one raw entry from `git worktree list --porcelain`.
type record struct {
	Path     string
	Head     string
	Branch   string
	Detached bool
	Bare     bool
}

func parsePorcelain(output string) []record {
	var records []record
	var current record
	started := false
	flush := func() {
		if started && current.Path != "" {
			records = append(records, current)
		}
		current = record{}
		started = false
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
			started = true
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		}
	}
	flush()
	return records
}

func classifyCheckout(raw record, baseline string) (Checkout, bool) {
	canonical, err := canonicalPath(raw.Path)
	if err != nil {
		return Checkout{}, false
	}
	checkout := Checkout{
		Path:       canonical,
		Branch:     raw.Branch,
		Head:       raw.Head,
		Detached:   raw.Detached || raw.Branch == "",
		Bare:       raw.Bare,
		SlugOrigin: SlugUnresolved,
	}
	if entry, err := os.Stat(filepath.Join(canonical, ".git")); err == nil && entry.IsDir() {
		checkout.Source = true
	}
	if basename := strings.ToLower(filepath.Base(canonical)); ValidSlug(basename) {
		checkout.PathSlug = basename
	}

	_, isBaselineBranch := baselineBranches[checkout.Branch]
	if isBaselineBranch || checkout.Branch == baseline {
		checkout.Baseline = true
		return checkout, true
	}
	if checkout.Bare || checkout.Detached {
		return checkout, true
	}

	// A feature/<slug> branch is the strongest local claim. The directory name
	// is only consulted when the branch is outside that namespace, and a
	// disagreement between the two is preserved via PathSlug for the caller to
	// report as a name mismatch.
	if suffix, ok := strings.CutPrefix(checkout.Branch, FeatureBranchPrefix); ok && ValidSlug(suffix) {
		checkout.Slug = suffix
		checkout.SlugOrigin = SlugFromBranch
		return checkout, true
	}
	if checkout.PathSlug != "" && !checkout.Source {
		checkout.Slug = checkout.PathSlug
		checkout.SlugOrigin = SlugFromPath
	}
	return checkout, true
}
