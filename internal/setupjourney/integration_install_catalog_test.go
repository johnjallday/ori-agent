package setupjourney

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func installCatalogEntries() []reviewedintegration.Entry {
	reaper, _ := reviewedintegration.Get("ori_reaper")
	other := reaper.Clone()
	other.Key, other.PluginID = "ori_other", "other-plugin"
	other.DisplayName, other.InstallTitle = "Other", "Install Ori Other Plugin"
	other.InstallDescription = "The Other integration is local to Ori."
	other.ExpectedBlueprintID, other.ExpectedProgramID = "other-song", "other-assistant"
	return []reviewedintegration.Entry{reaper, other}
}

func TestIntegrationInstallCatalogGeneratesOneQuestPerEntry(t *testing.T) {
	ctx := context.Background()
	entries := installCatalogEntries()
	catalog := NewIntegrationInstallQuestCatalog(func() []reviewedintegration.Entry { return entries }, &questPlugins{})

	summaries, err := catalog.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != len(entries) {
		t.Fatalf("listed %d install quests, want %d: %+v", len(summaries), len(entries), summaries)
	}
	for _, entry := range entries {
		id := "install_" + entry.Key
		var summary *QuestSummary
		for index := range summaries {
			if summaries[index].ID == id {
				summary = &summaries[index]
			}
		}
		if summary == nil {
			t.Fatalf("install quest %q missing from %+v", id, summaries)
		}
		if summary.Source != QuestSourceHost || summary.PluginID != "" || summary.TemplateID != "" || summary.AttachmentID != "" ||
			summary.Ownership != "host" || summary.IntegrationKey != entry.Key || summary.Title != entry.InstallTitle ||
			summary.DisplayName != entry.DisplayName || summary.PublisherLabel != entry.PublisherLabel ||
			!strings.Contains(summary.Description, entry.DisplayName) {
			t.Fatalf("install summary = %+v", summary)
		}
		if !validQuestKey(summary.QuestKey) {
			t.Fatalf("install quest key is not a valid host key: %+v", summary.QuestKey)
		}

		definition, err := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: id})
		if err != nil {
			t.Fatalf("lookup %q: %v", id, err)
		}
		d := definition.Declaration
		if d.Shape() != specialist.SetupJourneyShapeIntegrationInstall || d.ID != id || d.SchemaVersion != 1 || d.Version != 1 ||
			d.Title != entry.InstallTitle || d.IntegrationKey != entry.Key || d.ExpectedBlueprintID != entry.ExpectedBlueprintID ||
			d.ExpectedAssistantProgramID != entry.ExpectedProgramID || d.WorkspaceLaunch != nil || d.OwnerPluginID != "" ||
			definition.Ownership != "host" || definition.Key != (QuestKey{Source: QuestSourceHost, ID: id}) {
			t.Fatalf("install declaration = %+v / %+v", definition, d)
		}
		if d.Steps[0].ID != "integration" || d.Steps[0].Title != entry.InstallTitle || d.Steps[0].Description != entry.InstallDescription ||
			d.Steps[1].ID != "summary" || d.Steps[1].Title != entry.DisplayName+" plugin ready" {
			t.Fatalf("install steps = %+v", d.Steps)
		}
	}

	// IDs are stable across reads, and a returned declaration is independent.
	first, _ := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: "install_ori_reaper"})
	first.Declaration.Steps[0].Title = "mutated"
	again, _ := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: "install_ori_reaper"})
	if again.Declaration.Steps[0].Title != "Install Ori REAPER Plugin" {
		t.Fatal("install catalog returned shared declaration state")
	}
}

func TestIntegrationInstallCatalogHidesInstalledButStillResolves(t *testing.T) {
	ctx := context.Background()
	entries := installCatalogEntries()
	plugins := &questPlugins{items: []plugin.InstalledPlugin{{Name: "reaper-plugin", Version: "0.5.0", Enabled: false}}}
	catalog := NewIntegrationInstallQuestCatalog(func() []reviewedintegration.Entry { return entries }, plugins)

	summaries, err := catalog.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].ID != "install_ori_other" {
		t.Fatalf("installed (even disabled) plugin's install quest was listed: %+v", summaries)
	}
	if _, err := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: "install_ori_reaper"}); err != nil {
		t.Fatalf("installed plugin's install quest no longer resolves: %v", err)
	}

	plugins.err = errors.New("plugin store unavailable")
	if _, err := catalog.List(ctx); err == nil {
		t.Fatal("listing succeeded without knowing install state")
	}
	if _, err := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: "install_ori_reaper"}); err != nil {
		t.Fatalf("lookup depended on the plugin store: %v", err)
	}
}

func TestIntegrationInstallCatalogRejectsForeignKeys(t *testing.T) {
	ctx := context.Background()
	catalog := NewIntegrationInstallQuestCatalog(reviewedintegration.All, &questPlugins{})
	for name, key := range map[string]QuestKey{
		"unknown integration": {Source: QuestSourceHost, ID: "install_missing"},
		"bare key":            {Source: QuestSourceHost, ID: "ori_reaper"},
		"plugin source":       {Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: "install_ori_reaper"},
		"host with plugin":    {Source: QuestSourceHost, PluginID: "reaper-plugin", ID: "install_ori_reaper"},
		"email ops":           {Source: QuestSourceHost, ID: "email_ops_setup"},
	} {
		if _, err := catalog.Lookup(ctx, key); err == nil {
			t.Errorf("%s lookup succeeded", name)
		}
	}
}

// Every built-in registry entry generates a valid install quest, so a registry
// change cannot silently drop one from the catalog.
func TestBuiltInRegistryGeneratesValidInstallQuests(t *testing.T) {
	for _, entry := range reviewedintegration.All() {
		declaration, ok := integrationInstallDeclaration(entry)
		if !ok || !validHostQuestShape(declaration) {
			t.Fatalf("registry entry %q does not generate a valid install quest", entry.Key)
		}
	}
}

func installQuestServiceFixture(t *testing.T, plugins *questPlugins) (*Service, *SQLiteStore) {
	t.Helper()
	service, store := serviceFixture(t, defaultCanonicalReads())
	service.SetQuestCatalog(CombineQuestCatalogs(
		NewIntegrationInstallQuestCatalog(reviewedintegration.All, plugins),
		NewInstalledQuestCatalog(plugins),
	))
	return service, store
}

// FR 13, FR 14: the host-quest route family scopes an install quest, a status
// read creates nothing, and listing the catalog creates nothing.
func TestInstallQuestUsesTheHostQuestScopeAndCreatesNothingOnList(t *testing.T) {
	ctx := context.Background()
	service, store := installQuestServiceFixture(t, &questPlugins{})

	quests, err := service.ListQuests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, quest := range quests {
		found = found || (quest.Source == QuestSourceHost && quest.ID == "install_ori_reaper" && quest.IntegrationKey == "ori_reaper")
	}
	if !found {
		t.Fatalf("combined catalog omitted the install quest: %+v", quests)
	}
	if countJourneyRuns(t, store) != 0 {
		t.Fatal("listing quests created progress")
	}

	scoped, err := service.ForHostQuest(ctx, "local", "install_ori_reaper")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, err := scoped.Status(ctx, "local"); err != nil || exists {
		t.Fatalf("status before read exists=%v err=%v", exists, err)
	}
	if countJourneyRuns(t, store) != 0 {
		t.Fatal("status created progress")
	}
	projection, err := scoped.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Steps) != 2 || projection.Steps[0].Kind != specialist.SetupStepIntegrationInstall ||
		projection.Steps[1].Kind != specialist.SetupStepSummary || projection.Journey.Source != QuestSourceHost ||
		projection.Journey.PluginID != "" || projection.Journey.WorkspaceLaunch != nil ||
		projection.Journey.Title != "Install Ori REAPER Plugin" {
		t.Fatalf("install projection = %+v", projection)
	}
	root, err := store.GetRun(ctx, projection.RunID)
	if err != nil || root.SpecialistSlug != hostQuestSlug ||
		root.RelationshipID != questRelationshipID(QuestKey{Source: QuestSourceHost, ID: "install_ori_reaper"}) {
		t.Fatalf("install root = %+v err = %v", root, err)
	}
	if countJourneyRuns(t, store) != 1 {
		t.Fatalf("first read created %d rows, want 1", countJourneyRuns(t, store))
	}
}

type fixedQuestCatalog struct{ definition QuestDefinition }

func (c fixedQuestCatalog) List(context.Context) ([]QuestSummary, error) { return nil, nil }
func (c fixedQuestCatalog) Lookup(_ context.Context, key QuestKey) (QuestDefinition, error) {
	definition := c.definition
	definition.Key = key
	return definition, nil
}

// FR 13: a host quest is an account-link quest or the one install quest the
// host generates for a reviewed integration; nothing else scopes.
func TestHostQuestScopeAcceptsOnlyHostShapes(t *testing.T) {
	ctx := context.Background()
	reaper, _ := reviewedintegration.Get("ori_reaper")
	generated, ok := integrationInstallDeclaration(reaper)
	if !ok {
		t.Fatal("install declaration fixture is invalid")
	}
	specialistShape, err := specialist.NormalizeSetupJourney(*specialistJourneyForHostTest())
	if err != nil {
		t.Fatal(err)
	}
	wrongID := *generated
	wrongID.ID = "install_elsewhere"
	unreviewed := *generated
	unreviewed.ID, unreviewed.IntegrationKey = "install_fixture_integration", "fixture_integration"
	wrongBlueprint := *generated
	wrongBlueprint.ExpectedBlueprintID = "other-blueprint"

	for name, test := range map[string]struct {
		declaration *specialist.SetupJourney
		accept      bool
	}{
		"account link":          {declaration: accountLinkDeclarationFixture(t), accept: true},
		"generated install":     {declaration: generated, accept: true},
		"specialist shape":      {declaration: specialistShape},
		"install with other id": {declaration: &wrongID},
		"unreviewed install":    {declaration: &unreviewed},
		"install wrong target":  {declaration: &wrongBlueprint},
	} {
		t.Run(name, func(t *testing.T) {
			service, _ := serviceFixture(t, defaultCanonicalReads())
			service.SetQuestCatalog(fixedQuestCatalog{definition: QuestDefinition{Declaration: test.declaration, Ownership: "host"}})
			_, err := service.ForHostQuest(ctx, "local", test.declaration.ID)
			if test.accept && err != nil {
				t.Fatalf("host scope rejected %s: %v", name, err)
			}
			if !test.accept && err == nil {
				t.Fatalf("host scope accepted %s", name)
			}
		})
	}

	// A plugin source never carries the install shape.
	service, _ := serviceFixture(t, defaultCanonicalReads())
	owned := *generated
	owned.OwnerPluginID = "reaper-plugin"
	service.SetQuestCatalog(fixedQuestCatalog{definition: QuestDefinition{Declaration: &owned, Ownership: "plugin"}})
	if _, err := service.ForQuest(ctx, "local", "reaper-plugin", owned.ID); err == nil {
		t.Fatal("plugin scope accepted an install-shaped declaration")
	}
}
