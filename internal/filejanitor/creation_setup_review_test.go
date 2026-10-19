package filejanitor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelectedSetupReview_IsReadOnlyAndUsesCanonicalDisclosure(t *testing.T) {
	service, workspaces, root, reference := linkedSetupFixture(t)
	service.workspaces = listableFakeStore{workspaces}
	before, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := service.ReviewSelectedSetup("", root, nil)
	if err != nil || selected.Path != root || len(selected.Revision) != 64 {
		t.Fatalf("selected = %+v, %v", selected, err)
	}
	linked, err := service.ReviewLinkedSetup("ws-1", "local", reference, false)
	if err != nil || !reflect.DeepEqual(selected.Terms, linked.Terms) {
		t.Fatalf("disclosures disagree: %+v, %+v, %v", selected.Terms, linked.Terms, err)
	}
	after, err := service.store.LoadSettings("ws-1")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("review changed settings")
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("review created Filed")
	}
	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReviewSelectedSetup("", root, nil); err == nil {
		t.Fatal("new creation can take an owned root")
	}
	if _, err := service.ReviewSelectedSetup("ws-1", root, nil); err != nil {
		t.Fatalf("same workspace cannot revalidate root: %v", err)
	}
}
