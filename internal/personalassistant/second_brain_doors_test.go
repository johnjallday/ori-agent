package personalassistant

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// Open folder opens the connected folder once per request id, for either
// origin, and under ORI_NO_DESKTOP_OPEN launches nothing and says so in the log
// (Issue 776 FR15).
func TestSecondBrainOpenFolder_OpensOncePerRequestAndHonoursTheSwitch(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	if err := h.service.OpenFolder(ctx, "local", "open-1"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("nothing connected: %v", err)
	}
	view, err := h.create(t, "My Brain")
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	h.service.SetFolderOpener(func(path string) error { opened = append(opened, path); return nil })
	for _, id := range []string{"open-1", "open-1", "open-2"} {
		if err := h.service.OpenFolder(ctx, "local", id); err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
	}
	if len(opened) != 2 || opened[0] != view.Source.Path {
		t.Fatalf("opened = %v, want the folder twice (one replay)", opened)
	}
	if err := h.service.OpenFolder(ctx, "local", "bad id with spaces"); !errors.Is(err, ErrValidation) {
		t.Fatalf("a bad id: %v", err)
	}

	// The real opener, switched off: nothing launches and the log says so.
	h.service.SetFolderOpener(platform.OpenFolder)
	t.Setenv(platform.NoDesktopOpenEnv, "1")
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	if err := h.service.OpenFolder(ctx, "local", "open-3"); err != nil {
		t.Fatalf("switched off: %v", err)
	}
	if !strings.Contains(logged.String(), "Skipped desktop launch") {
		t.Fatalf("log = %q, want the skipped launch", logged.String())
	}
}

func TestSecondBrainPreview_OnlyOrisNotesInAFolderOriSetUp(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.tokens["documents"] = h.documents()
	view, err := h.service.Create(ctx, "local", SecondBrainCreateInput{
		SelectionToken: "documents", Name: "My Brain", RequestID: "create-1",
		Project: &SecondBrainCreateProject{Name: "Launch", Answer: "Finishing the launch plan"},
	})
	h.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := h.service.Preview(ctx, "local", secondbrain.PreviewProject, "Launch")
	if err != nil || preview.Title != "Launch" || !strings.Contains(preview.Body, "Finishing the launch plan") ||
		!strings.HasPrefix(preview.Link, "obsidian://open?path=") {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	mine := filepath.Join(view.Source.Path, "Inbox", "mine.md")
	if err := os.WriteFile(mine, []byte("# Mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Preview(ctx, "local", secondbrain.PreviewInbox, "mine.md"); !errors.Is(err, ErrSecondBrainNoteIsYours) {
		t.Fatalf("the user's note: %v", err)
	}
	if _, err := h.service.Preview(ctx, "local", secondbrain.PreviewInbox, "gone.md"); !errors.Is(err, ErrSecondBrainNoteNotFound) {
		t.Fatalf("a missing note: %v", err)
	}
	if _, err := h.service.Preview(ctx, "local", secondbrain.PreviewInbox, "../x.md"); !errors.Is(err, ErrValidation) {
		t.Fatalf("a path: %v", err)
	}
}

func TestSecondBrainPreviewAndPeople_NeverReadAConnectedVault(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	if _, err := h.service.Preview(ctx, "local", secondbrain.PreviewInbox, "anything.md"); !errors.Is(err, ErrSecondBrainNoteIsYours) {
		t.Fatalf("a connected vault previewed: %v", err)
	}
	if _, err := h.service.People(ctx, "local"); !errors.Is(err, ErrSecondBrainNoInbox) {
		t.Fatalf("a connected vault's People listed: %v", err)
	}
}

func TestSecondBrainPeople_ListsTheSeededPeople(t *testing.T) {
	h := newSecondBrainHarness(t)
	h.service.SetSeeds(seedsFor(0, 2))
	if _, err := h.create(t, "My Brain"); err != nil {
		t.Fatal(err)
	}
	people, err := h.service.People(context.Background(), "local")
	if err != nil || len(people.People) != 2 || people.People[0].Name != "Dana Lee" || people.People[0].Kind != secondbrain.NoteKindSeed ||
		!strings.HasPrefix(people.People[0].Link, "obsidian://open?path=") {
		t.Fatalf("people = %+v, %v", people, err)
	}
}
