//go:build darwin

package filesearch

import (
	"context"
	"errors"
	"github.com/johnjallday/ori-agent/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestDispatchPinsExactSavedFileAndRefusesChangedAuthority(t *testing.T) {
	f := setup(t)
	path := filepath.Join(f.docs, "Harbor.txt")
	must(t, os.WriteFile(path, []byte("data"), 0600))
	script := filepath.Join(f.docs, "launch.txt")
	must(t, os.WriteFile(script, nil, 0700))
	v, e := f.choose(t, "documents")
	must(t, e)
	f.await(t, "ready")
	get := func(name string) Hit {
		page, e := f.Query(t.Context(), "local", name, "", 1)
		must(t, e)
		if len(page.Hits) != 1 {
			t.Fatal("missing hit")
		}
		return page.Hits[0]
	}
	h := get("Harbor")
	run := func() error { return f.Act(t.Context(), "local", h.ID, h.Generation, false) }
	count := 0
	wantViewer := "TextEdit"
	f.dispatch = func(_ context.Context, viewer, pin string) error {
		count++
		if viewer != wantViewer {
			t.Fatalf("wrong reader %s", viewer)
		}
		a, e := os.Stat(pin)
		must(t, e)
		b, e := os.Stat(path)
		must(t, e)
		if !os.SameFile(a, b) {
			t.Fatal("wrong file reference")
		}
		return nil
	}
	t.Setenv(platform.NoDesktopOpenEnv, "1")
	if run() != ErrUnavailable {
		t.Fatal("disabled desktop claimed success")
	}
	t.Setenv(platform.NoDesktopOpenEnv, "0")
	must(t, run())
	wantViewer = ""
	must(t, f.Act(t.Context(), "local", h.ID, h.Generation, true))
	if e = f.Act(t.Context(), "other", h.ID, h.Generation, true); !errors.Is(e, ErrMissing) {
		t.Fatal("cross-owner action")
	}
	sc := get("launch")
	if sc.Reader != "" || f.Act(t.Context(), "local", sc.ID, sc.Generation, false) != ErrUnavailable {
		t.Fatal("executable opened")
	}
	must(t, os.Rename(path, path+"-old"))
	must(t, os.WriteFile(path, nil, 0600))
	if run() != ErrMissing {
		t.Fatal("substitution opened")
	}
	must(t, f.Remove(t.Context(), "local", v.ID))
	if run() != ErrMissing || count != 2 {
		t.Fatal("unexpected dispatch after remove", count)
	}
}
