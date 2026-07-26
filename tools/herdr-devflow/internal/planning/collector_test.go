package planning

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDiscoverMatchesOnlyExactArtifactNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prd-downloads-janitor.md"), "# Downloads Janitor\n\nbody\n")
	writeFile(t, filepath.Join(dir, "tasks-downloads-janitor.md"), "- [ ] 1.0 Start\n")
	writeFile(t, filepath.Join(dir, "prd-email-ops.md"), "# Email Ops\n")
	writeFile(t, filepath.Join(dir, "test-guide-downloads-janitor.md"), "# Guide\n")
	writeFile(t, filepath.Join(dir, "notes.md"), "# Notes\n")
	writeFile(t, filepath.Join(dir, "prd-Not_A_Slug.md"), "# Nope\n")
	writeFile(t, filepath.Join(dir, "prd-nested.md.bak"), "# Nope\n")

	set := Discover(dir, time.Now())
	if set.State != StateAvailable {
		t.Fatalf("directory state = %q, want available", set.State)
	}
	slugs := set.Slugs()
	if len(slugs) != 2 || slugs[0] != "downloads-janitor" || slugs[1] != "email-ops" {
		t.Fatalf("slugs = %v, want [downloads-janitor email-ops]", slugs)
	}

	janitor, _ := set.Feature("downloads-janitor")
	if janitor.PRD.State != StateAvailable || janitor.PRD.Title != "Downloads Janitor" {
		t.Fatalf("PRD = %+v, want available with recovered title", janitor.PRD)
	}
	if janitor.TaskList.State != StateAvailable {
		t.Fatalf("task list state = %q, want available", janitor.TaskList.State)
	}

	// A PRD with no task list must read as a real absence, not as unknown.
	emailOps, _ := set.Feature("email-ops")
	if emailOps.TaskList.State != StateAbsent {
		t.Fatalf("missing task list state = %q, want absent", emailOps.TaskList.State)
	}
	if emailOps.TaskList.Path != "" {
		t.Fatalf("absent artifact retained a path %q", emailOps.TaskList.Path)
	}
}

func TestDiscoverMissingDirectoryIsAbsentNotUnavailable(t *testing.T) {
	set := Discover(filepath.Join(t.TempDir(), "no-such-dir"), time.Now())
	if set.State != StateAbsent {
		t.Fatalf("state = %q, want absent", set.State)
	}
	if len(set.Features) != 0 {
		t.Fatalf("features = %v, want none", set.Features)
	}
}

func TestDiscoverEmptyArtifactIsMalformed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prd-empty-feature.md"), "")

	set := Discover(dir, time.Now())
	feature, ok := set.Feature("empty-feature")
	if !ok {
		t.Fatal("empty PRD was not discovered")
	}
	if feature.PRD.State != StateMalformed {
		t.Fatalf("state = %q, want malformed", feature.PRD.State)
	}
}

func TestLookupRejectsNonCanonicalSlug(t *testing.T) {
	for _, slug := range []string{"", "../escape", "Upper", "with space", "a/b"} {
		if _, err := Lookup(t.TempDir(), slug, time.Now()); err == nil {
			t.Fatalf("Lookup(%q) succeeded, want rejection", slug)
		}
	}
}

func TestLookupPrefersNamedArtifacts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prd-active-copy.md"), "intro\n\n# Active Copy\n")

	feature, err := Lookup(dir, "active-copy", time.Now())
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if feature.PRD.Title != "Active Copy" {
		t.Fatalf("title = %q, want Active Copy", feature.PRD.Title)
	}
	if feature.TaskList.State != StateAbsent {
		t.Fatalf("task list state = %q, want absent", feature.TaskList.State)
	}
}

func TestSanitizeStripsControlCharactersAndBounds(t *testing.T) {
	got := Sanitize("clean\x1b[31mvalue\x00\tmore", 0)
	if got != "clean[31mvalue more" {
		t.Fatalf("sanitized = %q", got)
	}
	if bounded := Sanitize("abcdef", 3); bounded != "abc…" {
		t.Fatalf("bounded = %q, want abc…", bounded)
	}
}
