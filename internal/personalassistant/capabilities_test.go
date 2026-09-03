package personalassistant

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type capabilityEmail struct{ status EmailCapabilityStatus }

func (s capabilityEmail) EmailCapability(context.Context, string) EmailCapabilityStatus {
	return s.status
}

func TestCapabilityService_ProjectsExistingSourcesWithoutGrantingAnything(t *testing.T) {
	service, _, _, _, _ := serviceMatrixFixture(StatusActive)
	workspaces := workspace.NewInMemoryStore()
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	hq.ID, hq.FolderSlug, hq.OwnerUserID = "hq-local", "personal-hq", "local"
	hq.DirectoryReferences = []workspace.DirectoryReference{{ID: "folder-1", Name: "Approved", Path: "files"}}
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Launch"})
	project.ID, project.FolderSlug, project.OwnerUserID = "project-1", "launch", "local"
	calendar := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Calendar Ops"})
	calendar.ID, calendar.FolderSlug, calendar.OwnerUserID = "calendar-1", "calendar-ops", "local"
	calendar.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "calendar-ops", Builtin: true})
	if err := calendar.UpsertMCPBinding(workspace.MCPBinding{
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
	foreign := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Foreign Calendar"})
	foreign.ID, foreign.FolderSlug, foreign.OwnerUserID = "foreign", "foreign-calendar", "another-user"
	foreign.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "calendar-ops", Builtin: true})
	for _, ws := range []*workspace.Workspace{hq, project, calendar, foreign} {
		if err := workspaces.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	projection, err := NewCapabilityService(service, workspaces, capabilityEmail{status: EmailCapabilityStatus{
		Status: CapabilityAvailable, Route: "https://evil.example/steal",
	}}).Get(context.Background(), "local")
	if err != nil || len(projection.Cards) != 4 {
		t.Fatalf("capabilities=%+v err=%v", projection, err)
	}
	cards := map[string]CapabilityCard{}
	for _, card := range projection.Cards {
		cards[card.Key] = card
		if card.ActionRoute == "https://evil.example/steal" || card.CanRead == "" || card.CanPropose == "" || card.RequiresConfirmation == "" {
			t.Fatalf("unsafe/incomplete card: %+v", card)
		}
	}
	if cards["email"].Status != CapabilityAvailable || cards["calendar"].Status != CapabilityAvailable ||
		cards["projects"].Status != CapabilityAvailable || cards["folders"].Status != CapabilityAvailable {
		t.Fatalf("healthy source states=%+v", cards)
	}
}

func TestCapabilityService_DistinguishesEmptyRevokedAndPreHire(t *testing.T) {
	service, _, _, _, _ := serviceMatrixFixture(StatusPaused)
	workspaces := workspace.NewInMemoryStore()
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	hq.ID, hq.OwnerUserID = "hq-local", "local"
	if err := workspaces.Save(hq); err != nil {
		t.Fatal(err)
	}
	projection, err := NewCapabilityService(service, workspaces, capabilityEmail{status: EmailCapabilityStatus{
		Status: CapabilityRevoked, Reason: "credential_revoked", Route: "/settings#google-account",
	}}).Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Cards[0].Status != CapabilityRevoked || projection.Cards[1].Status != CapabilityNotConfigured ||
		projection.Cards[2].Status != CapabilityHealthyEmpty || projection.Cards[3].Status != CapabilityHealthyEmpty {
		t.Fatalf("empty/revoked states=%+v", projection.Cards)
	}

	preHire, _, _, _, _ := serviceMatrixFixture(StatusNotHired)
	projection, err = NewCapabilityService(preHire, workspaces, nil).Get(context.Background(), "local")
	if err != nil || len(projection.Cards) != 0 {
		t.Fatalf("pre-hire capabilities=%+v err=%v", projection, err)
	}
}

func capabilityKeys(cards []CapabilityCard) []string {
	keys := make([]string, 0, len(cards))
	for _, card := range cards {
		keys = append(keys, card.Key)
	}
	return keys
}

func capabilityFixture(t *testing.T, slug string) *CapabilityProjection {
	t.Helper()
	service, store, _, _, _ := serviceMatrixFixture(StatusActive)
	store.state.SpecialistSlug = slug
	workspaces := workspace.NewInMemoryStore()
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	hq.ID, hq.FolderSlug, hq.OwnerUserID = "hq-local", "personal-hq", "local"
	if err := workspaces.Save(hq); err != nil {
		t.Fatal(err)
	}
	projection, err := NewCapabilityService(service, workspaces, nil).Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return projection
}

// The generic order is today's shipped order and must not move.
func TestCapabilityService_GenericCardOrderIsUnchanged(t *testing.T) {
	for _, slug := range []string{"", "retired_domain"} {
		projection := capabilityFixture(t, slug)
		if got := capabilityKeys(projection.Cards); !slices.Equal(got, []string{"email", "calendar", "projects", "folders"}) {
			t.Fatalf("slug %q: card order = %v", slug, got)
		}
		if projection.Suggestion != nil {
			t.Fatalf("slug %q: unexpected suggestion %+v", slug, projection.Suggestion)
		}
	}
}

func TestCapabilityService_SpecialistCardOrderPutsTheDomainFirst(t *testing.T) {
	projection := capabilityFixture(t, "music_production")
	// A producer's own work comes before email and calendar.
	if got := capabilityKeys(projection.Cards); !slices.Equal(got, []string{"projects", "folders", "calendar", "email"}) {
		t.Fatalf("card order = %v", got)
	}
	// Reordering must not drop, duplicate, or invent a card.
	if len(projection.Cards) != 4 {
		t.Fatalf("card count = %d", len(projection.Cards))
	}
}

func TestOrderCapabilityCardsKeepsUnmentionedCardsBehind(t *testing.T) {
	cards := []CapabilityCard{{Key: "email"}, {Key: "calendar"}, {Key: "projects"}, {Key: "folders"}}

	// An order naming only some keys leaves the rest in their existing order,
	// so a card added to the projection later still appears.
	got := capabilityKeys(orderCapabilityCards(cards, []string{"projects"}))
	if !slices.Equal(got, []string{"projects", "email", "calendar", "folders"}) {
		t.Fatalf("partial order = %v", got)
	}
	// Unknown and duplicated keys are ignored rather than producing a hole or
	// a repeated card.
	got = capabilityKeys(orderCapabilityCards(cards, []string{"folders", "nope", "folders", "email"}))
	if !slices.Equal(got, []string{"folders", "email", "calendar", "projects"}) {
		t.Fatalf("noisy order = %v", got)
	}
	got = capabilityKeys(orderCapabilityCards(cards, nil))
	if !slices.Equal(got, []string{"email", "calendar", "projects", "folders"}) {
		t.Fatalf("empty order = %v", got)
	}
}

func TestCapabilityService_SuggestsTheDomainWorkspaceUntilItExists(t *testing.T) {
	projection := capabilityFixture(t, "music_production")
	if projection.Suggestion == nil {
		t.Fatal("expected a post-hire workspace suggestion")
	}
	if projection.Suggestion.TemplateID != "reaper-song" {
		t.Fatalf("suggested template = %q", projection.Suggestion.TemplateID)
	}
	if !strings.HasPrefix(projection.Suggestion.ActionRoute, "/") {
		t.Fatalf("suggestion route must be app-relative: %q", projection.Suggestion.ActionRoute)
	}
	if projection.Suggestion.Title == "" || projection.Suggestion.ActionLabel == "" {
		t.Fatalf("suggestion = %+v", projection.Suggestion)
	}

	// Once the studio workspace exists the suggestion is noise. A blueprint
	// published by a plugin carries a namespaced template ID.
	service, store, _, _, _ := serviceMatrixFixture(StatusActive)
	store.state.SpecialistSlug = "music_production"
	workspaces := workspace.NewInMemoryStore()
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	hq.ID, hq.FolderSlug, hq.OwnerUserID = "hq-local", "personal-hq", "local"
	studio := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ivory"})
	studio.ID, studio.FolderSlug, studio.OwnerUserID = "studio-1", "ivory", "local"
	studio.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "plugin:reaper-plugin:reaper-song"})
	for _, ws := range []*workspace.Workspace{hq, studio} {
		if err := workspaces.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	withStudio, err := NewCapabilityService(service, workspaces, nil).Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if withStudio.Suggestion != nil {
		t.Fatalf("suggestion should disappear once the workspace exists: %+v", withStudio.Suggestion)
	}
	// The domain ordering still applies.
	if got := capabilityKeys(withStudio.Cards); !slices.Equal(got, []string{"projects", "folders", "calendar", "email"}) {
		t.Fatalf("card order = %v", got)
	}
}
