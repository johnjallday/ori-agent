package projecttemplates

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The workspace package repeats this package's primary-directory keys to list
// adopted folders without an import cycle; a linked folder must stay adopted.
func TestAttachLinkedDirectoryIsAnAdoptedFolder(t *testing.T) {
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Manuscript"})
	if _, err := AttachLinkedDirectory(ws, "Manuscript", folder); err != nil {
		t.Fatal(err)
	}
	got := ws.AdoptedFolders()
	if len(got) != 1 || got[0].Kind != workspace.AdoptedProjectFolder || got[0].Path != folder {
		t.Fatal(got)
	}
}
