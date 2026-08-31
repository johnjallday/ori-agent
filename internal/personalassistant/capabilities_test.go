package personalassistant

import (
	"context"
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
