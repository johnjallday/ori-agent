package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

// failingWorkspaceCreateStore makes the core workspace write fail while leaving
// every other store operation intact, so a test can observe exactly what the
// create path leaves behind when the workspace itself never comes into
// existence.
type failingWorkspaceCreateStore struct {
	session.HybridStore
	err error
}

func (s failingWorkspaceCreateStore) CreateWorkspace(ctx context.Context, ws *session.Workspace) error {
	return s.err
}

// writeRosterTemplate installs a metadata-only template declaring the given
// agent roster, and returns its template id.
func writeRosterTemplate(t *testing.T, handler *Handler, id string, manifest string) string {
	t.Helper()
	dir := filepath.Join(handler.templatesRootResolver(), id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertAgentAbsent(t *testing.T, handler *Handler, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := handler.agentStore.GetAgent(name); ok {
			t.Errorf("agent %q was left behind after a failed create; the request owns it and must roll it back", name)
		}
	}
}

func assertAgentPresent(t *testing.T, handler *Handler, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := handler.agentStore.GetAgent(name); !ok {
			t.Errorf("agent %q is missing; a pre-existing reused agent must survive rollback untouched", name)
		}
	}
}

// TestCreateWorkspaceLeavesNoNewAgentsWhenCoreCreateFails covers FR71: nothing
// the request would have created may survive a workspace that never existed.
// Seeding runs before the store write, so without cleanup the user is left with
// agents in Your Agents for a workspace they do not have.
func TestCreateWorkspaceLeavesNoNewAgentsWhenCoreCreateFails(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeRosterTemplate(t, handler, "roster-template", `{
		"name":"Roster Template",
		"agents":[
			{"name":"Campaign Lead","role":"orchestrator"},
			{"name":"Copywriter"}
		]
	}`)

	handler.store = failingWorkspaceCreateStore{
		HybridStore: handler.store,
		err:         errors.New("simulated core store failure"),
	}

	w, _ := postCreateWorkspace(t, handler, `{"name":"Launch","template_id":"roster-template"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the core create fails, got %d: %s", w.Code, w.Body.String())
	}

	assertAgentAbsent(t, handler, "Campaign Lead", "Copywriter")
}

// TestCreateWorkspaceSlugConflictRollbackLeavesNoNewAgents covers FR72. The
// folder-slug conflict path already rolls the workspace record back; the agents
// it seeded beforehand must go with it.
func TestCreateWorkspaceSlugConflictRollbackLeavesNoNewAgents(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeRosterTemplate(t, handler, "first-template", `{
		"name":"First Template",
		"agents":[{"name":"First Lead","role":"orchestrator"}]
	}`)
	writeRosterTemplate(t, handler, "second-template", `{
		"name":"Second Template",
		"agents":[{"name":"Second Lead","role":"orchestrator"},{"name":"Second Helper"}]
	}`)

	if w, _ := postCreateWorkspace(t, handler, `{"name":"Launch","template_id":"first-template"}`); w.Code != http.StatusCreated {
		t.Fatalf("seed workspace: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Same name, so the same folder slug: the second create is refused and its
	// workspace record rolled back.
	w, resp := postCreateWorkspace(t, handler, `{"name":"Launch","template_id":"second-template"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 folder-slug conflict, got %d: %s", w.Code, w.Body.String())
	}
	if conflict, ok := resp["conflict"].(map[string]any); !ok || conflict["type"] != "folder_slug" {
		t.Fatalf("expected a folder_slug conflict payload, got %v", resp)
	}

	assertAgentAbsent(t, handler, "Second Lead", "Second Helper")
	// The first workspace's agent is untouched by the second request's rollback.
	assertAgentPresent(t, handler, "First Lead")
}

// TestCreateWorkspaceRollbackNeverTouchesReusedAgents covers FR73. Rollback must
// remove only definitions this request actually created; an agent that already
// existed is reused, not owned, and deleting it would destroy the user's data.
func TestCreateWorkspaceRollbackNeverTouchesReusedAgents(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Shared Lead", &agentstore.CreateAgentConfig{
		SystemPrompt: "the user's own prompt",
	}); err != nil {
		t.Fatalf("seed shared agent: %v", err)
	}

	writeRosterTemplate(t, handler, "reuse-template", `{
		"name":"Reuse Template",
		"agents":[
			{"name":"Shared Lead","role":"orchestrator","system_prompt":"template prompt"},
			{"name":"Fresh Helper"}
		]
	}`)

	handler.store = failingWorkspaceCreateStore{
		HybridStore: handler.store,
		err:         errors.New("simulated core store failure"),
	}

	w, _ := postCreateWorkspace(t, handler, `{"name":"Launch","template_id":"reuse-template"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	assertAgentAbsent(t, handler, "Fresh Helper")
	assertAgentPresent(t, handler, "Shared Lead")

	// Reuse means reuse: the template's prompt never overwrites the saved one,
	// and rollback must not have mutated it either.
	shared, ok := handler.agentStore.GetAgent("Shared Lead")
	if !ok {
		t.Fatal("shared agent vanished")
	}
	if shared.Settings.SystemPrompt != "the user's own prompt" {
		t.Fatalf("reused agent prompt = %q, want the user's own prompt untouched", shared.Settings.SystemPrompt)
	}
}

// TestCreateWorkspaceRollbackRemovesCustomizedCopies covers FR71 for staged
// customizations: an override that renames a roster entry produces a brand-new
// definition, which the request owns just as much as an unrenamed one.
func TestCreateWorkspaceRollbackRemovesCustomizedCopies(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Shared Lead", &agentstore.CreateAgentConfig{
		SystemPrompt: "the user's own prompt",
	}); err != nil {
		t.Fatalf("seed shared agent: %v", err)
	}

	writeRosterTemplate(t, handler, "copy-template", `{
		"name":"Copy Template",
		"agents":[{"name":"Shared Lead","role":"orchestrator"}]
	}`)

	handler.store = failingWorkspaceCreateStore{
		HybridStore: handler.store,
		err:         errors.New("simulated core store failure"),
	}

	body := `{
		"name":"Launch",
		"template_id":"copy-template",
		"template_agent_overrides":[{"index":0,"name":"Shared Lead Studio","system_prompt":"be terse"}]
	}`
	w, _ := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	assertAgentAbsent(t, handler, "Shared Lead Studio")
	assertAgentPresent(t, handler, "Shared Lead")
}

// TestCreateWorkspaceValidatesCompositionBeforeSeeding covers FR70: a request
// that cannot produce a valid workspace must be refused before any agent
// definition is written, not cleaned up afterwards.
func TestCreateWorkspaceValidatesCompositionBeforeSeeding(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeRosterTemplate(t, handler, "roster-template", `{
		"name":"Roster Template",
		"agents":[{"name":"Campaign Lead","role":"orchestrator"},{"name":"Copywriter"}]
	}`)

	// entry_agent_name names an agent that is not in existing_agent_names, which
	// the composition validator rejects.
	body := `{
		"name":"Launch",
		"template_id":"roster-template",
		"existing_agent_names":[],
		"entry_agent_name":"Nobody At All"
	}`
	w, _ := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid composition, got %d: %s", w.Code, w.Body.String())
	}

	assertAgentAbsent(t, handler, "Campaign Lead", "Copywriter")
}

// TestCreateWorkspaceKeepsAgentsWhenProvisioningOnlyWarns covers FR74 and guards
// the obvious over-correction: rollback must fire when the workspace does not
// exist, and never when it does. A workspace created with a non-fatal
// provisioning warning is still a created workspace, and its team is valid.
func TestCreateWorkspaceKeepsAgentsWhenProvisioningOnlyWarns(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeRosterTemplate(t, handler, "roster-template", `{
		"name":"Roster Template",
		"agents":[{"name":"Campaign Lead","role":"orchestrator"},{"name":"Copywriter"}]
	}`)

	// A template_path that does not exist makes project instantiation fail
	// without preventing the workspace itself from being created.
	body := `{"name":"Launch","template_path":"/definitely/not/a/real/template/dir"}`
	w, resp := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 despite a project warning, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := resp["folder"].(map[string]any); !ok {
		t.Fatalf("expected the created workspace in the response: %v", resp)
	}

	// Now the same for a resolvable roster template: creation succeeds and the
	// seeded agents stay.
	w2, _ := postCreateWorkspace(t, handler, `{"name":"Second Launch","template_id":"roster-template"}`)
	if w2.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w2.Code, w2.Body.String())
	}
	assertAgentPresent(t, handler, "Campaign Lead", "Copywriter")
}

// TestCreateWorkspaceRepeatSubmissionDoesNotDuplicateAgents covers FR75. A retry
// (or a double click that gets through) must reuse the definitions the first
// attempt created rather than making near-duplicates, and must not attach the
// same agent twice.
func TestCreateWorkspaceRepeatSubmissionDoesNotDuplicateAgents(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeRosterTemplate(t, handler, "roster-template", `{
		"name":"Roster Template",
		"agents":[{"name":"Campaign Lead","role":"orchestrator"},{"name":"Copywriter"}]
	}`)

	if w, _ := postCreateWorkspace(t, handler, `{"name":"First","template_id":"roster-template"}`); w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	before := len(handler.agentStore.ListAgents())

	w, resp := postCreateWorkspace(t, handler, `{"name":"Second","template_id":"roster-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("second create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if after := len(handler.agentStore.ListAgents()); after != before {
		t.Fatalf("agent count went %d -> %d; the second create must reuse, not duplicate", before, after)
	}

	// Nor may the second workspace attach the same agent more than once.
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	seen := map[string]bool{}
	for _, inst := range sessWS.AgentInstances {
		if seen[inst.Name] {
			t.Fatalf("agent %q attached more than once to the same workspace", inst.Name)
		}
		seen[inst.Name] = true
	}
}

// TestCreateWorkspaceLegacyCallerWithoutExistingAgentNames covers FR76 and the
// compatibility note: a request that omits existing_agent_names keeps the old
// entry-agent behavior, and the new roster-collision check must not fire for it.
func TestCreateWorkspaceLegacyCallerWithoutExistingAgentNames(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeRosterTemplate(t, handler, "roster-template", `{
		"name":"Roster Template",
		"agents":[{"name":"Campaign Lead","role":"orchestrator"}]
	}`)

	body := `{
		"name":"Legacy",
		"template_id":"roster-template",
		"template_agent_overrides":[{"index":0,"name":"Renamed Lead"}]
	}`
	w, _ := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for a legacy caller, got %d: %s", w.Code, w.Body.String())
	}
	assertAgentPresent(t, handler, "Renamed Lead")
	assertAgentAbsent(t, handler, "Campaign Lead")
}

// TestCreateWorkspaceAllowsBlueprintAgentAlreadySelectedAsSavedAgent guards the
// precision of the FR70 collision check. A blueprint entry that shares a name
// with a selected saved agent is ordinary reuse — attached once, on purpose —
// and must NOT be rejected. Only a rename ONTO another member is a conflict.
func TestCreateWorkspaceAllowsBlueprintAgentAlreadySelectedAsSavedAgent(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Research Scout", &agentstore.CreateAgentConfig{}); err != nil {
		t.Fatalf("seed saved agent: %v", err)
	}
	writeRosterTemplate(t, handler, "shadow-template", `{
		"name":"Shadow Template",
		"agents":[{"name":"Research Scout","role":"orchestrator"}]
	}`)

	body := `{
		"name":"Shadowed",
		"template_id":"shadow-template",
		"existing_agent_names":["Research Scout"]
	}`
	w, resp := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for the ordinary reuse case, got %d: %s", w.Code, w.Body.String())
	}

	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	matches := 0
	for _, inst := range sessWS.AgentInstances {
		if inst.Name == "Research Scout" {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("Research Scout attached %d times, want exactly 1", matches)
	}
}

// TestCreateWorkspaceRejectsRosterNameCollidingWithSavedSelection covers the
// FR70 gap the client currently guards alone: validateTemplateAgentOverrideNames
// only checks blueprint specs against each other, so a customized copy renamed
// onto a selected saved agent would previously slip through to seeding.
func TestCreateWorkspaceRejectsRosterNameCollidingWithSavedSelection(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Research Scout", &agentstore.CreateAgentConfig{}); err != nil {
		t.Fatalf("seed saved agent: %v", err)
	}
	writeRosterTemplate(t, handler, "collide-template", `{
		"name":"Collide Template",
		"agents":[{"name":"Blueprint Lead","role":"orchestrator"}]
	}`)

	body := `{
		"name":"Launch",
		"template_id":"collide-template",
		"existing_agent_names":["Research Scout"],
		"template_agent_overrides":[{"index":0,"name":"Research Scout"}]
	}`
	w, _ := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when a blueprint agent is renamed onto a selected saved agent, got %d: %s",
			w.Code, w.Body.String())
	}
}
