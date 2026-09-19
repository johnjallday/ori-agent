package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func assistantPortfolioHTTPFixture(t *testing.T) (*Handler, *workspace.InMemoryStore, *workspace.Workspace, *workspace.Workspace) {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	store := workspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	declaration := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion,
		ID:            "portfolio-guide", StationName: "Portfolio Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "guide", Label: "Guide", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Coordinate."},
			{ID: "lead", Label: "Lead", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Lead."},
		},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Find stable preferences."},
	}
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HTTP Project"})
	project.OwnerUserID = "local"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:       "plugin:test:project",
		PluginOwner:      &workspace.PluginTemplateOwner{PluginID: "test", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1},
		AssistantProgram: declaration,
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	station, _, err := workspace.NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, station, project
}

func TestAssistantPortfolioHTTPReviewCommitAndHandoff(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	storedProject, _ := store.Get(project.ID)
	linkID := storedProject.GetAssistantProjectLink().ID
	fields := `{"status":"active","priority":3,"milestones":[{"id":"review","label":"Review"}],"blockers":[],"deliverables":["Mix"],"archive_review_state":"not_ready"}`

	reviewRecorder := httptest.NewRecorder()
	handler.ReviewAssistantPortfolio(reviewRecorder, assistantProgramRequest(http.MethodPost, "/portfolio/review", station.ID, `{"link_id":"`+linkID+`","if_revision":0,"fields":`+fields+`}`))
	if reviewRecorder.Code != http.StatusOK {
		t.Fatalf("portfolio review = %d: %s", reviewRecorder.Code, reviewRecorder.Body.String())
	}
	var review workspace.AssistantPortfolioReview
	if err := json.Unmarshal(reviewRecorder.Body.Bytes(), &review); err != nil || review.Token == "" || review.Project.ProjectWorkspaceID != project.ID {
		t.Fatalf("portfolio review body = %#v, %v", review, err)
	}

	commitRecorder := httptest.NewRecorder()
	handler.CommitAssistantPortfolio(commitRecorder, assistantProgramRequest(http.MethodPost, "/portfolio/commit", station.ID, `{"review_token":"`+review.Token+`","idempotency_key":"portfolio-http-1","fields":`+fields+`}`))
	if commitRecorder.Code != http.StatusOK {
		t.Fatalf("portfolio commit = %d: %s", commitRecorder.Code, commitRecorder.Body.String())
	}

	handoffRecorder := httptest.NewRecorder()
	handler.ReviewAssistantHandoff(handoffRecorder, assistantProgramRequest(http.MethodPost, "/handoffs/review", station.ID, `{"link_id":"`+linkID+`","title":"Prepare delivery","description":"Check the approved outputs.","state":"backlog"}`))
	if handoffRecorder.Code != http.StatusOK {
		t.Fatalf("handoff review = %d: %s", handoffRecorder.Code, handoffRecorder.Body.String())
	}
	var handoffReview workspace.AssistantPortfolioHandoffReview
	if err := json.Unmarshal(handoffRecorder.Body.Bytes(), &handoffReview); err != nil || handoffReview.Token == "" || handoffReview.Handoff.Assignment == "" {
		t.Fatalf("handoff review body = %#v, %v", handoffReview, err)
	}

	handoffCommit := httptest.NewRecorder()
	handler.CommitAssistantHandoff(handoffCommit, assistantProgramRequest(http.MethodPost, "/handoffs/commit", station.ID, `{"review_token":"`+handoffReview.Token+`","idempotency_key":"handoff-http-1","title":"Prepare delivery","description":"Check the approved outputs.","state":"backlog"}`))
	if handoffCommit.Code != http.StatusOK {
		t.Fatalf("handoff commit = %d: %s", handoffCommit.Code, handoffCommit.Body.String())
	}
	tickets, err := workspace.NewTicketService(store).List(workspace.TicketQuery{WorkspaceID: project.ID})
	if err != nil || len(tickets) != 1 || tickets[0].Title != "Prepare delivery" {
		t.Fatalf("handoff child Tickets = %#v, %v", tickets, err)
	}

	listRecorder := httptest.NewRecorder()
	handler.GetAssistantPortfolio(listRecorder, assistantProgramRequest(http.MethodGet, "/portfolio", project.ID, ""))
	if listRecorder.Code != http.StatusOK || !strings.Contains(listRecorder.Body.String(), `"project_workspace_id":"`+project.ID+`"`) || !strings.Contains(listRecorder.Body.String(), `"status":"active"`) {
		t.Fatalf("portfolio list = %d: %s", listRecorder.Code, listRecorder.Body.String())
	}
}

func TestAssistantPortfolioKeepsHomeEditsAvailableButBlocksHandoffWithoutProjectProvider(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 2, ID: "music_home", StationName: "Music Home", DefaultPrimaryName: "Producer", HireTitle: "Staff producer",
		Roles:      []projecttemplates.AssistantProgramHomeRole{{ID: "producer", Label: "Producer", Required: true, Primary: true, SystemPrompt: "Coordinate."}},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 3, MaxEventsPerProject: 1, MaxCandidates: 1, MaxEvidence: 3, Rubric: "Review."},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper", BlueprintID: "song", ProjectTeamID: "reaper_team", ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 3, MaxProjectTeamVersion: 3,
		}},
	}
	projectTeam := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: 1, Version: 3, ID: "reaper_team",
		Home:  projecttemplates.AssistantProjectHomeReference{ProviderPluginID: "music", ProgramID: "music_home", HomeSchemaVersion: 1, MinHomeVersion: 2, MaxHomeVersion: 2},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "engineer", Label: "Engineer", Required: true, Primary: true, SystemPrompt: "Work in the project."}},
	}
	homeOwner := &workspace.AssistantProgramHomeOwner{
		PluginID: "music", PluginVersion: "2.0.0", ProgramID: home.ID, HomeSchemaVersion: home.SchemaVersion, HomeVersion: home.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home), PluginGeneration: 11, ComponentFingerprint: strings.Repeat("a", 64),
	}
	projectOwner := &workspace.AssistantProjectProviderOwner{
		PluginID: "reaper", PluginVersion: "4.0.0", BlueprintID: "song", BlueprintVersion: 7,
		ProjectTeamID: projectTeam.ID, ProjectTeamSchema: projectTeam.SchemaVersion, ProjectTeamVersion: projectTeam.Version,
		ProjectTeamDigest: projecttemplates.AssistantProjectDigest(projectTeam), PluginGeneration: 19, ComponentFingerprint: strings.Repeat("b", 64),
	}
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music", ProgramID: home.ID}
	if err := store.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.Key = key
		state.Declaration = home.AssistantProgram()
		state.HomeProvider = homeOwner
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *workspace.Workspace) error {
		link := current.GetAssistantProjectLink()
		link.Key = key
		link.ProjectRoles = projectTeam.ProgramRoles()
		link.ProjectProvider = projectOwner
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	feature := []string{plugin.HostFeatureIndependentProgramHomesV1}
	installed := []plugin.InstalledPlugin{
		{Name: "music", Version: "2.0.0", Enabled: true, Generation: 11, ComponentFingerprint: strings.Repeat("a", 64), WorkspaceSurfaces: &plugin.SurfaceContribution{Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion}, RequiresHostFeatures: feature, AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home}}},
		{Name: "reaper", Version: "4.0.0", Enabled: false, Generation: 19, ComponentFingerprint: strings.Repeat("b", 64), WorkspaceSurfaces: &plugin.SurfaceContribution{Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion}, RequiresHostFeatures: feature}, ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "song", Version: 7, Template: projecttemplates.Template{AssistantProject: projectTeam}}}},
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: installed})
	storedProject, _ := store.Get(project.ID)
	linkID := storedProject.GetAssistantProjectLink().ID
	portfolio := httptest.NewRecorder()
	handler.ReviewAssistantPortfolio(portfolio, assistantProgramRequest(http.MethodPost, "/portfolio/review", station.ID, `{"link_id":"`+linkID+`","if_revision":0,"fields":{"status":"active","archive_review_state":"not_ready"}}`))
	if portfolio.Code != http.StatusOK {
		t.Fatalf("Home portfolio edit was blocked by project provider: %d %s", portfolio.Code, portfolio.Body.String())
	}
	handoff := httptest.NewRecorder()
	handler.ReviewAssistantHandoff(handoff, assistantProgramRequest(http.MethodPost, "/handoff/review", station.ID, `{"link_id":"`+linkID+`","title":"Prepare","state":"backlog"}`))
	if handoff.Code != http.StatusConflict || !strings.Contains(handoff.Body.String(), "project provider") {
		t.Fatalf("unavailable project provider handoff = %d %s", handoff.Code, handoff.Body.String())
	}

	// Provider evidence is checked again at commit, not trusted from the review.
	installed[1].Enabled = true
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: installed})
	reviewRecorder := httptest.NewRecorder()
	handler.ReviewAssistantHandoff(reviewRecorder, assistantProgramRequest(http.MethodPost, "/handoff/review", station.ID, `{"link_id":"`+linkID+`","title":"Race-safe prepare","state":"backlog"}`))
	if reviewRecorder.Code != http.StatusOK {
		t.Fatalf("available provider handoff review = %d %s", reviewRecorder.Code, reviewRecorder.Body.String())
	}
	var review workspace.AssistantPortfolioHandoffReview
	if err := json.Unmarshal(reviewRecorder.Body.Bytes(), &review); err != nil || review.Token == "" {
		t.Fatalf("handoff review = %#v, %v", review, err)
	}
	installed[1].Enabled = false
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: installed})
	commitRecorder := httptest.NewRecorder()
	handler.CommitAssistantHandoff(commitRecorder, assistantProgramRequest(http.MethodPost, "/handoff/commit", station.ID, `{"review_token":"`+review.Token+`","idempotency_key":"disabled-after-review","title":"Race-safe prepare","state":"backlog"}`))
	if commitRecorder.Code != http.StatusConflict || !strings.Contains(commitRecorder.Body.String(), "project provider") {
		t.Fatalf("provider removal between review and commit = %d %s", commitRecorder.Code, commitRecorder.Body.String())
	}
	tickets, err := workspace.NewTicketService(store).List(workspace.TicketQuery{WorkspaceID: project.ID})
	if err != nil || len(tickets) != 0 {
		t.Fatalf("stale handoff created child Tickets: %#v, %v", tickets, err)
	}
}

func TestAssistantProgramHTTPReviewedDisconnectPreservesProject(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	state := station.GetAssistantProgramState()
	reviewRecorder := httptest.NewRecorder()
	handler.ReviewAssistantDisconnect(reviewRecorder, assistantProgramRequest(http.MethodPost, "/disconnect/review", station.ID, `{"project_workspace_id":"`+project.ID+`","state_revision":`+strconv.FormatInt(state.StateRevision, 10)+`}`))
	if reviewRecorder.Code != http.StatusOK {
		t.Fatalf("disconnect review = %d: %s", reviewRecorder.Code, reviewRecorder.Body.String())
	}
	var review workspace.AssistantDisconnectReview
	if err := json.Unmarshal(reviewRecorder.Body.Bytes(), &review); err != nil || review.Token == "" || len(review.Impact) == 0 {
		t.Fatalf("disconnect review body = %#v, %v", review, err)
	}
	commitRecorder := httptest.NewRecorder()
	handler.CommitAssistantDisconnect(commitRecorder, assistantProgramRequest(http.MethodPost, "/disconnect/commit", station.ID, `{"token":"`+review.Token+`","idempotency_key":"disconnect-http-1"}`))
	if commitRecorder.Code != http.StatusOK {
		t.Fatalf("disconnect commit = %d: %s", commitRecorder.Code, commitRecorder.Body.String())
	}
	retained, err := store.Get(project.ID)
	if err != nil || retained.GetAssistantProjectLink() != nil {
		t.Fatalf("retained project = %#v, %v", retained, err)
	}
}

func TestAssistantProgramHTTPReviewedHomeRemovalRetainsProject(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	reviewRecorder := httptest.NewRecorder()
	handler.ReviewAssistantHomeRemoval(reviewRecorder, assistantProgramRequest(http.MethodPost, "/remove-home/review", station.ID, `{"state_revision":`+strconv.FormatInt(station.GetAssistantProgramState().StateRevision, 10)+`}`))
	if reviewRecorder.Code != http.StatusOK {
		t.Fatalf("Home removal review = %d: %s", reviewRecorder.Code, reviewRecorder.Body.String())
	}
	var review workspace.AssistantHomeRemovalReview
	if err := json.Unmarshal(reviewRecorder.Body.Bytes(), &review); err != nil || review.Token == "" || review.LinkedProjectCount != 1 {
		t.Fatalf("Home removal body = %#v, %v", review, err)
	}
	commitRecorder := httptest.NewRecorder()
	handler.CommitAssistantHomeRemoval(commitRecorder, assistantProgramRequest(http.MethodPost, "/remove-home/commit", station.ID, `{"token":"`+review.Token+`"}`))
	if commitRecorder.Code != http.StatusOK {
		t.Fatalf("Home removal commit = %d: %s", commitRecorder.Code, commitRecorder.Body.String())
	}
	if !strings.Contains(commitRecorder.Body.String(), `"trashed":true`) {
		t.Fatalf("Home removal receipt should report the Trash: %s", commitRecorder.Body.String())
	}
	trashed, err := store.Get(station.ID)
	if err != nil || trashed.Status != workspace.StatusTrashed || trashed.GetAssistantProgramState() != nil {
		t.Fatalf("removed Home should be trashed with its live state cleared: %#v, %v", trashed, err)
	}
	retained, err := store.Get(project.ID)
	if err != nil || retained.GetAssistantProjectLink() != nil {
		t.Fatalf("retained child = %#v, %v", retained, err)
	}
}

func TestAssistantPortfolioHTTPErrorsAreBounded(t *testing.T) {
	handler, _, station, _ := assistantPortfolioHTTPFixture(t)
	recorder := httptest.NewRecorder()
	handler.ReviewAssistantPortfolio(recorder, assistantProgramRequest(http.MethodPost, "/portfolio/review", station.ID, `{"link_id":"missing-private-path-/Users/person","if_revision":0,"fields":{"status":"active","archive_review_state":"not_ready"}}`))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "/Users/person") || strings.Contains(recorder.Body.String(), "missing-private") {
		t.Fatalf("safe error echoed untrusted link material: %s", recorder.Body.String())
	}
}
