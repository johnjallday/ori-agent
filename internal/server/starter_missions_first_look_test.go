package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// firstLookContext is the template context the starter-task seeding stamps on
// a folder's first look.
func firstLookContext() map[string]any {
	return map[string]any{"template_id": personalassistant.FolderFirstTaskTemplateID, "template_starter_task": true}
}

// addTask appends a task to a saved workspace and returns the stored copy.
func (s *starterStore) addTask(ws *workspace.Workspace, task workspace.Task) *workspace.Workspace {
	s.t.Helper()
	task.WorkspaceID = ws.ID
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	ws.Tasks = append(ws.Tasks, task)
	if err := s.store.Save(ws); err != nil {
		s.t.Fatalf("save %s: %v", ws.ID, err)
	}
	saved, err := s.store.Get(ws.ID)
	if err != nil {
		s.t.Fatalf("get %s: %v", ws.ID, err)
	}
	return saved
}

// seededFirstLook is a first look waiting for its start.
func seededFirstLook() workspace.Task {
	return workspace.Task{
		ID: "first-look", Description: "Tell me what is in this folder", To: "Researcher",
		Status: workspace.TaskStatusAssigned, TicketState: workspace.TicketStateReady,
		Context: firstLookContext(),
	}
}

// finishedFirstLook is a first look whose run succeeded: in Review, with a
// result.
func finishedFirstLook(result string) workspace.Task {
	task := seededFirstLook()
	done := time.Now().UTC()
	task.Status, task.TicketState = workspace.TaskStatusCompleted, workspace.TicketStateReview
	task.Result, task.CompletedAt = result, &done
	return task
}

func taskCompleted(workspaceID, taskID string, data map[string]any) workspace.Event {
	if data == nil {
		data = map[string]any{}
	}
	return workspace.NewTaskEvent(workspace.EventTaskCompleted, workspaceID, taskID, "Researcher", data)
}

// The mission pays for the agent's result on a folder's first look, and for
// nothing else that happens to finish (FR9).
func TestFolderFirstTaskFinished(t *testing.T) {
	s := newStarterStore(t)
	folder := s.addTask(s.add("ws-thesis", "Thesis", "writing-project", "", nil), seededFirstLook())
	folder = s.addTask(folder, workspace.Task{
		ID: "other", Description: "Draft chapter two", To: "Researcher", Status: workspace.TaskStatusAssigned,
	})
	// Another blueprint's own starter task is not a first look at a folder.
	setup := s.addTask(s.add("ws-setup", "Album", "reaper-song", "", nil), workspace.Task{
		ID: "setup", Description: "Set up the session", To: "Producer", Status: workspace.TaskStatusAssigned,
		Context: map[string]any{"template_id": "reaper-song", "template_starter_task": true},
	})
	// A finished look whose event carries no result: the stored one counts.
	stored := s.addTask(s.add("ws-notes", "Notes", "", "", nil), finishedFirstLook("Forty notes, six this week."))
	// A run that finished with nothing to read.
	empty := s.addTask(s.add("ws-empty", "Empty", "", "", nil), seededFirstLook())

	cases := []struct {
		name string
		ev   workspace.Event
		want bool
	}{
		{"the first look finished with a result", taskCompleted(folder.ID, "first-look", map[string]any{"result": "Three drafts."}), true},
		{"started by hand from the workspace page", taskCompleted(folder.ID, "first-look", map[string]any{"result": "Three drafts.", "manual": true}), true},
		{"the result is only on the stored task", taskCompleted(stored.ID, "first-look", nil), true},
		{"a blank result in the event falls back to the stored task", taskCompleted(stored.ID, "first-look", map[string]any{"result": "  "}), true},
		{"finished with no result anywhere", taskCompleted(empty.ID, "first-look", map[string]any{"result": ""}), false},
		{"another task in the same workspace", taskCompleted(folder.ID, "other", map[string]any{"result": "Done."}), false},
		{"another blueprint's starter task", taskCompleted(setup.ID, "setup", map[string]any{"result": "Done."}), false},
		{"a task id the workspace does not have", taskCompleted(folder.ID, "missing", map[string]any{"result": "Done."}), false},
		{"a workspace that does not exist", taskCompleted("ws-gone", "first-look", map[string]any{"result": "Done."}), false},
		{"no workspace on the event", taskCompleted("", "first-look", map[string]any{"result": "Done."}), false},
		{"a task that only started", workspace.NewTaskEvent(workspace.EventTaskStarted, folder.ID, "first-look", "Researcher", nil), false},
		{"a task that failed", workspace.NewTaskEvent(workspace.EventTaskFailed, folder.ID, "first-look", "Researcher", map[string]any{"error": "boom"}), false},
		{"no task id", workspace.Event{Type: workspace.EventTaskCompleted, WorkspaceID: folder.ID, Data: map[string]any{"result": "Done."}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := folderFirstTaskFinished(s.store, tc.ev); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
	if folderFirstTaskFinished(nil, taskCompleted(folder.ID, "first-look", map[string]any{"result": "Three drafts."})) {
		t.Fatal("a nil source reported a finished first look")
	}
}

func TestCompleteProgressionWiring_FirstLookCompletesTheMissionOnce(t *testing.T) {
	s := newStarterStore(t)
	folder := s.addTask(s.add("ws-thesis", "Thesis", "writing-project", "", nil), seededFirstLook())
	folder = s.addTask(folder, workspace.Task{
		ID: "other", Description: "Draft chapter two", To: "Researcher", Status: workspace.TaskStatusAssigned,
	})
	bus := workspace.NewEventBus(8, 16)
	t.Cleanup(bus.Shutdown)

	fires := make(chan string, 8)
	engine := progression.New(nil,
		progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithOnComplete(func(q progression.Quest) { fires <- q.ID }),
	)
	b := &ServerBuilder{
		workspaceFileStore: s.store, eventBus: bus, progressionEngine: engine,
		onboardingMgr: onboarding.NewManager(filepath.Join(t.TempDir(), "app_state.json")),
	}
	b.completeProgressionWiring()
	if engine.HasCompleted(progression.FolderFirstLookQuestID) {
		t.Fatal("a seeded first look completed the mission before it ran")
	}

	// Some other task finishing is not the first look.
	bus.Publish(taskCompleted(folder.ID, "other", map[string]any{"result": "Done."}))
	select {
	case id := <-fires:
		t.Fatalf("an unrelated task completed %s", id)
	case <-time.After(200 * time.Millisecond):
	}

	// The agent finishes its look. The run records the result, then publishes.
	finished := finishedFirstLook("Three drafts.")
	for i := range folder.Tasks {
		if folder.Tasks[i].ID == "first-look" {
			finished.WorkspaceID, finished.CreatedAt = folder.ID, folder.Tasks[i].CreatedAt
			folder.Tasks[i] = finished
		}
	}
	if err := s.store.Save(folder); err != nil {
		t.Fatal(err)
	}
	event := taskCompleted(folder.ID, "first-look", map[string]any{"result": "Three drafts.", "manual": true})
	bus.Publish(event)
	bus.Publish(event)

	select {
	case id := <-fires:
		if id != progression.FolderFirstLookQuestID {
			t.Fatalf("completed %s, want See what your assistant found", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a finished first look did not complete the mission")
	}
	select {
	case id := <-fires:
		t.Fatalf("a repeated event completed %s again", id)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAnyFolderFirstTaskFinished(t *testing.T) {
	if anyFolderFirstTaskFinished(nil) {
		t.Fatal("a nil source found a finished first look")
	}

	s := newStarterStore(t)
	s.addTask(s.add("ws-seeded", "Seeded", "", "", nil), seededFirstLook())
	// A blueprint's own task finishing is not evidence.
	s.addTask(s.add("ws-album", "Album", "reaper-song", "", nil), workspace.Task{
		ID: "setup", Status: workspace.TaskStatusCompleted, TicketState: workspace.TicketStateDone, Result: "Done.",
		Context: map[string]any{"template_id": "reaper-song", "template_starter_task": true},
	})
	// Someone else's workspace, and a trashed one, never count.
	s.addTask(s.add("ws-foreign", "Theirs", "", "another-user", nil), finishedFirstLook("Theirs."))
	trashed := s.addTask(s.add("ws-trashed", "Old", "", "", nil), finishedFirstLook("Old."))
	trashed.Status = workspace.StatusTrashed
	if err := s.store.Save(trashed); err != nil {
		t.Fatal(err)
	}
	if anyFolderFirstTaskFinished(s.store) {
		t.Fatal("found a finished first look where none counts")
	}

	s.addTask(s.add("ws-thesis", "Thesis", "writing-project", "", nil), finishedFirstLook("Three drafts."))
	if !anyFolderFirstTaskFinished(s.store) {
		t.Fatal("a finished first look in an active workspace was not found")
	}
}

// An install that ran a folder's first look before the mission existed sees it
// done after the upgrade: once, silently (so no Craft), and never again (FR12).
func TestCompleteProgressionWiring_FolderFirstLookReconcile(t *testing.T) {
	cases := []struct {
		name string
		task workspace.Task
		want bool
	}{
		{"a first look that already finished", finishedFirstLook("Three drafts."), true},
		{"a first look still waiting", seededFirstLook(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := onboarding.NewManager(filepath.Join(t.TempDir(), "app_state.json"))
			state := mgr.GetProgression()
			state.BackfilledAt = time.Now().Add(-90 * 24 * time.Hour)
			state.CompletedQuests = map[string]time.Time{
				progression.MeetAssistantQuestID: time.Now().Add(-80 * 24 * time.Hour),
				progression.ShowFolderQuestID:    time.Now().Add(-70 * 24 * time.Hour),
			}
			if err := mgr.SetProgression(state); err != nil {
				t.Fatal(err)
			}
			s := newStarterStore(t)
			s.addTask(s.add("ws-thesis", "Thesis", "writing-project", "", nil), tc.task)

			start := func() (*progression.Engine, *int) {
				fires := 0
				engine := progression.New(mgr,
					progression.WithGraph(progression.PersonalAssistantGraph()),
					progression.WithOnComplete(func(progression.Quest) { fires++ }),
				)
				b := &ServerBuilder{workspaceFileStore: s.store, onboardingMgr: mgr, progressionEngine: engine}
				b.completeProgressionWiring()
				return engine, &fires
			}

			engine, fires := start()
			if got := engine.HasCompleted(progression.FolderFirstLookQuestID); got != tc.want {
				t.Fatalf("See what your assistant found completed = %t, want %t", got, tc.want)
			}
			if *fires != 0 {
				t.Fatalf("grandfathering fired %d completions; it must be silent", *fires)
			}
			if _, recorded := mgr.GetProgression().Reconciled[folderFirstLookReconcileKey]; !recorded {
				t.Fatal("the pass was not persisted")
			}

			// A reset after the upgrade stays a blank slate across restarts: the
			// pass never runs again, so past work cannot reappear as done.
			if err := engine.Reset(); err != nil {
				t.Fatal(err)
			}
			if restarted, _ := start(); restarted.HasCompleted(progression.FolderFirstLookQuestID) {
				t.Fatal("a restart after a reset re-grandfathered the first look")
			}
		})
	}
}

// The mission card's reading of a first look. Which look is read is the folder
// digest's choice (see LatestFirstLook); this is only what the card takes from
// it.
func TestMissionFirstTaskOf(t *testing.T) {
	views := map[string]personalassistant.FolderFirstTaskView{
		"ws-thesis": {
			State: personalassistant.FolderFirstTaskSeeded, CanStart: true,
			WorkspaceName: "Thesis", WorkspaceRoute: "/workspaces/thesis", FolderName: "thesis-draft",
			TicketRoute: "/workspaces/thesis?ticket=t-1",
		},
		"ws-notes": {
			State:         personalassistant.FolderFirstTaskRunning,
			WorkspaceName: "Notes", WorkspaceRoute: "/workspaces/notes", FolderName: "notes",
		},
		"ws-blocked": {
			State: personalassistant.FolderFirstTaskSeeded, Reason: personalassistant.FolderFirstTaskReasonSetupOpen,
			Message:       "Open Album to finish its setup first.",
			WorkspaceName: "Album", WorkspaceRoute: "/workspaces/album",
		},
		"ws-paused": {
			State: personalassistant.FolderFirstTaskWaiting, Reason: personalassistant.FolderFirstTaskReasonNeedsInput,
			Message:       "The first look is waiting for your answer. Open Paper to continue.",
			WorkspaceName: "Paper", WorkspaceRoute: "/workspaces/paper",
		},
		"ws-no-model": {
			State: personalassistant.FolderFirstTaskSeeded, Reason: personalassistant.FolderFirstTaskReasonNoModel,
			Message:       "Add a model in Settings to run the first look.",
			WorkspaceName: "Code", WorkspaceRoute: "/workspaces/code",
		},
	}
	// A startable look: routes and names pass through; nothing is blocked.
	got := missionFirstTaskOf(views["ws-thesis"])
	want := progression.MissionFirstTask{
		WorkspaceName: "Thesis", WorkspaceRoute: "/workspaces/thesis", FolderName: "thesis-draft",
		State: progression.MissionFirstTaskSeeded, CanStart: true, TicketRoute: "/workspaces/thesis?ticket=t-1",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// A running look says nothing about being blocked.
	got = missionFirstTaskOf(views["ws-notes"])
	if got.State != progression.MissionFirstTaskRunning || got.WorkspaceName != "Notes" || got.CanStart || got.Blocked != "" {
		t.Fatalf("running = %+v", got)
	}

	// A seeded task that cannot start carries the sentence that says why; a
	// missing model points at Settings, since the workspace cannot fix it.
	got = missionFirstTaskOf(views["ws-blocked"])
	if got.CanStart || got.Blocked != "Open Album to finish its setup first." || got.BlockedURL != "" || got.BlockedLabel != "" {
		t.Fatalf("blocked in the workspace = %+v", got)
	}
	got = missionFirstTaskOf(views["ws-no-model"])
	if got.CanStart || got.Blocked != "Add a model in Settings to run the first look." ||
		got.BlockedURL != "/settings#system-model" || got.BlockedLabel != "Open Settings" {
		t.Fatalf("blocked on a model = %+v", got)
	}

	// A look that paused to ask the user something says so, and is answered in
	// the workspace.
	got = missionFirstTaskOf(views["ws-paused"])
	if got.State != progression.MissionFirstTaskWaiting || got.CanStart ||
		got.Blocked != "The first look is waiting for your answer. Open Paper to continue." || got.BlockedURL != "" {
		t.Fatalf("paused = %+v", got)
	}

	// A failed look's own message is not the card's: its copy is Try again.
	failed := missionFirstTaskOf(personalassistant.FolderFirstTaskView{
		State: personalassistant.FolderFirstTaskFailed, Reason: personalassistant.FolderFirstTaskReasonRunFailed,
		Message: "The first look did not finish. Try again.", WorkspaceName: "Thesis", WorkspaceRoute: "/workspaces/thesis",
	})
	if failed.State != progression.MissionFirstTaskFailed || failed.Blocked != "" {
		t.Fatalf("failed = %+v", failed)
	}

	// Nothing to point at.
	if got := missionFirstTaskOf(personalassistant.FolderFirstTaskView{}); got != (progression.MissionFirstTask{}) {
		t.Fatalf("empty view = %+v", got)
	}
}

// With no folder digest wired the mission keeps its static card, and a server
// whose mission is already resolved does not read at all.
func TestStarterMissionContext_FirstLookIsZeroWithoutAFolderDigest(t *testing.T) {
	s := newStarterStore(t)
	b := &ServerBuilder{workspaceFileStore: s.store}
	if got := b.starterMissionContext().FolderFirstTask; got != (progression.MissionFirstTask{}) {
		t.Fatalf("no folder digest: %+v", got)
	}
	if _, ok := b.folderFirstTaskView(context.Background(), "ws-any", true); ok {
		t.Fatal("a server with no session handler produced a first-look view")
	}
}
