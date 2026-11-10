package filejanitor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func storedRootFixture(t *testing.T) (*Service, *workspace.Workspace, JanitorSettings) {
	t.Helper()
	s, workspaces := newTestService(t)
	ws := workspaces.workspaces["ws-1"]
	ws.OwnerUserID = "local"
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{ID: workspace.CapabilityFileJanitor, Version: 1, Source: workspace.InstallSourceInPlace}); err != nil {
		t.Fatal(err)
	}
	settings := NewSettings(ws.ID)
	// Deliberately non-existent; a readiness/stat/root resolution path cannot
	// return this identity. Only Ori's own metadata directories exist.
	settings.RootPath, settings.RootID = "/Invented/not-mounted/Managed", "generation-one"
	settings.DirectoryReferenceID, settings.SetupCompletedAt = "ref", time.Now()
	ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "ref", WorkspaceID: ws.ID, Path: settings.RootPath}}
	ensureJanitorBinding(ws, settings.RootPath)
	if err := s.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	return s, ws, settings
}

func TestStoredManagedRootReadsOnlySavedMetadata(t *testing.T) {
	s, ws, settings := storedRootFixture(t)
	root, err := s.StoredManagedRoot("local", ws.ID)
	if err != nil || root.Path != settings.RootPath || root.Generation != settings.RootID {
		t.Fatal(root, err)
	}
	encoded, err := json.Marshal(root)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("server-only root serialized", string(encoded), err)
	}
	if len(ws.AgentInstances) != 0 {
		t.Fatal("fixture must be agentless")
	}
}

type unreadableRootWorkspace struct{ WorkspaceStore }

func (unreadableRootWorkspace) GetFolderWorkspace(string) (*workspace.Workspace, error) {
	return nil, errors.New("canonical record is unreadable")
}

func TestStoredManagedRootRejectsStaleFallbackGrant(t *testing.T) {
	s, ws, _ := storedRootFixture(t)
	s.workspaces = unreadableRootWorkspace{s.workspaces}
	if _, err := s.StoredManagedRoot("local", ws.ID); err != ErrManagedRootUnavailable {
		t.Fatal("unreadable canonical metadata fell back to an older grant", err)
	}
}

func TestStoredManagedRoot_ApprovedAliasProofIsSavedOnlyAndGenerationBound(t *testing.T) {
	s, ws, settings := storedRootFixture(t)
	alias := "/Invented/unmounted-alias"
	ws.DirectoryReferences[0].Path = alias
	if _, err := s.StoredManagedRoot("local", ws.ID); err == nil {
		t.Fatal("legacy alias invented approval proof")
	}
	settings.ApprovedReference = &ApprovedReferenceSpelling{RootID: settings.RootID, ReferenceID: settings.DirectoryReferenceID, Path: alias}
	if err := s.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if root, err := s.StoredManagedRoot("local", ws.ID); err != nil || root.Path != settings.RootPath {
		t.Fatal("saved alias proof probed a nonexistent folder", root, err)
	}
	for _, mode := range []string{"root", "reference", "spelling"} {
		copy := settings
		proof := *settings.ApprovedReference
		copy.ApprovedReference = &proof
		switch mode {
		case "root":
			proof.RootID = "other"
		case "reference":
			proof.ReferenceID = "other"
		case "spelling":
			proof.Path = "/Invented/other"
		}
		if err := s.store.SaveSettings(copy); err != nil {
			t.Fatal(err)
		}
		if _, err := s.StoredManagedRoot("local", ws.ID); err == nil {
			t.Fatal("foreign alias proof accepted", mode)
		}
	}
}

func TestStoredManagedRoot_ExplicitGrantCapturesAliasWithoutMigratingLegacyProof(t *testing.T) {
	s, batch, _ := draftServiceFixture(t, "one.png")
	settings, _ := s.store.LoadSettings("ws-1")
	ws, _ := s.workspaces.Get("ws-1")
	alias := filepath.Join(t.TempDir(), "approved-alias")
	if err := os.Symlink(settings.RootPath, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ref, err := ws.GetDirectoryReference(settings.DirectoryReferenceID)
	if err != nil {
		t.Fatal(err)
	}
	ref.Path = alias
	if err := ws.UpdateDirectoryReference(*ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StoredManagedRoot("local", "ws-1"); err == nil {
		t.Fatal("reference edit inferred alias permission")
	}
	paused := true
	status, err := s.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: alias, DirectoryReferenceID: ref.ID, Paused: &paused})
	if err != nil || status.Settings.ApprovedReference == nil || status.Settings.ApprovedReference.Path != alias {
		t.Fatalf("explicit alias grant did not capture proof: %+v %v", status.Settings.ApprovedReference, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadReviewGroups(t.Context(), "local", "ws-1", batch.ID); err != nil {
		t.Fatal("pure saved grouping resolved the alias", err)
	}
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: "select_filing_groups"}); err == nil {
		t.Fatal("missing physical alias permitted selection")
	}
}

func TestStoredManagedRootFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*workspace.Workspace, *JanitorSettings)
	}{
		{"wrong owner", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.OwnerUserID = "someone-else" }},
		{"removed capability", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.InstalledCapabilities = nil }},
		{"missing generation", func(_ *workspace.Workspace, s *JanitorSettings) { s.RootID = "" }},
		{"malformed generation", func(_ *workspace.Workspace, s *JanitorSettings) { s.RootID = "/Invented/private" }},
		{"revoked", func(_ *workspace.Workspace, s *JanitorSettings) { s.AccessRevokedAt = time.Now() }},
		{"incomplete setup", func(_ *workspace.Workspace, s *JanitorSettings) { s.SetupCompletedAt = time.Time{} }},
		{"wrong reference path", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.DirectoryReferences[0].Path = "/Invented/other" }},
		{"wrong reference owner", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.DirectoryReferences[0].WorkspaceID = "other" }},
		{"missing reference", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.DirectoryReferences = nil }},
		{"duplicate reference", func(ws *workspace.Workspace, _ *JanitorSettings) {
			ws.DirectoryReferences = append(ws.DirectoryReferences, ws.DirectoryReferences[0])
		}},
		{"disabled access", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.MCPBindings[0].Enabled = false }},
		{"widened access", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.MCPBindings[0].AllowedTools = nil }},
		{"removed tools", func(ws *workspace.Workspace, _ *JanitorSettings) { ws.MCPBindings[0].AllowedTools = []string{} }},
		{"wrong binding root", func(ws *workspace.Workspace, _ *JanitorSettings) {
			ws.MCPBindings[0].Config["roots"] = []string{"/Invented/other"}
		}},
		{"duplicate binding", func(ws *workspace.Workspace, _ *JanitorSettings) {
			ws.MCPBindings = append(ws.MCPBindings, ws.MCPBindings[0])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, ws, settings := storedRootFixture(t)
			test.change(ws, &settings)
			if err := s.store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if root, err := s.StoredManagedRoot("local", ws.ID); err != ErrManagedRootUnavailable || root.Path != "" {
				t.Fatal("invalid root exposed", root, err)
			}
		})
	}
	for _, body := range []string{"{", `{"workspace_id":"other"}`, `{}`} {
		t.Run("damaged settings "+body, func(t *testing.T) {
			s, ws, _ := storedRootFixture(t)
			path, err := s.store.settingsPath(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if root, err := s.StoredManagedRoot("local", ws.ID); err == nil || strings.Contains(root.Path, "Managed") {
				t.Fatal(root, err)
			}
		})
	}
}
