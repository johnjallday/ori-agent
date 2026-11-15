package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
)

func TestFolderAgentCompletedTitle_FilingStillNeedsPermission(t *testing.T) {
	view := folderagent.View{WorkspaceName: "Downloads", AgentName: "Downloads Manager", FilingPermissionRequired: true}
	if got := folderAgentCompletedTitle(view); got != "Downloads Manager is hired. One step left: allow filing." {
		t.Fatal(got)
	}
	view.FilingPermissionRequired = false
	if got := folderAgentCompletedTitle(view); got != "Downloads is set up" {
		t.Fatal(got)
	}
}
