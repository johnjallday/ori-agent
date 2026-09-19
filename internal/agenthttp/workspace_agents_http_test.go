package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type staticWorkspaceSource []store.WorkspaceAgentEntry

func (s staticWorkspaceSource) WorkspaceAgents() []store.WorkspaceAgentEntry { return s }

// rosterWithWorkspaceAgent is a composite with one root agent (Scout, also
// customised in the Studio workspace) and one agent only Studio holds.
func rosterWithWorkspaceAgent(t *testing.T) (*store.CompositeStore, string) {
	t.Helper()
	root := t.TempDir()
	c := compositeForRoot(t, root)
	if err := c.CreateAgent("Scout", &store.CreateAgentConfig{SystemPrompt: "my scout"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	c.SetWorkspaceAgentSource(staticWorkspaceSource{
		{WorkspaceID: "ws-1", WorkspaceName: "Studio", AgentName: "Scout", Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "studio scout"}}},
		{WorkspaceID: "ws-1", WorkspaceName: "Studio", AgentName: "Stranger", Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "from studio"}}},
	})
	return c, root
}

func TestRosterListCarriesEachEntrysOrigin(t *testing.T) {
	c, _ := rosterWithWorkspaceAgent(t)
	rr := httptest.NewRecorder()
	NewDashboardHandler(c).ListAgentsWithStats(rr, httptest.NewRequest(http.MethodGet, "/api/agents/dashboard/list", nil))
	var body struct {
		Agents []AgentListItem `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	origins := map[string]*store.AgentOrigin{}
	for _, item := range body.Agents {
		origins[item.Name] = item.Origin
	}
	if len(origins) != 2 {
		t.Fatalf("roster = %v, want Scout and Stranger once each", origins)
	}
	scout := origins["Scout"]
	if scout == nil || scout.Source != store.SourceRoster || len(scout.CustomisedIn) != 1 || scout.CustomisedIn[0].Name != "Studio" {
		t.Errorf("Scout origin = %+v, want roster customised in Studio", scout)
	}
	stranger := origins["Stranger"]
	if stranger == nil || stranger.Source != store.SourceWorkspace || stranger.WorkspaceID != "ws-1" || stranger.WorkspaceName != "Studio" {
		t.Errorf("Stranger origin = %+v, want workspace ws-1 Studio", stranger)
	}
}

func TestEditingAWorkspaceOnlyAgentIsAConflict(t *testing.T) {
	c, _ := rosterWithWorkspaceAgent(t)
	rr := httptest.NewRecorder()
	New(c).ServeHTTP(rr, httptest.NewRequest(http.MethodPatch, "/api/agents/Stranger", strings.NewReader(`{"system_prompt":"changed here"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "This agent belongs to the workspace Studio. Edit it there, or add it to your agents first.") {
		t.Errorf("body = %s", rr.Body.String())
	}
	if got, _ := c.GetAgent("Stranger"); got.Settings.SystemPrompt != "from studio" {
		t.Errorf("the refused edit changed the workspace copy in memory: %q", got.Settings.SystemPrompt)
	}
}

func TestBulkSkipsAWorkspaceOnlyAgentPerItem(t *testing.T) {
	c, _ := rosterWithWorkspaceAgent(t)
	rr := httptest.NewRecorder()
	New(c).HandleBulk(rr, httptest.NewRequest(http.MethodPost, "/api/agents/bulk",
		strings.NewReader(`{"operation":"add_tags","agent_names":["Scout","Stranger"],"tags":["team"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("bulk: %d %s", rr.Code, rr.Body.String())
	}
	var body bulkResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	results := map[string]bulkResult{}
	for _, r := range body.Results {
		results[r.Name] = r
	}
	if results["Scout"].Status != bulkStatusSucceeded {
		t.Errorf("Scout: %+v", results["Scout"])
	}
	if results["Stranger"].Status != bulkStatusSkipped || results["Stranger"].ReasonCode != reasonWorkspaceOwned {
		t.Errorf("Stranger: %+v, want skipped as workspace_owned_agent", results["Stranger"])
	}
}

func TestAnUnreadableAgentIsListedWithItsFileAndCannotBeEdited(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Agents", "Broken")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "agent_settings.json")
	if err := os.WriteFile(file, []byte(`{"Settings":`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := compositeForRoot(t, root)

	rr := httptest.NewRecorder()
	NewDashboardHandler(c).ListAgentsWithStats(rr, httptest.NewRequest(http.MethodGet, "/api/agents/dashboard/list", nil))
	var body struct {
		Agents []AgentListItem `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Agents) != 1 || body.Agents[0].State != "unreadable" || body.Agents[0].File != file {
		t.Fatalf("list = %+v, want Broken as unreadable naming %s", body.Agents, file)
	}

	rr = httptest.NewRecorder()
	New(c).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/agents?name=Broken", nil))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "could not be read") {
		t.Fatalf("delete: %d %s, want 409 naming the file", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the unreadable file was removed: %v", err)
	}
}

func TestCreatingAnAgentOnTheAgentsPageWritesARootAgent(t *testing.T) {
	root := t.TempDir()
	c := compositeForRoot(t, root)
	rr := httptest.NewRecorder()
	New(c).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(`{"name":"Quill","system_prompt":"You write."}`)))
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Agents", "Quill", "agent_settings.json")); err != nil {
		t.Fatalf("Quill was not written to the root: %v", err)
	}
	if origin, _ := c.AgentOrigin("Quill"); origin.Source != store.SourceRoster {
		t.Errorf("origin = %+v, want roster", origin)
	}
}

func TestAddFromWorkspaceMakesItOneOfYourAgents(t *testing.T) {
	c, root := rosterWithWorkspaceAgent(t)
	h := New(c)
	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.HandleAddFromWorkspace(rr, httptest.NewRequest(http.MethodPost, "/api/agents/add-from-workspace", strings.NewReader(body)))
		return rr
	}

	rr := post(`{"workspace_id":"ws-1","name":"Stranger"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Agents", "Stranger", "agent_settings.json")); err != nil {
		t.Errorf("the agent was not written to the root: %v", err)
	}
	if origin, _ := c.AgentOrigin("Stranger"); origin.Source != store.SourceRoster {
		t.Errorf("after adding, origin = %+v", origin)
	}

	if rr := post(`{"workspace_id":"ws-1","name":"Stranger"}`); rr.Code != http.StatusConflict {
		t.Errorf("adding twice: %d %s, want 409", rr.Code, rr.Body.String())
	}
	if rr := post(`{"workspace_id":"ws-1","name":"Nobody"}`); rr.Code != http.StatusNotFound {
		t.Errorf("unknown agent: %d, want 404", rr.Code)
	}
	if rr := post(`{"name":"Stranger"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("missing workspace_id: %d, want 400", rr.Code)
	}
}
