package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type cannedLinkFetcher struct {
	snapshot LinkSnapshot
	calls    int
}

func (f *cannedLinkFetcher) Fetch(context.Context, string) (LinkSnapshot, error) {
	f.calls++
	return f.snapshot, nil
}

type fixedSelection struct{ path string }

func (s fixedSelection) ResolveFor(token, scope string) (string, error) {
	if token != "trusted" || scope != "course-1" {
		return "", errors.New("not found")
	}
	return s.path, nil
}

func newCollectionService(t *testing.T) (*SourceService, *workspace.FileStore, *workspace.Workspace) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Course"})
	ws.ID = "course-1"
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{IntakeRequirements: []workspace.IntakeRequirement{{Key: "materials", Label: "Materials", Skill: "syllabus", Sources: workspace.IntakeSources{Files: true, URL: true, DirectoryKey: "course-folder"}, AcceptedExtensions: []string{".txt"}, ProposalKinds: []string{"ticket"}}}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	service := NewSourceService(store, store)
	service.SetProviderResolver(func(context.Context, string) (ModelProvider, error) { return ModelProvider{Name: "openai"}, nil })
	return service, store, ws
}

func acceptCollectionConsent(t *testing.T, service *SourceService, ws *workspace.Workspace) {
	t.Helper()
	if _, err := service.AcceptConsent(context.Background(), ws.ID, "materials", "user"); err != nil {
		t.Fatal(err)
	}
}

func TestAddLinkRequiresConsentAndStoresExtractedSnapshot(t *testing.T) {
	service, store, ws := newCollectionService(t)
	fetcher := &cannedLinkFetcher{snapshot: LinkSnapshot{URL: "https://example.com/syllabus", Title: "Syllabus", Content: "Quiz 1 is October 1", Body: []byte("<html>Quiz 1 is October 1</html>"), ContentType: "text/html"}}
	service.SetLinkFetcher(fetcher)
	if _, err := service.AddLink(context.Background(), ws.ID, "materials", "https://example.com/syllabus"); !errors.Is(err, ErrConsentNeeded) {
		t.Fatalf("error before consent = %v", err)
	}
	if fetcher.calls != 0 {
		t.Fatal("link was fetched before consent")
	}
	acceptCollectionConsent(t, service, ws)
	result, err := service.AddLink(context.Background(), ws.ID, "materials", "https://example.com/syllabus")
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "link" || result.Title != "Syllabus" || result.ContentHash == "" || fetcher.calls != 1 {
		t.Fatalf("result = %+v, calls=%d", result, fetcher.calls)
	}
	sources, err := service.ListSources(ws.ID, "materials")
	if err != nil || len(sources) != 1 || sources[0].FetchedAt == nil {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	text, err := service.ReadParsedText(ws.ID, sources[0])
	if err != nil || text != "Quiz 1 is October 1" {
		t.Fatalf("snapshot text = %q, %v", text, err)
	}
	root, _ := store.GetFolderPath(ws.ID)
	page, err := os.ReadFile(filepath.Join(root, StateDirName, filepath.FromSlash(sources[0].SnapshotFile)))
	if err != nil || string(page) != "<html>Quiz 1 is October 1</html>" {
		t.Fatalf("page snapshot = %q, %v", page, err)
	}
}

func TestAddLinkEnforcesTenLinkLimit(t *testing.T) {
	service, _, ws := newCollectionService(t)
	acceptCollectionConsent(t, service, ws)
	service.SetLinkFetcher(&cannedLinkFetcher{snapshot: LinkSnapshot{URL: "https://example.com/page", Title: "Page", Content: "content"}})
	for index := 0; index < MaxLinksPerIntake; index++ {
		if _, err := service.AddLink(context.Background(), ws.ID, "materials", "https://example.com/page"); err != nil {
			t.Fatalf("link %d: %v", index+1, err)
		}
	}
	if _, err := service.AddLink(context.Background(), ws.ID, "materials", "https://example.com/extra"); !errors.Is(err, ErrLinkLimit) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestAddFolderReportsImmediateFilesLeftOutByCap(t *testing.T) {
	service, _, ws := newCollectionService(t)
	acceptCollectionConsent(t, service, ws)
	folder := t.TempDir()
	for index := 0; index < MaxFilesPerIntake+2; index++ {
		name := filepath.Join(folder, fmt.Sprintf("file-%02d.txt", index))
		if err := os.WriteFile(name, []byte("text"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service.SetPathSelections(fixedSelection{path: folder})
	result, err := service.AddFolder(context.Background(), ws.ID, "materials", "trusted")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != MaxFilesPerIntake || result.Omitted != 2 || !strings.Contains(result.Folder.Message, "2 immediate") {
		t.Fatalf("result = files:%d omitted:%d folder:%+v", len(result.Files), result.Omitted, result.Folder)
	}
}

func TestAddFolderReadsOnlyImmediateContainedFiles(t *testing.T) {
	service, store, ws := newCollectionService(t)
	acceptCollectionConsent(t, service, ws)
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "inside.txt"), []byte("inside text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "nested", "ignored.txt"), []byte("nested secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(folder, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	service.SetPathSelections(fixedSelection{path: folder})
	result, err := service.AddFolder(context.Background(), ws.ID, "materials", "trusted")
	if err != nil {
		t.Fatal(err)
	}
	if result.Folder.Kind != "folder" || len(result.Files) != 2 {
		t.Fatalf("folder result = %+v", result)
	}
	statuses := map[string]string{}
	for _, file := range result.Files {
		statuses[file.Name] = file.Status
	}
	if statuses["inside.txt"] != SourceStatusParsed || statuses["escape.txt"] != SourceStatusSkipped {
		t.Fatalf("statuses = %+v", statuses)
	}
	sources, err := service.ListSources(ws.ID, "materials")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Name == "ignored.txt" {
			t.Fatal("nested file was collected")
		}
		if source.ParsedTextFile != "" {
			text, readErr := service.ReadParsedText(ws.ID, source)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(text, "outside secret") || strings.Contains(text, "nested secret") {
				t.Fatalf("escaped content was read: %q", text)
			}
		}
	}
	root, _ := store.GetFolderPath(ws.ID)
	if _, err := os.Stat(filepath.Join(root, workspace.FilesDir, UploadedFilesDir)); !os.IsNotExist(err) {
		t.Fatalf("folder source raw files were copied into workspace: %v", err)
	}
}
