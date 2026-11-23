package database

import (
	"path/filepath"
	"testing"
)

func TestSearchIndexIsDeviceLocalAppRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	ctx := t.Context()
	db, err := Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before, err := InspectReset(ctx, db.DB)
	if err != nil || len(before.Problems) > 0 {
		t.Fatalf("preflight: %v %v", before.Problems, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO filename_sources(id,owner,root,witness,generation,state) VALUES('a','local','synthetic','w',1,'ready'); INSERT INTO filename_entries(id,source,generation,relative,basename,folded,modified,witness,ancestors) VALUES('b','a',1,'Harbor.txt','Harbor.txt','harbor.txt',123,'w2','w')`)
	if err != nil {
		t.Fatal(err)
	}
	after, err := InspectReset(ctx, db.DB)
	if err != nil || len(after.Problems) > 0 {
		t.Fatalf("inspection: %v %v", after.Problems, err)
	}
	for name, n := range after.Counts {
		if n == nil || before.Counts[name] == nil {
			t.Fatal("missing inventory", name)
		}
		want := *before.Counts[name]
		if name == "filename_sources" || name == "filename_entries" {
			want++
		}
		if *n != want {
			t.Fatal("unexpected side effect", name)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	counts, err := ResetAppRecords(ctx, path)
	if err != nil || counts["filename_sources"] != 1 || counts["filename_entries"] != 1 {
		t.Fatal("reset failed to purge search", err)
	}
}
