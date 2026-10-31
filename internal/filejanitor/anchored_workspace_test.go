package filejanitor

import (
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestConfirmSetupReplacesDefaultAnchorGrantWithMetadataOnly(t *testing.T) {
	service, store := newTestService(t)
	root := inboxFixture(t)
	ws := store.workspaces["ws-1"]
	ws.SharedData = map[string]any{workspace.AnchorDirectoryReferenceIDKey: "anchor", workspace.PrimaryDirectoryIDKey: "anchor"}
	ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "anchor", WorkspaceID: ws.ID, Name: "Inbox", Path: root}}
	ws.MCPBindings = []workspace.MCPBinding{{ID: "default-grant", ServerName: "filesystem", Alias: workspace.AnchorFolderMCPAlias, Enabled: true,
		Config: map[string]any{"roots": []string{root}}, AllowedTools: []string{"read_text_file", "list_directory"}, DefaultSideEffect: workspace.SideEffectRead}}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1}}
	for range 2 {
		if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: ws.ID, Path: root}); err != nil {
			t.Fatal(err)
		}
		ws = store.workspaces["ws-1"]
		if len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].ID != "anchor" || len(ws.MCPBindings) != 1 {
			t.Fatalf("duplicate resources: %+v / %+v", ws.DirectoryReferences, ws.MCPBindings)
		}
		binding := ws.MCPBindings[0]
		if binding.ID != "default-grant" || binding.Alias != LegacyBindingAlias || !reflect.DeepEqual(binding.AllowedTools, JanitorReadTools) {
			t.Fatalf("wrong grant: %+v", binding)
		}
		for _, forbidden := range forbiddenAgentTools {
			if binding.ToolAllowed(forbidden) {
				t.Fatalf("agent can call %s", forbidden)
			}
		}
		installed, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
		if exclusive, recorded := installed.Owns(workspace.ResourceDirectoryReference, "anchor"); exclusive || !recorded {
			t.Fatal("anchor must remain shared")
		}
		if exclusive, recorded := installed.Owns(workspace.ResourceMCPBinding, binding.ID); !exclusive || !recorded {
			t.Fatal("Janitor must own its scoped binding")
		}
	}
}

func TestJanitorBindingPreservesOtherExplicitGrants(t *testing.T) {
	for _, tc := range []struct{ name, alias, root string }{
		{"user grant", "my-files", "/anchor"},
		{"different folder", workspace.AnchorFolderMCPAlias, "/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := &workspace.Workspace{SharedData: map[string]any{workspace.AnchorDirectoryReferenceIDKey: "anchor"},
				DirectoryReferences: []workspace.DirectoryReference{{ID: "anchor", Path: "/anchor"}},
				MCPBindings:         []workspace.MCPBinding{{ID: "explicit", ServerName: "filesystem", Alias: tc.alias, Config: map[string]any{"roots": []any{tc.root}}}}}
			before := ws.MCPBindings[0]
			ensureJanitorBinding(ws, "/anchor")
			if len(ws.MCPBindings) != 2 || !reflect.DeepEqual(before, ws.MCPBindings[0]) {
				t.Fatalf("changed unrelated grant: %+v", ws.MCPBindings)
			}
		})
	}
}

func TestAnchoredWorkspaceNeverPinsFilesystemAccess(t *testing.T) {
	for _, otherFolder := range []bool{false, true} {
		name := "anchor"
		if otherFolder {
			name = "other-folder"
		}
		t.Run(name, func(t *testing.T) {
			service, store := newTestService(t)
			anchor := inboxFixture(t)
			ws := store.workspaces["ws-1"]
			ws.SharedData = map[string]any{workspace.AnchorDirectoryReferenceIDKey: "anchor", workspace.PrimaryDirectoryIDKey: "anchor"}
			ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "anchor", Path: anchor, Name: "Inbox", WorkspaceID: ws.ID}}
			ws.MCPBindings = nil // Even deliberate removal must never restore implicit access.
			pinExistingFilesystemAccess(ws)
			if len(ws.MCPBindings) != 0 {
				t.Fatalf("pin widened access: %+v", ws.MCPBindings)
			}
			root := anchor
			if otherFolder {
				root = inboxFixture(t)
			}
			if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: ws.ID, Path: root}); err != nil {
				t.Fatal(err)
			}
			ws = store.workspaces["ws-1"]
			if len(ws.MCPBindings) != 1 || ws.MCPBindings[0].Alias != LegacyBindingAlias {
				t.Fatalf("unexpected bindings: %+v", ws.MCPBindings)
			}
			if ws.AnchorDirectoryReferenceID() != "anchor" || ws.DirectoryReferences[0].Path != anchor {
				t.Fatal("linking Janitor changed the anchor")
			}
		})
	}
}
