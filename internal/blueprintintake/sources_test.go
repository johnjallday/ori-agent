package blueprintintake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newSourceTestService(t *testing.T, accepted []string) (*SourceService, *workspace.FileStore, *workspace.Workspace) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Course"})
	ws.ID = "course-1"
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{IntakeRequirements: []workspace.IntakeRequirement{{
		Key: "materials", Label: "Materials", Skill: "syllabus", Sources: workspace.IntakeSources{Files: true},
		AcceptedExtensions: accepted, ProposalKinds: []string{"ticket"},
	}}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return NewSourceService(store, store), store, ws
}

func TestSourceService_AddFileCopiesParsesHashesAndPersists(t *testing.T) {
	service, store, ws := newSourceTestService(t, []string{".txt"})
	content := []byte("Quiz 1 is due on Friday.")
	result, err := service.AddFile(context.Background(), ws.ID, " Materials ", "syllabus.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if result.Status != SourceStatusParsed || result.ContentHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("result = %+v", result)
	}
	sources, err := service.ListSources(ws.ID, "materials")
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	root, _ := store.GetFolderPath(ws.ID)
	if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sources[0].FilePath))); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("copied file = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, StateDirName, filepath.FromSlash(sources[0].ParsedTextFile))); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("parsed text = %q, %v", data, err)
	}
}

func TestSourceService_AddFileRecordsIndependentStatuses(t *testing.T) {
	service, _, ws := newSourceTestService(t, []string{".txt", ".pdf"})
	tests := []struct {
		name     string
		filename string
		data     []byte
		want     string
	}{
		{name: "unsupported", filename: "malware.exe", data: []byte("not executable here"), want: SourceStatusSkipped},
		{name: "unreadable", filename: "broken.pdf", data: []byte("not a pdf"), want: SourceStatusUnreadable},
		{name: "too large", filename: "huge.txt", data: make([]byte, fileparser.MaxFileSize+1), want: SourceStatusTooLarge},
		{name: "parsed after failures", filename: "notes.txt", data: []byte("read me"), want: SourceStatusParsed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.AddFile(context.Background(), ws.ID, "materials", test.filename, bytes.NewReader(test.data))
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want {
				t.Fatalf("status = %q, want %q (%+v)", result.Status, test.want, result)
			}
		})
	}
	sources, err := service.ListSources(ws.ID, "materials")
	if err != nil || len(sources) != len(tests) {
		t.Fatalf("sources = %d, %v", len(sources), err)
	}
}

func TestSourceService_EnforcesFiftyFileCap(t *testing.T) {
	service, _, ws := newSourceTestService(t, []string{".txt"})
	for i := 0; i < MaxFilesPerIntake; i++ {
		if _, err := service.AddFile(context.Background(), ws.ID, "materials", "file.txt", bytes.NewBufferString("x")); err != nil {
			t.Fatalf("file %d: %v", i+1, err)
		}
	}
	result, err := service.AddFile(context.Background(), ws.ID, "materials", "one-too-many.txt", bytes.NewBufferString("x"))
	if !errors.Is(err, ErrFileLimit) || result.Status != SourceStatusSkipped {
		t.Fatalf("limit result = %+v, %v", result, err)
	}
	sources, err := service.ListSources(ws.ID, "materials")
	if err != nil || len(sources) != MaxFilesPerIntake {
		t.Fatalf("sources = %d, %v", len(sources), err)
	}
}
