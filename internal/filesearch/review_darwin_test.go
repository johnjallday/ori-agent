//go:build darwin

package filesearch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCustomReviewClassifiesPhysicalKnownFolderNotBasename(t *testing.T) {
	f := setup(t)
	for _, kind := range []string{"downloads", "custom"} {
		path := filepath.Join(f.home, "Downloads")
		if kind == "custom" {
			path = filepath.Join(f.home, "Project", "Downloads")
		}
		must(t, os.MkdirAll(path, 0700))
		f.pick = path
		intent, e := f.Begin("local", "custom")
		must(t, e)
		r, e := f.Review(t.Context(), "local", intent)
		must(t, e)
		if r.Kind != kind || r.Label != "Downloads" {
			t.Fatalf("review %+v, want %s", r, kind)
		}
	}
}

func TestReviewedFolderRequiresSameOwnerPolicyAndOriginalDirectory(t *testing.T) {
	for _, change := range []string{"cancel-picker", "another-selection", "another-owner", "policy", "replacement", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			f.pick = f.docs
			if change == "cancel-picker" {
				f.pick = ""
			}
			id, e := f.Begin("local", "custom")
			must(t, e)
			r, e := f.Review(t.Context(), "local", id)
			must(t, e)
			if change == "cancel-picker" {
				if r != nil {
					t.Fatal("cancel returned review")
				}
			} else {
				if r == nil || r.Label != "Documents" || r.ID == id {
					t.Fatal("expected distinct review")
				}
				owner := "local"
				switch change {
				case "another-selection":
					_, e = f.Begin(owner, "downloads")
					must(t, e)
				case "another-owner":
					owner = "other"
				case "policy":
					f.Policy = func() Policy { return Policy{Home: f.home, Protected: []string{f.docs}} }
				case "unreadable":
					if os.Geteuid() == 0 {
						t.Skip("root bypasses mode bits")
					}
					must(t, os.Chmod(f.docs, 0))
					t.Cleanup(func() { _ = os.Chmod(f.docs, 0700) })
				case "replacement":
					must(t, os.Rename(f.docs, filepath.Join(f.home, "original")))
					must(t, os.Mkdir(f.docs, 0700))
				}
				if _, e := f.GrantReviewed(t.Context(), owner, r.ID); !errors.Is(e, ErrChoice) && !errors.Is(e, ErrRoot) {
					t.Fatal("invalidated review authorized search", e)
				}
			}
			for _, table := range []string{"filename_entries", "filename_sources"} {
				var n int
				err := f.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
				if err != nil || n != 0 {
					t.Fatalf("%s has %d rows: %v", table, n, err)
				}
			}
		})
	}
}
