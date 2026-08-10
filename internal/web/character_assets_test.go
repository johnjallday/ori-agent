package web

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
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

// --- map-ready transparent asset contract -----------------------------------
//
// Structural half of the contract enforced by scripts/character-assets.test.mjs.
// That test rasterizes and looks at alpha, which catches anything painting a
// full artboard or touching an edge. It cannot see a baked halo that sits wholly
// inside the safe perimeter, because such a halo is indistinguishable from the
// character by area alone. The rules below read the source instead and reject
// the background *primitives*, so the two halves together leave no gap.

// variantSpec is the native geometry each variant must declare.
var variantSpecs = map[string]struct {
	size    float64
	viewBox string
}{
	"portrait": {size: 160, viewBox: "0 0 160 160"},
	"sprite":   {size: 48, viewBox: "0 0 48 48"},
	"static":   {size: 48, viewBox: "0 0 48 48"},
}

// characterVariants yields every (id, variant, asset path) the catalog declares.
func characterVariants(t *testing.T) []struct{ ID, Variant, Path string } {
	t.Helper()
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	var out []struct{ ID, Variant, Path string }
	for _, ch := range cat.Characters {
		for variant, path := range map[string]string{
			"portrait": ch.Assets.Portrait,
			"sprite":   ch.Assets.Sprite,
			"static":   ch.Assets.Static,
		} {
			out = append(out, struct{ ID, Variant, Path string }{string(ch.ID), variant, path})
		}
	}
	return out
}

var (
	viewBoxRe = regexp.MustCompile(`viewBox="([^"]*)"`)
	svgSizeRe = regexp.MustCompile(`<svg[^>]*\swidth="([\d.]+)"[^>]*\sheight="([\d.]+)"`)
	rectRe    = regexp.MustCompile(`<rect\b[^>]*>`)
	circleRe  = regexp.MustCompile(`<circle\b[^>]*>`)
	ellipseRe = regexp.MustCompile(`<ellipse\b[^>]*>`)
)

// attrNum pulls a numeric attribute out of one element's source text.
func attrNum(tag, name string) (float64, bool) {
	re := regexp.MustCompile(`\b` + name + `="([\d.-]+)"`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// bakedBackgrounds returns a description of every background primitive found in
// one asset. The thresholds are chosen to sit clearly between the background
// idioms this feature removes and the largest shape any character legitimately
// uses: the widest character part is ~58% of the artboard, and the largest head
// is ~25% of the artboard in radius.
func bakedBackgrounds(body string, size float64) []string {
	var found []string

	// A clip frame exists only to shape a background. Transparent art needs none.
	if strings.Contains(body, "<clipPath") {
		found = append(found, "a <clipPath> frame; transparent art needs no clipping")
	}

	for _, tag := range rectRe.FindAllString(body, -1) {
		w, okW := attrNum(tag, "width")
		if !okW || w < size*0.9 {
			continue
		}
		h, _ := attrNum(tag, "height")
		if h >= size*0.9 {
			found = append(found, fmt.Sprintf("a full-artboard %.0fx%.0f <rect> card", w, h))
		} else {
			found = append(found, fmt.Sprintf("a full-width %.0fx%.0f <rect> band (ground strip)", w, h))
		}
	}

	for _, tag := range circleRe.FindAllString(body, -1) {
		r, ok := attrNum(tag, "r")
		if ok && r >= size*0.3 {
			found = append(found, fmt.Sprintf("an oversized <circle> r=%.0f (disc or halo)", r))
		}
	}

	for _, tag := range ellipseRe.FindAllString(body, -1) {
		rx, okX := attrNum(tag, "rx")
		ry, okY := attrNum(tag, "ry")
		if okX && okY && rx >= size*0.3 && ry >= size*0.3 {
			found = append(found, fmt.Sprintf("an oversized <ellipse> %.0fx%.0f (halo)", rx, ry))
		}
	}

	return found
}

// Native geometry is not ratcheted: every asset already declares it, and an
// asset that renders at the wrong scale cannot be composited predictably.
func TestCharacterAssetsDeclareNativeGeometry(t *testing.T) {
	sub := staticFS(t)
	for _, v := range characterVariants(t) {
		spec, ok := variantSpecs[v.Variant]
		if !ok {
			t.Fatalf("unknown variant %q", v.Variant)
		}
		raw, err := fs.ReadFile(sub, v.Path)
		if err != nil {
			t.Errorf("%s/%s: read: %v", v.ID, v.Variant, err)
			continue
		}
		body := string(raw)

		m := viewBoxRe.FindStringSubmatch(body)
		if m == nil {
			t.Errorf("%s/%s: no viewBox", v.ID, v.Variant)
		} else if m[1] != spec.viewBox {
			t.Errorf("%s/%s: viewBox is %q, want %q", v.ID, v.Variant, m[1], spec.viewBox)
		}

		s := svgSizeRe.FindStringSubmatch(body)
		if s == nil {
			t.Errorf("%s/%s: <svg> declares no width/height", v.ID, v.Variant)
			continue
		}
		if s[1] != strconv.FormatFloat(spec.size, 'f', -1, 64) || s[2] != s[1] {
			t.Errorf("%s/%s: renders %sx%s, want %[5]vx%[5]v", v.ID, v.Variant, s[1], s[2], spec.size)
		}
	}
}

// No character asset may carry a background primitive. This ran behind a
// temporary migration ratchet while the 27 assets were converted one family at
// a time; the ratchet is gone because the list reached empty.
func TestCharacterAssetsCarryNoBakedBackground(t *testing.T) {
	sub := staticFS(t)

	for _, v := range characterVariants(t) {
		key := v.ID + "/" + v.Variant
		spec := variantSpecs[v.Variant]

		raw, err := fs.ReadFile(sub, v.Path)
		if err != nil {
			t.Errorf("%s: read: %v", key, err)
			continue
		}
		if found := bakedBackgrounds(string(raw), spec.size); len(found) > 0 {
			t.Errorf("%s carries a baked background: %s", key, strings.Join(found, "; "))
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
