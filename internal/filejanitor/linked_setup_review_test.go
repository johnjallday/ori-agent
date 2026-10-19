package filejanitor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func linkedSetupFixture(t *testing.T) (*Service, *fakeWorkspaceStore, string, string) {
	t.Helper()
	service, workspaces, base := installedService(t)
	root := mkdir(t, filepath.Join(base, "Inbox"))
	ws, _ := workspaces.Get("ws-1")
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Inbox", Path: root}); err != nil {
		t.Fatal(err)
	}
	return service, workspaces, root, ws.DirectoryReferences[0].ID
}

func TestLinkedSetupReview_ReadsOneExactCandidateWithoutConsequences(t *testing.T) {
	service, workspaces, root, reference := linkedSetupFixture(t)
	first, err := service.ReviewLinkedSetup("ws-1", "local", "", false)
	if err != nil || first.State != "review" || first.SelectedReference != reference || first.Revision == "" || first.Monitoring {
		t.Fatalf("review = %+v, %v", first, err)
	}
	if len(first.Candidates) != 1 || first.Candidates[0].Path != root || first.rootPath != root {
		t.Fatalf("wrong folder: %+v", first)
	}
	repeated, err := service.ReviewLinkedSetup("ws-1", "local", reference, false)
	if err != nil || !reflect.DeepEqual(first, repeated) {
		t.Fatalf("read was not stable: %+v, %v", repeated, err)
	}
	for _, path := range []string{filepath.Join(root, DefaultFilingRootName), func() string { p, _ := service.store.StateDir("ws-1"); return p }()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("read initialized filesystem state: %s, %v", path, err)
		}
	}
	ws, _ := workspaces.Get("ws-1")
	if len(ws.MCPBindings) != 0 || len(ws.DirectoryReferences) != 1 {
		t.Fatal("review changed grants")
	}
	if first.Terms.PrivacyMode != ContentModeMetadataOnly || !first.Terms.ExcludesFiled || first.Terms.DailyScanLocalTime != "09:00" || first.Terms.Timezone == "" {
		t.Fatalf("missing effective terms: %+v", first.Terms)
	}
}

func TestLinkedSetupReview_RejectsForeignOrArbitraryReferences(t *testing.T) {
	service, _, _, reference := linkedSetupFixture(t)
	for _, input := range []struct{ owner, reference string }{
		{"other-owner", reference}, {"", reference}, {"local", "foreign-reference"}, {"local", "/tmp/arbitrary-path"},
	} {
		if _, err := service.ReviewLinkedSetup("ws-1", input.owner, input.reference, false); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
}

func TestLinkedSetupReview_NoCandidateDoesNotInferPrimaryOrDownloads(t *testing.T) {
	service, workspaces, _ := installedService(t)
	folder, err := service.store.resolver.GetFolderPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := workspaces.Get("ws-1")
	ws.Name = "Downloads"
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Scaffold", Path: folder}); err != nil {
		t.Fatal(err)
	}
	ws.SharedData = map[string]any{"primary_directory_id": ws.DirectoryReferences[0].ID}
	review, err := service.ReviewLinkedSetup("ws-1", "local", "", false)
	if err != nil || review.State != "no_candidate" || len(review.Candidates) != 0 || review.Revision != "" {
		t.Fatalf("inferred folder: %+v, %v", review, err)
	}
}

func TestLinkedSetupReview_AmbiguityRequiresChoiceAndInvalidatesPriorReview(t *testing.T) {
	service, workspaces, _, reference := linkedSetupFixture(t)
	before, err := service.ReviewLinkedSetup("ws-1", "local", "", false)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := workspaces.Get("ws-1")
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Inbox", Path: inboxFixture(t)}); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := service.ReviewLinkedSetup("ws-1", "local", "", false)
	if err != nil || ambiguous.State != "choose_folder" || len(ambiguous.Candidates) != 2 || ambiguous.Revision != "" {
		t.Fatalf("silently chose among same-named folders: %+v, %v", ambiguous, err)
	}
	chosen, err := service.ReviewLinkedSetup("ws-1", "local", reference, false)
	if err != nil || chosen.State != "review" || chosen.SelectedReference != reference || chosen.Revision == before.Revision {
		t.Fatalf("choice did not bind candidate set: %+v, %v", chosen, err)
	}
}

func TestLinkedSetupReview_ChangedRootAndMonitoringTermsInvalidateRevision(t *testing.T) {
	service, workspaces, root, reference := linkedSetupFixture(t)
	before, err := service.ReviewLinkedSetup("ws-1", "local", reference, false)
	if err != nil {
		t.Fatal(err)
	}
	monitoring, err := service.ReviewLinkedSetup("ws-1", "local", reference, true)
	if err != nil || monitoring.Revision == before.Revision {
		t.Fatalf("monitoring not bound: %+v, %v", monitoring, err)
	}
	// Preserve the old inode so this proves replacement rather than relying on
	// whether the filesystem happens to recycle an immediately deleted inode.
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	mkdir(t, root)
	changed, err := service.ReviewLinkedSetup("ws-1", "local", reference, false)
	if err != nil || changed.State != "review" || changed.Revision == before.Revision {
		t.Fatalf("root replacement not bound: %+v, %v", changed, err)
	}
	ws, _ := workspaces.Get("ws-1")
	ws.OwnerUserID = "new-owner"
	if _, err := service.ReviewLinkedSetup("ws-1", "local", reference, false); err == nil {
		t.Fatal("old owner retained access")
	}
}

func TestLinkedSetupReview_UnavailableLinkIsNotSilentlySubstituted(t *testing.T) {
	service, _, root, reference := linkedSetupFixture(t)
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewLinkedSetup("ws-1", "local", "", false)
	if err != nil || review.State != "folder_unavailable" || review.SelectedReference != reference || review.Revision != "" {
		t.Fatalf("unavailable link = %+v, %v", review, err)
	}
}

func TestLinkedSetupReview_ConfiguredAndRevokedStatesNeverOfferReprovisioning(t *testing.T) {
	service, _, root, _ := linkedSetupFixture(t)
	paused := true
	status, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	chooseManual(t, service, status)
	review, err := service.ReviewLinkedSetup("ws-1", "local", "", true)
	if err != nil || review.State != "configured" || review.CurrentMode != "manual" || review.Revision != "" {
		t.Fatalf("existing manual settings offered reset: %+v, %v", review, err)
	}
	if _, err := service.RevokeAccess(nil, "ws-1"); err != nil {
		t.Fatal(err)
	}
	review, err = service.ReviewLinkedSetup("ws-1", "local", "", false)
	if err != nil || review.State != "revoked" || review.Revision != "" || len(review.Candidates) != 0 {
		t.Fatalf("adopted link revived offer: %+v, %v", review, err)
	}
}

func TestLinkedSetupReview_LegacyEmptyOrCorruptSettingsRequireRepair(t *testing.T) {
	for _, contents := range []string{`{"schema_version":2,"workspace_id":"ws-1"}`, `{broken`} {
		t.Run(contents, func(t *testing.T) {
			service, _, _, _ := linkedSetupFixture(t)
			path, err := service.store.settingsPath("ws-1")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			review, err := service.ReviewLinkedSetup("ws-1", "local", "", false)
			if err != nil || review.State != "repair_required" || review.Revision != "" {
				t.Fatalf("uncertain state became new permission: %+v, %v", review, err)
			}
		})
	}
}
