//go:build darwin

package filesearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type fixture struct {
	*Service
	home, docs, data string
	db               *database.DB
	pick             string
	gate             *resetstate.WorkGate
}

func setup(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	f := &fixture{home: filepath.Join(base, "home"), data: filepath.Join(base, "app"), gate: new(resetstate.WorkGate)}
	f.docs = filepath.Join(f.home, "Documents")
	must(t, os.MkdirAll(f.docs, 0700))
	f.db, err = database.Open(t.Context(), &database.Config{Path: filepath.Join(f.data, "profile.db")})
	must(t, err)
	t.Cleanup(func() { _ = f.db.Close() })
	f.Service, err = New(t.Context(), Options{DB: f.db.DB, Gate: f.gate, Policy: func() Policy {
		return Policy{Home: f.home, Protected: []string{f.data, filepath.Join(f.home, "Workspaces")}}
	}, Pick: func(context.Context) (string, bool, error) { return f.pick, f.pick != "", nil }})
	must(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) choose(t *testing.T, kind string) (*Source, error) {
	t.Helper()
	intent, e := f.Begin("local", kind)
	must(t, e)
	return f.Select(t.Context(), "local", intent)
}
func (f *fixture) await(t *testing.T, state string) Source {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		ss, e := f.Sources(t.Context(), "local")
		must(t, e)
		for _, s := range ss {
			if s.State == state {
				return s
			}
		}
		time.Sleep(time.Millisecond * 5)
	}
	ss, _ := f.Sources(t.Context(), "local")
	t.Fatalf("did not reach %s: %+v", state, ss)
	return Source{}
}
func TestOnlyBoundExplicitChoicesCanGrant(t *testing.T) {
	f := setup(t)
	v, e := f.choose(t, "custom")
	if e != nil || v != nil {
		t.Fatal("cancel minted a grant", e)
	}
	if _, e = f.choose(t, "downloads"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("absent Downloads:", e)
	}
	if _, e = f.Begin("local", f.docs); e != ErrRequest {
		t.Fatal("path choice accepted")
	}
	id, e := f.Begin("local", "documents")
	must(t, e)
	if _, e = f.Select(t.Context(), "other", id); e != ErrChoice {
		t.Fatal("owner mismatch accepted")
	}
	v, e = f.Select(t.Context(), "local", id)
	must(t, e)
	if _, e = f.Select(t.Context(), "local", id); e != ErrChoice {
		t.Fatal("replay accepted")
	}
	again, e := f.choose(t, "documents")
	must(t, e)
	if v.ID != again.ID {
		t.Fatal("duplicate grant")
	}
	f.await(t, "ready")
	ss, e := f.Sources(t.Context(), "other")
	must(t, e)
	if len(ss) != 0 {
		t.Fatal("cross-owner read")
	}
	must(t, f.Remove(t.Context(), "local", v.ID))
	if _, e = f.Select(t.Context(), "local", id); e != ErrChoice {
		t.Fatal("revived grant")
	}
	next, e := f.choose(t, "documents")
	must(t, e)
	if next.ID == v.ID {
		t.Fatal("reused retired ID")
	}
}
func TestUnsafeAndOverlappingRoots(t *testing.T) {
	f := setup(t)
	link := filepath.Join(f.home, "redirect")
	must(t, os.Symlink(f.docs, link))
	app := filepath.Join(f.home, "App.app", "Contents")
	private := filepath.Join(f.home, "Workspaces", "private")
	for _, p := range []string{private, app} {
		must(t, os.MkdirAll(p, 0700))
	}
	for _, p := range []string{"/", f.home, f.data, private, link, app, "relative", "/System", "/Volumes/Test"} {
		f.pick = p
		if _, e := f.choose(t, "custom"); e == nil {
			t.Fatalf("unsafe root accepted: %s", filepath.Base(p))
		}
	}
	_, e := f.choose(t, "documents")
	must(t, e)
	f.pick = filepath.Join(f.docs, "sub")
	must(t, os.Mkdir(f.pick, 0700))
	if _, e := f.choose(t, "custom"); e != ErrOverlap {
		t.Fatal("overlap:", e)
	}
}
func TestFirstBatchRemovalCannotRepublish(t *testing.T) {
	f := setup(t)
	for i := range 70 {
		must(t, os.WriteFile(filepath.Join(f.docs, fmt.Sprintf("Harbor-%d.txt", i)), nil, 0600))
	}
	published := make(chan struct{})
	f.afterPublish = func(ctx context.Context) { close(published); <-ctx.Done() }
	v, e := f.choose(t, "documents")
	must(t, e)
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("first batch missing")
	}
	page, e := f.Query(t.Context(), "local", "HARBOR", "", 0)
	must(t, e)
	if len(page.Hits) != 1 || page.Sources[0].State != "mapping" {
		t.Fatal("first batch not visible", page)
	}
	if e := f.gate.TryFence(t.Context()); e != resetstate.ErrWorkActive {
		t.Fatal("work not admitted")
	}
	intent, e := f.Begin("local", "custom")
	must(t, e)
	must(t, f.Remove(t.Context(), "local", v.ID))
	if _, e = f.Select(t.Context(), "local", intent); e != ErrChoice {
		t.Fatal("stale intent survived removal")
	}
	f.wg.Wait()
	page, e = f.Query(t.Context(), "local", "Harbor", "", 0)
	must(t, e)
	if len(page.Hits) != 0 || len(page.Sources) != 0 {
		t.Fatal("removed search still visible")
	}
	var n int
	must(t, f.db.QueryRow(`SELECT count(*) FROM filename_entries`).Scan(&n))
	if n != 0 {
		t.Fatal("orphaned rows")
	}
	must(t, f.gate.TryFence(t.Context()))
}
func TestScanStaysWithinScopeAndReopensWithoutCatchUp(t *testing.T) {
	f := setup(t)
	for _, p := range []string{"a/Harbor.txt", "b/Harbor.txt", "hidden.app/x.txt", ".tools/no.txt", "node_modules/mod.txt"} {
		path := filepath.Join(f.docs, p)
		must(t, os.MkdirAll(filepath.Dir(path), 0700))
		must(t, os.WriteFile(path, []byte("no body read needed"), 0600))
	}
	must(t, os.Symlink(f.data, filepath.Join(f.docs, "outside")))
	v, e := f.choose(t, "documents")
	must(t, e)
	f.await(t, "ready")
	page, e := f.Query(t.Context(), "local", "harBor", "", 1)
	must(t, e)
	if len(page.Hits) != 1 || page.Sources[0].Count != 2 || page.Next == "" {
		t.Fatal(page)
	}
	second, e := f.Query(t.Context(), "local", "harBor", page.Next, 1)
	must(t, e)
	if len(second.Hits) != 1 || page.Hits[0].Parent == second.Hits[0].Parent {
		t.Fatal(second)
	}
	raw, e := json.Marshal(page)
	must(t, e)
	if strings.Contains(string(raw), f.home) {
		t.Fatal("private root serialized")
	}
	f.Close()
	must(t, os.WriteFile(filepath.Join(f.docs, "new.txt"), nil, 0600))
	f.Service, e = New(t.Context(), f.Options)
	must(t, e)
	still, e := f.Query(t.Context(), "local", "new", "", 0)
	must(t, e)
	if len(still.Hits) != 0 || still.Sources[0].Count != 2 {
		t.Fatal("silent rescan", still)
	}
	must(t, f.Refresh(t.Context(), "local", v.ID, 1))
	ref := f.await(t, "ready")
	if ref.Count != 3 || ref.Generation != 2 {
		t.Fatal(ref)
	}
	if _, e = f.Query(t.Context(), "local", "harBor", page.Next, 1); e != ErrMissing {
		t.Fatal("stale cursor accepted")
	}
	must(t, os.Rename(f.docs, f.docs+"-old"))
	must(t, os.Mkdir(f.docs, 0700))
	lost, e := f.Query(t.Context(), "local", "", "", 50)
	must(t, e)
	if len(lost.Hits) != 0 || lost.Sources[0].State != "unavailable" {
		t.Fatal("replaced root leaked hits", lost)
	}
}
func TestSourceCapacityDoesNotBlockReselectingExistingRoot(t *testing.T) {
	f := setup(t)
	var first string
	for i := range maxSources + 1 {
		f.pick = filepath.Join(f.home, fmt.Sprintf("Folder-%d", i))
		must(t, os.Mkdir(f.pick, 0700))
		v, e := f.choose(t, "custom")
		if i == maxSources {
			if e != ErrLimit || v != nil {
				t.Fatal("source limit not enforced")
			}
			break
		}
		must(t, e)
		if i == 0 {
			first = v.ID
		}
	}
	f.pick = filepath.Join(f.home, "Folder-0")
	v, e := f.choose(t, "custom")
	must(t, e)
	if v.ID != first {
		t.Fatal("reselection created another source")
	}
}

func TestDeniedFolderAndTraversalLimit(t *testing.T) {
	f := setup(t)
	dir := filepath.Join(f.docs, "inaccessible")
	must(t, os.Mkdir(dir, 0))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	p := f.docs
	for range 34 {
		p = filepath.Join(p, "deep")
		must(t, os.Mkdir(p, 0700))
	}
	_, e := f.choose(t, "documents")
	must(t, e)
	if v := f.await(t, "partial"); v.Reason == "" {
		t.Fatal("partial without reason")
	}
	for _, term := range []string{strings.Repeat("x", 129), string([]byte{0xff})} {
		if _, e = f.Query(t.Context(), "local", term, "", 0); e != ErrRequest {
			t.Fatal("unbounded term")
		}
	}
}
