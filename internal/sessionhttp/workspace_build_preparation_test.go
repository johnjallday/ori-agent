package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type buildPreparationFixture struct {
	h          *Handler
	agents     *agentstore.CompositeStore
	workspaces workspace.Store
	builds     *memoryBuildStore
	janitor    *filejanitor.Service
	folders    *fakeBuildFolders
	build      personalassistant.WorkspaceBuildSession
	root       string
}

func newBuildPreparationFixture(t *testing.T) *buildPreparationFixture {
	t.Helper()
	h, agents, workspaces, cleanup := reviewedFileJanitorHandler(t)
	t.Cleanup(cleanup)
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "invoice.pdf")
	if err := os.WriteFile(file, []byte("unchanged synthetic source"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	tpl, err := h.resolveProjectTemplate("file-janitor", "")
	if err != nil {
		t.Fatal(err)
	}
	plan := h.buildTemplateAgentPlan(tpl)
	intent, err := json.Marshal(workspaceTeamIntent{Version: workspaceTeamIntentVersion, Mode: workspaceTeamModeStaffed, PlanRevision: plan.Revision})
	if err != nil {
		t.Fatal(err)
	}
	staffing, err := json.Marshal([]roleStaffingInput{{RoleID: projecttemplates.AgentRoleIDs(tpl.Agents)[0], Mode: roleStaffingModeCreate,
		Name: "Reviewed Curator", Provider: "ollama", Model: "reviewed-model", SystemPrompt: "Keep this exact edited prompt."}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	build := personalassistant.WorkspaceBuildSession{ID: uuid.NewString(), Status: personalassistant.WorkspaceBuildOpen,
		Version: 1, CreatedAt: now, UpdatedAt: now, EntryPoint: "home_cockpit_create",
		Draft: personalassistant.BuildDraft{TemplateID: "file-janitor", Name: "My reviewed inbox", Description: "Edited description", Mission: "My explicit goal",
			Color: "#557766", Tags: []string{"reviewed"}, TeamIntent: intent, RoleStaffing: staffing, ExistingAgentNames: []string{}},
		Folder: &personalassistant.BuildFolder{Observation: *testObservation()},
	}
	builds := &memoryBuildStore{now: now, doc: personalassistant.WorkspaceBuildDocument{SchemaVersion: personalassistant.WorkspaceBuildSchemaVersion, Version: 1, Sessions: []personalassistant.WorkspaceBuildSession{build}}}
	janitor := filejanitor.NewService(filejanitor.NewStore(h.workspaceStore), workspaces)
	folders := &fakeBuildFolders{root: root}
	h.workspaceBuild = &WorkspaceBuildDeps{Store: builds, Folders: folders,
		Assistant: func(context.Context, string) (*personalassistant.Projection, error) {
			return &personalassistant.Projection{HQWorkspaceID: "synthetic-hq"}, nil
		},
		FileJanitor: func() (*filejanitor.Service, filejanitor.PreparationAutomation) { return janitor, nil },
	}
	return &buildPreparationFixture{h, agents, workspaces, builds, janitor, folders, build, root}
}

func (f *buildPreparationFixture) call(action string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/build-sessions/"+f.build.ID+"/"+action, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.h.HandleWorkspaces(recorder, request)
	return recorder
}

func (f *buildPreparationFixture) review(t *testing.T) buildPreparationRequest {
	t.Helper()
	request := buildPreparationRequest{Version: f.build.Version}
	response := f.call("janitor-review", request)
	if response.Code != http.StatusOK {
		t.Fatalf("review: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Review struct {
			Revision string                   `json:"review_revision"`
			Team     []buildPreparationMember `json:"team"`
		} `json:"review"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Review.Team) != 1 || body.Review.Team[0].Name != "Reviewed Curator" || body.Review.Team[0].Model != "reviewed-model" {
		t.Fatalf("review substituted the default team: %+v", body)
	}
	request.ReviewRevision = body.Review.Revision
	return request
}

func TestWorkspaceBuildPreparation_ExactEditedDraftAndOneInitialResult(t *testing.T) {
	f := newBuildPreparationFixture(t)
	parentID := uuid.NewString()
	parent := &session.Workspace{ID: parentID, Name: "Reviewed Group", Kind: session.WorkspaceKindGroup, FolderSlug: "reviewed-group", OwnerUserID: "local"}
	if err := f.h.store.CreateWorkspace(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := f.h.workspaceStore.Save(&workspace.Workspace{ID: parentID, Name: parent.Name, Kind: "group", FolderSlug: parent.FolderSlug, OwnerUserID: "local", Status: workspace.StatusActive}); err != nil {
		t.Fatal(err)
	}
	f.build.Draft.ParentID = parentID
	if _, err := f.builds.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Session(f.build.ID).Draft.ParentID = parentID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request := f.review(t)
	if _, err := os.Stat(filepath.Join(f.root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("review created Filed")
	}
	if ids, _ := f.workspaces.List(); len(ids) != 1 {
		t.Fatal("review created a workspace")
	}
	if _, exists := f.agents.GetAgent("Reviewed Curator"); exists {
		t.Fatal("review created a profile")
	}
	response := f.call("janitor-prepare", request)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		WorkspaceID string                             `json:"workspace_id"`
		Preparation filejanitor.SetupPreparationResult `json:"preparation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	ws, err := f.h.readReviewedWorkspace(body.WorkspaceID)
	if err != nil || ws.Name != f.build.Draft.Name || ws.Description != f.build.Draft.Description || ws.Mission != f.build.Draft.Mission || ws.ParentID != parentID {
		t.Fatalf("edited draft changed: %+v, %v", ws, err)
	}
	row, err := f.h.store.GetWorkspace(context.Background(), body.WorkspaceID)
	if err != nil || row.Color != f.build.Draft.Color || !strings.Contains(strings.Join(ws.Tags, ","), "reviewed") {
		t.Fatalf("color/tags changed: %+v, %v", row, err)
	}
	profile, found := f.agents.GetAgent("Reviewed Curator")
	if !found || profile.Settings.Model != "reviewed-model" || profile.Settings.SystemPrompt != "Keep this exact edited prompt." || profile.AssistantSetup == nil {
		t.Fatalf("edited team changed: %+v", profile)
	}
	if _, found := f.agents.GetAgent("File Curator"); found {
		t.Fatal("default curator substituted for reviewed team")
	}
	if body.Preparation.Status == nil || !body.Preparation.Status.Settings.ManualScansOnly() || body.Preparation.Result == nil || body.Preparation.Result.EligibleCount != 1 {
		t.Fatalf("not manual-ready with initial result: %+v", body)
	}
	for range 2 {
		replay := f.call("janitor-prepare", request)
		if replay.Code != http.StatusOK {
			t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
		}
		var got struct {
			WorkspaceID string                             `json:"workspace_id"`
			Preparation filejanitor.SetupPreparationResult `json:"preparation"`
		}
		if err := json.Unmarshal(replay.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.WorkspaceID != body.WorkspaceID || got.Preparation.Result.BatchID != body.Preparation.Result.BatchID {
			t.Fatal("response-loss replay duplicated result")
		}
	}
	if ids, _ := f.workspaces.List(); len(ids) != 2 {
		t.Fatalf("duplicate workspace: %v", ids)
	}
	if source, err := os.ReadFile(filepath.Join(f.root, "invoice.pdf")); err != nil || string(source) != "unchanged synthetic source" {
		t.Fatal("source changed")
	}
}

func TestWorkspaceBuildPreparation_AssignedProfileKeepsItsEffectiveSettingsAndOwnership(t *testing.T) {
	f := newBuildPreparationFixture(t)
	if err := f.agents.CreateAgent("Saved Curator", &agentstore.CreateAgentConfig{Model: "saved-model", LLMProvider: "ollama", SystemPrompt: "Saved prompt", Role: types.RoleSpecialist}); err != nil {
		t.Fatal(err)
	}
	staffing, err := json.Marshal([]roleStaffingInput{{RoleID: "file-curator", Mode: roleStaffingModeAssign, Name: "Saved Curator"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.builds.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Session(f.build.ID).Draft.RoleStaffing = staffing
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := f.call("janitor-review", buildPreparationRequest{Version: 1})
	if preview.Code != http.StatusOK {
		t.Fatalf("assign preview: %s", preview.Body.String())
	}
	var body struct {
		Review struct {
			Revision string                   `json:"review_revision"`
			Team     []buildPreparationMember `json:"team"`
		} `json:"review"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Review.Team) != 1 || body.Review.Team[0].Name != "Saved Curator" || body.Review.Team[0].Model != "saved-model" {
		t.Fatalf("assign review changed saved configuration: %+v", body)
	}
	result := f.call("janitor-prepare", buildPreparationRequest{Version: 1, ReviewRevision: body.Review.Revision})
	if result.Code != http.StatusOK {
		t.Fatalf("assign preparation: %s", result.Body.String())
	}
	profile, _ := f.agents.GetAgent("Saved Curator")
	if profile.AssistantSetup != nil || profile.Settings.SystemPrompt != "Saved prompt" {
		t.Fatal("assigned profile was overwritten/claimed")
	}
	if _, found := f.agents.GetAgent("Reviewed Curator"); found {
		t.Fatal("default draft team was created")
	}
}

func TestWorkspaceBuildPreparation_StaleReviewAndExpiredSelectionWriteNothing(t *testing.T) {
	for _, changed := range []string{"draft", "team", "folder", "selection", "mode", "owner_unavailable"} {
		t.Run(changed, func(t *testing.T) {
			f := newBuildPreparationFixture(t)
			request := f.review(t)
			switch changed {
			case "draft":
				_, _ = f.builds.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
					doc.Session(f.build.ID).Draft.Name = "Another name"
					doc.Session(f.build.ID).Touch(time.Now())
					return nil
				})
			case "team":
				_, _ = f.builds.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
					doc.Session(f.build.ID).Draft.RoleStaffing = bytes.ReplaceAll(doc.Session(f.build.ID).Draft.RoleStaffing, []byte("reviewed-model"), []byte("changed-model"))
					return nil
				})
			case "folder":
				f.folders.root = t.TempDir()
			case "selection":
				f.folders.rootErr = personalassistant.ErrFolderSelection
			case "mode":
				request.Monitoring = true
			case "owner_unavailable":
				f.h.workspaceBuild.CurrentUserID = func(context.Context) (string, error) { return "", errors.New("identity unavailable") }
			}
			response := f.call("janitor-prepare", request)
			if response.Code < 400 {
				t.Fatalf("stale preparation accepted: %s", response.Body.String())
			}
			if ids, _ := f.workspaces.List(); len(ids) != 0 {
				t.Fatalf("rejection created workspaces: %v", ids)
			}
			if _, found := f.agents.GetAgent("Reviewed Curator"); found {
				t.Fatal("rejection created an agent")
			}
			if _, err := os.Stat(filepath.Join(f.root, "Filed")); !os.IsNotExist(err) {
				t.Fatal("rejection created Filed")
			}
		})
	}
}

func TestWorkspaceBuildPreparation_ConcurrentTabsShareTheAcceptedIdentity(t *testing.T) {
	f := newBuildPreparationFixture(t)
	request := f.review(t)
	responses := make([]*httptest.ResponseRecorder, 4)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func() { defer wg.Done(); responses[i] = f.call("janitor-prepare", request) }()
	}
	wg.Wait()
	for _, response := range responses {
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent: %d %s", response.Code, response.Body.String())
		}
	}
	ids, err := f.workspaces.List()
	if err != nil || len(ids) != 1 {
		t.Fatalf("workspace count = %v, %v", ids, err)
	}
	batches, err := f.janitor.ListBatches(ids[0])
	if err != nil || len(batches) != 1 {
		t.Fatalf("batch count = %+v, %v", batches, err)
	}
}

type failBuildCreationStore struct {
	session.HybridStore
	calls int
}

func (s *failBuildCreationStore) CreateWorkspace(context.Context, *session.Workspace) error {
	s.calls++
	return errors.New("injected create failure")
}

func TestWorkspaceBuildPreparation_UncertainCreateNeverAllocatesAnotherWorkspace(t *testing.T) {
	f := newBuildPreparationFixture(t)
	request := f.review(t)
	store := &failBuildCreationStore{HybridStore: f.h.store}
	f.h.store = store
	first := f.call("janitor-prepare", request)
	if first.Code < 400 {
		t.Fatal("create failure claimed success")
	}
	doc, _ := f.builds.Read(context.Background(), "local")
	claim := doc.Session(f.build.ID).Preparation
	if claim == nil || doc.Open().Status != personalassistant.WorkspaceBuildPreparing {
		t.Fatal("accepted identity was lost")
	}
	for range 2 {
		response := f.call("janitor-prepare", request)
		if response.Code < 400 || !strings.Contains(response.Body.String(), "creation_unconfirmed") {
			t.Fatalf("uncertain create = %d %s", response.Code, response.Body.String())
		}
	}
	if store.calls != 1 {
		t.Fatal("uncertain create was attempted again")
	}
	payload, err := buildPreparationCreatePayload(f.build)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	f.h.HandleWorkspaces(w, r)
	if w.Code != http.StatusConflict || store.calls != 1 {
		t.Fatal("ordinary create bypassed the accepted claim")
	}
}

func TestWorkspaceBuildPreparation_LaterRevocationOrLifecycleChangeStopsReplay(t *testing.T) {
	for _, change := range []string{"revoke", "pause", "remove", "reinstall", "owner", "archive", "team"} {
		t.Run(change, func(t *testing.T) {
			f := newBuildPreparationFixture(t)
			request := f.review(t)
			first := f.call("janitor-prepare", request)
			if first.Code != http.StatusOK {
				t.Fatalf("prepare: %s", first.Body.String())
			}
			doc, _ := f.builds.Read(context.Background(), "local")
			id := doc.Session(f.build.ID).Preparation.WorkspaceID
			var err error
			switch change {
			case "revoke":
				_, err = f.janitor.RevokeAccess(nil, id)
			case "pause":
				_, err = f.janitor.SetPaused(id, true)
			case "owner":
				err = f.h.workspaceStore.Update(id, func(ws *workspace.Workspace) error { ws.OwnerUserID = "another-owner"; return nil })
			case "team":
				err = f.h.workspaceStore.Update(id, func(ws *workspace.Workspace) error {
					ws.AgentInstances[0].CustomInstructions = "Later edit"
					return nil
				})
			default:
				err = f.workspaces.Update(id, func(ws *workspace.Workspace) error {
					switch change {
					case "remove":
						ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
					case "reinstall":
						install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
						install.InstalledAt = install.InstalledAt.Add(time.Second)
						ws.SetInstalledCapabilities([]workspace.InstalledCapability{install})
					case "archive":
						ws.Status = workspace.StatusTrashed
					}
					return nil
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			// Simulate restart/loss of the ephemeral build-folder hold. A completed
			// accepted operation must not need or revive that earlier selection.
			f.folders.rootErr = personalassistant.ErrFolderSelection
			f.janitor = filejanitor.NewService(filejanitor.NewStore(f.h.workspaceStore), f.workspaces)
			f.h.workspaceBuild.FileJanitor = func() (*filejanitor.Service, filejanitor.PreparationAutomation) { return f.janitor, nil }
			replay := f.call("janitor-prepare", request)
			if replay.Code < 400 {
				t.Fatalf("replay undid %s: %s", change, replay.Body.String())
			}
			if ids, _ := f.workspaces.List(); len(ids) != 1 {
				t.Fatalf("retry recreated a workspace: %v", ids)
			}
			batches, err := f.janitor.ListBatches(id)
			if err != nil || len(batches) != 1 {
				t.Fatalf("retry scanned again: %+v, %v", batches, err)
			}
		})
	}
}

func TestWorkspaceBuildPreparation_ModelPromptNamesTheHostReviewNotPermission(t *testing.T) {
	prompt := buildSystemPrompt(buildPromptInput{Session: &personalassistant.WorkspaceBuildSession{}})
	for _, required := range []string{"Ready to create", "Review & create", "never supplies consent"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("missing reviewed-create copy %q", required)
		}
	}
	if strings.Contains(prompt, "on the left") {
		t.Fatal("layout-dependent direction survived")
	}
}

func TestWorkspaceBuildPreparation_OrdinaryCreateStillGrantsNothing(t *testing.T) {
	f := newBuildPreparationFixture(t)
	payload, err := buildPreparationCreatePayload(f.build)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.h.HandleWorkspaces(w, httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(payload)))
	if w.Code != http.StatusCreated {
		t.Fatalf("ordinary create: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(f.root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("ordinary create granted File Janitor permission")
	}
	doc, _ := f.builds.Read(context.Background(), "local")
	if doc.Session(f.build.ID).Preparation != nil {
		t.Fatal("ordinary create manufactured consent")
	}
}
