package reapersetup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func fileFallbackFixture(t *testing.T) (*FileFallbackPreparer, *workspace.Workspace, string) {
	t.Helper()
	folder := t.TempDir()
	project := filepath.Join(folder, "song")
	if err := os.Mkdir(project, 0o750); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(project, "song.rpp")
	if err := os.WriteFile(entry, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	ws.SharedData = map[string]any{}
	ws.ProjectPath = "song"
	if err := workspace.SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	return NewFileFallbackPreparer(&runtimeTestSource{ws: ws, folder: folder}), ws, entry
}

func fallbackTask(ws *workspace.Workspace) workspace.Task {
	return workspace.Task{
		ID: "task", WorkspaceID: ws.ID, Description: "Adjust the existing session", Details: "Change the tempo.",
		RequiredCapabilities: []string{ReaperLiveControlCapability},
		FileFallbackFor:      []string{ReaperLiveControlCapability},
	}
}

func TestFileFallbackPromotesOnlyAuthoritativeProject(t *testing.T) {
	preparer, ws, entry := fileFallbackFixture(t)
	run, err := preparer.PrepareTaskFileFallback(context.Background(), ws.ID, fallbackTask(ws), ReaperLiveControlCapability)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Abort()
	prepared := run.PreparedTask()
	if prepared.RuntimeExecution == nil || !prepared.RuntimeExecution.FileOnly || !prepared.RuntimeExecution.DisableTools || len(prepared.RequiredCapabilities) != 0 {
		t.Fatalf("prepared task = %+v", prepared)
	}
	staged := filepath.Join(prepared.RuntimeExecution.WorkspaceRoot, prepared.RuntimeExecution.Filename)
	if err := os.WriteFile(staged, []byte("UPDATED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(entry)
	if string(got) != "UPDATED" {
		t.Fatalf("source = %q", got)
	}
}

func TestFileFallbackRejectsExtraFilesAndConcurrentProjectChanges(t *testing.T) {
	t.Run("extra staging file", func(t *testing.T) {
		preparer, ws, entry := fileFallbackFixture(t)
		run, err := preparer.PrepareTaskFileFallback(context.Background(), ws.ID, fallbackTask(ws), ReaperLiveControlCapability)
		if err != nil {
			t.Fatal(err)
		}
		defer run.Abort()
		root := run.PreparedTask().RuntimeExecution.WorkspaceRoot
		if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("not allowed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run.Commit(); err == nil {
			t.Fatal("extra files must prevent promotion")
		}
		got, _ := os.ReadFile(entry)
		if string(got) != "ORIGINAL" {
			t.Fatal("failed fallback changed source")
		}
	})

	t.Run("source changed concurrently", func(t *testing.T) {
		preparer, ws, entry := fileFallbackFixture(t)
		run, err := preparer.PrepareTaskFileFallback(context.Background(), ws.ID, fallbackTask(ws), ReaperLiveControlCapability)
		if err != nil {
			t.Fatal(err)
		}
		defer run.Abort()
		prepared := run.PreparedTask()
		if err := os.WriteFile(filepath.Join(prepared.RuntimeExecution.WorkspaceRoot, prepared.RuntimeExecution.Filename), []byte("FALLBACK"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(entry, []byte("USER CHANGE"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run.Commit(); err == nil {
			t.Fatal("concurrent source change must prevent promotion")
		}
		got, _ := os.ReadFile(entry)
		if string(got) != "USER CHANGE" {
			t.Fatal("fallback overwrote concurrent source change")
		}
	})
}
