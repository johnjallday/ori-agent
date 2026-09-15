package setupjourney

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

func hostQuestServiceFixture(t *testing.T) (*Service, *SQLiteStore, *specialist.SetupJourney) {
	t.Helper()
	_, store := openTestStore(t)
	declaration := accountLinkDeclarationFixture(t)
	reads := map[specialist.SetupStepKind]CanonicalStepRead{
		specialist.SetupStepWorkspaceCreate: {AvailableActions: []ActionID{ActionReviewTeam}},
	}
	service, err := NewService(store, &relationshipStub{err: errors.New("host quests need no relationship")}, readerRegistryStub(t, reads, nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(CombineQuestCatalogs(
		NewInstalledQuestCatalog(&questPlugins{}),
		NewHostQuestCatalog([]specialist.SetupJourney{*declaration}),
	))
	return service, store, declaration
}

func countJourneyRuns(t *testing.T, store *SQLiteStore) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM setup_journey_run").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestHostQuestCatalogListsAndLooksUpOnlyValidHostQuests(t *testing.T) {
	ctx := context.Background()
	valid := accountLinkDeclarationFixture(t)
	specialistShape, err := specialist.NormalizeSetupJourney(*specialistJourneyForHostTest())
	if err != nil {
		t.Fatal(err)
	}
	owned := *valid
	owned.ID = "owned_account_setup"
	owned.OwnerPluginID = "some-plugin"
	catalog := NewHostQuestCatalog([]specialist.SetupJourney{*valid, *specialistShape, owned, *valid})

	summaries, err := catalog.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("host catalog listed %d quests, want only the valid one: %+v", len(summaries), summaries)
	}
	summary := summaries[0]
	if summary.Source != QuestSourceHost || summary.ID != valid.ID || summary.PluginID != "" || summary.AttachmentID != "" ||
		summary.TemplateID != valid.ExpectedBlueprintID || summary.Ownership != "host" || summary.Title != valid.Title {
		t.Fatalf("host summary = %+v", summary)
	}

	definition, err := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: valid.ID})
	if err != nil || definition.Declaration == nil || definition.Declaration.ID != valid.ID || definition.Ownership != "host" {
		t.Fatalf("lookup = %+v, err = %v", definition, err)
	}
	definition.Declaration.Steps[0].Title = "mutated"
	again, _ := catalog.Lookup(ctx, QuestKey{Source: QuestSourceHost, ID: valid.ID})
	if again.Declaration.Steps[0].Title == "mutated" {
		t.Fatal("host catalog returned shared declaration state")
	}
	for name, key := range map[string]QuestKey{
		"unknown":         {Source: QuestSourceHost, ID: "missing"},
		"plugin key":      {Source: QuestSourcePlugin, PluginID: "reaper-plugin", ID: valid.ID},
		"host with owner": {Source: QuestSourceHost, PluginID: "reaper-plugin", ID: valid.ID},
		"specialist":      {Source: QuestSourceHost, ID: specialistShape.ID},
	} {
		if _, err := catalog.Lookup(ctx, key); err == nil {
			t.Errorf("%s lookup succeeded", name)
		}
	}
}

// FR 14, FR 19, FR 39: the first authorized read creates exactly one host root,
// every later entry point reuses it, a status read never creates one, and no
// plugin, user-template, or assistant-owned root is ever adopted.
func TestHostQuestRootIdentityIsExactAndStatusNeverCreates(t *testing.T) {
	ctx := context.Background()
	service, store, declaration := hostQuestServiceFixture(t)

	if _, err := service.ForHostQuest(ctx, "local", "missing_quest"); err == nil {
		t.Fatal("unknown host quest was scoped")
	}
	if _, err := service.ForHostQuest(ctx, "local", "Bad/ID"); err == nil {
		t.Fatal("invalid host quest id was scoped")
	}

	// A plugin-quest root and an assistant-owned root with the same journey ID
	// must not be adopted by the host quest.
	pluginKey := QuestKey{Source: QuestSourcePlugin, PluginID: "some-plugin", ID: declaration.ID}
	for _, spec := range []RootSpec{
		{OwnerUserID: "local", RelationshipID: questRelationshipID(pluginKey), SpecialistSlug: pluginQuestSlug, JourneyID: declaration.ID, DeclarationSchemaVersion: 1, DeclarationVersion: 1, StepIDs: []string{"team", "connect", "mailbox", "summary"}},
		{OwnerUserID: "local", RelationshipID: "assistant-1", SpecialistSlug: "music_production", JourneyID: declaration.ID, DeclarationSchemaVersion: 1, DeclarationVersion: 1, StepIDs: []string{"team", "connect", "mailbox", "summary"}},
	} {
		if _, _, err := store.CreateOrGetRoot(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	before := countJourneyRuns(t, store)

	scoped, err := service.ForHostQuest(ctx, "local", declaration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countJourneyRuns(t, store) != before {
		t.Fatal("scoping a host quest wrote a journey row")
	}
	status, exists, err := scoped.Status(ctx, "local")
	if err != nil || exists || status != nil {
		t.Fatalf("status before first read = %#v, exists = %v, err = %v", status, exists, err)
	}
	if countJourneyRuns(t, store) != before {
		t.Fatal("status read created a journey row")
	}

	first, err := scoped.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if countJourneyRuns(t, store) != before+1 {
		t.Fatalf("first host read created %d rows, want 1", countJourneyRuns(t, store)-before)
	}
	root, err := store.GetRun(ctx, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	hostKey := QuestKey{Source: QuestSourceHost, ID: declaration.ID}
	if root.SpecialistSlug != hostQuestSlug || root.RelationshipID != questRelationshipID(hostKey) || root.JourneyID != declaration.ID {
		t.Fatalf("host root identity = %#v", root)
	}
	if first.Journey.Source != QuestSourceHost || first.Journey.TemplateID != declaration.ExpectedBlueprintID || first.Journey.PluginID != "" {
		t.Fatalf("host projection source = %#v", first.Journey)
	}
	if questRelationshipID(hostKey) == questRelationshipID(pluginKey) {
		t.Fatal("host and plugin relationship digests collide")
	}

	// Another entry point reuses the same root; status now reports it.
	otherEntry, err := service.ForHostQuest(ctx, "local", declaration.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := otherEntry.Read(ctx, "local", "")
	if err != nil || again.RunID != first.RunID {
		t.Fatalf("second entry point read = %#v, err = %v", again, err)
	}
	status, exists, err = otherEntry.Status(ctx, "local")
	if err != nil || !exists || status == nil || status.RunID != first.RunID {
		t.Fatalf("status after read = %#v, exists = %v, err = %v", status, exists, err)
	}
	if countJourneyRuns(t, store) != before+1 {
		t.Fatal("reuse or status created another root")
	}

	// Another user never sees this root, and their status creates nothing.
	otherUser, err := service.ForHostQuest(ctx, "other-user", declaration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, err := otherUser.Status(ctx, "other-user"); err != nil || exists {
		t.Fatalf("other user status exists = %v, err = %v", exists, err)
	}
	if _, err := otherUser.Read(ctx, "other-user", first.RunID); err == nil {
		t.Fatal("another user read a foreign host run")
	}

	if _, err := store.FindQuestRoot(ctx, "local", QuestKey{Source: "custom", ID: declaration.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown quest source was queried: %v", err)
	}
}

func TestHostQuestAppearsInTheCombinedCatalog(t *testing.T) {
	service, store, declaration := hostQuestServiceFixture(t)
	quests, err := service.ListQuests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, quest := range quests {
		found = found || (quest.Source == QuestSourceHost && quest.ID == declaration.ID)
	}
	if !found {
		t.Fatalf("combined catalog omitted the host quest: %+v", quests)
	}
	if countJourneyRuns(t, store) != 0 {
		t.Fatal("listing quests wrote a journey row")
	}
}

// specialistJourneyForHostTest is a project_setup declaration, the shape only
// plugins and user templates author; no host catalog serves it.
func specialistJourneyForHostTest() *specialist.SetupJourney {
	return &specialist.SetupJourney{
		SchemaVersion: 1, Version: 1, ID: "specialist_fixture", Title: "Specialist", Description: "Project setup shape.",
		IntegrationKey: "fixture_integration", ExpectedBlueprintID: "fixture-blueprint", ExpectedAssistantProgramID: "fixture-program",
		Steps: []specialist.SetupJourneyStep{
			{ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Project", Description: "Connect."},
			{ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Workspace", Description: "Choose."},
			{ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Staffing", Description: "Staff."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Summary", Description: "Review."},
		},
		WorkspaceLaunch: &specialist.WorkspaceLaunchCopy{GroupTitle: "Group", GroupName: "Group"},
	}
}
