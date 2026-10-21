package orchestrationhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A folder's generic first look is told to read its folder with the workspace's
// read-only directory tools. A reply that did so is verified, not paused as a
// listing made without looking.
func TestFilesystemVerification_CountsTheWorkspaceDirectoryTools(t *testing.T) {
	for _, name := range []string{"workspace_directory_list", "workspace_directory_read", "list_directory", "read_file"} {
		if !isFilesystemReadVerificationTool(name) {
			t.Errorf("%s does not verify a listing", name)
		}
	}
	if isFilesystemReadVerificationTool("workspace_directories") {
		t.Error("naming the linked folders is not reading one")
	}

	// The generic first look's own description (personalassistant.FolderFirstTask).
	task := workspace.Task{Description: "Tell me what is in this folder and what looks most active"}
	reply := "The folder holds Tomatoes.md and Compost.md.\n\nNext: Merge the two plans."
	if blocked := classifyFilesystemListingVerificationFailure(task, reply, taskExecutionEvidence{}); blocked == nil {
		t.Fatal("a listing made without reading the folder must still be paused")
	}
	read := taskExecutionEvidence{SuccessfulFilesystemReadToolNames: []string{"workspace_directory_list"}}
	if blocked := classifyFilesystemListingVerificationFailure(task, reply, read); blocked != nil {
		t.Fatalf("a look that read its folder was paused: %+v", blocked)
	}
}
