package personalassistant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type capabilityEmail struct{ status EmailCapabilityStatus }

var errUnavailableForTest = errors.New("workspace source unavailable")

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

type emailOpsLocator struct {
	exists bool
	err    error
	users  []string
}

func (l *emailOpsLocator) HasEmailOpsWorkspace(userID string) (bool, error) {
	l.users = append(l.users, userID)
	return l.exists, l.err
}

// FR 40: only the "Set up email" state changes, and only when the user has no
// Email Ops workspace; every other state keeps its own route.
func TestCapabilityService_EmailSetupRoutesToTheQuestWithoutAnEmailOpsWorkspace(t *testing.T) {
	const questURL = "/?setup=quest&source=host&quest=email_ops_setup"
	service, _, _, _, _ := serviceMatrixFixture(StatusActive)
	workspaces := workspace.NewInMemoryStore()
	cases := []struct {
		name      string
		status    EmailCapabilityStatus
		locator   *emailOpsLocator
		wantRoute string
		wantLabel string
	}{
		{"not configured, no Email Ops", EmailCapabilityStatus{Status: CapabilityNotConfigured, Route: "/settings#google-account"}, &emailOpsLocator{}, questURL, "Set up email"},
		{"unavailable, no Email Ops", EmailCapabilityStatus{Status: CapabilityUnavailable}, &emailOpsLocator{}, questURL, "Set up email"},
		{"not configured, Email Ops exists", EmailCapabilityStatus{Status: CapabilityNotConfigured, Route: "/settings#google-account"}, &emailOpsLocator{exists: true}, "/settings#google-account", "Set up email"},
		{"locator failed", EmailCapabilityStatus{Status: CapabilityNotConfigured, Route: "/settings#google-account"}, &emailOpsLocator{err: errUnavailableForTest}, "/settings#google-account", "Set up email"},
		{"available stays a review", EmailCapabilityStatus{Status: CapabilityAvailable, Route: "/settings#google-account"}, &emailOpsLocator{}, "/settings#google-account", "Review email connection"},
		{"revoked stays a repair", EmailCapabilityStatus{Status: CapabilityRevoked, Route: "/settings#google-account"}, &emailOpsLocator{}, "/settings#google-account", "Repair email connection"},
		{"no locator wired", EmailCapabilityStatus{Status: CapabilityNotConfigured, Route: "/settings#google-account"}, nil, "/settings#google-account", "Set up email"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			capabilities := NewCapabilityService(service, workspaces, capabilityEmail{status: test.status})
			if test.locator != nil {
				capabilities.SetEmailOpsWorkspaceLocator(test.locator)
			}
			projection, err := capabilities.Get(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			email := projection.Cards[0]
			if email.Key != "email" || email.ActionRoute != test.wantRoute || email.ActionLabel != test.wantLabel || email.Status != test.status.Status {
				t.Fatalf("email card = %+v, want route %q label %q", email, test.wantRoute, test.wantLabel)
			}
			if test.locator != nil && test.status.Status != CapabilityAvailable && test.status.Status != CapabilityRevoked &&
				(len(test.locator.users) != 1 || test.locator.users[0] != "local") {
				t.Fatalf("locator asked about %v, want the current user once", test.locator.users)
			}
		})
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
