package tasklist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTracksNumberedParentAndSubtaskCheckboxes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	contents := `
- [x] 1.0 Parent complete
  - [x] 1.1 First complete
  - [ ] 1.2 Next implementation step
- [ ] 2.0 Later parent
- [ ] prose checkbox without a task number
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	progress := Read(path)
	if !progress.Exists || progress.Total != 4 || progress.Completed != 2 || progress.Next != "1.2 Next implementation step" || progress.Label() != "2/4" {
		t.Fatalf("Read() = %#v", progress)
	}
}

func TestReadHandlesCompleteMissingAndMalformedTaskLists(t *testing.T) {
	missing := Read(filepath.Join(t.TempDir(), "missing.md"))
	if missing.Exists || missing.Next == "" || missing.ParseIssue != "" {
		t.Fatalf("missing Read() = %#v", missing)
	}

	completePath := filepath.Join(t.TempDir(), "complete.md")
	if err := os.WriteFile(completePath, []byte("- [x] 1.0 Deliver feature\n"), 0600); err != nil {
		t.Fatal(err)
	}
	complete := Read(completePath)
	if complete.Total != 1 || complete.Completed != 1 || complete.Next != "All checklist items are marked complete; verify the feature before opening its PR." {
		t.Fatalf("complete Read() = %#v", complete)
	}

	malformedPath := filepath.Join(t.TempDir(), "malformed.md")
	if err := os.WriteFile(malformedPath, []byte("- [ ] task without ordinal\n- [x] also prose\n"), 0600); err != nil {
		t.Fatal(err)
	}
	malformed := Read(malformedPath)
	if malformed.Total != 0 || malformed.ParseIssue == "" || malformed.Next == "" {
		t.Fatalf("malformed Read() = %#v", malformed)
	}

	parentOnlyPath := filepath.Join(t.TempDir(), "parent-only.md")
	if err := os.WriteFile(parentOnlyPath, []byte("- [ ] 1.0 Deliver the vertical slice\n"), 0600); err != nil {
		t.Fatal(err)
	}
	parentOnly := Read(parentOnlyPath)
	if parentOnly.Total != 1 || parentOnly.Completed != 0 || parentOnly.Next != "1.0 Deliver the vertical slice" {
		t.Fatalf("parent-only Read() = %#v", parentOnly)
	}
}
