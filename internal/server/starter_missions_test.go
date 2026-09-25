package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// starterStore is a real folder store with helpers for the starter-mission
// workspace shapes.
type starterStore struct {
	t     *testing.T
	store *workspace.FileStore
}

func newStarterStore(t *testing.T) *starterStore {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace store: %v", err)
	}
	return &starterStore{t: t, store: store}
}

// add saves an active workspace created from templateID ("" for blank). A
// non-nil completedAt marks its setup wizard ready.
func (s *starterStore) add(id, name, templateID, owner string, completedAt *time.Time) *workspace.Workspace {
	s.t.Helper()
	ws := &workspace.Workspace{
		ID: id, Name: name, Status: workspace.StatusActive, OwnerUserID: owner,
	}
	if templateID != "" {
		ws.TemplateProvenance = &workspace.TemplateProvenance{TemplateID: templateID, Builtin: true}
		ws.SetupWizardProgress = &workspace.SetupWizardProgress{
			WizardVersion: 1, State: "in_progress", CompletedAt: completedAt,
		}
	}
	if err := s.store.Save(ws); err != nil {
		s.t.Fatalf("save %s: %v", id, err)
	}
	saved, err := s.store.Get(id)
	if err != nil {
		s.t.Fatalf("get %s: %v", id, err)
	}
	return saved
}

func TestComposeCompletionHooks_CallsEveryConsumerOnceInOrder(t *testing.T) {
	var calls []string
	hook := composeCompletionHooks(
		func(_ context.Context, id string) { calls = append(calls, "help-task:"+id) },
		nil, // an unwired consumer is skipped, not a panic
		func(_ context.Context, id string) { calls = append(calls, "mission:"+id) },
	)
	hook(context.Background(), "ws-1")
	if len(calls) != 2 || calls[0] != "help-task:ws-1" || calls[1] != "mission:ws-1" {
		t.Fatalf("calls = %v, want the help task then the mission, once each", calls)
	}
}

func TestFindJanitorWorkspace_MatchesOnlyFileJanitorProvenance(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		templateID string
		owner      string
		want       bool
	}{
		{"file janitor", "file-janitor", "", true},
		{"retired downloads janitor", "downloads-janitor", "", true},
		{"owned by the local user", "file-janitor", userprofile.LocalUserID, true},
		{"email ops", "email-ops", "", false},
		{"blank workspace", "", "", false},
		{"someone else's janitor", "file-janitor", "another-user", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStarterStore(t)
			s.add("ws-1", "Tidy Downloads", tc.templateID, tc.owner, &now)
			slug, ready, ok := findJanitorWorkspace(s.store)
			if ok != tc.want {
				t.Fatalf("found = %t, want %t", ok, tc.want)
			}
			if ok && (slug == "" || !ready) {
				t.Fatalf("slug %q ready %t", slug, ready)
			}
		})
	}
}

func TestFindJanitorWorkspace_PrefersAReadyWorkspace(t *testing.T) {
	s := newStarterStore(t)
	now := time.Now()
	ready := s.add("ws-ready", "Tidy Downloads", "file-janitor", "", &now)
	s.add("ws-unfinished", "Tidy Desktop", "file-janitor", "", nil)

	slug, isReady, ok := findJanitorWorkspace(s.store)
	if !ok || !isReady || slug != ready.FolderSlug {
		t.Fatalf("got %q ready=%t ok=%t, want the ready workspace %q", slug, isReady, ok, ready.FolderSlug)
	}
}

func TestFindJanitorWorkspace_IgnoresATrashedWorkspace(t *testing.T) {
	s := newStarterStore(t)
	ws := s.add("ws-1", "Tidy Downloads", "file-janitor", "", nil)
	ws.Status = workspace.StatusTrashed
	if err := s.store.Save(ws); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := findJanitorWorkspace(s.store); ok {
		t.Fatal("a trashed File Janitor workspace still counts")
	}
}

// Show your assistant a folder always starts at the chooser: a File Janitor
// workspace mid-setup no longer turns the card into "Finish setup".
func TestStarterMissionContext_ShowFolderKeepsItsCardWhateverTheJanitorState(t *testing.T) {
	s := newStarterStore(t)
	b := &ServerBuilder{workspaceFileStore: s.store}
	engine := progression.New(nil, progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithMissionContext(b.starterMissionContext))

	folder := func() progression.QuestView {
		for _, m := range engine.Status().Missions {
			if m.ID == progression.ShowFolderQuestID {
				return m
			}
		}
		t.Fatal("Mission 03 missing")
		return progression.QuestView{}
	}

	if m := folder(); m.ActionURL != progression.ShowFolderActionURL || m.ActionLabel != "Start" || m.InProgress {
		t.Fatalf("no workspace: %+v", m)
	}
	ws := s.add("ws-1", "Tidy Downloads", "file-janitor", "", nil)
	if m := folder(); m.ActionURL != progression.ShowFolderActionURL || m.ActionLabel != "Start" || m.InProgress {
		t.Fatalf("unfinished janitor workspace: %+v", m)
	}
	now := time.Now()
	ws.SetupWizardProgress.CompletedAt = &now
	if err := s.store.Save(ws); err != nil {
		t.Fatal(err)
	}
	if m := folder(); m.ActionURL != progression.ShowFolderActionURL || m.InProgress {
		t.Fatalf("ready janitor workspace: %+v", m)
	}
}

func TestCompleteShowFolderOnWizardReady(t *testing.T) {
	s := newStarterStore(t)
	janitor := s.add("ws-janitor", "Tidy Downloads", "file-janitor", "", nil)
	email := s.add("ws-email", "Email Ops", "email-ops", "", nil)

	var fires int
	engine := progression.New(nil,
		progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithOnComplete(func(q progression.Quest) {
			if q.ID == progression.ShowFolderQuestID {
				fires++
			}
		}),
	)
	b := &ServerBuilder{workspaceFileStore: s.store}

	// Before progression is built the hook is a no-op, never a panic.
	b.completeShowFolderOnWizardReady(context.Background(), janitor.ID)

	b.progressionEngine = engine
	b.completeShowFolderOnWizardReady(context.Background(), email.ID)
	b.completeShowFolderOnWizardReady(context.Background(), "missing")
	if engine.HasCompleted(progression.ShowFolderQuestID) {
		t.Fatal("a non-janitor wizard completed Mission 03")
	}

	b.completeShowFolderOnWizardReady(context.Background(), janitor.ID)
	b.completeShowFolderOnWizardReady(context.Background(), janitor.ID)
	if !engine.HasCompleted(progression.ShowFolderQuestID) || fires != 1 {
		t.Fatalf("completed=%t fires=%d, want completed once", engine.HasCompleted(progression.ShowFolderQuestID), fires)
	}
}

// linkWorkspaceFolder attaches path as the workspace's primary project
// directory, as showing a folder does. The folder is created when missing, as
// a blueprint scaffold would be.
func (s *starterStore) linkWorkspaceFolder(ws *workspace.Workspace, path string) {
	s.t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		s.t.Fatal(err)
	}
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: "dir-" + ws.ID, Name: "Linked", Path: path}); err != nil {
		s.t.Fatal(err)
	}
	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	ws.SharedData[projecttemplates.PrimaryDirectoryIDKey] = "dir-" + ws.ID
	if err := s.store.Save(ws); err != nil {
		s.t.Fatal(err)
	}
}

func TestLinkedProjectWorkspaces_CountsOnlyOutsidePrimaryFolders(t *testing.T) {
	s := newStarterStore(t)
	outside := t.TempDir()

	linked := s.add("ws-linked", "Thesis", "writing-project", "", nil)
	s.linkWorkspaceFolder(linked, outside)

	// A blueprint scaffold sits inside the workspace's own folder: not linked.
	scaffold := s.add("ws-scaffold", "Album", "reaper-song", "", nil)
	own, err := s.store.GetFolderPath(scaffold.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.linkWorkspaceFolder(scaffold, filepath.Join(own, "project"))

	// A reference that is not the primary does not count either.
	plain := s.add("ws-plain", "Notes", "", "", nil)
	if err := plain.AddDirectoryReference(workspace.DirectoryReference{ID: "dir-plain", Name: "Refs", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Save(plain); err != nil {
		t.Fatal(err)
	}

	trashed := s.add("ws-trashed", "Old", "", "", nil)
	s.linkWorkspaceFolder(trashed, t.TempDir())
	trashed.Status = workspace.StatusTrashed
	if err := s.store.Save(trashed); err != nil {
		t.Fatal(err)
	}

	b := &ServerBuilder{workspaceFileStore: s.store}
	if got := linkedProjectWorkspaces(s.store, b.workspaceFolderPath); got != 1 {
		t.Fatalf("linked project workspaces = %d, want 1 (only the outside primary folder)", got)
	}
	if got := linkedProjectWorkspaces(nil, b.workspaceFolderPath); got != 0 {
		t.Fatalf("no store counted %d", got)
	}
}

// The one-time show-folder pass (FR43): a completed Tidy your Downloads or an
// already linked outside folder marks the new mission done, silently; a Tidy
// that was only skipped leaves it open.
func TestCompleteProgressionWiring_ShowFolderReconcile(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, s *starterStore, state *types.ProgressionState)
		want bool
	}{
		{"tidy completed", func(_ *testing.T, _ *starterStore, state *types.ProgressionState) {
			state.CompletedQuests[progression.TidyDownloadsQuestID] = time.Now().Add(-40 * 24 * time.Hour)
		}, true},
		{"tidy only skipped", func(_ *testing.T, _ *starterStore, state *types.ProgressionState) {
			state.SkippedQuests = map[string]time.Time{progression.TidyDownloadsQuestID: time.Now().Add(-40 * 24 * time.Hour)}
		}, false},
		{"linked outside folder", func(t *testing.T, s *starterStore, _ *types.ProgressionState) {
			s.linkWorkspaceFolder(s.add("ws-thesis", "Thesis", "writing-project", "", nil), t.TempDir())
		}, true},
		{"scaffolded project only", func(_ *testing.T, s *starterStore, _ *types.ProgressionState) {
			ws := s.add("ws-album", "Album", "reaper-song", "", nil)
			own, err := s.store.GetFolderPath(ws.ID)
			if err != nil {
				s.t.Fatal(err)
			}
			s.linkWorkspaceFolder(ws, filepath.Join(own, "project"))
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := onboarding.NewManager(filepath.Join(t.TempDir(), "app_state.json"))
			state := mgr.GetProgression()
			state.BackfilledAt = time.Now().Add(-90 * 24 * time.Hour)
			state.CompletedQuests = map[string]time.Time{"t1-first-message": time.Now().Add(-80 * 24 * time.Hour)}
			s := newStarterStore(t)
			tc.seed(t, s, &state)
			if err := mgr.SetProgression(state); err != nil {
				t.Fatal(err)
			}

			fires := 0
			engine := progression.New(mgr,
				progression.WithGraph(progression.PersonalAssistantGraph()),
				progression.WithOnComplete(func(progression.Quest) { fires++ }),
			)
			b := &ServerBuilder{workspaceFileStore: s.store, onboardingMgr: mgr, progressionEngine: engine}
			b.completeProgressionWiring()

			if got := engine.HasCompleted(progression.ShowFolderQuestID); got != tc.want {
				t.Fatalf("Show your assistant a folder completed = %t, want %t", got, tc.want)
			}
			if fires != 0 {
				t.Fatalf("grandfathering fired %d completions; it must be silent", fires)
			}
			if _, recorded := mgr.GetProgression().Reconciled[showFolderReconcileKey]; !recorded {
				t.Fatal("the pass was not persisted")
			}
		})
	}
}

func TestScanProgression_FileJanitorReady(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name        string
		completedAt *time.Time
		want        bool
	}{
		{"unfinished setup", nil, false},
		{"ready setup", &now, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStarterStore(t)
			s.add("ws-1", "Tidy Downloads", "file-janitor", "", tc.completedAt)
			b := &ServerBuilder{
				workspaceFileStore: s.store,
				onboardingMgr:      onboarding.NewManager(filepath.Join(t.TempDir(), "app_state.json")),
			}
			if got := b.scanProgression().FileJanitorReady; got != tc.want {
				t.Fatalf("FileJanitorReady = %t, want %t", got, tc.want)
			}
		})
	}
}

// connectCalendar gives a workspace an enabled binding that can list calendars
// and events, which is what "Calendar Ops is connected" means.
func connectCalendar(t *testing.T, ws *workspace.Workspace) {
	t.Helper()
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID: "calendar-binding", ServerName: "calendar", Enabled: true,
		CapabilityMappings: []workspace.CapabilityMapping{{
			Capability: "calendar",
			Operations: map[string]workspace.OperationMapping{
				"list_calendars": {Tool: "calendar_list"},
				"list_events":    {Tool: "events_list"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOnEmailSetupFirstReady_CompletesOnlyForTheEmailQuest(t *testing.T) {
	engine := progression.New(nil, progression.WithGraph(progression.PersonalAssistantGraph()))
	hook := onEmailSetupFirstReady(engine)

	hook("local", setupjourney.QuestKey{Source: setupjourney.QuestSourcePlugin, ID: hostquests.EmailOpsSetupQuestID})
	hook("local", setupjourney.QuestKey{Source: setupjourney.QuestSourceHost, ID: "another_quest"})
	if engine.HasCompleted(progression.ConnectSourceQuestID) {
		t.Fatal("a journey other than the host email setup completed Mission 03")
	}
	hook("local", setupjourney.QuestKey{Source: setupjourney.QuestSourceHost, ID: hostquests.EmailOpsSetupQuestID})
	if !engine.HasCompleted(progression.ConnectSourceQuestID) {
		t.Fatal("the email setup's first ready did not complete Mission 03")
	}
	onEmailSetupFirstReady(nil)("local", setupjourney.QuestKey{Source: setupjourney.QuestSourceHost, ID: hostquests.EmailOpsSetupQuestID})
}

func TestCalendarBindingConnected(t *testing.T) {
	s := newStarterStore(t)
	calendar := s.add("ws-calendar", "Calendar Ops", "calendar-ops", "", nil)
	connectCalendar(t, calendar)
	if err := s.store.Save(calendar); err != nil {
		t.Fatal(err)
	}
	unready := s.add("ws-calendar-unready", "Calendar Two", "calendar-ops", "", nil)
	other := s.add("ws-other", "Launch", "content-production", "", nil)
	connectCalendar(t, other)
	if err := s.store.Save(other); err != nil {
		t.Fatal(err)
	}

	bindingCreated := func(id string) workspace.Event {
		return workspace.Event{Type: workspace.EventWorkspaceUpdated, WorkspaceID: id, Data: map[string]any{"action": "mcp_binding_created"}}
	}
	cases := []struct {
		name string
		ev   workspace.Event
		want bool
	}{
		{"connected Calendar Ops", bindingCreated(calendar.ID), true},
		{"Calendar Ops without a ready binding", bindingCreated(unready.ID), false},
		{"a calendar binding on another blueprint", bindingCreated(other.ID), false},
		{"a different update", workspace.Event{Type: workspace.EventWorkspaceUpdated, WorkspaceID: calendar.ID, Data: map[string]any{"action": "renamed"}}, false},
		{"an unknown workspace", bindingCreated("missing"), false},
	}
	for _, tc := range cases {
		if got := calendarBindingConnected(s.store, tc.ev); got != tc.want {
			t.Errorf("%s: got %t, want %t", tc.name, got, tc.want)
		}
	}
	if calendarBindingConnected(nil, bindingCreated(calendar.ID)) {
		t.Error("a nil source reported a connection")
	}
}

func TestCompleteProgressionWiring_CalendarConnectionCompletesMissionThreeOnce(t *testing.T) {
	s := newStarterStore(t)
	// Not connected yet: the wiring's backfill must find nothing to grandfather.
	calendar := s.add("ws-calendar", "Calendar Ops", "calendar-ops", "", nil)
	bus := workspace.NewEventBus(8, 16)
	t.Cleanup(bus.Shutdown)

	fires := make(chan string, 4)
	engine := progression.New(nil,
		progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithOnComplete(func(q progression.Quest) { fires <- q.ID }),
	)
	b := &ServerBuilder{
		workspaceFileStore: s.store, eventBus: bus, progressionEngine: engine,
		onboardingMgr: onboarding.NewManager(filepath.Join(t.TempDir(), "app_state.json")),
	}
	b.completeProgressionWiring()
	if engine.HasCompleted(progression.ConnectSourceQuestID) {
		t.Fatal("backfill completed Mission 03 before any calendar was connected")
	}

	// The user connects the calendar; the binding handler publishes the event.
	connectCalendar(t, calendar)
	if err := s.store.Save(calendar); err != nil {
		t.Fatal(err)
	}
	event := workspace.Event{Type: workspace.EventWorkspaceUpdated, WorkspaceID: calendar.ID, Data: map[string]any{"action": "mcp_binding_created"}}
	bus.Publish(event)
	bus.Publish(event)

	select {
	case id := <-fires:
		if id != progression.ConnectSourceQuestID {
			t.Fatalf("completed %s, want Mission 03", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a connected Calendar Ops workspace did not complete Mission 03")
	}
	select {
	case id := <-fires:
		t.Fatalf("a second binding event completed %s again", id)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestScanStarterWorkspaces_CountsProjectsAndCalendarReadiness(t *testing.T) {
	s := newStarterStore(t)
	s.add("ws-hq", "Personal HQ", "personal-ops", "", nil)
	s.add("ws-janitor", "Tidy Downloads", "file-janitor", "", nil)
	s.add("ws-email", "Email Ops", "email-ops", "", nil)
	s.add("ws-blank", "Launch Plan", "", "", nil)
	s.add("ws-content", "Content", "content-production", "", nil)
	s.add("ws-foreign", "Their Project", "", "another-user", nil)
	group := s.add("ws-group", "Clients", "", "", nil)
	group.Kind = "group"
	if err := s.store.Save(group); err != nil {
		t.Fatal(err)
	}

	calendarReady, projects := scanStarterWorkspaces(s.store, "ws-hq")
	if calendarReady || projects != 2 {
		t.Fatalf("calendarReady=%t projects=%d, want false and 2 (blank + content)", calendarReady, projects)
	}

	calendar := s.add("ws-calendar", "Calendar Ops", "calendar-ops", "", nil)
	if ready, _ := scanStarterWorkspaces(s.store, "ws-hq"); ready {
		t.Fatal("an unconnected Calendar Ops workspace counted as ready")
	}
	connectCalendar(t, calendar)
	if err := s.store.Save(calendar); err != nil {
		t.Fatal(err)
	}
	calendarReady, projects = scanStarterWorkspaces(s.store, "ws-hq")
	if !calendarReady || projects != 2 {
		t.Fatalf("calendarReady=%t projects=%d, want true and still 2", calendarReady, projects)
	}
}

// An install that backfilled before the starter missions existed, with a
// ready File Janitor, sees Mission 03 done after the upgrade: once, silently,
// and never again.
func TestCompleteProgressionWiring_GrandfathersAnUpgradedInstallOnce(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := onboarding.NewManager(statePath)
	state := mgr.GetProgression()
	state.BackfilledAt = time.Now().Add(-90 * 24 * time.Hour)
	state.CompletedQuests = map[string]time.Time{"t1-first-message": time.Now().Add(-80 * 24 * time.Hour)}
	if err := mgr.SetProgression(state); err != nil {
		t.Fatal(err)
	}
	s := newStarterStore(t)
	now := time.Now()
	s.add("ws-janitor", "Tidy Downloads", "file-janitor", "", &now)

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
	if !engine.HasCompleted(progression.ShowFolderQuestID) {
		t.Fatal("a ready File Janitor on an upgraded install was not grandfathered")
	}
	if *fires != 0 {
		t.Fatalf("grandfathering fired %d completions; it must be silent", *fires)
	}
	for _, key := range []string{starterMissionsReconcileKey, showFolderReconcileKey} {
		if _, recorded := mgr.GetProgression().Reconciled[key]; !recorded {
			t.Fatalf("the %s pass was not persisted", key)
		}
	}

	// A reset after the upgrade stays a blank slate across restarts.
	if err := engine.Reset(); err != nil {
		t.Fatal(err)
	}
	restarted, _ := start()
	if restarted.HasCompleted(progression.ShowFolderQuestID) {
		t.Fatal("a restart after a reset re-grandfathered Mission 03")
	}
}

func TestScanProgression_LegacyFirstDayCompletion(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := onboarding.NewManager(statePath)
	state := mgr.GetProgression()
	state.CompletedQuests = map[string]time.Time{progression.PersonalAssistantFirstDayQuestID: time.Now()}
	if err := mgr.SetProgression(state); err != nil {
		t.Fatal(err)
	}
	engine := progression.New(mgr, progression.WithGraph(progression.PersonalAssistantGraph()))
	b := &ServerBuilder{onboardingMgr: mgr, progressionEngine: engine}

	snap := b.scanProgression()
	if !snap.LegacyFirstDayCompleted {
		t.Fatal("a persisted Plan my first day completion was not read as evidence")
	}
	if err := engine.Backfill(progression.ScannerFunc(func() progression.Snapshot { return snap })); err != nil {
		t.Fatal(err)
	}
	if !engine.HasCompleted(progression.ConnectSourceQuestID) {
		t.Fatal("the legacy first-day completion did not grandfather Mission 03")
	}
}
