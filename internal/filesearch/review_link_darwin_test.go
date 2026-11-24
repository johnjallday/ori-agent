//go:build darwin

package filesearch

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Make searchable, started from a linked folder's row in Files (merge-tree-into-
// files PR 2, task 2.1): the page names a workspace and one of its links; the
// host resolves the folder from that link and observes it now.

type storeCounts struct{ sources, links int }

func countStores(t *testing.T, f *fixture) storeCounts {
	t.Helper()
	var c storeCounts
	must(t, f.DB.QueryRow(`SELECT COUNT(*) FROM filename_sources`).Scan(&c.sources))
	must(t, f.DB.QueryRow(`SELECT COUNT(*) FROM filename_workspace_links`).Scan(&c.links))
	return c
}

func workspaceRefs(t *testing.T, f *fixture, id string) string {
	t.Helper()
	var refs string
	must(t, f.DB.QueryRow(`SELECT directory_references_json FROM workspaces WHERE id=?`, id).Scan(&refs))
	return refs
}

func TestReviewLinkThenCancelChangesNothingAndConfirmConnects(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	legacyLink(t, f, "local", "one", f.docs)
	refs := workspaceRefs(t, f, "one")

	r, e := f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	if r.Label != "Documents" || r.Source != "" || len(r.Matches) != 1 || r.Matches[0].ID != "one" || r.Matches[0].Link != "ref-one" {
		t.Fatalf("review: %+v", r)
	}
	// An older link without a recorded identity asks to be verified.
	if !r.Matches[0].Verify || r.Matches[0].Connected {
		t.Fatalf("older link not offered for verification: %+v", r.Matches[0])
	}
	// Cancel is the absence of Connect: neither store changed.
	if c := countStores(t, f); c != (storeCounts{}) {
		t.Fatalf("review wrote to the index stores: %+v", c)
	}
	if workspaceRefs(t, f, "one") != refs {
		t.Fatal("review changed the workspace's links")
	}
	if _, e = f.Connect(t.Context(), "local", r.ID, "one", "ref-one", false); !errors.Is(e, ErrChoice) {
		t.Fatal("connected an unverified older link", e)
	}
	v, e := f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true)
	must(t, e)
	f.await(t, "ready")
	ss, e := f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].ID != v.Source || ss[0].Workspace == nil || ss[0].Workspace.ID != "one" || ss[0].Workspace.Link != "ref-one" {
		t.Fatalf("not searchable through its link: %+v", ss)
	}
	if workspaceRefs(t, f, "one") != refs {
		t.Fatal("consent changed the workspace's links")
	}
	// Once recorded, the link reviews again without asking to verify.
	r, e = f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	if r.Source != v.Source || r.Matches[0].Verify || !r.Matches[0].Connected {
		t.Fatalf("recorded link review: %+v", r)
	}
}

func TestReviewLinkIsOwnerBound(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	_, err := f.DB.Exec(`INSERT INTO users(id,created_at,updated_at) VALUES('other-owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	must(t, err)
	legacyLink(t, f, "local", "mine", f.docs)
	legacyLink(t, f, "other-owner", "theirs", f.docs)
	for _, c := range []struct{ owner, workspace, link string }{
		{"local", "theirs", "ref-theirs"},   // another owner's workspace
		{"other-owner", "mine", "ref-mine"}, // and the other way round
		{"local", "mine", "ref-theirs"},     // a link of another workspace
		{"local", "mine", "missing-link"},   // no such link
		{"local", "missing", "ref-missing"}, // no such workspace
	} {
		if _, e := f.ReviewLink(t.Context(), c.owner, c.workspace, c.link); !errors.Is(e, ErrMissing) {
			t.Errorf("%+v: %v", c, e)
		}
	}
	if _, e := f.ReviewLink(t.Context(), "", "mine", "ref-mine"); !errors.Is(e, ErrRequest) {
		t.Error("anonymous review", e)
	}
	// The live workspace store has the last word on the link.
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return false }
	if _, e := f.ReviewLink(t.Context(), "local", "mine", "ref-mine"); !errors.Is(e, ErrMissing) {
		t.Error("link the workspace store does not confirm", e)
	}
	if c := countStores(t, f); c != (storeCounts{}) {
		t.Fatalf("refused reviews wrote: %+v", c)
	}
}

func TestReviewLinkConfirmationsConverge(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	legacyLink(t, f, "local", "one", f.docs)

	// Two tabs: the later review replaces the earlier one. The earlier tab's
	// confirmation is refused and the later one connects; one source, one link.
	first, e := f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	second, e := f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	if _, e = f.Connect(t.Context(), "local", first.ID, "one", "ref-one", true); !errors.Is(e, ErrChoice) {
		t.Fatal("stale tab confirmed", e)
	}
	// A double click, and a replay of the same confirmation, land on one source.
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i := range ids {
		wg.Go(func() {
			v, e := f.Connect(t.Context(), "local", second.ID, "one", "ref-one", true)
			if e != nil {
				t.Errorf("confirmation %d: %v", i, e)
				return
			}
			ids[i] = v.Source
		})
	}
	wg.Wait()
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("concurrent confirmations diverged: %v", ids)
	}
	v, e := f.Connect(t.Context(), "local", second.ID, "one", "ref-one", true)
	must(t, e)
	if v.Source != ids[0] {
		t.Fatal("replayed confirmation made another source")
	}
	if c := countStores(t, f); c != (storeCounts{sources: 1, links: 1}) {
		t.Fatalf("stores after converging: %+v", c)
	}
}

func TestReviewLinkForAFolderSearchableThroughAnotherWorkspace(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	legacyLink(t, f, "local", "one", f.docs)
	legacyLink(t, f, "local", "two", f.docs)
	r, e := f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	v, e := f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true)
	must(t, e)
	// The second workspace's row sees that the folder is already searchable
	// through the first; connecting it there would take the index away.
	r, e = f.ReviewLink(t.Context(), "local", "two", "ref-two")
	must(t, e)
	if r.Source != v.Source || len(r.Matches) != 2 || !r.Matches[0].Connected || r.Matches[1].Connected {
		t.Fatalf("review of the second link: %+v", r)
	}
	if _, e = f.Connect(t.Context(), "local", r.ID, "two", "ref-two", true); !errors.Is(e, ErrChoice) {
		t.Fatal("moved the index to another workspace", e)
	}
	ss, e := f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 1 || ss[0].Workspace == nil || ss[0].Workspace.ID != "one" {
		t.Fatalf("connection changed: %+v", ss)
	}
}

func TestReviewLinkRefusesAMissingOrReplacedFolder(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	legacyLink(t, f, "local", "one", f.docs)
	r, e := f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	v, e := f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true)
	must(t, e)
	f.await(t, "ready")
	// Removed from search, the link keeps the identity it recorded.
	must(t, f.Remove(t.Context(), "local", v.Source))
	must(t, os.Rename(f.docs, f.docs+"-moved"))
	if _, e = f.ReviewLink(t.Context(), "local", "one", "ref-one"); !errors.Is(e, ErrLink) {
		t.Fatal("reviewed a missing folder", e)
	}
	must(t, os.Mkdir(f.docs, 0700))
	if _, e = f.ReviewLink(t.Context(), "local", "one", "ref-one"); !errors.Is(e, ErrLink) {
		t.Fatal("a replacement folder inherited the link", e)
	}
	must(t, os.Remove(f.docs))
	must(t, os.Rename(f.docs+"-moved", f.docs))
	if _, e = f.ReviewLink(t.Context(), "local", "one", "ref-one"); e != nil {
		t.Fatal("the original folder, back in place, cannot be reviewed", e)
	}
	// A link into a protected folder is refused by policy, not by identity.
	protected := filepath.Join(f.home, "Workspaces", "inside")
	must(t, os.MkdirAll(protected, 0700))
	legacyLink(t, f, "local", "guarded", protected)
	if _, e = f.ReviewLink(t.Context(), "local", "guarded", "ref-guarded"); !errors.Is(e, ErrRoot) {
		t.Fatal("protected folder reviewed", e)
	}
}

func TestIndexingFailureAfterConsentKeepsTheConsent(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	locked := filepath.Join(f.docs, "locked")
	must(t, os.Mkdir(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	legacyLink(t, f, "local", "one", f.docs)
	r, e := f.ReviewLink(t.Context(), "local", "one", "ref-one")
	must(t, e)
	v, e := f.Connect(t.Context(), "local", r.ID, "one", "ref-one", true)
	must(t, e)
	// The scan could not read everything: the folder is partly indexed, says
	// why, and stays searchable through its link. Nothing is rolled back.
	got := f.await(t, "partial")
	if got.ID != v.Source || got.Reason == "" || got.Workspace == nil || got.Workspace.Link != "ref-one" {
		t.Fatalf("partial index after consent: %+v", got)
	}
	if c := countStores(t, f); c != (storeCounts{sources: 1, links: 1}) {
		t.Fatalf("consent rolled back: %+v", c)
	}
}
