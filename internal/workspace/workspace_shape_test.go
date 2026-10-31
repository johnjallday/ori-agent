package workspace

import "testing"

func TestAnchoredWorkspaceNeverSynthesizesFilesystemBinding(t *testing.T) {
	ws := &Workspace{SharedData: map[string]any{AnchorDirectoryReferenceIDKey: "anchor"}, DirectoryReferences: []DirectoryReference{{ID: "anchor", Path: "/external"}}}
	if got := synthesizeFilesystemBinding(nil, ws); got != nil {
		t.Fatalf("synthesized anchor access: %+v", got)
	}
	if err := ws.DeleteDirectoryReference("anchor"); err == nil {
		t.Fatal("removed the anchor")
	}
	if len(ws.DirectoryReferences) != 1 {
		t.Fatal("anchor changed on refused deletion")
	}
	delete(ws.SharedData, AnchorDirectoryReferenceIDKey)
	if got := synthesizeFilesystemBinding(nil, ws); got == nil {
		t.Fatal("changed legacy synthesis")
	}
}

func TestWorkspaceShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		data map[string]any
		want string
	}{
		{"legacy", nil, ShapeManaged},
		{"primary-only", map[string]any{PrimaryDirectoryIDKey: "ref"}, ShapeManaged},
		{"blank-anchor", map[string]any{AnchorDirectoryReferenceIDKey: " "}, ShapeManaged},
		{"anchor", map[string]any{AnchorDirectoryReferenceIDKey: "ref"}, ShapeAnchored},
		{"import-enabled", map[string]any{"folder_import": map[string]any{"enabled": true}}, ShapeImported},
		{"import-string", map[string]any{"folder_import": map[string]any{"enabled": "TRUE"}}, ShapeImported},
		{"import-path", map[string]any{"folder_import": map[string]any{"path": "/external"}}, ShapeImported},
		{"import-disabled", map[string]any{"folder_import": map[string]any{"enabled": false}}, ShapeManaged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := &Workspace{SharedData: tc.data}
			if got := ws.Shape(); got != tc.want {
				t.Fatalf("Shape = %q, want %q", got, tc.want)
			}
			if got := summarizeWorkspace(ws).Shape; got != tc.want {
				t.Fatalf("summary shape = %q", got)
			}
		})
	}
}
