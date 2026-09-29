package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func continuityStageFixture(t *testing.T, sqlEvidence bool) (string, workspacecontinuity.Inspection, []byte) {
	t.Helper()
	root := t.TempDir()
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Portable authored work"})
	ws.OwnerUserID, ws.FolderSlug, ws.Version = "local", "portable-authored-work", 11
	ws.CreatedAt, ws.UpdatedAt, ws.Status = at, at.Add(time.Hour), StatusActive
	ws.Designation = "personal_hq"
	ws.ProjectPath = "/unavailable-source/project"
	ws.DirectoryReferences = []DirectoryReference{{ID: "external", WorkspaceID: ws.ID, Path: "/unavailable-source/reference", Name: "Source reference", CreatedAt: at, UpdatedAt: at}}
	ws.SharedData = map[string]any{"authored_id": json.Number("9007199254740993")}
	ws.TicketSequence = 2
	ws.Tasks = []Task{
		{ID: "completed", WorkspaceID: ws.ID, Status: TaskStatusCompleted, Result: "Exact result", CreatedAt: at, TicketNumber: 1},
		{ID: "in-progress", WorkspaceID: ws.ID, Status: TaskStatusInProgress, CurrentRunID: "source-handle", CreatedAt: at, TicketNumber: 2, WakeMacEnabled: true},
	}
	ws.Messages = []AgentMessage{{ID: "collaboration", Content: "Exact <historical> text", Timestamp: at}}
	ws.Attachments = []Attachment{{ID: "document", WorkspaceID: ws.ID, Type: AttachmentTypeDoc, Body: "Private body", CreatedAt: at, UpdatedAt: at}}
	ws.MissionEnabled = true
	canonical, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, WorkspaceConfigFile), canonical, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, NotesDir), 0700); err != nil {
		t.Fatal(err)
	}
	memory := []byte("Private remembered work; not a command")
	note := []byte("Authored note with <markup> preserved")
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), memory, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, NotesDir, "meeting.md"), note, 0600); err != nil {
		t.Fatal(err)
	}
	record, err := SnapshotContinuityWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	var desc ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &desc); err != nil {
		t.Fatal(err)
	}
	if sqlEvidence {
		desc.SQLMetadata = &ContinuityWorkspaceSQLMetadata{Color: ""}
	}
	record, err = workspacecontinuity.EncodeRecord(ws.ID, desc)
	if err != nil {
		t.Fatal(err)
	}
	chunk, ref, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{
		Version: 1, WorkspaceID: ws.ID, Domain: "workspace", Family: "workspaces", Records: []workspacecontinuity.Record{record},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := []workspacecontinuity.Fingerprint{
		{Path: "MEMORY.md", Digest: workspacecontinuity.Digest(memory), Bytes: int64(len(memory))},
		{Path: NotesDir + "/meeting.md", Digest: workspacecontinuity.Digest(note), Bytes: int64(len(note))},
		{Path: WorkspaceConfigFile, Digest: workspacecontinuity.Digest(canonical), Bytes: int64(len(canonical))},
	}
	manifest := workspacecontinuity.Manifest{
		Version: 1, Generation: uuid.NewString(), WorkspaceID: ws.ID, CheckpointAt: at,
		SourceRevision: 1, Files: files, SourceFingerprint: workspacecontinuity.FilesDigest(files),
	}
	for _, domain := range workspacecontinuity.DomainNames() {
		c := workspacecontinuity.Component{Domain: domain, Version: 1, Availability: workspacecontinuity.Empty}
		if domain == "workspace" {
			c.Availability = workspacecontinuity.Present
			c.Chunks = []workspacecontinuity.ChunkRef{ref}
			c.Counts = map[string]int64{"workspaces": 1}
		}
		manifest.Components = append(manifest.Components, c)
	}
	if err := workspacecontinuity.Publish(t.Context(), root, manifest,
		func(_ context.Context, digest string) (io.ReadCloser, error) {
			if digest != ref.Digest {
				return nil, workspacecontinuity.ErrIncomplete
			}
			return io.NopCloser(bytes.NewReader(chunk)), nil
		}, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	inspected, err := workspacecontinuity.Inspect(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	return root, inspected, canonical
}

func privateContinuityStage(t *testing.T) string {
	t.Helper()
	stage := t.TempDir()
	if err := os.Chmod(stage, 0700); err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestStageContinuityWorkspacePreservesAuthoredWorkWithoutExecutableState(t *testing.T) {
	root, inspected, original := continuityStageFixture(t, true)
	stage := privateContinuityStage(t)
	if err := StageContinuityWorkspace(t.Context(), root, stage, inspected, ""); err != nil {
		t.Fatal(err)
	}
	count, err := StageContinuityTextFiles(t.Context(), root, stage, inspected)
	if err != nil || count != 2 {
		t.Fatal("authored text files were not staged", count, err)
	}
	for _, want := range []struct{ path, content string }{{"MEMORY.md", "Private remembered work; not a command"}, {NotesDir + "/meeting.md", "Authored note with <markup> preserved"}} {
		data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, want.path, workspacecontinuity.MaxChunkBytes)
		if err != nil || string(data) != want.content {
			t.Fatal("staged authored bytes changed", want.path, err)
		}
	}
	if _, err := StageContinuityTextFiles(t.Context(), root, stage, inspected); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("staging silently replaced existing text", err)
	}
	after, err := os.ReadFile(filepath.Join(root, WorkspaceConfigFile)) // #nosec G304 -- test-owned workspace root
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("staging changed source", err)
	}
	data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("9007199254740993")) || bytes.Contains(data, []byte("9007199254740992")) {
		t.Fatal("authored identity was rounded")
	}
	if bytes.Contains(data, []byte("source-handle")) {
		t.Fatal("source process handle became destination work")
	}
	var projected Workspace
	if err := json.Unmarshal(data, &projected); err != nil {
		t.Fatal(err)
	}
	if projected.ID != inspected.Manifest.WorkspaceID || projected.Version != 1 || projected.Designation != "" ||
		projected.ProjectPath != "" || len(projected.DirectoryReferences) != 0 || len(projected.Tasks) != 2 ||
		projected.Tasks[0].Status != TaskStatusCompleted || projected.Tasks[0].Result != "Exact result" ||
		projected.Tasks[1].Status != TaskStatusFailed || projected.Tasks[1].WakeMacEnabled || !projected.MissionEnabled ||
		len(projected.Messages) != 1 || projected.Messages[0].Content != "Exact <historical> text" ||
		len(projected.Attachments) != 1 || projected.Attachments[0].Body != "Private body" {
		t.Fatal("staging lost authored work or preserved executable authority")
	}
	if err := StageContinuityWorkspace(t.Context(), root, stage, inspected, ""); !errors.Is(err, workspacecontinuity.ErrCollision) {
		t.Fatal("staging overwrote a prior file", err)
	}
}

func TestStageContinuityWorkspaceRefusesUnreviewedSourceOrUnsafeDestination(t *testing.T) {
	root, inspected, original := continuityStageFixture(t, true)
	stage := privateContinuityStage(t)
	if err := StageContinuityWorkspace(t.Context(), root, root, inspected, ""); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("source was used as staging", err)
	}
	inside := filepath.Join(root, "stage")
	if err := os.Mkdir(inside, 0750); err != nil {
		t.Fatal(err)
	}
	if err := StageContinuityWorkspace(t.Context(), root, inside, inspected, ""); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("staging entered the source tree", err)
	}
	if err := StageContinuityWorkspace(t.Context(), root, stage, inspected, "../escape"); !errors.Is(err, workspacecontinuity.ErrInvalid) {
		t.Fatal("unreviewed parent identity was used", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := StageContinuityWorkspace(ctx, root, stage, inspected, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled staging wrote", err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(stage, link); err != nil {
			t.Fatal(err)
		}
		if err := StageContinuityWorkspace(t.Context(), root, link, inspected, ""); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
			t.Fatal("linked staging destination accepted", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, WorkspaceConfigFile), []byte(`{"id":"different"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := StageContinuityWorkspace(t.Context(), root, stage, inspected, ""); err == nil {
		t.Fatal("changed source staged")
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatal("refused source published staged content", err, entries)
	}
	if bytes.Equal(original, []byte(`{"id":"different"}`)) {
		t.Fatal("fixture did not change")
	}
}

func TestStageContinuityTextFilesRefusesChangedSourceAndLinkedStage(t *testing.T) {
	root, inspected, _ := continuityStageFixture(t, true)
	stage := privateContinuityStage(t)
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("changed after review"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StageContinuityTextFiles(t.Context(), root, stage, inspected); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("changed source memory entered staging", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed review published text", entries, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	root, inspected, _ = continuityStageFixture(t, true)
	stage = privateContinuityStage(t)
	outside := privateContinuityStage(t)
	if err := os.Symlink(outside, filepath.Join(stage, NotesDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := StageContinuityTextFiles(t.Context(), root, stage, inspected); err == nil {
		t.Fatal("linked notes directory accepted")
	}
	entries, err = os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("linked stage wrote outside", entries, err)
	}
}

func TestStageContinuityTextFilesRefusesUnclaimedOwnedFilesBeforeAnyWrite(t *testing.T) {
	for _, variant := range []string{"unclaimed_note", "nested_notes", "unclaimed_link"} {
		t.Run(variant, func(t *testing.T) {
			root, inspected, _ := continuityStageFixture(t, true)
			stage := privateContinuityStage(t)
			switch variant {
			case "unclaimed_note":
				if err := os.WriteFile(filepath.Join(root, NotesDir, "unsaved.md"), []byte("private extra"), 0600); err != nil {
					t.Fatal(err)
				}
			case "nested_notes":
				if err := os.Mkdir(filepath.Join(root, NotesDir, "private-extra"), 0700); err != nil {
					t.Fatal(err)
				}
			case "unclaimed_link":
				if runtime.GOOS == "windows" {
					t.Skip("symlinks require platform permissions")
				}
				if err := os.Symlink(filepath.Join(root, "MEMORY.md"), filepath.Join(root, NotesDir, "shortcut.md")); err != nil {
					t.Fatal(err)
				}
			}
			// The files are outside the signed manifest; Inspect alone cannot
			// detect them. The domain inventory must fail before staging begins.
			if _, err := workspacecontinuity.Inspect(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			if _, err := StageContinuityTextFiles(t.Context(), root, stage, inspected); err == nil {
				t.Fatal("undeclared source bytes were silently skipped")
			}
			entries, err := os.ReadDir(stage)
			if err != nil || len(entries) != 0 {
				t.Fatal("incomplete source staged text", entries, err)
			}
		})
	}
}

func TestStageContinuityWorkspaceRequiresSQLRegistrationEvidence(t *testing.T) {
	root, inspected, _ := continuityStageFixture(t, false)
	stage := privateContinuityStage(t)
	if err := StageContinuityWorkspace(t.Context(), root, stage, inspected, ""); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("file-only record was staged", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatal("file-only stage wrote bytes", err)
	}
}
