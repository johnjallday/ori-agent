package filejanitor

import (
	"encoding/json"
	"os"
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
