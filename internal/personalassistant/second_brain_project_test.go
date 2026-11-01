package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func projectInput(name, answer, requestID string) SecondBrainProjectInput {
	return SecondBrainProjectInput{Name: name, Answer: answer, RequestID: requestID}
}

func TestSecondBrainProject_WritesTheAnswerAsTheReadmeAndRemembersItWasAnswered(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	var wrote atomic.Int32
	h.service.OnWritten(func(_ context.Context, _ string, kinds map[string]int) {
		wrote.Add(int32(kinds[secondbrain.NoteKindProject]))
	})

	note, err := h.service.WriteProject(ctx, "local", projectInput("Launch", "  Finishing the\nLaunch album ", "project-1"))
	if err != nil || note.Name != "Launch" || note.Rel != "Projects/Launch/README.md" || !strings.HasPrefix(note.Link, "obsidian://open?path=") {
		t.Fatalf("note = %+v, %v", note, err)
	}
	content := readTestFile(t, brain, "Projects", "Launch", "README.md")
	mark := secondbrain.ReadMark([]byte(content))
	if !mark.OwnedByOri || mark.Kind != secondbrain.NoteKindProject {
		t.Fatalf("mark = %+v", mark)
	}
	// The answer is the first paragraph, in the user's words folded to one line.
	if !strings.Contains(content, "# Launch\n\nFinishing the Launch album\n") || !strings.Contains(content, "## How Ori knows this\n\n- You told Ori this, ") {
		t.Fatalf("note:\n%s", content)
	}
	if wrote.Load() != 1 {
		t.Fatalf("the mission hook saw %d project writes", wrote.Load())
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil || status.FillIn.ProjectAsk != "answered" {
		t.Fatalf("fill_in = %+v, %v", status.FillIn, err)
	}
	if report, found := h.service.WrittenReport(ctx, "local"); !found || !strings.Contains(report.Sentence, "a project") {
		t.Fatalf("report = %+v, %v", report, found)
	}
	// It is offered as a project to make a workspace from.
	offered := false
	for _, project := range status.Projects {
		offered = offered || project.Name == "Launch"
	}
	if !offered {
		t.Fatalf("the new project is not offered as a workspace: %+v", status.Projects)
	}

	// A replay returns the same note and writes nothing.
	before := snapshotTree(t, brain)
	again, err := h.service.WriteProject(ctx, "local", projectInput("Launch", "Finishing the Launch album", "project-1"))
	if err != nil || again != note || snapshotTree(t, brain) != before || wrote.Load() != 1 {
		t.Fatalf("replay = %+v, %v (writes %d)", again, err, wrote.Load())
	}
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "project-1"}); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("the id reused for another action: %v", err)
	}
}

// A workspace made later with the project's name replaces the answer's note with
// its own README: one file, not a second (FR22).
func TestSecondBrainProject_AWorkspaceOfTheSameNameUpdatesTheSameReadme(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	if _, err := h.service.WriteProject(ctx, "local", projectInput("Launch", "Finishing the Launch album.", "project-1")); err != nil {
		t.Fatal(err)
	}
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) {
		return secondbrain.Holdings{Projects: []secondbrain.HeldProject{{ID: "ws-launch", Name: "Launch", Goal: "Ship the album in June."}}}, nil
	})
	result, err := h.service.Sync(ctx, "local", "test")
	if err != nil || result.Written[secondbrain.NoteKindProject] != 1 {
		t.Fatalf("Sync = %+v, %v", result, err)
	}
	content := readTestFile(t, brain, "Projects", "Launch", "README.md")
	if !strings.Contains(content, "Ship the album in June.") || strings.Contains(content, "Finishing the Launch album.") {
		t.Fatalf("the README was not updated in place:\n%s", content)
	}
	entries, err := os.ReadDir(filepath.Join(brain, "Projects"))
	if err != nil {
		t.Fatal(err)
	}
	folders := 0
	for _, entry := range entries {
		if entry.IsDir() {
			folders++
		}
	}
	if folders != 1 {
		t.Fatalf("Projects holds %d folders, want one", folders)
	}
	// And it is no longer offered as a project to make a workspace from.
	status, err := h.service.Status(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range status.Projects {
		if project.Name == "Launch" {
			t.Fatalf("a project that is a workspace is still offered: %+v", status.Projects)
		}
	}
}

func TestSecondBrainProject_RefusesWhatItShouldNeverWrite(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	for name, tc := range map[string]struct {
		input SecondBrainProjectInput
		want  error
	}{
		"a name with a slash":    {projectInput("../Evil", "An answer.", "p-1"), ErrSecondBrainProjectName},
		"a nested name":          {projectInput("a/b", "An answer.", "p-2"), ErrSecondBrainProjectName},
		"a hidden name":          {projectInput(".hidden", "An answer.", "p-3"), ErrSecondBrainProjectName},
		"an empty name":          {projectInput("  ", "An answer.", "p-4"), ErrSecondBrainProjectName},
		"an empty answer":        {projectInput("Launch", " \n ", "p-5"), ErrValidation},
		"a secret for an answer": {projectInput("Launch", "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789", "p-6"), ErrValidation},
		"a bad request id":       {projectInput("Launch", "An answer.", "has a space"), ErrValidation},
	} {
		before := snapshotTree(t, brain)
		if _, err := h.service.WriteProject(ctx, "local", tc.input); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
		if snapshotTree(t, brain) != before {
			t.Errorf("%s changed the tree", name)
		}
	}
	// A README the user already has there is theirs.
	writeNoteFile(t, brain, "Projects/Taken/README.md", "# Mine\n")
	before := snapshotTree(t, brain)
	if _, err := h.service.WriteProject(ctx, "local", projectInput("Taken", "An answer.", "p-7")); !errors.Is(err, secondbrain.ErrUserModified) {
		t.Fatalf("over the user's README = %v", err)
	}
	if snapshotTree(t, brain) != before {
		t.Fatal("the user's README changed")
	}
}

func TestSecondBrainProject_OnlyWhereOriMayWrite(t *testing.T) {
	ctx := context.Background()
	if _, err := newSecondBrainHarness(t).service.WriteProject(ctx, "local", projectInput("Launch", "An answer.", "p-1")); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("nothing connected: %v", err)
	}
	connected := newSecondBrainHarness(t)
	connected.connectVault(t)
	if _, err := connected.service.WriteProject(ctx, "local", projectInput("Launch", "An answer.", "p-1")); !errors.Is(err, secondbrain.ErrNotOwned) {
		t.Fatalf("a connected vault: %v", err)
	}
	// An older setup that has not yet allowed project notes is asked first.
	h, brain := createdHarness(t)
	beforeTheFillIn(t, h)
	before := snapshotTree(t, brain)
	if _, err := h.service.WriteProject(ctx, "local", projectInput("Launch", "An answer.", "p-1")); !errors.Is(err, ErrSecondBrainFillInNotAllowed) {
		t.Fatalf("before the consent answer: %v", err)
	}
	if snapshotTree(t, brain) != before {
		t.Fatal("a refused project wrote something")
	}
	if _, err := h.service.FillInDecision(ctx, "local", "allow", "fi-1"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	if _, err := h.service.WriteProject(ctx, "local", projectInput("Launch", "An answer.", "p-2")); err != nil {
		t.Fatalf("after the consent answer: %v", err)
	}
}

func TestSecondBrainProject_SkipIsRecordedOnceAndNeverAsksAgain(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	before := snapshotTree(t, brain)
	view, err := h.service.SkipProject(ctx, "local", "skip-1")
	if err != nil || view.FillIn == nil || view.FillIn.ProjectAsk != "skipped" {
		t.Fatalf("skip = %+v, %v", view.FillIn, err)
	}
	if replay, err := h.service.SkipProject(ctx, "local", "skip-1"); err != nil || replay.FillIn.ProjectAsk != "skipped" {
		t.Fatalf("replay = %+v, %v", replay.FillIn, err)
	}
	if snapshotTree(t, brain) != before {
		t.Fatal("a skip wrote to the notes")
	}
	// An answer after a skip stands, and a skip after an answer does not take it back.
	if _, err := h.service.WriteProject(ctx, "local", projectInput("Launch", "An answer.", "project-1")); err != nil {
		t.Fatal(err)
	}
	if again, err := h.service.SkipProject(ctx, "local", "skip-2"); err != nil || again.FillIn.ProjectAsk != "answered" {
		t.Fatalf("a second skip = %+v, %v", again.FillIn, err)
	}
	if _, err := h.service.SkipProject(ctx, "local", "has a space"); !errors.Is(err, ErrValidation) {
		t.Fatalf("a bad id = %v", err)
	}
	if _, err := newSecondBrainHarness(t).service.SkipProject(ctx, "local", "skip-3"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("nothing connected = %v", err)
	}
	connected := newSecondBrainHarness(t)
	connected.connectVault(t)
	if _, err := connected.service.SkipProject(ctx, "local", "skip-4"); !errors.Is(err, secondbrain.ErrNotOwned) {
		t.Fatalf("a connected vault = %v", err)
	}
}

func writeNoteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
