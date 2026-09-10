package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type userQuestLibrary struct {
	mu      sync.Mutex
	items   []projecttemplates.Template
	lockErr error
}

func (l *userQuestLibrary) ListUserSetupQuestTemplates(context.Context) ([]projecttemplates.Template, error) {
	return append([]projecttemplates.Template(nil), l.items...), nil
}

func (l *userQuestLibrary) WithUserSetupQuestMutationLock(_ context.Context, operation func() error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lockErr != nil {
		return l.lockErr
	}
	return operation()
}

func (l *userQuestLibrary) FindUserSetupQuestTemplate(_ context.Context, id string) (projecttemplates.Template, error) {
	for _, template := range l.items {
		if template.ID == id {
			return template, nil
		}
	}
	return projecttemplates.Template{}, errors.New("not found")
}

func userQuestCatalogFixture(t *testing.T) (projecttemplates.Template, *userQuestLibrary) {
	t.Helper()
	template, err := projecttemplates.LoadFolder("../projecttemplates/testdata/user-setup-quest-eligible")
	if err != nil {
		t.Fatal(err)
	}
	draft := projecttemplates.DefaultUserSetupQuestDraft()
	draft.IntegrationKey = "ori_reaper"
	quest, err := projecttemplates.NewUserSetupQuest(template, nil, draft)
	if err != nil {
		t.Fatal(err)
	}
	template.UserSetupQuest = quest
	template.UserSetupQuestRevision = projecttemplates.UserSetupQuestRevision(quest)
	return template, &userQuestLibrary{items: []projecttemplates.Template{template}}
}

type questPlugins struct {
	items []plugin.InstalledPlugin
	err   error
}

func (p *questPlugins) List() ([]plugin.InstalledPlugin, error) { return p.items, p.err }

var reaperQuestKey = QuestKey{PluginID: "reaper-plugin", ID: "reaper_setup"}

func questPluginFixture(t *testing.T) plugin.InstalledPlugin {
	t.Helper()
	entry, _ := specialist.Get("music_production")
	d, err := specialist.NormalizeSetupJourney(*entry.SetupJourney)
	if err != nil {
		t.Fatal(err)
	}
	return plugin.InstalledPlugin{
		Name: reaperQuestKey.PluginID, Version: "0.5.0", Enabled: true,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			RequiresHostFeatures: []string{plugin.HostFeatureSetupQuestsV1}, SetupQuests: []plugin.SetupQuest{*d},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: d.ExpectedBlueprintID, Template: projecttemplates.Template{
			SetupQuestID:     d.ID,
			AssistantProgram: &workspace.AssistantProgramDeclaration{ID: d.ExpectedAssistantProgramID},
		}}},
	}
}

func TestQuestCatalogBindsInstalledDeclarationAndExplicitLegacyBootstrap(t *testing.T) {
	ctx := context.Background()
	plugins := &questPlugins{}
	catalog := NewInstalledQuestCatalog(plugins)
	legacy, err := catalog.Lookup(ctx, reaperQuestKey)
	if err != nil || legacy.Ownership != "host_compatibility" || legacy.LegacySlug != "music_production" {
		t.Fatalf("legacy=%#v err=%v", legacy, err)
	}
	p := questPluginFixture(t)
	p.WorkspaceSurfaces.SetupQuests[0].Title = "Plugin-owned music setup"
	plugins.items = []plugin.InstalledPlugin{p}
	owned, err := catalog.Lookup(ctx, reaperQuestKey)
	if err != nil || owned.Ownership != "plugin" || owned.Declaration.Title != "Plugin-owned music setup" || owned.Declaration.OwnerPluginID != p.Name {
		t.Fatalf("owned=%#v err=%v", owned, err)
	}
	list, err := catalog.List(ctx)
	if err != nil || len(list) != 1 || list[0].TemplateID != "plugin:reaper-plugin:reaper-song" {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	// Returned declarations cannot mutate persisted data or a later read.
	owned.Declaration.Steps[0].ID = "changed"
	again, err := catalog.Lookup(ctx, reaperQuestKey)
	if err != nil || again.Declaration.Steps[0].ID != "integration" {
		t.Fatalf("aliased=%#v err=%v", again, err)
	}
}

func TestQuestCatalogRejectsInvalidOwnershipAndNeverDowngradesToLegacy(t *testing.T) {
	cases := map[string]func(*plugin.InstalledPlugin){
		"foreign integration": func(p *plugin.InstalledPlugin) { p.WorkspaceSurfaces.SetupQuests[0].IntegrationKey = "other" },
		"foreign blueprint":   func(p *plugin.InstalledPlugin) { p.WorkspaceSurfaces.SetupQuests[0].ExpectedBlueprintID = "other" },
		"foreign program": func(p *plugin.InstalledPlugin) {
			p.WorkspaceSurfaces.SetupQuests[0].ExpectedAssistantProgramID = "other"
		},
		"unknown primitive":      func(p *plugin.InstalledPlugin) { p.WorkspaceSurfaces.SetupQuests[0].Steps[0].Kind = "shell" },
		"missing launch":         func(p *plugin.InstalledPlugin) { p.WorkspaceSurfaces.SetupQuests[0].WorkspaceLaunch = nil },
		"unresolved blueprint":   func(p *plugin.InstalledPlugin) { p.ResolvedBlueprints = nil },
		"wrong reference":        func(p *plugin.InstalledPlugin) { p.ResolvedBlueprints[0].Template.SetupQuestID = "another" },
		"wrong resolved program": func(p *plugin.InstalledPlugin) { p.ResolvedBlueprints[0].Template.AssistantProgram.ID = "another" },
		"duplicate": func(p *plugin.InstalledPlugin) {
			p.WorkspaceSurfaces.SetupQuests = append(p.WorkspaceSurfaces.SetupQuests, p.WorkspaceSurfaces.SetupQuests[0])
		},
		"unsupported schema": func(p *plugin.InstalledPlugin) { p.WorkspaceSurfaces.SetupQuests[0].SchemaVersion = 99 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := questPluginFixture(t)
			change(&p)
			catalog := NewInstalledQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{p}})
			if _, err := catalog.Lookup(context.Background(), reaperQuestKey); err == nil {
				t.Fatal("invalid authored declaration fell back to legacy")
			}
		})
	}
	p := questPluginFixture(t)
	p.WorkspaceSurfaces.SetupQuests[0].ID = "replacement_quest"
	p.ResolvedBlueprints[0].Template.SetupQuestID = "replacement_quest"
	catalog := NewInstalledQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{p}})
	if _, err := catalog.Lookup(context.Background(), reaperQuestKey); err == nil {
		t.Fatal("removed ID silently fell back")
	}
	if _, err := catalog.Lookup(context.Background(), QuestKey{PluginID: "foreign-plugin", ID: "replacement_quest"}); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestQuestRunsDoNotRequireOrCreateAnAssistantAcceptance(t *testing.T) {
	ctx := context.Background()
	service, store := serviceFixture(t, defaultCanonicalReads())
	relationships := &relationshipStub{err: errors.New("no assistant relationship")}
	service.relationships = relationships
	service.SetQuestCatalog(NewInstalledQuestCatalog(&questPlugins{}))
	quests, err := service.ListQuests(ctx)
	if err != nil || len(quests) != 1 {
		t.Fatalf("catalog=%#v err=%v", quests, err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM setup_journey_run").Scan(&count); err != nil || count != 0 {
		t.Fatalf("catalog created progress: count=%d err=%v", count, err)
	}
	scoped, err := service.ForQuest(ctx, "local", reaperQuestKey.PluginID, reaperQuestKey.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, err := scoped.Read(ctx, "local", "")
	if err != nil || root.Journey.PluginID != reaperQuestKey.PluginID {
		t.Fatalf("root=%#v err=%v", root, err)
	}
	opened, err := scoped.Open(ctx, "local", root.RunID, PresentationMutation{IfRevision: root.StateRevision, IdempotencyKey: "quest-open"})
	if err != nil {
		t.Fatal(err)
	}
	dismissed, err := scoped.Dismiss(ctx, "local", root.RunID, PresentationMutation{IfRevision: opened.StateRevision, IdempotencyKey: "quest-dismiss"})
	if err != nil || !dismissed.Dismissed {
		t.Fatalf("dismiss=%#v err=%v", dismissed, err)
	}
	if _, err := service.Read(ctx, "local", ""); err == nil {
		t.Fatal("quest scope mutated the shared assistant service")
	}
	relationships.err, relationships.state = nil, acceptedRelationship()
	alias, err := service.Read(ctx, "local", "")
	if err != nil || alias.RunID != root.RunID || !alias.Dismissed {
		t.Fatalf("assistant alias=%#v err=%v", alias, err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM setup_journey_run").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicated roots: count=%d err=%v", count, err)
	}
	other, err := service.ForQuest(ctx, "another-user", reaperQuestKey.PluginID, reaperQuestKey.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Read(ctx, "another-user", root.RunID); err == nil {
		t.Fatal("cross-user root accepted")
	}
}

func TestQuestResumesLegacyRootAndReviewWithoutRewritingIdentity(t *testing.T) {
	ctx := context.Background()
	service, store := serviceFixture(t, defaultCanonicalReads())
	adapter := &syntheticJourneyAdapter{}
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
		t.Fatal(err)
	}
	legacy, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	request := ActionMutation{IfRevision: legacy.StateRevision, IdempotencyKey: "old-review", Input: json.RawMessage(`{}`)}
	review, err := service.Mutate(ctx, "local", legacy.RunID, ActionReviewInstall, request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.GetRun(ctx, legacy.RunID)
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(NewInstalledQuestCatalog(&questPlugins{}))
	service.relationships = &relationshipStub{err: errors.New("offer no longer accepted")}
	scoped, err := service.ForQuest(ctx, "local", reaperQuestKey.PluginID, reaperQuestKey.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := scoped.Read(ctx, "local", "")
	if err != nil || resumed.RunID != legacy.RunID {
		t.Fatalf("resume=%#v err=%v", resumed, err)
	}
	replayed, err := scoped.Mutate(ctx, "local", legacy.RunID, ActionReviewInstall, request)
	if err != nil || replayed.Review.Token != review.Review.Token || adapter.reviews != 2 {
		t.Fatalf("replay=%#v calls=%d err=%v", replayed, adapter.reviews, err)
	}
	after, err := store.GetRun(ctx, legacy.RunID)
	if err != nil || after.RelationshipID != before.RelationshipID || after.SpecialistSlug != before.SpecialistSlug {
		t.Fatalf("identity rewritten: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestQuestRefusesAmbiguousLegacyRoots(t *testing.T) {
	ctx := context.Background()
	service, store := serviceFixture(t, defaultCanonicalReads())
	root, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	spec := testRootSpec()
	spec.RelationshipID, spec.SpecialistSlug, spec.JourneyID = "other-assistant", "music_production", root.Journey.ID
	if _, _, err := store.CreateOrGetRoot(ctx, spec); err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(NewInstalledQuestCatalog(&questPlugins{}))
	if _, err := service.ForQuest(ctx, "local", reaperQuestKey.PluginID, reaperQuestKey.ID); err == nil {
		t.Fatal("ambiguous roots selected silently")
	}
}

func TestUserTemplateQuestLockFailureCreatesNoBindingOrRoot(t *testing.T) {
	template, library := userQuestCatalogFixture(t)
	library.lockErr = errors.New("lock unavailable")
	service, store := serviceFixture(t, defaultCanonicalReads())
	service.SetQuestCatalog(CombineQuestCatalogs(NewUserTemplateQuestCatalog(library)))
	scoped, err := service.ForUserTemplateQuest(context.Background(), "local", template.ID, template.UserSetupQuest.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.Read(context.Background(), "local", ""); err == nil {
		t.Fatal("read succeeded without the cross-process mutation lock")
	}
	var runs, bindings int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM setup_journey_run`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM setup_user_template_binding`).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || bindings != 0 {
		t.Fatalf("lock failure wrote runs=%d bindings=%d", runs, bindings)
	}
}

func TestUserTemplateQuestCatalogAndServiceKeepSourceAwareDurableIdentity(t *testing.T) {
	template, library := userQuestCatalogFixture(t)
	catalog := NewUserTemplateQuestCatalog(library)
	items, err := catalog.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list=%+v err=%v", items, err)
	}
	summary := items[0]
	if summary.Source != QuestSourceUserTemplate || summary.PluginID != "" || summary.TemplateID != template.ID ||
		summary.AttachmentID != template.UserSetupQuest.AttachmentID {
		t.Fatalf("summary=%+v", summary)
	}

	_, store := openTestStore(t)
	scopes := make(map[specialist.SetupStepKind][]ReadScope)
	service, err := NewService(store, &relationshipStub{err: errors.New("not used")}, readerRegistryStub(t, defaultCanonicalReads(), nil, nil, scopes))
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(catalog)
	scoped, err := service.ForUserTemplateQuest(context.Background(), "local", template.ID, template.UserSetupQuest.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := scoped.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Journey.Source != QuestSourceUserTemplate || projection.Journey.PluginID != "" ||
		projection.Journey.TemplateID != template.ID || projection.Journey.AttachmentID != template.UserSetupQuest.AttachmentID {
		t.Fatalf("projection identity=%+v", projection.Journey)
	}
	for kind, reads := range scopes {
		if len(reads) == 0 || reads[0].QuestSource != QuestSourceUserTemplate || reads[0].UserTemplateID != template.ID {
			t.Fatalf("%s scope=%+v", kind, reads)
		}
	}
	binding, err := store.GetUserTemplateBinding(context.Background(), "local", template.ID)
	if err != nil || binding.AttachmentID != template.UserSetupQuest.AttachmentID ||
		binding.DefinitionDigest != projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest) {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	if err := scoped.claimUserTemplateResultRoots(context.Background(), "local", CanonicalResult{
		HomeWorkspaceID: "home-1", ProjectWorkspaceID: "project-1",
	}); err != nil {
		t.Fatal(err)
	}
	var claims int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM setup_user_template_root_claim`).Scan(&claims); err != nil || claims != 3 {
		t.Fatalf("root claims=%d err=%v", claims, err)
	}

	resumed, err := service.ForUserTemplateQuest(context.Background(), "local", template.ID, template.UserSetupQuest.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := resumed.Read(context.Background(), "local", projection.RunID)
	if err != nil || again.RunID != projection.RunID {
		t.Fatalf("resume=%+v err=%v", again, err)
	}

	// Current filesystem drift never replaces the immutable binding.
	drifted := template
	drifted.ProjectEntry = &projecttemplates.ProjectEntry{RelativePath: template.ProjectEntry.RelativePath, OpenAfterCreateDefault: true}
	library.items[0] = drifted
	driftScope, err := service.ForUserTemplateQuest(context.Background(), "local", template.ID, template.UserSetupQuest.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = driftScope.Read(context.Background(), "local", "")
	var failureValue *Failure
	if !errors.As(err, &failureValue) || failureValue.ReasonCode != ReasonDeclarationInvalid {
		t.Fatalf("drift read error=%v", err)
	}
}

func TestUserTemplateQuestCatalogRejectsAttachmentAndQuestCollisions(t *testing.T) {
	first, library := userQuestCatalogFixture(t)
	second := first
	second.ID = "second-template"
	second.UserSetupQuest = first.UserSetupQuest.Clone()
	second.UserSetupQuest.Declaration.ExpectedBlueprintID = second.ID
	library.items = append(library.items, second)
	items, err := NewUserTemplateQuestCatalog(library).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("colliding quests were listed: %+v", items)
	}
}
