package filejanitor

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

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
