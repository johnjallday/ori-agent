package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

func reviewForPlan(plan templateAgentPlan) templateAgentReview {
	expectations := make([]templateAgentReviewExpectation, 0, len(plan.Agents))
	for index, item := range plan.Agents {
		expectations = append(expectations, templateAgentReviewExpectation{
			Index: index, Name: item.Name, Action: item.Action,
		})
	}
	return templateAgentReview{
		Version:      templateAgentReviewVersion,
		PlanRevision: plan.Revision,
		Expectations: expectations,
	}
}

func TestTemplateAgentPlanRevisionIsDeterministicAndDefinitionSensitive(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	handler.SetSystemModelReader(fakeSystemModelReader{provider: "codex", model: "gpt-5.3-codex", reasoningEffort: "high"})

	tpl := projecttemplates.Template{
		ID:   "reviewed-template",
		Name: "Reviewed Template",
		Agents: []projecttemplates.AgentSpec{{
			Name: "Lead", Role: "orchestrator", SystemPrompt: "Lead the work.",
			Tools: projecttemplates.ToolDefaults{Skills: []string{"planning"}},
		}},
	}
	first := handler.buildTemplateAgentPlan(tpl)
	second := handler.buildTemplateAgentPlan(tpl)
	if first.Revision == "" || first.Revision != second.Revision {
		t.Fatalf("stable plan revisions = %q and %q", first.Revision, second.Revision)
	}

	changed := tpl
	changed.Agents = append([]projecttemplates.AgentSpec(nil), tpl.Agents...)
	changed.Agents[0].SystemPrompt = "Lead differently."
	if got := handler.buildTemplateAgentPlan(changed).Revision; got == first.Revision {
		t.Fatal("changing a user-visible definition did not change the revision")
	}

	if err := handler.agentStore.CreateAgent("Lead", &agentstore.CreateAgentConfig{SystemPrompt: "Saved definition"}); err != nil {
		t.Fatalf("create saved Lead: %v", err)
	}
	reused := handler.buildTemplateAgentPlan(tpl)
	if reused.Agents[0].Action != "reuse" || reused.Revision == first.Revision {
		t.Fatalf("create/reuse resolution did not change revision: before=%+v after=%+v", first, reused)
	}
}

func TestValidateTemplateAgentReviewRequiresExactCompleteExpectations(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Lead"},
		projecttemplates.AgentSpec{Name: "Writer"},
	)
	plan := handler.buildTemplateAgentPlan(tpl)
	review := reviewForPlan(plan)
	if err := handler.validateTemplateAgentReview(&review, tpl, tpl); err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}

	missing := review
	missing.Expectations = missing.Expectations[:1]
	if err := handler.validateTemplateAgentReview(&missing, tpl, tpl); !errors.Is(err, errTemplateAgentReviewMalformed) {
		t.Fatalf("incomplete expectations error = %v, want malformed", err)
	}

	wrongName := review
	wrongName.Expectations = append([]templateAgentReviewExpectation(nil), review.Expectations...)
	wrongName.Expectations[1].Name = "Other"
	if err := handler.validateTemplateAgentReview(&wrongName, tpl, tpl); !errors.Is(err, errTemplateAgentReviewMalformed) {
		t.Fatalf("wrong-name error = %v, want malformed", err)
	}

	malformedReviews := map[string]templateAgentReview{
		"unknown version": func() templateAgentReview {
			candidate := review
			candidate.Version = 2
			return candidate
		}(),
		"blank revision": func() templateAgentReview {
			candidate := review
			candidate.PlanRevision = " "
			return candidate
		}(),
		"duplicate index": func() templateAgentReview {
			candidate := review
			candidate.Expectations = append([]templateAgentReviewExpectation(nil), review.Expectations...)
			candidate.Expectations[1].Index = 0
			return candidate
		}(),
		"out of range index": func() templateAgentReview {
			candidate := review
			candidate.Expectations = append([]templateAgentReviewExpectation(nil), review.Expectations...)
			candidate.Expectations[1].Index = 4
			return candidate
		}(),
		"noncanonical action": func() templateAgentReview {
			candidate := review
			candidate.Expectations = append([]templateAgentReviewExpectation(nil), review.Expectations...)
			candidate.Expectations[1].Action = "CREATE"
			return candidate
		}(),
	}
	for name, candidate := range malformedReviews {
		t.Run(name, func(t *testing.T) {
			if err := handler.validateTemplateAgentReview(&candidate, tpl, tpl); !errors.Is(err, errTemplateAgentReviewMalformed) {
				t.Fatalf("error = %v, want malformed", err)
			}
		})
	}

	if err := handler.agentStore.CreateAgent("Writer", nil); err != nil {
		t.Fatalf("create concurrent Writer: %v", err)
	}
	if err := handler.validateTemplateAgentReview(&review, tpl, tpl); !errors.Is(err, errTemplateAgentReviewStale) {
		t.Fatalf("changed action error = %v, want stale", err)
	}
}

func TestSeedTemplateAgentsStrictCreatesCompleteRoster(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	if err := handler.agentStore.CreateAgent("Saved", &agentstore.CreateAgentConfig{SystemPrompt: "keep me"}); err != nil {
		t.Fatalf("create Saved: %v", err)
	}
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Saved"},
		projecttemplates.AgentSpec{Name: "Fresh", SystemPrompt: "new prompt"},
	)
	plan := handler.buildTemplateAgentPlan(tpl)
	review := reviewForPlan(plan)
	ws := &session.Workspace{ID: "strict", Name: "Strict"}

	seed, err := handler.seedTemplateAgentsStrict(ws, tpl, tpl, review)
	if err != nil {
		t.Fatalf("strict seed: %v", err)
	}
	if len(seed.OwnedNames) != 1 || seed.OwnedNames[0] != "Fresh" {
		t.Fatalf("owned names = %v, want Fresh", seed.OwnedNames)
	}
	if currentWorkspaceEntryAgentName(ws) != "Saved" || !wsHasAgent(ws, "Fresh") {
		t.Fatalf("strict workspace roster = %#v", ws.AgentInstances)
	}
	saved, _ := handler.agentStore.GetAgent("Saved")
	if saved.Settings.SystemPrompt != "keep me" {
		t.Fatalf("reused definition was mutated: %+v", saved.Settings)
	}
}

type failNthAgentCreateStore struct {
	agentstore.Store
	calls  int
	failAt int
}

func (s *failNthAgentCreateStore) CreateAgent(name string, config *agentstore.CreateAgentConfig) error {
	s.calls++
	if s.calls == s.failAt {
		return errors.New("injected strict create failure")
	}
	return s.Store.CreateAgent(name, config)
}

func TestCreateWorkspaceWithReviewedTemplateAgentIsAtomic(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "reviewed-roster", `{
		"name":"Reviewed Roster",
		"agents":[{"name":"Blueprint Lead","role":"orchestrator","type":"general","system_prompt":"recommended"}]
	}`)
	tpl, err := handler.resolveProjectTemplate("reviewed-roster", "")
	if err != nil {
		t.Fatalf("resolve template: %v", err)
	}
	plan := handler.buildTemplateAgentPlan(tpl)
	review := reviewForPlan(plan)
	review.Expectations[0].Name = "Launch Lead"
	body, err := json.Marshal(map[string]any{
		"name":        "Launch",
		"template_id": "reviewed-roster",
		"template_agent_overrides": []map[string]any{{
			"index": 0, "name": "Launch Lead", "model": "gpt-5.1", "provider": "openai", "system_prompt": "custom launch prompt",
		}},
		"template_agent_review": review,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder, response := postCreateWorkspace(t, handler, string(body))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if _, exists := handler.agentStore.GetAgent("Blueprint Lead"); exists {
		t.Fatal("unstaged blueprint name was created")
	}
	created, exists := handler.agentStore.GetAgent("Launch Lead")
	if !exists || created.Settings.Model != "gpt-5.1" || created.Settings.Provider != "openai" || created.Settings.SystemPrompt != "custom launch prompt" {
		t.Fatalf("reviewed agent definition = %+v, exists=%v", created, exists)
	}
	workspaceID := response["folder"].(map[string]any)["id"].(string)
	workspace, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if currentWorkspaceEntryAgentName(workspace) != "Launch Lead" || len(workspace.AgentInstances) != 1 {
		t.Fatalf("reviewed workspace roster = %#v", workspace.AgentInstances)
	}
}

func TestCreateWorkspaceWithReviewedMixedTemplateRosterKeepsReuseAndOrder(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := handler.agentStore.CreateAgent("Saved Lead", &agentstore.CreateAgentConfig{SystemPrompt: "saved and unchanged"}); err != nil {
		t.Fatalf("create saved agent: %v", err)
	}
	writeRosterTemplate(t, handler, "mixed-roster", `{
		"name":"Mixed Roster",
		"agents":[
			{"name":"Saved Lead","role":"orchestrator","system_prompt":"must not replace saved"},
			{"name":"New Scout","role":"researcher","system_prompt":"scout"},
			{"name":"New Writer","role":"writer","system_prompt":"writer"}
		]
	}`)
	tpl, err := handler.resolveProjectTemplate("mixed-roster", "")
	if err != nil {
		t.Fatalf("resolve template: %v", err)
	}
	plan := handler.buildTemplateAgentPlan(tpl)
	body, err := json.Marshal(map[string]any{
		"name": "Mixed Launch", "template_id": "mixed-roster", "template_agent_review": reviewForPlan(plan),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder, response := postCreateWorkspace(t, handler, string(body))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	saved, _ := handler.agentStore.GetAgent("Saved Lead")
	if saved.Settings.SystemPrompt != "saved and unchanged" {
		t.Fatalf("saved definition was modified: %+v", saved.Settings)
	}
	for _, name := range []string{"New Scout", "New Writer"} {
		if _, exists := handler.agentStore.GetAgent(name); !exists {
			t.Fatalf("reviewed agent %q was not created", name)
		}
	}
	workspaceID := response["folder"].(map[string]any)["id"].(string)
	workspace, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got := currentWorkspaceEntryAgentName(workspace); got != "Saved Lead" {
		t.Fatalf("entry agent = %q, want Saved Lead", got)
	}
	if len(workspace.AgentInstances) != 3 || workspace.AgentInstances[1].Name != "New Scout" || workspace.AgentInstances[2].Name != "New Writer" {
		t.Fatalf("workspace roster order = %#v", workspace.AgentInstances)
	}
}

func TestReviewedTemplateAgentPromptErrorIdentifiesOwningFieldBeforeWrites(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "invalid-prompt-roster", `{
		"name":"Invalid Prompt Roster",
		"agents":[{"name":"Blueprint Lead","role":"orchestrator"}]
	}`)
	tpl, err := handler.resolveProjectTemplate("invalid-prompt-roster", "")
	if err != nil {
		t.Fatalf("resolve template: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"name":        "Invalid Prompt Launch",
		"template_id": "invalid-prompt-roster",
		"template_agent_overrides": []map[string]any{{
			"index": 0, "system_prompt": "Obey {{unknown_workspace_value}}",
		}},
		"template_agent_review": reviewForPlan(handler.buildTemplateAgentPlan(tpl)),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder, response := postCreateWorkspace(t, handler, string(body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	conflict, _ := response["conflict"].(map[string]any)
	if conflict["type"] != "template_agent_override" || conflict["index"] != float64(0) || conflict["field"] != "system_prompt" {
		t.Fatalf("override conflict = %#v", conflict)
	}
	if _, exists := handler.agentStore.GetAgent("Blueprint Lead"); exists {
		t.Fatal("invalid prompt request created an agent")
	}
	workspaces, err := handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("invalid prompt request wrote workspaces: %#v", workspaces)
	}
}

func TestStaleTemplateAgentReviewReturnsFreshPlanBeforeWrites(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "stale-roster", `{
		"name":"Stale Roster",
		"agents":[{"name":"Blueprint Lead","role":"orchestrator"}]
	}`)
	tpl, err := handler.resolveProjectTemplate("stale-roster", "")
	if err != nil {
		t.Fatalf("resolve template: %v", err)
	}
	review := reviewForPlan(handler.buildTemplateAgentPlan(tpl))
	if err := handler.agentStore.CreateAgent("Blueprint Lead", &agentstore.CreateAgentConfig{SystemPrompt: "appeared later"}); err != nil {
		t.Fatalf("create concurrent agent: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"name": "Stale Launch", "template_id": "stale-roster", "template_agent_review": review,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder, response := postCreateWorkspace(t, handler, string(body))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	conflict, _ := response["conflict"].(map[string]any)
	if conflict["type"] != "template_agent_plan" {
		t.Fatalf("conflict = %#v", conflict)
	}
	fresh, _ := response["template_agent_plan"].(map[string]any)
	if fresh["revision"] == review.PlanRevision {
		t.Fatal("stale response did not include a fresh revision")
	}
	workspaces, err := handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("stale review wrote workspaces: %#v", workspaces)
	}
}

func TestReviewedTemplateAgentStaleSourcesReturnConflictWithoutWrites(t *testing.T) {
	tests := []struct {
		name         string
		manifest     string
		beforePlan   func(*Handler) error
		mutate       func(*Handler, *testing.T)
		overrides    []map[string]any
		adjustReview func(*templateAgentReview)
	}{
		{
			name:     "template manifest changed",
			manifest: `{"name":"Roster","agents":[{"name":"Lead","system_prompt":"before"}]}`,
			mutate: func(handler *Handler, t *testing.T) {
				writeRosterTemplate(t, handler, "stale-source", `{"name":"Roster","agents":[{"name":"Lead","system_prompt":"after"}]}`)
			},
		},
		{
			name:     "system default changed",
			manifest: `{"name":"Roster","agents":[{"name":"Lead"}]}`,
			beforePlan: func(handler *Handler) error {
				handler.SetSystemModelReader(fakeSystemModelReader{provider: "codex", model: "before-model"})
				return nil
			},
			mutate: func(handler *Handler, _ *testing.T) {
				handler.SetSystemModelReader(fakeSystemModelReader{provider: "codex", model: "after-model"})
			},
		},
		{
			name:     "saved agent appeared",
			manifest: `{"name":"Roster","agents":[{"name":"Lead"}]}`,
			mutate: func(handler *Handler, t *testing.T) {
				if err := handler.agentStore.CreateAgent("Lead", &agentstore.CreateAgentConfig{SystemPrompt: "appeared"}); err != nil {
					t.Fatalf("create appeared agent: %v", err)
				}
			},
		},
		{
			name:     "saved agent disappeared",
			manifest: `{"name":"Roster","agents":[{"name":"Lead"}]}`,
			beforePlan: func(handler *Handler) error {
				return handler.agentStore.CreateAgent("Lead", &agentstore.CreateAgentConfig{SystemPrompt: "saved"})
			},
			mutate: func(handler *Handler, t *testing.T) {
				if err := handler.agentStore.DeleteAgent("Lead"); err != nil {
					t.Fatalf("delete saved agent: %v", err)
				}
			},
		},
		{
			name:     "saved agent settings changed",
			manifest: `{"name":"Roster","agents":[{"name":"Lead"}]}`,
			beforePlan: func(handler *Handler) error {
				return handler.agentStore.CreateAgent("Lead", &agentstore.CreateAgentConfig{SystemPrompt: "before"})
			},
			mutate: func(handler *Handler, t *testing.T) {
				if err := handler.agentStore.DeleteAgent("Lead"); err != nil {
					t.Fatalf("replace saved agent: %v", err)
				}
				if err := handler.agentStore.CreateAgent("Lead", &agentstore.CreateAgentConfig{SystemPrompt: "after"}); err != nil {
					t.Fatalf("recreate saved agent: %v", err)
				}
			},
		},
		{
			name:     "staged copy became occupied",
			manifest: `{"name":"Roster","agents":[{"name":"Lead"}]}`,
			overrides: []map[string]any{{
				"index": 0, "name": "Lead copy",
			}},
			adjustReview: func(review *templateAgentReview) {
				review.Expectations[0].Name = "Lead copy"
			},
			mutate: func(handler *Handler, t *testing.T) {
				if err := handler.agentStore.CreateAgent("Lead copy", &agentstore.CreateAgentConfig{SystemPrompt: "occupied"}); err != nil {
					t.Fatalf("occupy copy name: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			writeRosterTemplate(t, handler, "stale-source", test.manifest)
			if test.beforePlan != nil {
				if err := test.beforePlan(handler); err != nil {
					t.Fatalf("prepare plan: %v", err)
				}
			}
			tpl, err := handler.resolveProjectTemplate("stale-source", "")
			if err != nil {
				t.Fatalf("resolve template: %v", err)
			}
			review := reviewForPlan(handler.buildTemplateAgentPlan(tpl))
			if test.adjustReview != nil {
				test.adjustReview(&review)
			}
			if test.mutate != nil {
				test.mutate(handler, t)
			}
			agentsBefore := append([]string(nil), handler.agentStore.ListAgents()...)
			body, err := json.Marshal(map[string]any{
				"name":                     "Stale Source",
				"template_id":              "stale-source",
				"template_agent_overrides": test.overrides,
				"template_agent_review":    review,
			})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			recorder, response := postCreateWorkspace(t, handler, string(body))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
			}
			conflict, _ := response["conflict"].(map[string]any)
			if conflict["type"] != "template_agent_plan" || response["template_agent_plan"] == nil {
				t.Fatalf("stale response = %#v", response)
			}
			if test.name == "staged copy became occupied" {
				if conflict["index"] != float64(0) || conflict["name"] != "Lead copy" || conflict["expected_action"] != "create" || conflict["actual_action"] != "reuse" {
					t.Fatalf("copy conflict details = %#v", conflict)
				}
			}
			if got := handler.agentStore.ListAgents(); len(got) != len(agentsBefore) {
				t.Fatalf("agents changed during stale request: before=%v after=%v", agentsBefore, got)
			}
			workspaces, err := handler.store.ListWorkspaces(context.Background())
			if err != nil {
				t.Fatalf("ListWorkspaces: %v", err)
			}
			if len(workspaces) != 0 {
				t.Fatalf("stale request wrote workspaces: %#v", workspaces)
			}
		})
	}
}

func TestStrictTemplateAgentFailureAfterReuseNeverDeletesSavedDefinition(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	base := handler.agentStore
	if err := base.CreateAgent("Saved", &agentstore.CreateAgentConfig{SystemPrompt: "keep"}); err != nil {
		t.Fatalf("create saved agent: %v", err)
	}
	failing := &failNthAgentCreateStore{Store: base, failAt: 2}
	handler.SetAgentStore(failing)
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Saved"},
		projecttemplates.AgentSpec{Name: "First New"},
		projecttemplates.AgentSpec{Name: "Second New"},
	)
	review := reviewForPlan(handler.buildTemplateAgentPlan(tpl))

	seed, err := handler.seedTemplateAgentsStrict(&session.Workspace{ID: "strict-reuse"}, tpl, tpl, review)
	if err == nil {
		t.Fatal("expected second request-owned create to fail")
	}
	recorder := httptest.NewRecorder()
	handler.respondStrictTemplateAgentSeedError(recorder, seed, err)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	saved, exists := base.GetAgent("Saved")
	if !exists || saved.Settings.SystemPrompt != "keep" {
		t.Fatalf("reused saved definition changed or disappeared: %+v, exists=%v", saved, exists)
	}
	if _, exists := base.GetAgent("First New"); exists {
		t.Fatal("request-owned definition survived strict rollback")
	}
}

func TestStrictTemplateAgentFailureRollsBackEarlierDefinitions(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	base := handler.agentStore
	failing := &failNthAgentCreateStore{Store: base, failAt: 2}
	handler.SetAgentStore(failing)
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "First"},
		projecttemplates.AgentSpec{Name: "Second"},
	)
	review := reviewForPlan(handler.buildTemplateAgentPlan(tpl))

	seed, err := handler.seedTemplateAgentsStrict(&session.Workspace{ID: "strict"}, tpl, tpl, review)
	if err == nil {
		t.Fatal("expected second strict create to fail")
	}
	recorder := httptest.NewRecorder()
	handler.respondStrictTemplateAgentSeedError(recorder, seed, err)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if _, exists := base.GetAgent("First"); exists {
		t.Fatal("first request-owned definition survived strict rollback")
	}
	if _, exists := base.GetAgent("Second"); exists {
		t.Fatal("failed second definition unexpectedly exists")
	}
}
