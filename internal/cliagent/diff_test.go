package cliagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiffDetector_MTime_NewFile(t *testing.T) {
	dir := t.TempDir()
	d := NewDiffDetector()

	snap, err := d.Snapshot(dir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Create a new file
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	changes, err := d.Compare(snap, dir)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Path != "new.txt" {
		t.Errorf("expected path new.txt, got %s", changes[0].Path)
	}
	if changes[0].ChangeType != ChangeAdded {
		t.Errorf("expected added, got %s", changes[0].ChangeType)
	}
}

func TestDiffDetector_MTime_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(f, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDiffDetector()
	snap, err := d.Snapshot(dir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Modify the file (need to ensure mtime changes)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(f, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	changes, err := d.Compare(snap, dir)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].ChangeType != ChangeModified {
		t.Errorf("expected modified, got %s", changes[0].ChangeType)
	}
}

func TestDiffDetector_MTime_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "todelete.txt")
	if err := os.WriteFile(f, []byte("bye"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDiffDetector()
	snap, err := d.Snapshot(dir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}

	changes, err := d.Compare(snap, dir)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].ChangeType != ChangeDeleted {
		t.Errorf("expected deleted, got %s", changes[0].ChangeType)
	}
}

func TestDiffDetector_MTime_NoChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stable.txt"), []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDiffDetector()
	snap, err := d.Snapshot(dir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	changes, err := d.Compare(snap, dir)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestParseGitNameStatus(t *testing.T) {
	input := "M\tfile1.go\nA\tfile2.go\nD\tfile3.go\nR100\told.go\tnew.go\n"
	changes := parseGitNameStatus([]byte(input))

	if len(changes) != 4 {
		t.Fatalf("expected 4 changes, got %d", len(changes))
	}

	expected := []struct {
		path string
		ct   ChangeType
	}{
		{"file1.go", ChangeModified},
		{"file2.go", ChangeAdded},
		{"file3.go", ChangeDeleted},
		{"new.go", ChangeModified}, // Rename treated as modified
	}

	for i, e := range expected {
		if changes[i].Path != e.path {
			t.Errorf("[%d] expected path %s, got %s", i, e.path, changes[i].Path)
		}
		if changes[i].ChangeType != e.ct {
			t.Errorf("[%d] expected %s, got %s", i, e.ct, changes[i].ChangeType)
		}
	}
}

func TestEnrichWithNumstat(t *testing.T) {
	changes := []FileChange{
		{Path: "file1.go", ChangeType: ChangeModified},
		{Path: "file2.go", ChangeType: ChangeAdded},
	}

	numstat := "10\t3\tfile1.go\n25\t0\tfile2.go\n"
	enrichWithNumstat(changes, []byte(numstat))

	if changes[0].LinesAdded != 10 || changes[0].LinesRemoved != 3 {
		t.Errorf("file1: expected +10 -3, got +%d -%d", changes[0].LinesAdded, changes[0].LinesRemoved)
	}
	if changes[1].LinesAdded != 25 || changes[1].LinesRemoved != 0 {
		t.Errorf("file2: expected +25 -0, got +%d -%d", changes[1].LinesAdded, changes[1].LinesRemoved)
	}
}
