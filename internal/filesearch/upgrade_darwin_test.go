//go:build darwin

package filesearch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dumpSearchStores writes out everything the filename index and the workspace
// links keep, in a fixed order, so two moments can be compared exactly.
func dumpSearchStores(t *testing.T, f *fixture) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id,owner,root,witness,generation,state,COALESCE(reason,''),COALESCE(mapped_at,0) FROM filename_sources ORDER BY id`,
		`SELECT owner,workspace_id,reference_id,witness,COALESCE(source,'') FROM filename_workspace_links ORDER BY owner,workspace_id,reference_id`,
		`SELECT source,generation,COUNT(*) FROM filename_entries GROUP BY source,generation ORDER BY source,generation`,
		`SELECT id,COALESCE(directory_references_json,'') FROM workspaces ORDER BY id`,
	} {
		rows, err := f.DB.Query(q)
		must(t, err)
		cols, err := rows.Columns()
		must(t, err)
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			must(t, rows.Scan(ptrs...))
			fmt.Fprintln(&b, values...)
		}
		must(t, rows.Err())
		_ = rows.Close()
		fmt.Fprintln(&b, "--")
	}
	return b.String()
}

func awaitAllSettled(t *testing.T, f *fixture) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		ss, err := f.Sources(t.Context(), "local")
		must(t, err)
		busy := false
		for _, s := range ss {
			busy = busy || s.State == "queued" || s.State == "mapping"
		}
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("indexing did not settle")
}

// Upgrading converts nothing (merge-tree-into-files PR 2, requirement 44): a
// search-only folder, a workspace link that was never made searchable, and a
// link made searchable all come back from a restart exactly as they were.
// Reading the list, as Files does on every visit, writes nothing either.
func TestRestartAndListingConvertNothing(t *testing.T) {
	f := setup(t)
	f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
	// Search-only: Documents, granted on its own.
	_, err := f.choose(t, "documents")
	must(t, err)
	// Workspace-only: a folder a workspace links to, never made searchable.
	plain := filepath.Join(f.home, "Projects", "Sketches")
	must(t, os.MkdirAll(plain, 0700))
	legacyLink(t, f, "local", "plain", plain)
	// Connected: another linked folder, made searchable through its link.
	albums := filepath.Join(f.home, "Albums")
	must(t, os.MkdirAll(albums, 0700))
	must(t, os.WriteFile(filepath.Join(albums, "track.wav"), []byte("x"), 0600))
	legacyLink(t, f, "local", "albums", albums)
	f.pick = albums
	r := reviewPick(t, f, albums)
	_, err = f.Connect(t.Context(), "local", r.ID, "albums", "ref-albums", true)
	must(t, err)
	awaitAllSettled(t, f)

	before := dumpSearchStores(t, f)
	listed, err := f.Sources(t.Context(), "local")
	must(t, err)
	if len(listed) != 2 {
		t.Fatalf("want a search-only and a connected folder, got %+v", listed)
	}
	for range 3 {
		_, err = f.Sources(t.Context(), "local")
		must(t, err)
	}
	if after := dumpSearchStores(t, f); after != before {
		t.Fatalf("listing changed the stores:\nbefore\n%s\nafter\n%s", before, after)
	}

	// Restart over the same profile.
	f.Close()
	restarted, err := New(t.Context(), f.Options)
	must(t, err)
	t.Cleanup(restarted.Close)
	again, err := restarted.Sources(t.Context(), "local")
	must(t, err)
	// Compared as Files receives it.
	was, err := json.Marshal(listed)
	must(t, err)
	now, err := json.Marshal(again)
	must(t, err)
	if string(was) != string(now) {
		t.Fatalf("a restart changed the list:\nbefore %s\nafter  %s", was, now)
	}
	if after := dumpSearchStores(t, f); after != before {
		t.Fatalf("a restart changed the stores:\nbefore\n%s\nafter\n%s", before, after)
	}
}
