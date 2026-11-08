package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func recommendedCreationFixture(t *testing.T, extraCreate bool) (*buildFixture, personalassistant.WorkspaceBuildSession, map[string]any) {
	t.Helper()
	h, _, _, cleanup := templateTestEnv(t)
	t.Cleanup(cleanup)
	writeRosterTemplate(t, h, "staffing-fixture", `{"name":"Software team","description":"Build software tools","agents":[{"name":"Engineer","role":"specialist"},{"name":"Helper","role":"specialist"}]}`)
	tpl, err := h.resolveProjectTemplate("staffing-fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	f := newBuildFixtureOn(t, h, true)
	f.catalog = []WorkspaceBuildCatalogEntry{readyEntry(tpl)}
	if _, err := store.MutateDefinition(h.agentStore, "Scout", func(a *agent.Agent) error {
		a.Settings.Provider, a.Settings.Model, a.Settings.SystemPrompt = "scripted", "scripted-1", "private preserved prompt"
		a.Metadata = &types.AgentMetadata{Description: "Builds and reviews software tools"}
		a.Appearance = &types.AgentAppearance{Mode: types.AppearanceModeGenerated, Generated: &types.GeneratedAppearance{Color: "#125522"}}
		a.DefaultToolbox = &types.AgentDefaultToolbox{Name: "Authored direct-chat tools", Version: 3, Skills: []types.DefaultToolboxSkillRef{{CapabilityID: "fixture-tool", DisplayName: "Fixture tool"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	build := f.start(t)
	_, err = f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		current := doc.Session(build.ID)
		current.Draft = personalassistant.BuildDraft{TemplateID: tpl.ID, Name: "Reviewed software", Description: "Software tools", Mission: "Build software tools",
			WorkspaceBootstrap: json.RawMessage(`{"goal":"Build software tools","systems":"","context":""}`)}
		current.Touch(f.store.Now())
		build = *current
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state := h.workspaceBuildStaffing(t.Context(), "local", &build, f.catalog)
	var candidate rosterstaffing.Candidate
	for _, c := range state.input.Roster.Candidates {
		if c.Name == "Scout" {
			candidate = c
		}
	}
	proposal := rosterstaffing.Proposal{}
	for i, role := range state.input.Roles {
		choice := rosterstaffing.RoleProposal{RoleID: role.ID, State: rosterstaffing.NeedsChoice}
		if i == 0 {
			choice.State = rosterstaffing.Recommended
			choice.Choices = []rosterstaffing.Choice{{Candidate: candidate.Handle, Evidence: []string{candidate.Evidence[0].ID}}}
		}
		proposal.Roles = append(proposal.Roles, choice)
	}
	reply, _ := json.Marshal(buildReply{Say: "Review your saved software agent.", Ask: buildReplyAsk{Question: "Use the recommendation or choose manually?", AllowFreeText: true}, Patch: buildReplyPatch{Set: []string{}}, Staffing: proposal})
	f.provider.replies = []string{string(reply)}
	status, body := f.turn(t, build, map[string]any{"text": "Use my saved software team"})
	if status != http.StatusOK {
		t.Fatalf("recommendation: %d %+v", status, body)
	}
	build = sessionOf(t, body)
	if build.Staffing == nil || len(build.Staffing.Roles[0].Choices) != 1 {
		t.Fatalf("no host-validated choice: %+v", build.Staffing)
	}
	roles := projecttemplates.AgentRoleIDs(tpl.Agents)
	fills := []roleStaffingInput{{RoleID: roles[0], Mode: roleStaffingModeAssign, Name: "Scout", Recommendation: build.Staffing.Roles[0].Choices[0].Handle}}
	if extraCreate {
		fills = append(fills, roleStaffingInput{RoleID: roles[1], Mode: roleStaffingModeCreate, Name: "New helper"})
	}
	draft := build.Draft
	draft.RoleStaffing, _ = json.Marshal(fills)
	status, body = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build.ID+"/draft", map[string]any{"version": build.Version, "draft": draft, "edited_fields": []string{"team"}})
	if status != http.StatusOK {
		t.Fatalf("select: %d %+v", status, body)
	}
	build = sessionOf(t, body)
	if build.SelectedStaffing != nil {
		t.Fatal("private selected source leaked in response")
	}
	encoded, _ := json.Marshal(build.Draft)
	var material map[string]any
	_ = json.Unmarshal(encoded, &material)
	material["mission"] = build.Draft.Mission
	material["build_session_id"], material["build_review_version"] = build.ID, build.Version
	return f, build, material
}

func TestWorkspaceBuildStaffingSelectionCanSelectSeparateRolesButCannotUndoExplicitClear(t *testing.T) {
	for _, scenario := range []string{"same_context", "changed_context"} {
		t.Run(scenario, func(t *testing.T) {
			f, _, _ := recommendedCreationFixture(t, false)
			if err := f.handler.agentStore.CreateAgent("Second Scout", &store.CreateAgentConfig{LLMProvider: "scripted", Model: "scripted-1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.MutateDefinition(f.handler.agentStore, "Second Scout", func(a *agent.Agent) error {
				a.Metadata = &types.AgentMetadata{Description: "Tests software tools"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var build personalassistant.WorkspaceBuildSession
			_, err := f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
				current := doc.Open()
				if scenario == "changed_context" {
					current.Draft.Mission = "Build different software tools"
				}
				state := f.handler.workspaceBuildStaffing(t.Context(), "local", current, f.catalog)
				proposal := rosterstaffing.Proposal{}
				for i, role := range state.input.Roles {
					name := "Scout"
					if i > 0 {
						name = "Second Scout"
					}
					for _, candidate := range state.input.Roster.Candidates {
						if candidate.Name != name {
							continue
						}
						proposal.Roles = append(proposal.Roles, rosterstaffing.RoleProposal{RoleID: role.ID, State: rosterstaffing.Recommended, Choices: []rosterstaffing.Choice{{Candidate: candidate.Handle, Evidence: []string{candidate.Evidence[0].ID}}}})
					}
				}
				finishBuildStaffing(current, buildOutcome{kind: "reply", reply: buildReply{Staffing: proposal}}, state)
				current.Touch(f.store.Now())
				build = *current
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if build.Staffing.State != rosterstaffing.Recommended || len(build.Staffing.Roles[1].Choices) != 1 {
				t.Fatal("fixture did not obtain two validated recommendations")
			}
			var fills []roleStaffingInput
			_ = json.Unmarshal(build.Draft.RoleStaffing, &fills)
			original := fills[0]
			second := build.Staffing.Roles[1]
			fills = append(fills, roleStaffingInput{RoleID: second.RoleID, Name: second.Choices[0].Name, Mode: roleStaffingModeAssign, Recommendation: second.Choices[0].Handle})
			patch := func() (int, map[string]any) {
				draft := build.Draft
				draft.RoleStaffing, _ = json.Marshal(fills)
				return f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build.ID+"/draft", map[string]any{"version": build.Version, "draft": draft, "edited_fields": []string{"team"}})
			}
			status, body := patch()
			if scenario == "changed_context" {
				if status != http.StatusConflict {
					t.Fatal("adding a role silently renewed the first role's old context")
				}
				return
			}
			if status != http.StatusOK {
				t.Fatalf("second recommendation was invalidated by selecting the first: %d %+v", status, body)
			}
			build = sessionOf(t, body)
			doc, _ := f.store.Read(t.Context(), "local")
			if len(doc.Open().SelectedStaffing.Choices) != 2 {
				t.Fatal("second selection replaced the first")
			}
			fills = fills[1:]
			status, body = patch()
			if status != http.StatusOK {
				t.Fatalf("explicit clear failed: %d %+v", status, body)
			}
			build = sessionOf(t, body)
			if build.Staffing.Roles[0].State != rosterstaffing.Stale || build.Staffing.Roles[1].State != rosterstaffing.Recommended {
				t.Fatal("clear did not invalidate only the cleared role")
			}
			fills = append(fills, original)
			if status, _ := patch(); status != http.StatusConflict {
				t.Fatal("old advice undid an explicit clear")
			}
		})
	}
}

func TestWorkspaceBuildStaffingSelectionManualChoiceAndContextFences(t *testing.T) {
	for _, change := range []string{"clear", "manual_assign", "sync_clear", "goal", "source", "owner"} {
		t.Run(change, func(t *testing.T) {
			f, build, _ := recommendedCreationFixture(t, false)
			draft := build.Draft
			var fills []roleStaffingInput
			_ = json.Unmarshal(draft.RoleStaffing, &fills)
			sync := false
			switch change {
			case "clear":
				draft.RoleStaffing = json.RawMessage(`[]`)
			case "manual_assign":
				fills[0].Recommendation = ""
				draft.RoleStaffing, _ = json.Marshal(fills)
			case "sync_clear":
				draft.RoleStaffing = json.RawMessage(`[]`)
				sync = true
			case "goal":
				draft.Mission = "A different purpose"
			case "source":
				if _, err := store.MutateDefinition(f.handler.agentStore, "Scout", func(a *agent.Agent) error { a.Settings.SystemPrompt = "new"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if _, err := f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
					doc.Session(build.ID).SelectedStaffing.OwnerUserID = "foreign"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			status, _ := f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build.ID+"/draft", map[string]any{"version": build.Version, "draft": draft, "sync": sync})
			want := http.StatusConflict
			if change == "clear" || change == "manual_assign" || change == "goal" || change == "source" {
				want = http.StatusOK
			}
			if status != want {
				t.Fatalf("%s: status=%d want=%d", change, status, want)
			}
			doc, _ := f.store.Read(t.Context(), "local")
			if (change == "clear" || change == "manual_assign") && doc.Session(build.ID).SelectedStaffing != nil {
				t.Fatal("manual action retained recommendation authority")
			}
			if change == "goal" || change == "source" {
				current := doc.Session(build.ID)
				encoded, _ := json.Marshal(current.Draft)
				var material map[string]any
				_ = json.Unmarshal(encoded, &material)
				material["mission"] = current.Draft.Mission
				material["build_session_id"], material["build_review_version"] = current.ID, current.Version
				response, _ := creationOperationRequest(t, f.handler, http.MethodPost, "", map[string]any{"id": "d6c89e4b-4b08-46be-80ed-e58a5ce28ce1", "request": material})
				if response.Code != http.StatusConflict {
					t.Fatalf("stale retained intent renewed itself: %d %s", response.Code, response.Body)
				}
			}
			if f.provider.calls != 1 {
				t.Fatal("editing/polling invoked ranking")
			}
		})
	}
}

func TestWorkspaceBuildStaffingContextUsesCreationOptionSemantics(t *testing.T) {
	build := &personalassistant.WorkspaceBuildSession{ID: "same-build", ContextVersion: 3}
	draft := personalassistant.BuildDraft{TemplateID: "software", WorkspaceBootstrap: json.RawMessage(`{"goal":"Build software","systems":"","context":""}`)}
	before, err := staffingDraftContext("local", build, draft, "revision")
	if err != nil {
		t.Fatal(err)
	}
	draft.WorkspaceBootstrap = json.RawMessage(`{"goal":"Build software"}`)
	after, err := staffingDraftContext("local", build, draft, "revision")
	if err != nil || before != after {
		t.Fatal("equivalent empty option fields changed reviewed context")
	}
	draft.WorkspaceBootstrap = json.RawMessage(`{"goal":"Build software","context":"different requirements"}`)
	changed, err := staffingDraftContext("local", build, draft, "revision")
	if err != nil || changed == before {
		t.Fatal("different setup requirements retained reviewed context")
	}
}

func TestWorkspaceBuildStaffingSelectionReviewsAndBindsExactSource(t *testing.T) {
	f, build, material := recommendedCreationFixture(t, false)
	before := store.LibraryDefinition(f.handler.agentStore, "Scout").Definition
	count := len(f.handler.agentStore.ListAgents())
	id, digest := reviewCreationOperation(t, f.handler, material)
	if rows, _ := f.handler.store.ListWorkspaces(t.Context()); len(rows) != 0 {
		t.Fatal("review assigned a profile")
	}
	response, body := creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if response.Code != http.StatusCreated {
		t.Fatalf("confirm: %d %s", response.Code, response.Body)
	}
	workspaceID := body["folder"].(map[string]any)["id"].(string)
	ws, err := f.handler.store.GetWorkspace(t.Context(), workspaceID)
	if err != nil || len(ws.AgentInstances) != 1 || ws.AgentInstances[0].Name != "Scout" || ws.AgentInstances[0].RoleSource != workspace.RoleSourceAssigned {
		t.Fatalf("wrong canonical binding: %+v %v", ws, err)
	}
	if count != len(f.handler.agentStore.ListAgents()) || !reflect.DeepEqual(before, store.LibraryDefinition(f.handler.agentStore, "Scout").Definition) {
		t.Fatal("reuse changed or added profiles")
	}
	for range 2 {
		response, _ = creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if response.Code != http.StatusCreated {
			t.Fatalf("replay: %d %s", response.Code, response.Body)
		}
	}
	if f.provider.calls != 1 {
		t.Fatal("review/confirm replay called the ranker")
	}
	doc, _ := f.store.Read(t.Context(), "local")
	if doc.Session(build.ID).Status != personalassistant.WorkspaceBuildCreated {
		t.Fatal("build not settled")
	}
}

func TestWorkspaceBuildStaffingSelectionPreservesOtherAssignmentAndPrivateCopy(t *testing.T) {
	for _, partialFailure := range []bool{false, true} {
		name := "success"
		if partialFailure {
			name = "partial_failure"
		}
		t.Run(name, func(t *testing.T) {
			f, _, material := recommendedCreationFixture(t, true)
			h := f.handler
			before := store.LibraryDefinition(h.agentStore, "Scout").Definition
			count := len(h.agentStore.ListAgents())
			other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Other assignment"})
			other.AgentInstances = []workspace.AgentInstance{{ID: "other-scout", Name: "Scout", RoleID: "other-role", RoleSource: workspace.RoleSourceAssigned, CustomInstructions: "Private other-assignment instructions"}}
			if err := session.NewWorkspaceStoreAdapter(h.store).Save(other); err != nil {
				t.Fatal(err)
			}
			if err := h.workspaceStore.Save(other); err != nil {
				t.Fatal(err)
			}
			private := store.LibraryDefinition(h.agentStore, "Scout").Definition
			private.Settings.SystemPrompt = "Private workspace-copy instructions"
			private.Settings.Model = "private-workspace-model"
			if err := h.workspaceStore.SaveWorkspaceAgent(other.ID, "Scout", private); err != nil {
				t.Fatal(err)
			}
			otherBefore, err := h.store.GetWorkspace(t.Context(), other.ID)
			if err != nil || len(otherBefore.AgentInstances) != 1 || otherBefore.AgentInstances[0].CustomInstructions != other.AgentInstances[0].CustomInstructions {
				t.Fatal("other assignment fixture was not persisted")
			}
			copyBefore, exists, err := h.workspaceStore.GetWorkspaceAgent(other.ID, "Scout")
			if err != nil || !exists {
				t.Fatalf("private copy unavailable: %v", err)
			}
			id, digest := reviewCreationOperation(t, h, material)
			if partialFailure {
				base := h.agentStore
				h.SetAgentStore(&changeSourceDuringCreate{Store: base, StaffingSourceGuard: base.(store.StaffingSourceGuard), afterCreate: func() { h.workspaceBuild.ProfileModelConfigured = func(string, string) bool { return false } }})
			}
			response, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
			wantStatus, wantProfiles, wantWorkspaces := http.StatusCreated, count+1, 2
			if partialFailure {
				wantStatus, wantProfiles, wantWorkspaces = http.StatusConflict, count, 1
			}
			if response.Code != wantStatus {
				t.Fatalf("confirm: %d %s", response.Code, response.Body)
			}
			if len(h.agentStore.ListAgents()) != wantProfiles {
				t.Fatal("unexpected profile creation/rollback")
			}
			if !reflect.DeepEqual(before, store.LibraryDefinition(h.agentStore, "Scout").Definition) {
				t.Fatal("reuse changed the saved authored definition")
			}
			copyAfter, exists, err := h.workspaceStore.GetWorkspaceAgent(other.ID, "Scout")
			if err != nil || !exists || !reflect.DeepEqual(copyBefore, copyAfter) {
				t.Fatal("reuse changed the other workspace's private copy")
			}
			otherAfter, err := h.store.GetWorkspace(t.Context(), other.ID)
			if err != nil || !reflect.DeepEqual(otherBefore.AgentInstances, otherAfter.AgentInstances) {
				t.Fatal("reuse changed the other assignment")
			}
			rows, err := h.store.ListWorkspaces(t.Context())
			if err != nil || len(rows) != wantWorkspaces {
				t.Fatal("unexpected canonical workspace effects")
			}
			if f.provider.calls != 1 {
				t.Fatal("review or commit ranked again")
			}
		})
	}
}

func TestWorkspaceBuildStaffingSelectionRejectsChangedSourceBeforeEffects(t *testing.T) {
	for _, change := range []string{"edit", "replacement", "delete", "readiness"} {
		t.Run(change, func(t *testing.T) {
			f, _, material := recommendedCreationFixture(t, true)
			id, digest := reviewCreationOperation(t, f.handler, material)
			switch change {
			case "edit":
				if _, err := store.MutateDefinition(f.handler.agentStore, "Scout", func(a *agent.Agent) error { a.Settings.SystemPrompt = "changed"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "replacement", "delete":
				if err := f.handler.agentStore.DeleteAgent("Scout"); err != nil {
					t.Fatal(err)
				}
				if change == "replacement" {
					if err := f.handler.agentStore.CreateAgent("Scout", &store.CreateAgentConfig{LLMProvider: "scripted", Model: "scripted-1"}); err != nil {
						t.Fatal(err)
					}
				}
			case "readiness":
				f.handler.workspaceBuild.ProfileModelConfigured = func(string, string) bool { return false }
			}
			response, body := creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
			if response.Code != http.StatusConflict || body["code"] != "staffing_review_changed" {
				t.Fatalf("changed source: %d %s", response.Code, response.Body)
			}
			if rows, _ := f.handler.store.ListWorkspaces(t.Context()); len(rows) != 0 {
				t.Fatal("stale source wrote workspace")
			}
			if _, exists := f.handler.agentStore.GetAgent("New helper"); exists {
				t.Fatal("stale source created a helper")
			}
		})
	}
}

func TestWorkspaceBuildStaffingSelectionCannotDowngradeOrChangeReviewedIntent(t *testing.T) {
	for _, change := range []string{"direct", "omitted_handle", "omitted_version", "wrong_handle", "create_instead", "wrong_role", "wrong_name", "changed_goal", "changed_destination"} {
		t.Run(change, func(t *testing.T) {
			f, _, material := recommendedCreationFixture(t, false)
			role := material["role_staffing"].([]any)[0].(map[string]any)
			switch change {
			case "omitted_handle":
				delete(role, "recommendation")
			case "omitted_version":
				delete(material, "build_review_version")
			case "wrong_handle":
				role["recommendation"] = "foreign_choice"
			case "create_instead":
				role["mode"] = "create"
			case "wrong_role":
				role["role_id"] = "wrong"
			case "wrong_name":
				role["name"] = "Luna"
			case "changed_goal":
				material["mission"] = "A different purpose"
			case "changed_destination":
				material["location"] = t.TempDir()
			}
			if change == "direct" {
				status, _ := f.do(t, http.MethodPost, "/api/workspaces", material)
				if status != http.StatusConflict {
					t.Fatalf("direct create accepted: %d", status)
				}
			} else {
				response, _ := creationOperationRequest(t, f.handler, http.MethodPost, "", map[string]any{"id": "d6c89e4b-4b08-46be-80ed-e58a5ce28ce1", "request": material})
				if response.Code != http.StatusConflict {
					t.Fatalf("tampered review accepted: %d %s", response.Code, response.Body)
				}
			}
			if rows, _ := f.handler.store.ListWorkspaces(t.Context()); len(rows) != 0 {
				t.Fatal("tampered request created a workspace")
			}
		})
	}
}

type changeSourceDuringCreate struct {
	store.Store
	store.StaffingSourceGuard
	afterCreate func()
}

func (s *changeSourceDuringCreate) ReadStaffingSnapshot(ctx context.Context) store.StaffingSnapshot {
	return store.ReadStaffingSnapshot(ctx, s.Store)
}
func (s *changeSourceDuringCreate) CreateStaffingProfile(ctx context.Context, name string, config *store.CreateAgentConfig) (store.StaffingSelection, error) {
	selected, err := s.Store.(store.StaffingProfileCreator).CreateStaffingProfile(ctx, name, config)
	if err != nil {
		return selected, err
	}
	if s.afterCreate != nil {
		s.afterCreate()
		return selected, nil
	}
	_, err = store.MutateDefinition(s.Store, "Scout", func(a *agent.Agent) error { a.Settings.SystemPrompt = "changed during seeding"; return nil })
	return selected, err
}

func TestWorkspaceBuildStaffingSelectionRechecksAtCanonicalBindingWrite(t *testing.T) {
	for _, change := range []string{"source", "template", "readiness", "created_replacement"} {
		t.Run(change, func(t *testing.T) {
			f, _, material := recommendedCreationFixture(t, true)
			id, digest := reviewCreationOperation(t, f.handler, material)
			base := f.handler.agentStore
			interleaved := &changeSourceDuringCreate{Store: base, StaffingSourceGuard: base.(store.StaffingSourceGuard)}
			if change == "template" {
				interleaved.afterCreate = func() {
					writeRosterTemplate(t, f.handler, "staffing-fixture", `{"name":"Changed team","agents":[{"name":"Engineer","role":"specialist"}]}`)
				}
			}
			if change == "readiness" {
				interleaved.afterCreate = func() { f.handler.workspaceBuild.ProfileModelConfigured = func(string, string) bool { return false } }
			}
			if change == "created_replacement" {
				interleaved.afterCreate = func() {
					if err := base.DeleteAgent("New helper"); err != nil {
						t.Fatal(err)
					}
					if err := base.CreateAgent("New helper", &store.CreateAgentConfig{SystemPrompt: "replacement"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.handler.SetAgentStore(interleaved)
			response, _ := creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
			if response.Code != http.StatusConflict {
				t.Fatalf("changed review was bound: %d %s", response.Code, response.Body)
			}
			if rows, _ := f.handler.store.ListWorkspaces(t.Context()); len(rows) != 0 {
				t.Fatal("changed review acquired a canonical binding")
			}
			if helper, exists := base.GetAgent("New helper"); change == "created_replacement" {
				if !exists || helper.Settings.SystemPrompt != "replacement" {
					t.Fatal("rollback deleted the replacement")
				}
			} else if exists {
				t.Fatal("uncommitted create survived rollback")
			}
			if _, exists := base.GetAgent("Scout"); !exists {
				t.Fatal("rollback deleted reused source")
			}
			assertCreationOperationState(t, f.handler, id, "reconciliation_required")
		})
	}
}
