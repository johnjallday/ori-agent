package projectlibrary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The folder agent's Home stage connects a fed folder as a root through
// PickFromPortfolio and then commits scoped listings, one after another, for
// the scopes the library itself issued, walking down to songs at any depth
// (folder-agent-evolution FR37). These tests pin the library behaviour that
// walk relies on.

func deepMusicRoot(t *testing.T, paths ...string) (*Roots, Scope, Root) {
	t.Helper()
	r, scope, _, _ := rootTestService(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "Label 2026")
	for _, rel := range paths {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := DirectoryIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	resolver := portfolioTestResolver(func(context.Context, string, string, string) (string, string, error) {
		return root, identity, nil
	})
	pick, err := r.PickFromPortfolio(context.Background(), scope, "folder-agent-ws", resolver)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := r.library.Read(scope)
	review, err := r.Review(scope, pick, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	connected, _, err := r.Commit(scope, review.Token, "connect-deep")
	if err != nil {
		t.Fatal(err)
	}
	return r, scope, connected
}

// walkScopes scans the root, then repeatedly the deepest issued scope that
// leads to each wanted folder, as the folder agent's NextScopes does.
func walkScopes(t *testing.T, r *Roots, scope Scope, root Root, wanted []string, budget int) (scans int) {
	t.Helper()
	doc, _ := r.library.Read(scope)
	review, err := r.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, "root-scan"); err != nil {
		t.Fatal(err)
	}
	scanned := map[string]bool{}
	for scans < budget {
		doc, _ = r.library.Read(scope)
		page, err := r.ListScanScopes(scope, root.ID, doc.Revision, 0)
		if err != nil {
			t.Fatal(err)
		}
		byFolder := map[string]string{}
		for _, row := range page.Rows {
			byFolder[filepath.ToSlash(row.RelativeFolder)] = row.ID
		}
		var next []string
		for _, want := range wanted {
			for at := want; at != "." && at != ""; at = filepath.ToSlash(filepath.Dir(at)) {
				if id, ok := byFolder[at]; ok {
					if !scanned[at] {
						next = append(next, id)
						scanned[at] = true
					}
					break
				}
			}
		}
		if len(next) == 0 {
			return scans
		}
		for _, id := range next {
			if scans >= budget {
				return scans
			}
			doc, _ = r.library.Read(scope)
			review, err := r.ReviewScopedScan(scope, root.ID, id, doc.Revision)
			if err != nil {
				t.Fatal(err)
			}
			scans++
			if _, _, err := r.CommitScopedScan(context.Background(), scope, root.ID, id, review.Token, fmt.Sprintf("scope-%d", scans)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return scans
}

func listedFolders(t *testing.T, r *Roots, scope Scope) []string {
	t.Helper()
	doc, _ := r.library.Read(scope)
	var out []string
	for _, entry := range doc.Entries {
		for _, observation := range entry.Observations {
			out = append(out, filepath.ToSlash(observation.RelativeFolder))
		}
	}
	return out
}

func TestScopedScanChain_ReachesSongsSixFoldersDown(t *testing.T) {
	r, scope, root := deepMusicRoot(t,
		"2025/Artist A/Album X/Disc 1/Song One/Song One.rpp",
		"2026/Artist B/Album Y/Disc 2/Song Two/Song Two.rpp",
	)
	wanted := []string{"2025/Artist A/Album X/Disc 1", "2026/Artist B/Album Y/Disc 2"}
	scans := walkScopes(t, r, scope, root, wanted, 48)
	listed := strings.Join(listedFolders(t, r, scope), ",")
	for _, song := range []string{"2025/Artist A/Album X/Disc 1/Song One", "2026/Artist B/Album Y/Disc 2/Song Two"} {
		if !strings.Contains(listed, song) {
			t.Fatalf("%s not listed after %d scoped scans: %s", song, scans, listed)
		}
	}
	if scans > 8 {
		t.Fatalf("the walk took %d scoped scans for two songs", scans)
	}
}

func TestScopedScanChain_BudgetLeavesFoldersUnreached(t *testing.T) {
	r, scope, root := deepMusicRoot(t,
		"2025/Artist A/Album X/Disc 1/Song One/Song One.rpp",
	)
	scans := walkScopes(t, r, scope, root, []string{"2025/Artist A/Album X/Disc 1"}, 1)
	if scans != 1 {
		t.Fatalf("scans = %d", scans)
	}
	if strings.Contains(strings.Join(listedFolders(t, r, scope), ","), "Song One") {
		t.Fatal("one scoped scan cannot reach a song six folders down")
	}
}
