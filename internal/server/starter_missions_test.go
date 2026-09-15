package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
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

func TestStarterMissionContext_ResolvesTidyDownloadsFromTheFolderStore(t *testing.T) {
	s := newStarterStore(t)
	b := &ServerBuilder{workspaceFileStore: s.store}
	engine := progression.New(nil, progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithMissionContext(b.starterMissionContext))

	tidy := func() progression.QuestView {
		for _, m := range engine.Status().Missions {
			if m.ID == progression.TidyDownloadsQuestID {
				return m
			}
		}
		t.Fatal("Mission 02 missing")
		return progression.QuestView{}
	}

	// No File Janitor workspace: Start the walkthrough.
	if m := tidy(); m.ActionURL != progression.TidyDownloadsActionURL || m.ActionLabel != "Start" || m.InProgress {
		t.Fatalf("no workspace: %+v", m)
	}

	// Created but not set up: Finish setup on that workspace, in progress.
	ws := s.add("ws-1", "Tidy Downloads", "file-janitor", "", nil)
	if m := tidy(); m.ActionURL != "/workspaces/"+ws.FolderSlug || m.ActionLabel != "Finish setup" || !m.InProgress {
		t.Fatalf("unfinished workspace: %+v", m)
	}

	// Ready: the presentation is unchanged (the hook completes the quest).
	now := time.Now()
	ws.SetupWizardProgress.CompletedAt = &now
	if err := s.store.Save(ws); err != nil {
		t.Fatal(err)
	}
	if m := tidy(); m.ActionURL != progression.TidyDownloadsActionURL || m.InProgress {
		t.Fatalf("ready workspace: %+v", m)
	}
}

func TestCompleteTidyDownloadsOnWizardReady(t *testing.T) {
	s := newStarterStore(t)
	janitor := s.add("ws-janitor", "Tidy Downloads", "file-janitor", "", nil)
	email := s.add("ws-email", "Email Ops", "email-ops", "", nil)

	var fires int
	engine := progression.New(nil,
		progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithOnComplete(func(q progression.Quest) {
			if q.ID == progression.TidyDownloadsQuestID {
				fires++
			}
		}),
	)
	b := &ServerBuilder{workspaceFileStore: s.store}

	// Before progression is built the hook is a no-op, never a panic.
	b.completeTidyDownloadsOnWizardReady(context.Background(), janitor.ID)

	b.progressionEngine = engine
	b.completeTidyDownloadsOnWizardReady(context.Background(), email.ID)
	b.completeTidyDownloadsOnWizardReady(context.Background(), "missing")
	if engine.HasCompleted(progression.TidyDownloadsQuestID) {
		t.Fatal("a non-janitor wizard completed Mission 02")
	}

	b.completeTidyDownloadsOnWizardReady(context.Background(), janitor.ID)
	b.completeTidyDownloadsOnWizardReady(context.Background(), janitor.ID)
	if !engine.HasCompleted(progression.TidyDownloadsQuestID) || fires != 1 {
		t.Fatalf("completed=%t fires=%d, want completed once", engine.HasCompleted(progression.TidyDownloadsQuestID), fires)
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
