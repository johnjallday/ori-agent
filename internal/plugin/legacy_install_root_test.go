package plugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// copyTree copies a directory tree, so a fixture bundle can be updated in
// place without touching the checked-in example.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 -- test fixture path under the repo's examples tree
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}

func exampleSurfaceBundle(t *testing.T, dst string) {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "workspace-surface-demo"))
	if err != nil {
		t.Fatal(err)
	}
	copyTree(t, src, dst)
}

func TestCanonicalInstallRootRepairsLegacyRelativeRecords(t *testing.T) {
	base := t.TempDir()
	bundle := filepath.Join(base, "bundle")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatal(err)
	}
	cloneDir := filepath.Join(base, "clones")
	cloned := filepath.Join(cloneDir, "cloned-plugin")
	if err := os.MkdirAll(cloned, 0o750); err != nil {
		t.Fatal(err)
	}

	t.Run("an absolute root is used as recorded", func(t *testing.T) {
		got, err := canonicalInstallRoot(bundle, bundle, cloneDir)
		if err != nil || got != bundle {
			t.Fatalf("root = %q, err = %v", got, err)
		}
	})

	t.Run("a legacy git-cloned root is found under the clone directory", func(t *testing.T) {
		got, err := canonicalInstallRoot("cloned-plugin", "https://example.test/cloned-plugin.git", cloneDir)
		if err != nil || got != cloned {
			t.Fatalf("root = %q, err = %v", got, err)
		}
	})

	t.Run("a legacy root recorded from the old working directory still resolves there", func(t *testing.T) {
		t.Chdir(base)
		got, err := canonicalInstallRoot("bundle", "bundle", "")
		if err != nil || got != bundle {
			t.Fatalf("root = %q, err = %v", got, err)
		}
	})

	t.Run("a legacy root is recovered from an absolute recorded source", func(t *testing.T) {
		// The server now starts somewhere else entirely, so the recorded
		// relative root no longer resolves against the working directory.
		t.Chdir(t.TempDir())
		got, err := canonicalInstallRoot("bundle", bundle, "")
		if err != nil || got != bundle {
			t.Fatalf("root = %q, err = %v", got, err)
		}
	})

	t.Run("an unlocatable root fails loudly instead of resolving somewhere else", func(t *testing.T) {
		t.Chdir(t.TempDir())
		got, err := canonicalInstallRoot("bundle", "https://example.test/gone.git", "")
		if err == nil {
			t.Fatalf("a missing install root silently resolved to %q", got)
		}
		if !strings.Contains(err.Error(), "could not be located") {
			t.Fatalf("error is not actionable: %v", err)
		}
	})
}

// TestLegacyRelativeRecordUpdatesToContributedBlueprints is the upgrade path
// this feature depends on: a plugin installed before install roots were
// absolutized, updated from a server that no longer starts in the directory
// the record was written in, must resolve its real bundle, re-disclose the
// trust change, and end up recording the blueprints it now contributes —
// without leaving the store and the checkout describing different things.
func TestLegacyRelativeRecordUpdatesToContributedBlueprints(t *testing.T) {
	base := t.TempDir()
	bundle := filepath.Join(base, "bundle")
	exampleSurfaceBundle(t, bundle)

	pluginsDir := t.TempDir()
	registrar := &fakeRegistrar{}
	skills := &fakeSkills{}
	manager := NewManager(registrar, skills, pluginsDir, "")

	// A legacy record: relative install root, no recorded contribution, and
	// enabled by a user who had been using it.
	legacy := InstalledPlugin{
		Name: "workspace-surface-demo", Version: "0.0.9",
		Source: bundle, Format: FormatClaude,
		InstallDir:  "bundle",
		Generation:  1,
		Enabled:     true,
		InstalledAt: time.Now().UTC().Add(-24 * time.Hour),
	}
	if err := manager.store.Put(legacy); err != nil {
		t.Fatal(err)
	}

	// The server is restarted from a different working directory.
	t.Chdir(t.TempDir())

	report, changed, err := manager.UpdatePreview("workspace-surface-demo")
	if err != nil {
		t.Fatalf("update preview against a legacy record: %v", err)
	}
	if !changed {
		t.Fatal("a legacy record gaining a contribution was reported as unchanged")
	}
	if report.String() == "" {
		t.Fatal("the update preview disclosed nothing")
	}

	// The trust gate still governs: declining changes nothing.
	if _, err := manager.Update("workspace-surface-demo", func(TrustReport) bool { return false }); err == nil {
		t.Fatal("a declined update was applied")
	}
	declined, _, err := manager.store.Get("workspace-surface-demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(declined.ResolvedBlueprints) != 0 || declined.InstallDir != "bundle" || declined.Generation != 1 {
		t.Fatalf("a declined update mutated the record: %+v", declined)
	}

	confirmed := false
	updated, err := manager.Update("workspace-surface-demo", func(TrustReport) bool {
		confirmed = true
		return true
	})
	if err != nil {
		t.Fatalf("update a legacy record: %v", err)
	}
	if !confirmed {
		t.Fatal("a trust-changing update skipped its disclosure")
	}
	if !filepath.IsAbs(updated.InstallDir) {
		t.Fatalf("update did not heal the legacy relative root: %q", updated.InstallDir)
	}
	if len(updated.ResolvedBlueprints) != 1 {
		t.Fatalf("update did not record the contributed blueprints: %+v", updated.ResolvedBlueprints)
	}
	blueprint := updated.ResolvedBlueprints[0]
	if !filepath.IsAbs(blueprint.SkeletonRoot) || blueprint.SkeletonDigest == "" {
		t.Fatalf("blueprint was recorded without a validated skeleton: %+v", blueprint)
	}
	if !strings.HasPrefix(blueprint.SkeletonRoot, updated.InstallDir) {
		t.Fatalf("blueprint skeleton resolved outside the install root: %q not under %q",
			blueprint.SkeletonRoot, updated.InstallDir)
	}
	if updated.WorkspaceSurfaces == nil {
		t.Fatal("update did not record the workspace surface contribution")
	}
	if !updated.Enabled {
		t.Fatal("update silently disabled a plugin the user had enabled")
	}
	if updated.Generation <= legacy.Generation {
		t.Fatalf("generation did not advance: %d -> %d", legacy.Generation, updated.Generation)
	}

	// The store and the checkout now agree: what was recorded is what is there.
	stored, _, err := manager.store.Get("workspace-surface-demo")
	if err != nil {
		t.Fatal(err)
	}
	if stored.InstallDir != updated.InstallDir || len(stored.ResolvedBlueprints) != 1 {
		t.Fatalf("stored record diverged from the update result: %+v", stored)
	}
	if _, err := os.Stat(filepath.Join(stored.InstallDir, OriManifestDir, OriManifestFile)); err != nil {
		t.Fatalf("recorded install root does not hold the bundle: %v", err)
	}
}
