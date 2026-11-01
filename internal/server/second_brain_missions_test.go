package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func firstWeekCompleted(builder *ServerBuilder) map[string]bool {
	done := map[string]bool{}
	for _, id := range progression.SecondBrainFirstWeekQuestIDs() {
		done[id] = builder.progressionEngine.HasCompleted(id)
	}
	return done
}

func firstWeekResolved(builder *ServerBuilder, id string) bool {
	for _, mission := range builder.progressionEngine.Status().Missions {
		if mission.ID == id {
			return mission.Status == "completed" || mission.Status == "skipped"
		}
	}
	return false
}

func newProjectWorkspace(t *testing.T, f *draftServerFixture, name string) string {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	ws.Mission = "Release the debut EP."
	if err := f.builder.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws.ID
}

func setUpSecondBrain(t *testing.T, f *draftServerFixture) {
	t.Helper()
	if _, err := f.builder.personalAssistantSecondBrain.Create(t.Context(), "local", personalassistant.SecondBrainCreateInput{RequestID: "mission-setup"}); err != nil {
		t.Fatal(err)
	}
	f.builder.personalAssistantSecondBrain.Wait()
}

// The three first-week missions complete from what Ori wrote into the folder,
// seen on the server, one kind at a time.
func TestFirstWeekMissions_CompleteFromWhatOriWrites(t *testing.T) {
	f := newDraftServerFixture(t)
	service := f.builder.personalAssistantSecondBrain
	setUpSecondBrain(t, f)
	if done := firstWeekCompleted(f.builder); done[progression.CaptureFirstNoteQuestID] || done[progression.SaveFirstJournalQuestID] || done[progression.GiveProjectQuestID] {
		t.Fatalf("a fresh setup completed a first-week mission: %v", done)
	}

	if _, err := service.Capture(t.Context(), "local", "Ask Dana about the pressing run", "mission-capture"); err != nil {
		t.Fatal(err)
	}
	if done := firstWeekCompleted(f.builder); !done[progression.CaptureFirstNoteQuestID] || done[progression.SaveFirstJournalQuestID] {
		t.Fatalf("after a capture: %v", done)
	}

	mirror := journalDailyMirror{builder: f.builder}
	if got := mirror.MirrorDaily(t.Context(), "local", "2026-11-02", "# Journal\n\nShipped the mix."); got != personalhq.DailyMirrored {
		t.Fatalf("the journal was not copied: %q", got)
	}
	if done := firstWeekCompleted(f.builder); !done[progression.SaveFirstJournalQuestID] || done[progression.GiveProjectQuestID] {
		t.Fatalf("after a journal: %v", done)
	}

	// A workspace becomes a project note through the sync, and the sync reports
	// the kind it wrote.
	project := newProjectWorkspace(t, f, "Album Launch")
	if _, err := service.Sync(t.Context(), "local", "test"); err != nil {
		t.Fatal(err)
	}
	if done := firstWeekCompleted(f.builder); !done[progression.GiveProjectQuestID] {
		t.Fatalf("after a project note for %s: %v", project, done)
	}
}

// The reconcile and the backfill read the snapshot, and the snapshot reads the
// folder: an install whose notes already hold a capture sees the mission done
// from durable state, never from the browser.
func TestFirstWeekMissions_TheSnapshotReadsTheNotesOnDisk(t *testing.T) {
	f := newDraftServerFixture(t)
	service := f.builder.personalAssistantSecondBrain
	if snapshot := f.builder.scanProgression(); snapshot.SecondBrainCaptured || snapshot.SecondBrainJournaled || snapshot.SecondBrainProject {
		t.Fatalf("nothing connected: %+v", snapshot)
	}
	setUpSecondBrain(t, f)
	if _, err := service.Capture(t.Context(), "local", "An old capture", "old-capture"); err != nil {
		t.Fatal(err)
	}

	snapshot := f.builder.scanProgression()
	if !snapshot.SecondBrainCaptured || snapshot.SecondBrainJournaled || snapshot.SecondBrainProject {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

// A vault the user connected is never written to, and a declined ask has no
// folder at all: the missions that ask Ori to write are resolved for them.
func TestFirstWeekMissions_AreSkippedWhereOriWillNotWrite(t *testing.T) {
	f := newDraftServerFixture(t)
	engine := f.builder.progressionEngine
	for _, id := range progression.SecondBrainFirstWeekQuestIDs() {
		if firstWeekResolved(f.builder, id) {
			t.Fatalf("%s starts resolved", id)
		}
	}
	skipFirstWeekMissions(engine)
	for _, id := range progression.SecondBrainFirstWeekQuestIDs() {
		if !firstWeekResolved(f.builder, id) || engine.HasCompleted(id) {
			t.Fatalf("%s: resolved %t, completed %t", id, firstWeekResolved(f.builder, id), engine.HasCompleted(id))
		}
	}
}

// The startup pass reads the status view: no decision on record resolves nothing,
// and a declined ask resolves all three. (A connected vault takes the same path
// through its origin, which the OnConnected hook and the pass both read.)
func TestFirstWeekMissions_ADeclinedAskResolvesThemAtStartup(t *testing.T) {
	f := newDraftServerFixture(t)
	engine := f.builder.progressionEngine
	view, err := f.builder.personalAssistantSecondBrain.Status(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != nil {
		t.Fatalf("nothing is connected yet: %+v", view.Source)
	}
	f.builder.skipFirstWeekWithoutASecondBrainOriWrites(engine)
	for _, id := range progression.SecondBrainFirstWeekQuestIDs() {
		if firstWeekResolved(f.builder, id) {
			t.Fatalf("%s was resolved with no decision on record", id)
		}
	}
	if _, err := f.builder.personalAssistantSecondBrain.DecideAsk(t.Context(), "local", secondbrain.DecisionNone, "ask-none"); err != nil {
		t.Fatal(err)
	}
	f.builder.skipFirstWeekWithoutASecondBrainOriWrites(engine)
	for _, id := range progression.SecondBrainFirstWeekQuestIDs() {
		if !firstWeekResolved(f.builder, id) {
			t.Fatalf("%s was not resolved after declining", id)
		}
	}
}
