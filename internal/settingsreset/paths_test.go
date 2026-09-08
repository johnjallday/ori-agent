package settingsreset

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

func TestPreviewPathConfinementHandlesSiblingsMissingPathsAndAliases(t *testing.T) {
	f := resetfixture.New(t)
	p := f.Paths()
	root, err := resolvePath(p.DataDir)
	mustPreview(t, err)
	child, err := resolvePath(filepath.Join(p.DataDir, "missing", "file.json"))
	mustPreview(t, err)
	if !containsPath(root, child) || containsPath(root, filepath.Join(p.Root, "data-backup", "settings.json")) {
		t.Fatal("separator-aware confinement failed")
	}
	volumeRoot := filepath.VolumeName(root) + string(filepath.Separator)
	if !pathsOverlap(volumeRoot, root) {
		t.Fatal("filesystem root did not protect its descendants")
	}
	for _, bad := range []string{"", " ", ":memory:", "bad\x00path", string([]byte{'/', 0xff})} {
		if _, err := resolvePath(bad); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("native Windows link permissions are not available in this fixture")
	}
	mustPreview(t, os.Symlink(filepath.Join(p.Root, "external"), filepath.Join(p.DataDir, "escape")))
	escaped, err := resolvePath(filepath.Join(p.DataDir, "escape", "settings.json"))
	mustPreview(t, err)
	if containsPath(root, escaped) {
		t.Fatal("escaping symlink accepted")
	}
	mustPreview(t, os.Symlink(filepath.Join(p.Root, "nonexistent"), filepath.Join(p.DataDir, "dangling")))
	if _, err := resolvePath(filepath.Join(p.DataDir, "dangling", "new.json")); err == nil {
		t.Fatal("dangling symlink treated as missing directory")
	}
	retained := filepath.Join(p.Vaults, "retained.bin")
	linked := filepath.Join(p.DataDir, "linked-settings.json")
	mustPreview(t, os.Link(retained, linked))
	if !pathsOverlap(retained, linked) {
		t.Fatal("hard-linked retained contents were not protected")
	}
}

// Port the old HTTP validator cases to the live planner, not a copied test-only
// deletion algorithm. Every configured owner below stays in the owned fixture.
func TestPreviewConfiguredOwnerConfinementRejectsRootsTraversalAndLinks(t *testing.T) {
	f, owners, planner := previewFixture(t)
	for _, root := range []string{"", " ", filepath.VolumeName(f.Paths().DataDir) + string(filepath.Separator)} {
		owners.DataDir = root
		view, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
		mustPreview(t, err)
		if !hasBlocker(view, "installation_unavailable") {
			t.Fatal("unsafe installation root accepted")
		}
	}
	owners.DataDir = f.Paths().DataDir
	for _, path := range []string{
		filepath.Join(f.Paths().Root, "data-backup", "app_state.json"),
		filepath.Join(f.Paths().DataDir, "..", "outside", "app_state.json"),
		f.Paths().DataDir,
	} {
		owners.Setup = onboarding.NewManager(path)
		view, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
		mustPreview(t, err)
		if !hasBlocker(view, "target_outside_installation") {
			t.Fatal("sibling/traversal/root target accepted")
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(f.Paths().DataDir, "outside-link")
		mustPreview(t, os.Symlink(f.Paths().WorkDir, link))
		owners.Setup = onboarding.NewManager(filepath.Join(link, "app_state.json"))
		view, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
		mustPreview(t, err)
		if !hasBlocker(view, "target_outside_installation") {
			t.Fatal("escaping owner link accepted")
		}
	}
	f.AssertPreserved(t)
}

func TestPreviewFileCountsDoNotFollowLinksOrPretendErrorsAreZero(t *testing.T) {
	f := resetfixture.New(t)
	missing := countFiles(t.Context(), filepath.Join(f.Paths().DataDir, "missing"))
	if missing.Count == nil || *missing.Count != 0 {
		t.Fatal("absent root was not correctly counted")
	}
	mustPreview(t, f.WriteFile("data/uploads/file.txt", []byte("fixture")))
	root := filepath.Join(f.Paths().DataDir, "uploads")
	count := countFiles(t.Context(), root)
	if count.Count == nil || *count.Count != 1 {
		t.Fatal("owned file count wrong")
	}
	if runtime.GOOS == "windows" {
		t.Skip("native Windows link permissions are not available in this fixture")
	}
	mustPreview(t, os.Symlink(f.Paths().Vaults, filepath.Join(root, "external-link")))
	count = countFiles(t.Context(), root)
	if count.Count != nil || count.UnavailableReason == "" {
		t.Fatal("uninspected linked contents were reported as a known count")
	}
}
