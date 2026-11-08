package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestReviewedHomeRecreationKeepsSharedProfileButNotOldAuthorityAndPreservesUndoConflict(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires supported isolated Trash")
	}
	t.Setenv("HOME", t.TempDir())
	h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	profiles, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	h.SetAgentStore(profiles)
	newBuildFixtureOn(t, h, false)
	if err := profiles.UpdateAgent("Scout", func(profile *agent.Agent) error {
		profile.Settings.Provider, profile.Settings.Model, profile.Settings.SystemPrompt = "scripted", "scripted-1", "AUTHORED_PROFILE_CANARY"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	adapter := wireReviewedGroupStaffing(t, h, workspaces)
	before, err := json.Marshal(store.LibraryDefinition(profiles, "Scout").Definition)
	if err != nil {
		t.Fatal(err)
	}
	count := len(profiles.ListAgents())
	createHome := func(key string) string {
		t.Helper()
		request := map[string]any{"group_template_id": id, "revision": revision, "name": "Retained Home", "role_staffing": []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "assign", Name: "Scout"}}}
		code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
		if code != http.StatusOK {
			t.Fatalf("review %d", code)
		}
		request["group_review_token"], request["idempotency_key"] = body["group_template_review"].(map[string]any)["review_token"], key
		code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
		result, _ := body["group_template"].(map[string]any)
		outcome, _ := result["home_staffing"].(map[string]any)
		if code != http.StatusOK || outcome["state"] != "staffed" {
			t.Fatalf("commit %d", code)
		}
		return result["home_workspace_id"].(string)
	}
	remove := func(homeID string) {
		t.Helper()
		home, err := workspaces.Get(homeID)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		h.ReviewAssistantHomeRemoval(recorder, assistantProgramRequest(http.MethodPost, "/remove-home/review", homeID,
			fmt.Sprintf(`{"state_revision":%d}`, home.GetAssistantProgramState().StateRevision)))
		var review workspace.AssistantHomeRemovalReview
		if err := json.Unmarshal(recorder.Body.Bytes(), &review); err != nil || recorder.Code != http.StatusOK || review.Token == "" {
			t.Fatalf("removal review %d", recorder.Code)
		}
		recorder = httptest.NewRecorder()
		h.CommitAssistantHomeRemoval(recorder, assistantProgramRequest(http.MethodPost, "/remove-home/commit", homeID, `{"token":"`+review.Token+`"}`))
		if recorder.Code != http.StatusOK {
			t.Fatalf("removal %d", recorder.Code)
		}
	}
	oldID := createHome("lifecycle-original")
	other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Standalone"})
	other.OwnerUserID = "local"
	if err := workspaces.Save(other); err != nil {
		t.Fatal(err)
	}
	if err := workspaces.Update(other.ID, func(current *workspace.Workspace) error {
		current.AgentInstances = []workspace.AgentInstance{{ID: "other-scout", Name: "Scout", Role: "Independent reviewer", CustomInstructions: "OTHER_ASSIGNMENT_CANARY"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other, _ = workspaces.Get(other.ID)
	unrelatedSession := &session.Session{Title: "Standalone conversation", AgentName: "Scout", FolderID: other.ID}
	if err := h.store.CreateSession(context.Background(), unrelatedSession); err != nil {
		t.Fatal(err)
	}
	if err := h.store.AddMessage(context.Background(), unrelatedSession.ID, &session.Message{Role: session.RoleUser, Content: "OTHER_SESSION_CANARY"}); err != nil {
		t.Fatal(err)
	}
	otherBefore, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	// These inert records are canonical fixture state, not running services or
	// tool calls. Each nonempty category must stay attached to the old Home.
	consent := &workspace.ProjectStaffingConsent{GrantedAt: time.Now(), Source: workspace.ProjectStaffingConsentReviewedCreate,
		PluginID: "neutral", BlueprintID: "project", TeamDigest: strings.Repeat("a", 64),
		Roles: []workspace.ProjectStaffingConsentRole{{RoleID: "project-lead", AgentName: "Scout"}}}
	if err := consent.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := workspaces.Update(oldID, func(current *workspace.Workspace) error {
		current.AgentInstances[0].CustomInstructions = "OLD_HOME_CONTEXT_CANARY"
		current.ScheduledTasks = []workspace.ScheduledTask{{ID: "old-schedule", WorkspaceID: oldID, Name: "Old schedule", To: "Scout", Prompt: "OLD_SCHEDULE_CANARY", Enabled: true}}
		current.DirectoryReferences = []workspace.DirectoryReference{{ID: "old-root", WorkspaceID: oldID, Name: "Old root", Path: t.TempDir()}}
		current.MCPBindings = []workspace.MCPBinding{{ID: "old-mcp", ServerName: "inert-fixture", Enabled: true, Config: map[string]any{"private": "OLD_MCP_CANARY"}}}
		current.Messages = []workspace.AgentMessage{{Content: "OLD_HISTORY_CANARY"}}
		current.SetSharedData("old-library", map[string]any{"private": "OLD_LIBRARY_CANARY"})
		state := current.GetAssistantProgramState()
		state.ProjectStaffingConsent = consent.Clone()
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	remove(oldID)
	// Existing Undo works before a replacement, without recreating profiles.
	restored := httptest.NewRecorder()
	h.HandleWorkspaces(restored, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+oldID+"/restore", nil))
	if restored.Code != http.StatusOK {
		t.Fatalf("Undo before replacement %d", restored.Code)
	}
	old, err := workspaces.Get(oldID)
	if err != nil || old.GetAssistantProgramState() == nil {
		t.Fatal("Undo lost canonical Home", err)
	}
	remove(oldID)
	newID := createHome("lifecycle-replacement")
	if newID == oldID {
		t.Fatal("recreation restored old Home instead of creating its reviewed target")
	}
	current, err := workspaces.Get(newID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"OLD_HOME_CONTEXT_CANARY", "OLD_SCHEDULE_CANARY", "OLD_MCP_CANARY", "OLD_HISTORY_CANARY", "OLD_LIBRARY_CANARY"} {
		if bytes.Contains(encoded, []byte(canary)) {
			t.Fatal("replacement inherited old private runtime state")
		}
	}
	if len(current.ScheduledTasks) != 0 || len(current.DirectoryReferences) != 0 || len(current.MCPBindings) != 0 ||
		len(current.Messages) != 0 || current.GetAssistantProgramState().GetProjectStaffingConsent() != nil {
		t.Fatal("replacement inherited old authority")
	}
	// The replacement owns both the canonical Home key and folder slug. Undo
	// must retain the normal conflict rather than force another active Home.
	restored = httptest.NewRecorder()
	h.HandleWorkspaces(restored, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+oldID+"/restore", nil))
	if restored.Code != http.StatusConflict {
		t.Fatalf("Undo after replacement %d, want normal conflict", restored.Code)
	}
	if err := adapter.UnstaffRoleFromWorkspace(context.Background(), newID, "home-lead"); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(store.LibraryDefinition(profiles, "Scout").Definition)
	if err != nil || !bytes.Equal(before, after) || len(profiles.ListAgents()) != count {
		t.Fatal("recreation, Undo or clear changed/deleted the shared profile", err)
	}
	other, _ = workspaces.Get(other.ID)
	otherAfter, err := json.Marshal(other)
	if err != nil || !bytes.Equal(otherBefore, otherAfter) {
		t.Fatal("recreation or clear changed unrelated assignment", err)
	}
	retained, err := h.store.GetSession(context.Background(), unrelatedSession.ID)
	if err != nil || retained.AgentName != "Scout" || len(retained.Messages) != 1 || retained.Messages[0].Content != "OTHER_SESSION_CANARY" {
		t.Fatal("recreation or clear lost unrelated conversation", err)
	}
}
