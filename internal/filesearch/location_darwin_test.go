//go:build darwin

package filesearch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocationQueryScopesBeforePaging(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	var locations []string
	for i, parent := range []string{"archive", "incoming"} {
		f.pick = filepath.Join(f.home, parent, "Copies")
		must(t, os.MkdirAll(f.pick, 0700))
		for _, name := range []string{"a.pdf", "b.pdf"} {
			p := filepath.Join(f.pick, name)
			must(t, os.WriteFile(p, nil, 0600))
			stamp := time.Unix(int64(20-i), 0)
			must(t, os.Chtimes(p, stamp, stamp))
		}
		v, err := f.choose(t, "custom")
		must(t, err)
		f.wg.Wait()
		locations = append(locations, v.ID)
	}
	query := func(source, cursor string) (Page, error) {
		return f.QueryLocation(ctx, "local", ".PDF", source, cursor, 1)
	}
	all, err := query("", "")
	must(t, err)
	scoped, err := query(locations[1], "")
	must(t, err)
	if all.Hits[0].Source != locations[0] || scoped.Hits[0].Source != locations[1] || scoped.Next == "" {
		t.Fatal("location applied after pagination")
	}
	second, err := query(locations[1], scoped.Next)
	must(t, err)
	if len(second.Hits) != 1 || second.Hits[0].ID == scoped.Hits[0].ID || second.Next != "" {
		t.Fatal("unstable tied results")
	}
	for _, tc := range [][2]string{{locations[1], all.Next}, {"", scoped.Next}, {locations[0], scoped.Next}, {"Copies", ""}} {
		if _, err = query(tc[0], tc[1]); err != ErrMissing {
			t.Fatal("invalid scope/cursor accepted", err)
		}
	}
	if _, err = f.QueryLocation(ctx, "other", ".pdf", locations[1], "", 1); err != ErrMissing {
		t.Fatal("foreign scope accepted")
	}
	must(t, f.Refresh(ctx, "local", locations[1], 1))
	f.wg.Wait()
	if _, err = query(locations[1], scoped.Next); err != ErrMissing {
		t.Fatal("stale cursor accepted")
	}
	must(t, f.Remove(ctx, "local", locations[1]))
	if _, err = query(locations[1], ""); err != ErrMissing {
		t.Fatal("removed scope accepted")
	}
}
