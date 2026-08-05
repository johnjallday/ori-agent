package web

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
)

// staticFS roots the embedded tree where the catalog's repo-relative asset
// paths begin, i.e. at `characters/...`.
func staticFS(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(Static, "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}
	return sub
}

// The catalog validates path *shape* at load time; this proves the files are
// actually embedded. Without it a forgotten `git add` ships as a broken
// portrait at runtime instead of a red build (FR-114/FR-124).
func TestEveryCatalogAssetIsEmbedded(t *testing.T) {
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if err := cat.ValidateAssetsExist(staticFS(t)); err != nil {
		t.Fatal(err)
	}
}

// The inverse sweep: an SVG sitting under characters/ that no catalog entry
// references is either dead weight or an asset someone forgot to register.
func TestNoOrphanCharacterAssets(t *testing.T) {
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	declared := make(map[string]bool)
	for _, p := range cat.AssetPaths() {
		declared[p] = true
	}

	sub := staticFS(t)
	err = fs.WalkDir(sub, "characters", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".svg") {
			return nil
		}
		if !declared[path] {
			t.Errorf("asset %q is not referenced by any catalog entry", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk characters/: %v", err)
	}
}

// Production art is hand-authored SVG with no external dependency. A remote
// reference would silently reintroduce a third-party asset the provenance
// register claims does not exist (FR-111), and would break offline rendering.
func TestCharacterAssetsHaveNoExternalReferences(t *testing.T) {
	sub := staticFS(t)
	forbidden := []string{"http://", "https://", "//fonts.", "<image", "xlink:href", "url(#ext", "@import"}

	err := fs.WalkDir(sub, "characters", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".svg") {
			return nil
		}
		raw, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		body := string(raw)
		for _, needle := range forbidden {
			// The xmlns declaration is the one legitimate https:// occurrence.
			if needle == "https://" {
				stripped := strings.ReplaceAll(body, `xmlns="http://www.w3.org/2000/svg"`, "")
				if strings.Contains(stripped, "https://") {
					t.Errorf("%s contains an external https reference", path)
				}
				continue
			}
			if needle == "http://" {
				continue // covered by the xmlns-stripped check above
			}
			if strings.Contains(body, needle) {
				t.Errorf("%s contains forbidden external reference %q", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk characters/: %v", err)
	}
}

// Reduced motion must not cost the user information: the static variant has to
// exist for every animated sprite and must itself be free of animation (FR-120).
func TestStaticVariantsCarryNoAnimation(t *testing.T) {
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	sub := staticFS(t)
	for _, ch := range cat.Characters {
		raw, err := fs.ReadFile(sub, ch.Assets.Static)
		if err != nil {
			t.Errorf("%s: read static asset: %v", ch.ID, err)
			continue
		}
		body := string(raw)
		for _, needle := range []string{"@keyframes", "animation:", "<animate"} {
			if strings.Contains(body, needle) {
				t.Errorf("%s static variant contains %q; it must be motionless", ch.ID, needle)
			}
		}
	}
}

// Every animated sprite must also honour the OS preference on its own, so the
// file stays safe wherever it is embedded directly rather than through the
// resolver (defence in depth for FR-120).
func TestAnimatedSpritesRespectReducedMotion(t *testing.T) {
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	sub := staticFS(t)
	for _, ch := range cat.Characters {
		raw, err := fs.ReadFile(sub, ch.Assets.Sprite)
		if err != nil {
			t.Errorf("%s: read sprite: %v", ch.ID, err)
			continue
		}
		body := string(raw)
		if !strings.Contains(body, "@keyframes") {
			continue // a still sprite needs no guard
		}
		if !strings.Contains(body, "prefers-reduced-motion") {
			t.Errorf("%s sprite animates without a prefers-reduced-motion guard", ch.ID)
		}
	}
}
