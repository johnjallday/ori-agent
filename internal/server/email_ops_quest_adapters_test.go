package server

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type questWorkspaceSource struct {
	workspaces []*workspace.Workspace
	listErr    error
}

func (s *questWorkspaceSource) ListActive() ([]*workspace.Workspace, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	// Lean records, as the SQLite-primary list returns: no provenance.
	lean := make([]*workspace.Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		lean = append(lean, &workspace.Workspace{ID: ws.ID})
	}
	return lean, nil
}

func (s *questWorkspaceSource) Get(id string) (*workspace.Workspace, error) {
	for _, ws := range s.workspaces {
		if ws.ID == id {
			return ws, nil
		}
	}
	return nil, errors.New("not found")
}

func emailOpsQuestWorkspace(id, name, slug, owner string, updated time.Time) *workspace.Workspace {
	return &workspace.Workspace{
		ID: id, Name: name, FolderSlug: slug, OwnerUserID: owner, UpdatedAt: updated,
		TemplateProvenance: &workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true},
	}
}

func emailOpsQuestScope() setupjourney.ReadScope {
	return setupjourney.ReadScope{
		OwnerUserID: "local", Shape: specialist.SetupJourneyShapeAccountLink,
		ExpectedBlueprintID: workspace.EmailOpsTemplateID, RunKind: setupjourney.RunKindRoot,
	}
}

// FR 23: step 1 finds the user's newest active Email Ops workspace through a
// provenance-hydrated source, never creates one, and records only its ID.
func TestEmailOpsWorkspaceCreateReaderResolvesWithoutCreating(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	source := &questWorkspaceSource{}
	reader := emailOpsWorkspaceCreateReader{source: source}

	pending, err := reader.Read(ctx, emailOpsQuestScope())
	if err != nil || pending.Complete || pending.BlockedReason != "" ||
		!reflect.DeepEqual(pending.AvailableActions, []setupjourney.ActionID{setupjourney.ActionReviewTeam}) ||
		pending.WorkspaceCreate == nil || pending.WorkspaceCreate.WorkspaceID != "" || pending.Result.ProjectWorkspaceID != "" {
		t.Fatalf("no workspace: %+v err=%v", pending, err)
	}

	source.workspaces = []*workspace.Workspace{
		emailOpsQuestWorkspace("older", "Old Email Ops", "old-email-ops", "local", now.Add(-time.Hour)),
		emailOpsQuestWorkspace("newest", "Email Ops", "email-ops", "", now),
		emailOpsQuestWorkspace("foreign", "Their Email Ops", "their-email-ops", "someone-else", now.Add(time.Hour)),
		{ID: "plain", Name: "Not Email Ops", FolderSlug: "plain", UpdatedAt: now.Add(2 * time.Hour)},
	}
	found, err := reader.Read(ctx, emailOpsQuestScope())
	if err != nil || !found.Complete || found.Result.ProjectWorkspaceID != "newest" ||
		!reflect.DeepEqual(found.AvailableActions, []setupjourney.ActionID{setupjourney.ActionOpenWorkspace}) {
		t.Fatalf("found workspace: %+v err=%v", found, err)
	}
	want := &setupjourney.WorkspaceCreateProjection{
		TemplateTitle: "Email Ops", WorkspaceID: "newest", WorkspaceLabel: "Email Ops", WorkspaceRoute: "/workspaces/email-ops",
	}
	if !reflect.DeepEqual(found.WorkspaceCreate, want) {
		t.Fatalf("projection = %+v, want %+v", found.WorkspaceCreate, want)
	}

	// A foreign template scope or a failing store is an unavailable owner.
	foreignScope := emailOpsQuestScope()
	foreignScope.ExpectedBlueprintID = "calendar-ops"
	if _, err := reader.Read(ctx, foreignScope); err == nil {
		t.Fatal("reader served a non-Email-Ops blueprint")
	}
	source.listErr = errors.New("store unavailable")
	if _, err := reader.Read(ctx, emailOpsQuestScope()); err == nil {
		t.Fatal("reader hid a store failure")
	}
	if _, err := (emailOpsWorkspaceCreateReader{}).Read(ctx, emailOpsQuestScope()); err == nil {
		t.Fatal("reader without a source claimed a result")
	}
}

func TestEmailOpsWorkspaceLocatorUsesTheQuestResolver(t *testing.T) {
	source := &questWorkspaceSource{}
	locator := emailOpsWorkspaceLocator{source: source}
	if exists, err := locator.HasEmailOpsWorkspace("local"); err != nil || exists {
		t.Fatalf("empty store exists=%v err=%v", exists, err)
	}
	source.workspaces = []*workspace.Workspace{emailOpsQuestWorkspace("foreign", "Email Ops", "email-ops", "someone-else", time.Now())}
	if exists, _ := locator.HasEmailOpsWorkspace("local"); exists {
		t.Fatal("another user's Email Ops workspace counted")
	}
	source.workspaces = append(source.workspaces, emailOpsQuestWorkspace("mine", "Email Ops", "email-ops-2", "local", time.Now()))
	if exists, err := locator.HasEmailOpsWorkspace("local"); err != nil || !exists {
		t.Fatalf("owned workspace exists=%v err=%v", exists, err)
	}
	source.listErr = errors.New("store unavailable")
	if _, err := locator.HasEmailOpsWorkspace("local"); err == nil {
		t.Fatal("a store failure read as no workspace")
	}
	if _, err := (emailOpsWorkspaceLocator{}).HasEmailOpsWorkspace("local"); err == nil {
		t.Fatal("a locator without a source answered")
	}
}

func TestQuestWorkspaceLabelAndRouteAreBounded(t *testing.T) {
	if got := questWorkspaceLabel("  Email   Ops  "); got != "Email Ops" {
		t.Fatalf("label = %q", got)
	}
	if got := questWorkspaceLabel(""); got != "Email Ops" {
		t.Fatalf("empty label = %q", got)
	}
	long := questWorkspaceLabel(strings.Repeat("é", 200))
	if len(long) > maxQuestWorkspaceLabelBytes || !strings.HasPrefix(strings.Repeat("é", 60), long) {
		t.Fatalf("long label = %q (%d bytes)", long, len(long))
	}
	for slug, want := range map[string]string{
		"email-ops":   "/workspaces/email-ops",
		"":            "",
		"has space":   "",
		"a/b":         "",
		"..":          "",
		"mail?x=1":    "",
		"café-inbox":  "",
		"email_ops-2": "/workspaces/email_ops-2",
	} {
		if got := questWorkspaceRoute(slug); got != want {
			t.Errorf("route(%q) = %q, want %q", slug, got, want)
		}
	}
}

// FR 10: specialist summaries are exactly what they were; account-link
// summaries offer only the host shape's closed actions, gated on a model.
// fakeQuestList is a catalog that lists fixed quests and resolves none.
type fakeQuestList []setupjourney.QuestSummary

func (l fakeQuestList) List(context.Context) ([]setupjourney.QuestSummary, error) { return l, nil }
func (l fakeQuestList) Lookup(context.Context, setupjourney.QuestKey) (setupjourney.QuestDefinition, error) {
	return setupjourney.QuestDefinition{}, errors.New("not resolvable")
}

func TestSetupSummaryReaderOffersActionsByShape(t *testing.T) {
	ctx := context.Background()
	for _, scope := range []setupjourney.ReadScope{
		{RunKind: setupjourney.RunKindRoot},
		{RunKind: setupjourney.RunKindRoot, Shape: specialist.SetupJourneyShapeSpecialist, HomeWorkspaceID: "home", ProjectWorkspaceID: "project"},
		{RunKind: setupjourney.RunKindChild, Shape: specialist.SetupJourneyShapeSpecialist, HomeWorkspaceID: "home", ProjectWorkspaceID: "project"},
	} {
		legacy, legacyErr := readSetupSummary(ctx, scope)
		for _, available := range []bool{false, true} {
			got, err := setupSummaryReader{modelAvailable: func() bool { return available }}.Read(ctx, scope)
			if !reflect.DeepEqual(got, legacy) || !errors.Is(err, legacyErr) {
				t.Fatalf("specialist summary changed for %+v: %+v, want %+v", scope, got, legacy)
			}
		}
	}

	// An install quest's summary offers only its handoff and the Plugins page.
	install := setupjourney.ReadScope{
		RunKind: setupjourney.RunKindRoot, Shape: specialist.SetupJourneyShapeIntegrationInstall,
		IntegrationKey: "ori_reaper", HomeWorkspaceID: "home", ProjectWorkspaceID: "project",
	}
	withoutQuest, err := setupSummaryReader{pluginQuests: fakeQuestList{}}.Read(ctx, install)
	if err != nil || !reflect.DeepEqual(withoutQuest.AvailableActions, []setupjourney.ActionID{setupjourney.ActionOpenPlugins}) ||
		withoutQuest.Handoff != nil {
		t.Fatalf("install summary without a plugin quest = %+v", withoutQuest)
	}
	pluginQuest := setupjourney.QuestSummary{
		QuestKey: setupjourney.QuestKey{Source: setupjourney.QuestSourcePlugin, PluginID: "reaper-plugin", ID: "reaper_setup"},
		Title:    "Set up REAPER",
	}
	withQuest, err := setupSummaryReader{pluginQuests: fakeQuestList{pluginQuest}}.Read(ctx, install)
	if err != nil || !reflect.DeepEqual(withQuest.AvailableActions, []setupjourney.ActionID{setupjourney.ActionContinueIntegrationSetup, setupjourney.ActionOpenPlugins}) ||
		withQuest.Handoff == nil || withQuest.Handoff.ID != "reaper_setup" || withQuest.Handoff.PluginID != "reaper-plugin" || withQuest.Handoff.Title != "Set up REAPER" {
		t.Fatalf("install summary with a plugin quest = %+v", withQuest)
	}

	account := emailOpsQuestScope()
	noWorkspace, err := setupSummaryReader{modelAvailable: func() bool { return true }}.Read(ctx, account)
	if err != nil || len(noWorkspace.AvailableActions) != 0 {
		t.Fatalf("summary offered actions before the workspace existed: %+v", noWorkspace)
	}
	account.ProjectWorkspaceID = "workspace-email-ops"
	withModel, _ := setupSummaryReader{modelAvailable: func() bool { return true }}.Read(ctx, account)
	if !reflect.DeepEqual(withModel.AvailableActions, []setupjourney.ActionID{setupjourney.ActionOpenWorkspace, setupjourney.ActionStartInboxTriage}) {
		t.Fatalf("summary with a model = %+v", withModel.AvailableActions)
	}
	for _, reader := range []setupSummaryReader{{}, {modelAvailable: func() bool { return false }}} {
		withoutModel, _ := reader.Read(ctx, account)
		if !reflect.DeepEqual(withoutModel.AvailableActions, []setupjourney.ActionID{setupjourney.ActionOpenWorkspace, setupjourney.ActionOpenModelSettings}) {
			t.Fatalf("summary without a model = %+v", withoutModel.AvailableActions)
		}
	}
	for _, action := range withModel.AvailableActions {
		switch action {
		case setupjourney.ActionOpenLiveSetup, setupjourney.ActionOpenSampleLibrarySetup, setupjourney.ActionConnectAnotherProject, setupjourney.ActionOpenProject:
			t.Fatalf("account-link summary offered a specialist action %q", action)
		}
	}
}
