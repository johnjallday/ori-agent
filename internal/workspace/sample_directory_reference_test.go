package workspace

import "testing"

func TestSampleLibraryDirectoryReferenceIsNotGenericAgentOrFileAuthority(t *testing.T) {
	root := t.TempDir()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Home"})
	if err := ws.AddDirectoryReference(DirectoryReference{ID: "sample-root", Name: "Samples", Path: root, Purpose: "sample_library"}); err != nil {
		t.Fatal(err)
	}
	if roots := collectWorkspaceDirectoryRoots(ws); len(roots) != 0 {
		t.Fatalf("sample root leaked to runtime roots: %#v", roots)
	}
	if _, err := ws.ListDirectoryFiles("sample-root"); err == nil {
		t.Fatal("generic listing accepted sample root")
	}
	if _, err := ws.ReadDirectoryFile("sample-root", "kick.wav"); err == nil {
		t.Fatal("generic read accepted sample root")
	}
	if err := ws.UpdateDirectoryReference(DirectoryReference{ID: "sample-root", Name: "Other", Path: root}); err == nil {
		t.Fatal("generic update accepted sample root")
	}
	if err := ws.DeleteDirectoryReference("sample-root"); err == nil {
		t.Fatal("generic delete accepted sample root")
	}
	if directories := limitTaskPromptDirectories(ws.DirectoryReferences, taskPromptMaxDirectories); len(directories) != 0 {
		t.Fatalf("sample root leaked to prompt projection: %#v", directories)
	}
}
