//go:build darwin

package filesearch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func legacyLink(t *testing.T, f *fixture, owner, id, path string) {
	t.Helper()
	now := time.Now().UTC()
	refs, e := json.Marshal([]map[string]any{{"workspace_id": id, "id": "ref-" + id, "path": path, "created_at": now}, {"workspace_id": id, "id": "other-" + id, "path": filepath.Join(f.home, "Untouched")}})
	must(t, e)
	_, e = f.DB.Exec(`INSERT INTO workspaces(id,name,folder_slug,owner_user_id,created_at,updated_at,directory_references_json) VALUES(?,?,?, ?,?,?,?)`, id, "Archive", id, owner, now, now, string(refs))
	must(t, e)
}
func reviewPick(t *testing.T, f *fixture, path string) *FolderReview {
	t.Helper()
	f.pick = path
	intent, e := f.Begin("local", "custom")
	must(t, e)
	r, e := f.Review(t.Context(), "local", intent)
	must(t, e)
	return r
}
func TestReviewAndConnectUsesOneOwnedCurrentLink(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(owner, path string, m WorkspaceMatch) bool { return m.ID != "missing-sidecar" }
	other := filepath.Join(f.home, "Project", "Documents")
	must(t, os.MkdirAll(other, 0700))
	for _, id := range []string{"one", "two", "missing-sidecar"} {
		legacyLink(t, f, "local", id, f.docs)
	}
	_, err := f.DB.Exec(`INSERT INTO users(id,created_at,updated_at) VALUES('other-owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	must(t, err)
	legacyLink(t, f, "other-owner", "foreign", f.docs)
	legacyLink(t, f, "local", "same-name", other)
	// Upgrade from an independent grant. Listing does not observe legacy links or
	// attach one; its first completed generation remains intact through connect.
	source, e := f.choose(t, "documents")
	must(t, e)
	before := f.await(t, "ready")
	ss, e := f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].Workspace != nil {
		t.Fatal("implicit pairing")
	}
	r, e := f.ReviewSource(t.Context(), "local", source.ID)
	must(t, e)
	if len(r.Matches) != 2 || r.Source != source.ID || r.Matches[1].ID != "two" {
		t.Fatalf("incorrect choices: %+v", r)
	}
	if _, e = f.Connect(t.Context(), "local", r.ID, "foreign", "ref-foreign", true); !errors.Is(e, ErrChoice) {
		t.Fatal(e)
	}
	if _, e = f.Connect(t.Context(), "local", r.ID, "two", "ref-two", false); !errors.Is(e, ErrChoice) {
		t.Fatal("adopted historical link without explicit attestation", e)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			v, e := f.Connect(t.Context(), "local", r.ID, "two", "ref-two", true)
			if e != nil || v.Source != source.ID {
				t.Errorf("concurrent confirmation %v %v", v, e)
			}
		})
	}
	wg.Wait()
	ss, e = f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].Workspace == nil || ss[0].Workspace.ID != "two" || ss[0].Generation != before.Generation || ss[0].Count != before.Count {
		t.Fatalf("grant or index changed: %+v", ss)
	}
	r = reviewPick(t, f, f.docs)
	if !r.Matches[1].Connected {
		t.Fatal("opposite entry lost connection")
	}
	if _, e = f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true); !errors.Is(e, ErrChoice) {
		t.Fatal("stole connected grant", e)
	}
	must(t, f.Remove(t.Context(), "local", source.ID))
	r = reviewPick(t, f, f.docs)
	if r.Matches[1].Verify || r.Matches[1].Connected {
		t.Fatal("removal discarded link evidence or restored grant")
	}
	v, e := f.Connect(t.Context(), "local", r.ID, "two", "ref-two", false)
	must(t, e)
	if v.Source == source.ID {
		t.Fatal("reused revoked source")
	}
	_, e = f.DB.Exec(`UPDATE workspaces SET name='Renamed',folder_slug='renamed' WHERE id='two'`)
	must(t, e)
	ss, e = f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].Workspace == nil || ss[0].Workspace.Slug != "renamed" || ss[0].Workspace.ID != "two" || ss[0].ID != v.Source {
		t.Fatal("workspace rename broke navigation", ss)
	}
	_, e = f.DB.Exec(`UPDATE workspaces SET directory_references_json='[]' WHERE id='two'`)
	must(t, e)
	ss, e = f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].Workspace != nil {
		t.Fatal("unlinked workspace still associated")
	}
}

// A connected folder that goes missing still names the workspace link it was
// connected to, so that link's row in Files can say the folder is unavailable.
// Nothing about the folder itself is observed for that, and nothing is
// regranted when it comes back: the same source simply reads as current again.
func TestUnavailableSourceKeepsItsSavedConnection(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	legacyLink(t, f, "local", "one", f.docs)
	r := reviewPick(t, f, f.docs)
	v, e := f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true)
	must(t, e)
	f.await(t, "ready")
	must(t, os.Rename(f.docs, f.docs+"-moved"))
	ss, e := f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].State != "unavailable" || ss[0].Count != 0 {
		t.Fatalf("missing folder not reported as unavailable: %+v", ss)
	}
	if ss[0].Workspace == nil || ss[0].Workspace.ID != "one" || ss[0].Workspace.Link != "ref-one" || !ss[0].Workspace.Connected || ss[0].Workspace.Verify {
		t.Fatalf("unavailable folder lost its saved connection: %+v", ss[0].Workspace)
	}
	// Another workspace linking the same path was never connected: no match.
	legacyLink(t, f, "local", "two", f.docs)
	ss, e = f.Sources(t.Context(), "local")
	must(t, e)
	if ss[0].Workspace == nil || ss[0].Workspace.ID != "one" {
		t.Fatalf("listing enumerated another candidate: %+v", ss[0].Workspace)
	}
	must(t, os.Rename(f.docs+"-moved", f.docs))
	ss, e = f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].ID != v.Source || ss[0].State == "unavailable" || ss[0].Workspace == nil || ss[0].Workspace.ID != "one" {
		t.Fatalf("restored folder: %+v", ss)
	}
}

func TestWorkspaceConnectRefusesStaleReviewAndRollsBackFailedGrant(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	legacyLink(t, f, "local", "one", f.docs)
	r := reviewPick(t, f, f.docs)
	if len(r.Matches) != 1 {
		t.Fatalf("missing candidate: %+v", r)
	}
	_, e := f.DB.Exec(`CREATE TRIGGER reject_link BEFORE INSERT ON filename_workspace_links BEGIN SELECT RAISE(ABORT,'injected link failure'); END`)
	must(t, e)
	if _, e = f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	ss, e := f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 0 {
		t.Fatal("partial grant survived transaction")
	}
	_, e = f.DB.Exec(`DROP TRIGGER reject_link`)
	must(t, e)
	v, e := f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true)
	must(t, e)
	must(t, f.Remove(t.Context(), "local", v.Source))
	r = reviewPick(t, f, f.docs)
	must(t, os.Rename(f.docs, f.docs+"-moved"))
	must(t, os.Mkdir(f.docs, 0700))
	if _, e = f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true); !errors.Is(e, ErrRoot) {
		t.Fatal("replacement inherited stale action", e)
	}
	r = reviewPick(t, f, f.docs)
	if len(r.Matches) != 0 {
		t.Fatal("replacement matched original link")
	}
}
