package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// installingAdapter installs the fixture plugin when its commit runs, the way
// the reviewed integration adapter changes the plugin store mid-action.
type installingAdapter struct {
	plugins *questPlugins
	install plugin.InstalledPlugin
}

func (a *installingAdapter) material(input json.RawMessage) ActionReviewMaterial {
	return ActionReviewMaterial{
		CommitAction: ActionInstall, InputDigest: Digest(input),
		OwnerRevisionDigest: Digest([]byte("owner")), DisclosureDigest: Digest([]byte("disclosure")),
	}
}
func (a *installingAdapter) InputDigest(_ ActionID, input json.RawMessage) (string, error) {
	return Digest(input), nil
}
func (a *installingAdapter) Review(_ context.Context, _ ReadScope, _ ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	return a.material(input), nil
}
func (a *installingAdapter) PrepareCommit(_ context.Context, _ ReadScope, _ ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	return a.material(input), nil
}
func (a *installingAdapter) Commit(context.Context, ReadScope, ActionID, json.RawMessage, ActionReviewMaterial) (CanonicalResult, error) {
	a.plugins.items = []plugin.InstalledPlugin{a.install}
	return CanonicalResult{IntegrationPluginID: a.install.Name, IntegrationVersion: a.install.Version}, nil
}
func (a *installingAdapter) ConsequenceObserved(_ ActionID, read CanonicalStepRead) bool {
	return read.Complete
}

// installAliasFixture serves the accepted music specialist with a plugin store
// the test controls. The install step is complete exactly when a plugin is
// installed, and the summary offers the Plugins page.
func installAliasFixture(t *testing.T) (*Service, *SQLiteStore, *questPlugins) {
	t.Helper()
	_, store := openTestStore(t)
	plugins := &questPlugins{}
	reads := defaultCanonicalReads()
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		kind := kind
		readers[kind] = CanonicalReaderFunc(func(_ context.Context, scope ReadScope) (CanonicalStepRead, error) {
			switch {
			case kind == specialist.SetupStepIntegrationInstall && len(plugins.items) > 0:
				return CanonicalStepRead{Complete: true, Result: CanonicalResult{IntegrationPluginID: plugins.items[0].Name, IntegrationVersion: plugins.items[0].Version}}, nil
			case kind == specialist.SetupStepIntegrationInstall:
				return CanonicalStepRead{AvailableActions: []ActionID{ActionReviewInstall}}, nil
			case kind == specialist.SetupStepSummary && scope.Shape == specialist.SetupJourneyShapeIntegrationInstall:
				return CanonicalStepRead{AvailableActions: []ActionID{ActionOpenPlugins}}, nil
			}
			return reads[kind], nil
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(CombineQuestCatalogs(
		NewIntegrationInstallQuestCatalog(reviewedintegration.All, plugins),
		NewInstalledQuestCatalog(plugins),
	))
	return service, store, plugins
}

// FR 20: the accepted specialist's alias is the install quest while the plugin
// is not installed, or is installed but lists no quest for the integration;
// otherwise it is the plugin's own quest.
func TestAssistantAliasResolvesInstallQuestUntilThePluginListsItsQuest(t *testing.T) {
	ctx := context.Background()
	service, _, plugins := installAliasFixture(t)

	before, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Journey.Source != QuestSourceHost || before.Journey.ID != "install_ori_reaper" ||
		before.Journey.Title != "Install Ori REAPER Plugin" || len(before.Steps) != 2 {
		t.Fatalf("pre-install alias = %+v", before.Journey)
	}
	overview, err := service.Overview(ctx, "local")
	if err != nil || overview.Root.Journey.Title != "Install Ori REAPER Plugin" {
		t.Fatalf("pre-install Home overview = %+v, err = %v", overview, err)
	}

	// Installed, but an older manifest declares no setup_quests_v2 quest.
	older := questPluginFixture(t)
	older.Version = "0.5.2"
	older.WorkspaceSurfaces.RequiresHostFeatures = []string{"setup_quests_v1"}
	plugins.items = []plugin.InstalledPlugin{older}
	stillInstall, err := service.Read(ctx, "local", "")
	if err != nil || stillInstall.Journey.ID != "install_ori_reaper" || stillInstall.RunID != before.RunID {
		t.Fatalf("installed-without-quest alias = %+v, err = %v", stillInstall, err)
	}

	plugins.items = []plugin.InstalledPlugin{questPluginFixture(t)}
	after, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if after.Journey.PluginID != "reaper-plugin" || after.Journey.ID != "reaper_setup" || len(after.Steps) != 4 ||
		after.RunID == before.RunID {
		t.Fatalf("post-install alias = %+v", after.Journey)
	}

	// A plugin store that cannot be read fails safe to the install quest,
	// whose own read reports the problem.
	plugins.err = errors.New("plugin store unavailable")
	unreadable, err := service.Read(ctx, "local", "")
	if err != nil || unreadable.Journey.ID != "install_ori_reaper" {
		t.Fatalf("unreadable store alias = %+v, err = %v", unreadable, err)
	}
}

// Installing inside an alias action must finish against the install root it
// started on, not switch to the plugin quest between the claim and the read.
func TestAssistantAliasActionStaysOnTheQuestItStartedOn(t *testing.T) {
	ctx := context.Background()
	service, store, plugins := installAliasFixture(t)
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, &installingAdapter{
		plugins: plugins, install: questPluginFixture(t),
	}); err != nil {
		t.Fatal(err)
	}
	root, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Mutate(ctx, "local", root.RunID, ActionReviewInstall, ActionMutation{
		IfRevision: root.StateRevision, IdempotencyKey: "review-install", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Mutate(ctx, "local", root.RunID, ActionInstall, ActionMutation{
		IfRevision: review.Journey.StateRevision, IdempotencyKey: "install", ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("alias install action failed after installing: %v", err)
	}
	if result.Journey.RunID != root.RunID || result.Journey.Journey.ID != "install_ori_reaper" ||
		result.Journey.Lifecycle != LifecycleReady || result.Journey.Receipts.IntegrationPluginID != "reaper-plugin" {
		t.Fatalf("install result = %+v", result.Journey)
	}
	stored, err := store.GetRun(ctx, root.RunID)
	if err != nil || stored.IntegrationVersion != "0.6.0" {
		t.Fatalf("install root = %+v, err = %v", stored, err)
	}

	// The same user now reaches the plugin quest through the alias.
	next, err := service.Read(ctx, "local", "")
	if err != nil || next.Journey.PluginID != "reaper-plugin" {
		t.Fatalf("alias after install = %+v, err = %v", next, err)
	}
}

type listedQuests []QuestSummary

func (l listedQuests) List(context.Context) ([]QuestSummary, error) { return l, nil }
func (l listedQuests) Lookup(context.Context, QuestKey) (QuestDefinition, error) {
	return QuestDefinition{}, failure(ReasonJourneyUnavailable, 0)
}

func TestIntegrationHandoffNamesTheListedPluginQuest(t *testing.T) {
	ctx := context.Background()
	if got := IntegrationHandoff(ctx, listedQuests{}, "ori_reaper"); got != nil {
		t.Fatalf("empty catalog handoff = %+v", got)
	}
	catalog := listedQuests{
		{QuestKey: QuestKey{Source: QuestSourceHost, ID: "install_ori_reaper"}, Title: "Install Ori REAPER Plugin"},
		{QuestKey: QuestKey{Source: QuestSourcePlugin, PluginID: "other-plugin", ID: "a_setup"}, Title: "Other"},
		{QuestKey: QuestKey{Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: "z_setup"}, Title: "Later"},
		{QuestKey: QuestKey{Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: "reaper_setup"}, Title: "Set up REAPER"},
	}
	got := IntegrationHandoff(ctx, catalog, "ori_reaper")
	want := &QuestHandoffProjection{Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: "reaper_setup", Title: "Set up REAPER"}
	if got == nil || *got != *want {
		t.Fatalf("handoff = %+v, want %+v", got, want)
	}
	if IntegrationHandoff(ctx, catalog, "unknown_key") != nil || IntegrationHandoff(ctx, nil, "ori_reaper") != nil {
		t.Fatal("handoff resolved without a reviewed key or catalog")
	}
	unsafe := listedQuests{{QuestKey: QuestKey{Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: "reaper_setup"}, Title: "<b>Set up</b>"}}
	if IntegrationHandoff(ctx, unsafe, "ori_reaper") != nil {
		t.Fatal("handoff carried unsafe title text")
	}
}

func TestSummaryHandoffTravelsOnlyWithItsContinueAction(t *testing.T) {
	handoff := &QuestHandoffProjection{Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: "reaper_setup", Title: "Set up REAPER"}
	for name, test := range map[string]struct {
		kind  specialist.SetupStepKind
		read  CanonicalStepRead
		valid bool
	}{
		"continue with target": {kind: specialist.SetupStepSummary, valid: true, read: CanonicalStepRead{
			AvailableActions: []ActionID{ActionContinueIntegrationSetup, ActionOpenPlugins}, Handoff: handoff,
		}},
		"plugins only":    {kind: specialist.SetupStepSummary, valid: true, read: CanonicalStepRead{AvailableActions: []ActionID{ActionOpenPlugins}}},
		"continue alone":  {kind: specialist.SetupStepSummary, read: CanonicalStepRead{AvailableActions: []ActionID{ActionContinueIntegrationSetup}}},
		"target alone":    {kind: specialist.SetupStepSummary, read: CanonicalStepRead{AvailableActions: []ActionID{ActionOpenPlugins}, Handoff: handoff}},
		"target off step": {kind: specialist.SetupStepIntegrationInstall, read: CanonicalStepRead{Handoff: handoff}},
		"host target": {kind: specialist.SetupStepSummary, read: CanonicalStepRead{
			AvailableActions: []ActionID{ActionContinueIntegrationSetup},
			Handoff:          &QuestHandoffProjection{Source: QuestSourceHost, ID: "email_ops_setup", Title: "Email"},
		}},
		"unnormalized target": {kind: specialist.SetupStepSummary, read: CanonicalStepRead{
			AvailableActions: []ActionID{ActionContinueIntegrationSetup},
			Handoff:          &QuestHandoffProjection{Source: QuestSourcePlugin, PluginID: "REAPER-plugin", ID: "reaper_setup", Title: "Set up"},
		}},
	} {
		if got := validCanonicalRead(test.kind, test.read); got != test.valid {
			t.Errorf("%s: valid = %v, want %v", name, got, test.valid)
		}
	}
}
