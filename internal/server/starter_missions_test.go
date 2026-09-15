package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/progression"
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
