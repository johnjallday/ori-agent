package externalmemory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func geminiFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "gemini"))
	if err != nil {
		t.Fatal(err)
	}
	return copyFixture(t, root)
}

func TestGeminiSource_GlobalFileIsOneInstructionPerBlock(t *testing.T) {
	entries, problems, err := NewGeminiSource(geminiFixture(t)).Read(context.Background())
	if err != nil || len(problems) != 0 {
		t.Fatalf("err=%v problems=%+v", err, problems)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (two bullets and a paragraph)", len(entries))
	}
	for _, e := range entries {
		if e.Category != "instruction" || e.Folder != "" || e.Source != SourceGeminiCLI {
			t.Fatalf("global entry: %+v", e)
		}
	}
	find(t, entries, "Answer in the language the user writes in.")
}

func TestGeminiSource_ReadsEachListedProject(t *testing.T) {
	shapes := map[string]func(folder string) any{
		"object of projects": func(f string) any { return map[string]any{"projects": map[string]string{f: "slug"}} },
		"plain array":        func(f string) any { return []string{f} },
		"keyed by path":      func(f string) any { return map[string]any{f: map[string]string{"hash": "abc"}} },
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			root := geminiFixture(t)
			project := t.TempDir()
			mustWrite(t, filepath.Join(project, "GEMINI.md"), "- Use the repo's own test command.\n")
			data, _ := json.Marshal(shape(project))
			mustWrite(t, filepath.Join(root, "projects.json"), string(data))

			entries, problems, err := NewGeminiSource(root).Read(context.Background())
			if err != nil || len(problems) != 0 {
				t.Fatalf("err=%v problems=%+v", err, problems)
			}
			got := find(t, entries, "Use the repo's own test command.")
			if got.Folder != filepath.Clean(project) || got.Category != "instruction" {
				t.Fatalf("project entry: %+v", got)
			}
			if len(entries) != 4 {
				t.Fatalf("entries = %d, want 3 global + 1 project", len(entries))
			}
		})
	}
}

func TestGeminiSource_EmptyFileYieldsNoEntries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "GEMINI.md"), "")
	source := NewGeminiSource(root)
	if !source.Available() {
		t.Fatal("an empty GEMINI.md still makes the source available")
	}
	entries, problems, err := source.Read(context.Background())
	if err != nil || len(problems) != 0 || len(entries) != 0 {
		t.Fatalf("entries=%d problems=%+v err=%v", len(entries), problems, err)
	}
}

func TestGeminiSource_BadProjectsFileIsReportedNotFatal(t *testing.T) {
	root := geminiFixture(t)
	mustWrite(t, filepath.Join(root, "projects.json"), "{not json")
	entries, problems, err := NewGeminiSource(root).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Reason != "invalid JSON" || len(entries) != 3 {
		t.Fatalf("entries=%d problems=%+v", len(entries), problems)
	}
}

func TestGeminiSource_UnavailableWithoutEitherFile(t *testing.T) {
	source := NewGeminiSource(t.TempDir())
	if source.Available() {
		t.Fatal("empty folder reported available")
	}
	if _, _, err := source.Read(context.Background()); err == nil {
		t.Fatal("reading an unavailable source should fail it")
	}
}
