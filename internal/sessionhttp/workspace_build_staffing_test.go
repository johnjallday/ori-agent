package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func staffingBuildFixture(t *testing.T) (*buildFixture, personalassistant.WorkspaceBuildSession, buildReply) {
	t.Helper()
	f := newBuildFixture(t, true)
	f.catalog = f.catalog[1:2] // the real ordinary builder's one-role Code Project
	if err := f.handler.agentStore.UpdateAgent("Scout", func(a *agent.Agent) error {
		a.Settings.Provider, a.Settings.Model = "scripted", "scripted-1"
		a.Settings.SystemPrompt = "PRIVATE_PROMPT_CANARY"
		a.Metadata = &types.AgentMetadata{Description: "Builds and reviews software projects"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	session := f.start(t)
	state := f.handler.workspaceBuildStaffing(context.Background(), f.handler.workspaceBuildUserID(context.Background()), &session, f.catalog)
	if state == nil || len(state.input.Roles) != 1 {
		t.Fatal("no staffing input")
	}
	var selected rosterstaffing.Candidate
	for _, candidate := range state.input.Roster.Candidates {
		if candidate.Name == "Scout" {
			selected = candidate
		}
	}
	if selected.Handle == "" {
		t.Fatal("canonical candidate unavailable")
	}
	reply := buildReply{
		Say: "Review the saved profile recommendation.", Ask: buildReplyAsk{Question: "Use the recommendation or choose manually?", AllowFreeText: true},
		Patch:    buildReplyPatch{Set: []string{buildFieldBlueprint, buildFieldName, buildFieldDescription}, BlueprintID: "code-project", Name: "Tooling", Description: "Build software tools"},
		Staffing: rosterstaffing.Proposal{Roles: []rosterstaffing.RoleProposal{{RoleID: state.input.Roles[0].ID, State: rosterstaffing.Recommended, Choices: []rosterstaffing.Choice{{Candidate: selected.Handle, Evidence: []string{selected.Evidence[0].ID}}}}}},
	}
	return f, session, reply
}

func TestWorkspaceBuildStaffingOneTurnIsAdvisoryAndReadOnly(t *testing.T) {
	f, session, reply := staffingBuildFixture(t)
	encoded, _ := json.Marshal(reply)
	f.provider.replies = []string{string(encoded)}
	before, _ := json.Marshal(store.LibraryDefinition(f.handler.agentStore, "Scout").Definition)
	profiles := len(f.handler.agentStore.ListAgents())
	workspaces, err := f.handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status, body := f.turn(t, session, map[string]any{"text": "Build software tools"})
	if status != http.StatusOK {
		t.Fatalf("turn: %d %v", status, body)
	}
	got := sessionOf(t, body)
	if got.Staffing == nil || got.Staffing.State != rosterstaffing.Recommended || got.Staffing.Roles[0].Choices[0].Name != "Scout" {
		t.Fatalf("missing useful advice: %+v", got.Staffing)
	}
	if got.TeamPatch != nil || len(got.Draft.RoleStaffing) != 0 {
		t.Fatal("recommendation applied staffing")
	}
	if got.Staffing.Roles[0].Choices[0].SourceWitness != "" {
		t.Fatal("server witness in browser response")
	}
	doc, _ := f.store.Read(context.Background(), "local")
	if doc.Open().Staffing.Roles[0].Choices[0].SourceWitness == "" {
		t.Fatal("server did not retain source evidence")
	}
	for i := 0; i < 3; i++ {
		if status, _ := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/"+session.ID, nil); status != http.StatusOK {
			t.Fatal("read failed")
		}
	}
	if status, _ := f.turn(t, session, map[string]any{"text": "Build software tools"}); status != http.StatusConflict {
		t.Fatal("old turn was replayed")
	}
	if f.provider.calls != 1 {
		t.Fatalf("expected one ordinary call, got %d", f.provider.calls)
	}
	after, _ := json.Marshal(store.LibraryDefinition(f.handler.agentStore, "Scout").Definition)
	afterWorkspaces, err := f.handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || profiles != len(f.handler.agentStore.ListAgents()) || len(workspaces) != len(afterWorkspaces) {
		t.Fatal("recommendation mutated domain state")
	}
	stored, _ := json.Marshal(doc.Open().Staffing)
	for _, text := range []string{f.provider.systems[0], string(stored)} {
		if strings.Contains(text, "PRIVATE_PROMPT_CANARY") {
			t.Fatal("private prompt leaked")
		}
	}
}

func TestWorkspaceBuildStaffingInvalidReplyIsNotNoFit(t *testing.T) {
	f, session, reply := staffingBuildFixture(t)
	reply.Staffing.Roles[0].Choices[0].Evidence = []string{"invented-performance-evidence"}
	encoded, _ := json.Marshal(reply)
	f.provider.replies = []string{string(encoded)}
	_, body := f.turn(t, session, map[string]any{"text": "Build software tools"})
	got := sessionOf(t, body)
	if got.Staffing == nil || got.Staffing.State != rosterstaffing.Unavailable || len(got.Staffing.Roles) != 0 {
		t.Fatalf("invalid advice became a match: %+v", got.Staffing)
	}
	if got.Draft.Name != "Tooling" || f.provider.calls != 1 {
		t.Fatal("unavailable ranking blocked ordinary drafting or triggered another call")
	}
}

func TestWorkspaceBuildStaffingPromptOmitsPrivateDraftConfiguration(t *testing.T) {
	session := personalassistant.WorkspaceBuildSession{
		TeamPatch: &personalassistant.BuildTeamPatch{Roles: []personalassistant.BuildRole{{RoleID: "engineer", Mode: "create", AgentName: "Scout", SystemPrompt: "TEAM_PROMPT_CANARY", Model: "/private/model"}}},
		Draft:     personalassistant.BuildDraft{RoleStaffing: json.RawMessage(`[{"role_id":"engineer","mode":"create","name":"Scout","system_prompt":"DRAFT_PROMPT_CANARY","appearance":{"path":"/private/face"}}]`)},
	}
	data, err := json.Marshal(buildPromptDraft(&session))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"CANARY", "/private/", "system_prompt", "appearance"} {
		if strings.Contains(string(data), value) {
			t.Fatalf("private draft data in prompt: %s", value)
		}
	}
	if !strings.Contains(string(data), "Scout") || !strings.Contains(string(data), "engineer") {
		t.Fatal("lost explicit team choice")
	}
}

func TestWorkspaceBuildStaffingExplicitClearAndLateReplyStayStale(t *testing.T) {
	f, session, reply := staffingBuildFixture(t)
	state := f.handler.workspaceBuildStaffing(context.Background(), "local", &session, f.catalog)
	session.Draft.TemplateID = "code-project"
	session.EditedFields = map[string]int64{"team": state.version + 1}
	finishBuildStaffing(&session, buildOutcome{kind: "reply", reply: reply}, state)
	if session.Staffing.State != rosterstaffing.Stale || len(session.Staffing.Roles) != 0 || session.TeamPatch != nil {
		t.Fatal("late advice replaced explicit clear")
	}
	session.EditedFields = nil
	finishBuildStaffing(&session, buildOutcome{kind: "reply", reply: reply}, state)
	if session.Staffing.State != rosterstaffing.Recommended {
		t.Fatal("fixture failed")
	}
	session.InvalidateBuildInference(true)
	if session.Staffing.State != rosterstaffing.Stale {
		t.Fatal("context invalidation kept recommendation current")
	}
}
