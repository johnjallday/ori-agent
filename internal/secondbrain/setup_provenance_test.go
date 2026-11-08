package secondbrain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A setup operation's folder carries its provenance from the first write, so a
// crash between making the folder and recording the connection leaves a folder
// only that operation can recognize (assistant-hq-onboarding 3.2).

func TestCreate_WithProvenanceWritesTheMarkerFirst(t *testing.T) {
	parent := canonicalTemp(t)
	var order []string
	creator := Creator{Provenance: "hq-notes-abc", beforeWrite: func(rel string) error {
		order = append(order, rel)
		return nil
	}}
	if _, err := creator.Create(parent, DefaultFolderName, Seeds{}); err != nil {
		t.Fatal(err)
	}
	if len(order) == 0 || order[0] != SetupMarkerName {
		t.Fatalf("first write = %v, want the marker first", order)
	}
	root, err := os.OpenRoot(filepath.Join(parent, DefaultFolderName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if got := readSetupMarker(root); got != "hq-notes-abc" {
		t.Fatalf("marker = %q", got)
	}
}

func TestCreate_WithoutProvenanceWritesNoMarker(t *testing.T) {
	parent := canonicalTemp(t)
	if _, err := (Creator{}).Create(parent, DefaultFolderName, Seeds{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, DefaultFolderName, SetupMarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an ordinary setup wrote a provenance marker: %v", err)
	}
}

// The crash window: the marker and part of the layout are on disk, nothing
// recorded them. Recover finishes the layout and never overwrites what is there.
func TestRecover_FinishesItsOwnHalfMadeFolderWithoutOverwriting(t *testing.T) {
	parent := canonicalTemp(t)
	crash := errors.New("crash")
	// A Create that fails rolls back; to model a process that died instead, make
	// the folder by hand the way a crash would leave it.
	target := filepath.Join(parent, DefaultFolderName)
	if err := os.MkdirAll(filepath.Join(target, FolderInbox), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, SetupMarkerName), setupMarkerContent("hq-notes-own"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := []byte("# Inbox\n\nThe user already wrote here.\n")
	if err := os.WriteFile(filepath.Join(target, FolderInbox, "README.md"), edited, 0o600); err != nil {
		t.Fatal(err)
	}
	creation, err := (Creator{}).Recover(parent, DefaultFolderName, "hq-notes-own")
	if err != nil {
		t.Fatal(err)
	}
	if creation.Path != target || creation.FollowUps != 0 || creation.People != 0 {
		t.Fatalf("creation = %+v (recovery must not seed again)", creation)
	}
	if got := readTestFile(t, target, FolderInbox, "README.md"); got != string(edited) {
		t.Fatalf("an existing file was overwritten: %q", got)
	}
	for _, entry := range createdLayout {
		if _, err := os.Stat(filepath.Join(target, entry.folder, "README.md")); err != nil {
			t.Errorf("%s/README.md missing after recovery: %v", entry.folder, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("root README missing after recovery: %v", err)
	}

	// A recovery that itself fails removes only what it added.
	if err := os.RemoveAll(filepath.Join(target, "Archive")); err != nil {
		t.Fatal(err)
	}
	failing := Creator{beforeWrite: func(rel string) error {
		if rel == "Archive/README.md" {
			return crash
		}
		return nil
	}}
	if _, err := failing.Recover(parent, DefaultFolderName, "hq-notes-own"); !errors.Is(err, crash) {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	if _, err := os.Stat(filepath.Join(target, "Archive")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed recovery left what it added: %v", err)
	}
	if got := readTestFile(t, target, FolderInbox, "README.md"); got != string(edited) {
		t.Fatalf("a failed recovery touched an existing file: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, SetupMarkerName)); err != nil {
		t.Fatalf("the marker must stay for the next attempt: %v", err)
	}
}

func TestRecover_NeverAdoptsAFolderItDidNotMake(t *testing.T) {
	parent := canonicalTemp(t)
	target := filepath.Join(parent, DefaultFolderName)
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "mine.md"), []byte("user note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"no marker":         nil,
		"another operation": setupMarkerContent("hq-notes-other"),
		"junk marker":       []byte("{\"operation_id\":\"x\",\"extra\":1}"),
		"oversized marker":  append(setupMarkerContent("hq-notes-own"), make([]byte, 2048)...),
	}
	for name, marker := range cases {
		t.Run(name, func(t *testing.T) {
			markerPath := filepath.Join(target, SetupMarkerName)
			_ = os.Remove(markerPath)
			if marker != nil {
				if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := (Creator{}).Recover(parent, DefaultFolderName, "hq-notes-own"); !errors.Is(err, ErrNotOwnSetup) {
				t.Fatalf("err = %v, want ErrNotOwnSetup", err)
			}
			entries, _ := os.ReadDir(target)
			for _, entry := range entries {
				if entry.Name() != "mine.md" && entry.Name() != SetupMarkerName {
					t.Fatalf("recovery wrote %q into a folder it did not make", entry.Name())
				}
			}
		})
	}
}

func TestRecover_RefusesALinkInPlaceOfTheFolder(t *testing.T) {
	parent := canonicalTemp(t)
	elsewhere := canonicalTemp(t)
	if err := os.WriteFile(filepath.Join(elsewhere, SetupMarkerName), setupMarkerContent("hq-notes-own"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(parent, DefaultFolderName)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := (Creator{}).Recover(parent, DefaultFolderName, "hq-notes-own"); !errors.Is(err, ErrNotOwnSetup) {
		t.Fatalf("err = %v, want ErrNotOwnSetup for a link", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 1 {
		t.Fatalf("recovery followed the link and wrote %d entries", len(entries))
	}
}

func TestRecord_ValidatesTheSetupOperation(t *testing.T) {
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	valid := SetupOperation{
		OperationID: "hq-notes-abc", HQRequestID: "hq-1", Parent: "/tmp/root", Name: DefaultFolderName,
		Status: SetupPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := (Record{Setup: &valid}).Validate(); err != nil {
		t.Fatalf("valid operation refused: %v", err)
	}
	broken := map[string]func(*SetupOperation){
		"bad id":        func(o *SetupOperation) { o.OperationID = "has space" },
		"relative":      func(o *SetupOperation) { o.Parent = "relative/root" },
		"unclean":       func(o *SetupOperation) { o.Parent = "/tmp/root/../x" },
		"path name":     func(o *SetupOperation) { o.Name = "a/b" },
		"hidden name":   func(o *SetupOperation) { o.Name = ".notes" },
		"status":        func(o *SetupOperation) { o.Status = "ready" },
		"no created at": func(o *SetupOperation) { o.CreatedAt = time.Time{} },
	}
	for name, mutate := range broken {
		op := valid
		mutate(&op)
		if err := (Record{Setup: &op}).Validate(); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: err = %v, want ErrInvalidRecord", name, err)
		}
	}
	if got := valid.Target(); got != filepath.Join("/tmp/root", DefaultFolderName) {
		t.Fatalf("Target = %q", got)
	}
}

func TestFillIn_AcceptsTheDeferredProjectStateBesideHistoricalOnes(t *testing.T) {
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	for _, state := range []string{"", ProjectAnswered, ProjectSkipped, ProjectDeferred} {
		fill := FillIn{ProjectAsk: state}
		if state != "" {
			fill.ProjectAskAt = &at
		}
		if err := validateFillIn(fill); err != nil {
			t.Errorf("%q refused: %v", state, err)
		}
	}
	if err := validateFillIn(FillIn{ProjectAsk: "maybe", ProjectAskAt: &at}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("an unknown project state was accepted: %v", err)
	}
}
