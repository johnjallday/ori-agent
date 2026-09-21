package personalassistanthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type milestoneRelationships struct{}

func (milestoneRelationships) Get(context.Context, string) (*personalassistant.Projection, error) {
	return &personalassistant.Projection{State: personalassistant.APIStateActive, AssistantID: "assistant-1", DisplayName: "Ari"}, nil
}

type milestoneResolver struct{}

func (milestoneResolver) Resolve(context.Context, string) ([]assistantsetup.Target, error) {
	return nil, nil
}
func (milestoneResolver) ResolveOne(_ context.Context, _ string, id string) (*assistantsetup.Target, error) {
	return &assistantsetup.Target{WorkspaceID: id, Name: assistantsetup.WorkspaceName, Supported: true, Route: "/workspaces/" + id}, nil
}

type milestonePlans struct{}

func (milestonePlans) ReviewFileJanitorPlan(context.Context, string) (assistantsetup.TeamPlan, error) {
	return assistantsetup.TeamPlan{
		BlueprintID: assistantsetup.BlueprintID, BlueprintVersion: 2, BlueprintDigest: "blueprint-digest-value",
		PlanRevision: "team-revision", Roles: []assistantsetup.TeamRole{{
			RoleID: "file-curator", Name: "File Curator", Action: "create", ConfigDigest: "config-digest-value",
		}},
	}, nil
}

type milestonePreparer struct{ calls int }

func (p *milestonePreparer) PrepareFileJanitor(_ context.Context, request assistantsetup.PrepareRequest) (assistantsetup.WorkspaceResult, error) {
	p.calls++
	return assistantsetup.WorkspaceResult{
		WorkspaceID: request.WorkspaceID, AgentInstanceID: "instance-1", ProfileProvenanceID: request.ProfileProvenanceID,
		ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest-value",
	}, nil
}

func TestAssistantSetupProjectionCarriesSafeMilestonesAndGetsWriteNothing(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := assistantsetup.NewSQLiteStore(db)
	preparer := &milestonePreparer{}
	service := assistantsetup.NewService(store, milestoneRelationships{}, milestoneResolver{}, milestonePlans{}, preparer)

	proposal, err := service.Get(ctx, "owner-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Milestones) != 0 {
		t.Fatalf("a proposal must not carry milestones: %+v", proposal.Milestones)
	}
	accepted, _, err := service.Accept(ctx, "owner-1", proposal.Proposal.Revision, "")
	if err != nil {
		t.Fatal(err)
	}

	handler := assistantSetupHTTPHandler(service)
	get := func() string {
		recorder := httptest.NewRecorder()
		handler.GetAssistantSetup(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/setup/file-janitor", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	before, err := store.GetRun(ctx, "owner-1", accepted.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	resourcesBefore, _ := store.ListResources(ctx, "owner-1", accepted.Run.ID)
	operationsBefore, _ := store.ListOperations(ctx, "owner-1", accepted.Run.ID)

	body := get()
	if second := get(); second != body {
		t.Fatalf("repeated GETs differ:\n%s\n%s", body, second)
	}

	var envelope struct {
		Setup struct {
			Milestones []map[string]any `json:"milestones"`
		} `json:"setup"`
		Milestones []map[string]any `json:"milestones"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	milestones := envelope.Setup.Milestones
	if len(milestones) == 0 {
		milestones = envelope.Milestones
	}
	if len(milestones) != 2 || milestones[0]["status"] != "created" || milestones[1]["status"] != "created" {
		t.Fatalf("milestones in body = %s", body)
	}

	encoded, _ := json.Marshal(milestones)
	text := string(encoded)
	for _, forbidden := range []string{"provenance", "digest", "prompt", "token", "/Users", "/tmp", `\\`, "path"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("milestones expose %q: %s", forbidden, text)
		}
	}
	for _, milestone := range milestones {
		for key, value := range milestone {
			if str, ok := value.(string); ok && strings.HasPrefix(str, "/") {
				t.Fatalf("milestone %q looks like a path: %q", key, str)
			}
		}
	}

	after, err := store.GetRun(ctx, "owner-1", accepted.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	resourcesAfter, _ := store.ListResources(ctx, "owner-1", accepted.Run.ID)
	operationsAfter, _ := store.ListOperations(ctx, "owner-1", accepted.Run.ID)
	if after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) ||
		len(resourcesAfter) != len(resourcesBefore) || len(operationsAfter) != len(operationsBefore) {
		t.Fatalf("GET wrote state: run %d→%d resources %d→%d operations %d→%d",
			before.Revision, after.Revision, len(resourcesBefore), len(resourcesAfter), len(operationsBefore), len(operationsAfter))
	}
	if preparer.calls != 1 {
		t.Fatalf("GETs re-entered the preparer: calls=%d", preparer.calls)
	}
}
