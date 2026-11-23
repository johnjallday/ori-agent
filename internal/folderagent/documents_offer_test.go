//go:build unix

package folderagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestDocumentsOfferOrdersMarkedMembersWithoutReadingFiles(t *testing.T) {
	root := build(t, "Work/Thesis/main.tex", "Other/Thesis/main.tex", "Alpha/go.mod", "Beta/go.mod", "Recent/main.tex", "Vault/.obsidian/", "Vault/main.tex", "My Notes/Private/main.tex", "sk-abcdefgh/main.tex", "Ignored State/main.tex", "Loose/hello.tex")
	notebook := t.TempDir()
	if err := os.MkdirAll(filepath.Join(notebook, "Projects", "Recent"), 0750); err != nil {
		t.Fatal(err)
	}
	// Normalize the direct-file times so tie order differs from walk order.
	for _, f := range []string{"Work/Thesis/main.tex", "Other/Thesis/main.tex", "Alpha/go.mod", "Beta/go.mod", "Recent/main.tex"} {
		p := filepath.Join(root, f)
		stamp := time.Unix(100, 0)
		if f == "Work/Thesis/main.tex" {
			stamp = time.Unix(200, 0)
		}
		if f == "Recent/main.tex" {
			stamp = time.Unix(300, 0)
		}
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	h, id := guidedLook(t, root)
	h.svc.deps.DocumentsScope = func(context.Context, string) (DocumentsScope, error) {
		return DocumentsScope{Notebook: notebook, Writable: true, Protected: []string{filepath.Join(root, "Ignored State")}}, nil
	}
	offer := func() *DocumentsOffer {
		t.Helper()
		o, err := h.svc.DocumentsOffer(t.Context(), "u1", "hq", "attempt", id)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	o := offer()
	if o == nil || len(o.Projects) != 4 {
		t.Fatalf("offer: %+v", o)
	}
	for i, want := range []string{"Work/Thesis", "Alpha", "Beta", "Other/Thesis"} {
		c := o.Projects[i]
		if c.Location != "Documents/"+want || len(c.Ref) != 64 {
			t.Fatal(c)
		}
	}
	// Both a replaced parent and an unwalked member cease to be evidence; no
	// loose filename or finished-report prose can fill their places.
	if err := os.Rename(filepath.Join(root, "Work"), filepath.Join(root, "Moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Moved"), filepath.Join(root, "Work")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.deps.Store.Update(id, func(r *Record) error {
		for i := range r.Map.Folders {
			if r.Map.Folders[i].Path == "Beta" {
				r.Map.Folders[i].State = foldermap.StateUnopened
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o = offer()
	if o == nil || len(o.Projects) != 3 || o.Projects[2].Name != "Recent" {
		t.Fatalf("partial offer: %+v", o)
	}
}
