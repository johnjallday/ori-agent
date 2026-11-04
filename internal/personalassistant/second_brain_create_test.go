package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func (h *secondBrainHarness) documents() string { return filepath.Dir(h.music.Path) }

func (h *secondBrainHarness) create(t *testing.T, name string) (SecondBrainView, error) {
	t.Helper()
	h.tokens["documents"] = h.documents()
	view, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{SelectionToken: "documents", Name: name, RequestID: "create-1"})
	h.service.Wait()
	return view, err
}

func seedsFor(followUps, people int) SecondBrainSeeds {
	return func(context.Context, string) (secondbrain.Seeds, error) {
		var seeds secondbrain.Seeds
		titles := []string{"Call the distributor", "Renew the lease", "Send the invoice"}
		for _, title := range titles[:followUps] {
			seeds.FollowUps = append(seeds.FollowUps, secondbrain.SeedFollowUp{Title: title})
		}
		for _, name := range []string{"Dana Lee", "Sam Ortiz"}[:people] {
			seeds.People = append(seeds.People, secondbrain.SeedPerson{Name: name, How: name + " is someone you work with."})
		}
		return seeds, nil
	}
}

func TestSecondBrainCreate_WithoutAPickUsesTheDefaultPlace(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	root := filepath.Join(filepath.Dir(h.documents()), "Ori Workspaces")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	h.service.deps.DefaultParent = func() string { return root }

	status, err := h.service.Status(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Second Brain")
	if status.SetupDefault == nil || status.SetupDefault.ParentName != "Ori Workspaces" || status.SetupDefault.Path != want {
		t.Fatalf("setup default = %+v", status.SetupDefault)
	}

	// A name sent with no pick cannot steer the folder somewhere else.
	view, err := h.service.Create(ctx, "local", SecondBrainCreateInput{Name: "../escape", RequestID: "default-1"})
	h.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if view.Source == nil || view.Source.Origin != secondbrain.OriginCreatedByOri || filepath.Base(view.Source.Path) != "Second Brain" {
		t.Fatalf("source = %+v", view.Source)
	}
	if _, err := os.Stat(filepath.Join(want, "Inbox")); err != nil {
		t.Fatalf("the layout was not written: %v", err)
	}
	if view.SetupDefault != nil {
		t.Fatalf("a connected folder still offers a default: %+v", view.SetupDefault)
	}
}

func TestSecondBrainCreate_WithoutAPickOrADefaultAsksForAPick(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.service.deps.DefaultParent = func() string { return "" }
	status, err := h.service.Status(ctx, "local")
	if err != nil || status.SetupDefault != nil {
		t.Fatalf("status = %+v, %v", status.SetupDefault, err)
	}
	if _, err := h.service.Create(ctx, "local", SecondBrainCreateInput{RequestID: "default-2"}); !errors.Is(err, ErrSecondBrainSelection) {
		t.Fatalf("err = %v, want ErrSecondBrainSelection", err)
	}
}

func TestSecondBrainCreate_ATakenDefaultPlaceIsRefusedAndLeftAlone(t *testing.T) {
	h := newSecondBrainHarness(t)
	root := t.TempDir()
	note := filepath.Join(root, "Second Brain", "mine.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.service.deps.DefaultParent = func() string { return root }
	_, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{RequestID: "default-3"})
	var folderErr *SecondBrainFolderError
	if !errors.As(err, &folderErr) {
		t.Fatalf("err = %v, want a folder error", err)
	}
	if data, readErr := os.ReadFile(note); readErr != nil || string(data) != "mine" {
		t.Fatalf("the existing note changed: %q, %v", data, readErr)
	}
}

func TestSecondBrainCreate_SetsUpConnectsAndReportsTheSeed(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.service.SetSeeds(seedsFor(2, 1))
	var learned []secondbrain.Source
	h.service.OnConnected(func(_ context.Context, _ string, source secondbrain.Source) { learned = append(learned, source) })

	view, err := h.create(t, "My Brain")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(h.documents(), "My Brain")
	if view.Status != SecondBrainConnected || view.LookID != "look-1" || view.Source.Path != wantPath ||
		view.Source.Origin != secondbrain.OriginCreatedByOri || view.Source.Kind != secondbrain.KindObsidian ||
		!strings.HasPrefix(view.Source.OpenLink, "obsidian://open?path=") {
		t.Fatalf("create = %+v", view)
	}
	if len(learned) != 1 || !learned[0].CreatedByOri() {
		t.Fatalf("OnConnected saw %+v", learned)
	}
	if got := SecondBrainKnowledgeProposal(learned[0]).Text; got != "You keep your notes in a folder Ori set up called My Brain." {
		t.Fatalf("learned fact = %q", got)
	}

	look, err := h.service.Look(ctx, "local")
	if err != nil || look.State != secondbrain.LookFinished || look.Result.Headline != "Your notes are set up: 2 follow-ups in Inbox, 1 person." {
		t.Fatalf("look = %+v, %v", look, err)
	}
	for _, rel := range []string{"README.md", ".obsidian/app.json", "Inbox/call-the-distributor.md", "Inbox/renew-the-lease.md", "People/Dana Lee.md", "Daily/README.md"} {
		if _, err := os.Stat(filepath.Join(wantPath, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// The follow-ups were seeded from the user's own; adding them again would
	// only duplicate them.
	accepted, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{Suggested: true, RequestID: "accept-1"})
	if err != nil || accepted.Created != 0 {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	if all, _ := h.followUps.List(ctx, followup.Filter{UserID: "local"}); len(all) != 0 {
		t.Fatalf("seeded items became follow-ups: %d", len(all))
	}

	status, err := h.service.Status(ctx, "local")
	if err != nil || status.Source.Origin != secondbrain.OriginCreatedByOri {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestSecondBrainCreate_AnEmptySeedIsReportedHonestly(t *testing.T) {
	h := newSecondBrainHarness(t)
	if _, err := h.create(t, "Fresh Brain"); err != nil {
		t.Fatal(err)
	}
	look, err := h.service.Look(context.Background(), "local")
	if err != nil || look.Result.Headline != secondbrain.EmptySetupHeadline {
		t.Fatalf("look = %+v, %v", look, err)
	}
	// Nothing to decide: the look is acted at finish and never waits (Issue 776 FR10).
	status, err := h.service.Status(context.Background(), "local")
	if err != nil || status.LastLook == nil || !status.LastLook.Acted {
		t.Fatalf("last look = %+v, %v", status.LastLook, err)
	}
}

func TestSecondBrainCreate_ASeededOnlyLookIsActedAtFinish(t *testing.T) {
	h := newSecondBrainHarness(t)
	h.service.SetSeeds(seedsFor(2, 1))
	if _, err := h.create(t, "Seeded Brain"); err != nil {
		t.Fatal(err)
	}
	status, err := h.service.Status(context.Background(), "local")
	if err != nil || status.LastLook == nil || !status.LastLook.Acted || status.LastLook.OpenItems != 2 {
		t.Fatalf("last look = %+v, %v", status.LastLook, err)
	}
	// Today then shows no result row, only the Done line.
	today := &TodayService{secondBrain: h.service}
	_, needs, done := today.secondBrainItems(context.Background(), "local", "personal-hq", true, *h.clock)
	for _, item := range needs {
		if item.Kind == "second_brain_result" {
			t.Fatalf("a look with nothing to decide waits under Needs you: %+v", item)
		}
	}
	if len(done) == 0 || done[0].Title != "Looked at Seeded Brain · 2 open items" {
		t.Fatalf("done = %+v", done)
	}
}

func TestSecondBrainCreate_WritesTheProjectAsTheFirstNoteAndNamesIt(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	var wrote []map[string]int
	h.service.OnWritten(func(_ context.Context, _ string, kinds map[string]int) { wrote = append(wrote, kinds) })
	h.tokens["documents"] = h.documents()
	view, err := h.service.Create(ctx, "local", SecondBrainCreateInput{
		SelectionToken: "documents", Name: "My Brain", RequestID: "create-1",
		Project: &SecondBrainCreateProject{Name: "Launch", Answer: "  Finishing the launch plan\n by Friday "},
	})
	h.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(view.Source.Path, "Projects", "Launch", "README.md")
	data, err := os.ReadFile(note) // #nosec G304 -- the test's own temp tree
	if err != nil || !strings.Contains(string(data), "\nFinishing the launch plan by Friday\n") {
		t.Fatalf("project note = %q, %v", data, err)
	}
	look, err := h.service.Look(ctx, "local")
	if err != nil || look.Result.Headline != "Your notes are set up. I wrote your first note: Launch." {
		t.Fatalf("look = %+v, %v", look, err)
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if status.FillIn == nil || status.FillIn.ProjectAsk != secondbrain.ProjectAnswered {
		t.Fatalf("fill_in = %+v", status.FillIn)
	}
	if status.WrittenByOri == nil || status.WrittenByOri.ByKind[secondbrain.NoteKindProject] != 1 {
		t.Fatalf("written_by_ori = %+v", status.WrittenByOri)
	}
	if status.SetupPreview != nil {
		t.Fatalf("a connected status carries a setup preview: %+v", status.SetupPreview)
	}
	if len(wrote) != 1 || wrote[0][secondbrain.NoteKindProject] != 1 {
		t.Fatalf("wrote = %v", wrote)
	}
}

func TestSecondBrainCreate_WithoutAProjectRecordsTheQuestionSkipped(t *testing.T) {
	h := newSecondBrainHarness(t)
	if _, err := h.create(t, "My Brain"); err != nil {
		t.Fatal(err)
	}
	status, err := h.service.Status(context.Background(), "local")
	if err != nil || status.FillIn == nil || status.FillIn.ProjectAsk != secondbrain.ProjectSkipped {
		t.Fatalf("fill_in = %+v, %v", status.FillIn, err)
	}
}

func TestSecondBrainCreate_ABadProjectIsRefusedBeforeAnythingIsMade(t *testing.T) {
	h := newSecondBrainHarness(t)
	h.tokens["documents"] = h.documents()
	before := snapshotTree(t, h.documents())
	long := strings.Repeat("a", SecondBrainProjectAnswerMax+1)
	for name, tc := range map[string]struct {
		project SecondBrainCreateProject
		want    error
	}{
		"a path":       {SecondBrainCreateProject{Name: "../Out", Answer: "x"}, ErrSecondBrainProjectName},
		"a slash":      {SecondBrainCreateProject{Name: "a/b", Answer: "x"}, ErrSecondBrainProjectName},
		"empty answer": {SecondBrainCreateProject{Name: "Launch", Answer: " "}, ErrValidation},
		"too long":     {SecondBrainCreateProject{Name: "Launch", Answer: long}, ErrValidation},
		"a secret":     {SecondBrainCreateProject{Name: "Launch", Answer: "ghp_abcdefghijklmnop"}, ErrValidation},
	} {
		t.Run(name, func(t *testing.T) {
			project := tc.project
			_, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{
				SelectionToken: "documents", Name: "My Brain", RequestID: "create-" + strings.ReplaceAll(name, " ", "-"), Project: &project,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if after := snapshotTree(t, h.documents()); after != before {
				t.Fatalf("a refused project changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestSecondBrainStatus_PreviewsWhatASetupWouldSeed(t *testing.T) {
	h := newSecondBrainHarness(t)
	status, err := h.service.Status(context.Background(), "local")
	if err != nil || status.SetupPreview == nil || *status.SetupPreview != (SecondBrainSetupPreview{}) {
		t.Fatalf("nothing held previews %+v, %v", status.SetupPreview, err)
	}
	h.service.SetSeeds(seedsFor(3, 2))
	status, err = h.service.Status(context.Background(), "local")
	if err != nil || status.SetupPreview == nil || status.SetupPreview.FollowUps != 3 || status.SetupPreview.People != 2 || status.SetupPreview.Project {
		t.Fatalf("preview = %+v, %v", status.SetupPreview, err)
	}
}

func TestSecondBrainCreate_AReplayReturnsTheFirstCallAndMakesNothingMore(t *testing.T) {
	h := newSecondBrainHarness(t)
	first, err := h.create(t, "My Brain")
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, h.documents())
	replay, err := h.create(t, "My Brain")
	if err != nil || replay.LookID != first.LookID || replay.Source.Path != first.Source.Path {
		t.Fatalf("replay = %+v, %v; want %+v", replay, err, first)
	}
	if after := snapshotTree(t, h.documents()); after != before {
		t.Fatalf("a replay changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := h.service.Connect(context.Background(), "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "create-1"}); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("the id reused for another action: %v", err)
	}
}

func TestSecondBrainCreate_RefusesWithOneSentenceAndLeavesTheTreeAlone(t *testing.T) {
	h := newSecondBrainHarness(t)
	h.tokens["documents"] = h.documents()
	before := snapshotTree(t, h.documents())

	check := func(name string, input SecondBrainCreateInput, want string) {
		t.Helper()
		_, err := h.service.Create(context.Background(), "local", input)
		var folderErr *SecondBrainFolderError
		switch {
		case want == "":
			t.Errorf("%s: want an error", name)
		case !errors.As(err, &folderErr) || folderErr.Message != want:
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
	// "Second Brain" is the Music vault the harness already put there.
	check("a target with content", SecondBrainCreateInput{SelectionToken: "documents", Name: "Second Brain", RequestID: "c-1"},
		"Second Brain already has something in it. Choose another name or location.")
	for _, name := range []string{"../escape", "a/b", ".hidden", ""} {
		check("name "+name, SecondBrainCreateInput{SelectionToken: "documents", Name: name, RequestID: "c-2"},
			"Choose a simple folder name, without slashes.")
	}
	h.tokens["home"] = filepath.Dir(h.documents())
	_, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{SelectionToken: "home", Name: "Brain", RequestID: "c-3"})
	var folderErr *SecondBrainFolderError
	if !errors.As(err, &folderErr) {
		t.Errorf("the home folder as the parent: %v", err)
	}
	if _, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{SelectionToken: "gone", Name: "Brain", RequestID: "c-4"}); !errors.Is(err, ErrSecondBrainSelection) {
		t.Errorf("an expired token: %v", err)
	}
	if _, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{SelectionToken: "documents", Name: "Brain", RequestID: "has space"}); !errors.Is(err, ErrValidation) {
		t.Errorf("a bad request id: %v", err)
	}
	if _, err := h.service.Create(context.Background(), "local", SecondBrainCreateInput{Name: "Brain", RequestID: "c-5"}); !errors.Is(err, ErrSecondBrainSelection) {
		t.Errorf("no token and no default place: %v", err)
	}
	if after := snapshotTree(t, h.documents()); after != before {
		t.Fatalf("a refusal changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if status, _ := h.service.Status(context.Background(), "local"); status.Status != SecondBrainNotConnected {
		t.Fatalf("a refused setup connected something: %+v", status)
	}
}

func TestSecondBrainCreate_RefusesWhileAFolderIsConnected(t *testing.T) {
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	before := snapshotTree(t, h.documents())
	if _, err := h.create(t, "My Brain"); !errors.Is(err, ErrSecondBrainAlreadyConnected) {
		t.Fatalf("err = %v, want ErrSecondBrainAlreadyConnected", err)
	}
	if after := snapshotTree(t, h.documents()); after != before {
		t.Fatalf("the refusal changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSecondBrainCreate_AnUnreadableSeedDoesNotStopTheFolder(t *testing.T) {
	h := newSecondBrainHarness(t)
	h.service.SetSeeds(func(context.Context, string) (secondbrain.Seeds, error) {
		return secondbrain.Seeds{FollowUps: []secondbrain.SeedFollowUp{{Title: "dropped"}}}, errors.New("database is locked")
	})
	if _, err := h.create(t, "My Brain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.documents(), "My Brain", "Inbox", "dropped.md")); err == nil {
		t.Fatal("seeds from a failed read were used")
	}
}

func TestSecondBrainCreate_DisconnectKeepsTheFolderAndEndsTheWritePermission(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.service.SetSeeds(seedsFor(1, 0))
	if _, err := h.create(t, "My Brain"); err != nil {
		t.Fatal(err)
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, filepath.Join(h.documents(), "My Brain"))
	if _, err := h.service.Disconnect(ctx, "local", "disconnect-1", status.Version); err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, filepath.Join(h.documents(), "My Brain")); after != before {
		t.Fatalf("disconnecting changed the folder.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, connected, _ := h.service.ConnectedSource(ctx, "local"); connected {
		t.Fatal("still connected")
	}
}

// snapshotTree lists every entry under dir with its size, so two equal
// snapshots mean nothing was added, removed or rewritten.
func snapshotTree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		lines = append(lines, rel+" "+info.Mode().String()+" "+strconv.FormatInt(size, 10))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}
