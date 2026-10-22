package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeAssistantReader struct {
	state personalassistant.APIState
	err   error
}

func (f *fakeAssistantReader) Get(context.Context, string) (*personalassistant.Projection, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &personalassistant.Projection{State: f.state}, nil
}

// failingSaveStore refuses every workspace write, as a full disk would.
type failingSaveStore struct{ workspace.Store }

func (failingSaveStore) Save(*workspace.Workspace) error { return errors.New("disk full") }

// failingCreateRecords refuses to record a hire.
type failingCreateRecords struct {
	personalassistant.HireRecordStore
}

func (failingCreateRecords) Create(context.Context, personalassistant.HireRecord) (personalassistant.HireRecord, error) {
	return personalassistant.HireRecord{}, errors.New("database is locked")
}

type hireFixture struct {
	h          *Handler
	agents     store.Store
	workspaces workspace.Store
	records    *personalassistant.SQLiteHireRecords
	assistant  *fakeAssistantReader
}

func newHireFixture(t *testing.T) *hireFixture {
	t.Helper()
	dir := t.TempDir()
	agents, err := store.NewFileStore(filepath.Join(dir, "agents_index.json"), types.Settings{Model: "default-model", Temperature: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := workspace.NewFileStore(filepath.Join(dir, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(dir, "ori.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &hireFixture{
		h: New(agents), agents: agents, workspaces: workspaces,
		records:   personalassistant.NewSQLiteHireRecords(db),
		assistant: &fakeAssistantReader{state: personalassistant.APIStateActive},
	}
	f.h.SetWorkspaceStore(workspaces)
	f.h.SetPersonalAssistantSupport(f.assistant, nil)
	f.h.SetHireRecords(f.records)
	return f
}

func (f *hireFixture) hire(t *testing.T, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	f.h.HandleHire(rr, httptest.NewRequest(http.MethodPost, "/api/agents/hire", strings.NewReader(body)))
	payload := map[string]any{}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON (%d): %s", rr.Code, rr.Body.String())
	}
	return rr, payload
}

func (f *hireFixture) workspaceCount(t *testing.T) int {
	t.Helper()
	ids, err := f.workspaces.List()
	if err != nil {
		t.Fatal(err)
	}
	return len(ids)
}

func TestHire_CreatesTheAgentAndABlankWorkspaceWithTheBriefAsGoal(t *testing.T) {
	f := newHireFixture(t)
	rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita",
		"brief":"Check the release notes for mistakes before Friday.","workspace_name":"Release Check"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}

	ag, ok := f.agents.GetAgent("Rita")
	if !ok || ag.Role != types.RoleValidator {
		t.Fatalf("agent = %+v, %v; want a Reviewer named Rita", ag, ok)
	}
	if ag.Appearance == nil {
		t.Fatal("the hired agent has no appearance; the hire applies one as the Advanced form does")
	}

	ws, err := f.workspaces.Get(got["workspace_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "Release Check" || ws.Mission != "Check the release notes for mistakes before Friday." {
		t.Fatalf("workspace %q goal %q", ws.Name, ws.Mission)
	}
	if members := ws.AgentNames(); len(members) != 1 || members[0] != "Rita" || ws.EntryAgentName() != "Rita" {
		t.Fatalf("members %v entry %q; want Rita alone, as entry agent", members, ws.EntryAgentName())
	}
	if !ws.GoalFirstRunPending {
		t.Fatal("the goal's first run is not armed")
	}
	if got["workspace_route"] != "/workspaces/"+ws.FolderSlug || got["agent_name"] != "Rita" || got["blueprint"] != "blank" {
		t.Fatalf("response %v", got)
	}

	record, err := f.records.Get(t.Context(), got["hire_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if record.State != personalassistant.HireStateLooking || record.WorkspaceID != ws.ID || record.AgentName != "Rita" ||
		record.RequestID != "req-1" || record.UserID != "local" || record.Role != "validator" || record.RunID != "" {
		t.Fatalf("record %+v", record)
	}
}

func TestHire_DefaultsTheBriefAndDerivesAFreeWorkspaceName(t *testing.T) {
	f := newHireFixture(t)
	rr, first := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita","brief":"  "}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if first["brief"] != "Check the work here for mistakes and flag problems before they ship." || first["workspace_name"] != "Check the Work Here" {
		t.Fatalf("defaults: %v", first)
	}
	rr, second := f.hire(t, `{"request_id":"req-2","role":"validator","name":"Rafa"}`)
	if rr.Code != http.StatusCreated || second["workspace_name"] != "Check the Work Here 2" {
		t.Fatalf("second hire (%d): %v", rr.Code, second)
	}
}

func TestHire_SpecialistDefaultBriefNamesItsDomain(t *testing.T) {
	f := newHireFixture(t)
	rr, got := f.hire(t, `{"request_id":"req-1","role":"specialist","name":"Mix","domain":"mixing"}`)
	if rr.Code != http.StatusCreated || got["brief"] != "Act as the mixing expert for this workspace." {
		t.Fatalf("(%d) %v", rr.Code, got)
	}
	ag, _ := f.agents.GetAgent("Mix")
	if ag.Metadata == nil || ag.Metadata.RoutingProfile == nil || len(ag.Metadata.RoutingProfile.Domains) != 1 {
		t.Fatalf("the domain is not on the agent as catalog create puts it: %+v", ag.Metadata)
	}
}

func TestHire_RepeatedRequestIDReturnsTheFirstHire(t *testing.T) {
	f := newHireFixture(t)
	body := `{"request_id":"req-1","role":"validator","name":"Rita","brief":"Check it."}`
	rr, first := f.hire(t, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	rr, again := f.hire(t, body)
	if rr.Code != http.StatusOK || again["replayed"] != true || again["hire_id"] != first["hire_id"] ||
		again["workspace_id"] != first["workspace_id"] || again["workspace_route"] != first["workspace_route"] {
		t.Fatalf("replay (%d): %v; first %v", rr.Code, again, first)
	}
	if n := f.workspaceCount(t); n != 1 {
		t.Fatalf("%d workspaces after a replay, want 1", n)
	}
}

func TestHire_RefusesARequestIDAnotherUserUsed(t *testing.T) {
	f := newHireFixture(t)
	if _, err := f.records.Create(t.Context(), personalassistant.HireRecord{
		ID: "hire-x", UserID: "someone-else", RequestID: "req-1", AgentName: "Other", WorkspaceID: "ws-x",
		Brief: "Theirs.", Role: "validator", Blueprint: "blank", State: personalassistant.HireStateLooking,
	}); err != nil {
		t.Fatal(err)
	}
	rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita","brief":"Mine."}`)
	if rr.Code != http.StatusConflict || got["error"] != "request_id_conflict" {
		t.Fatalf("(%d) %v", rr.Code, got)
	}
	if _, ok := f.agents.GetAgent("Rita"); ok {
		t.Fatal("an agent was created for a refused request")
	}
}

func TestHire_RequiresAHiredPersonalAssistant(t *testing.T) {
	for _, tc := range []struct {
		state personalassistant.APIState
		ok    bool
	}{
		{personalassistant.APIStateNeedsHire, false},
		{personalassistant.APIStateHiring, false},
		{personalassistant.APIStateRepairNeeded, false},
		{personalassistant.APIStateNeedsHQ, true},
		{personalassistant.APIStateProvisioningHQ, true},
		{personalassistant.APIStateActive, true},
		{personalassistant.APIStatePaused, true},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			f := newHireFixture(t)
			f.assistant.state = tc.state
			rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita","brief":"Check it."}`)
			if tc.ok != (rr.Code == http.StatusCreated) {
				t.Fatalf("(%d) %v", rr.Code, got)
			}
			if !tc.ok && (rr.Code != http.StatusConflict || got["error"] != "assistant_required" || f.workspaceCount(t) != 0) {
				t.Fatalf("refusal (%d) %v", rr.Code, got)
			}
		})
	}
	t.Run("no assistant wired", func(t *testing.T) {
		f := newHireFixture(t)
		f.h.SetPersonalAssistantSupport(nil, nil)
		if rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita"}`); rr.Code != http.StatusConflict {
			t.Fatalf("(%d) %v", rr.Code, got)
		}
	})
}

func TestHire_RefusesInvalidRequestsAndCreatesNothing(t *testing.T) {
	long := strings.Repeat("word ", 61)
	for name, tc := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"no request id":       {`{"role":"validator","name":"Rita"}`, http.StatusBadRequest, "invalid_hire"},
		"unknown role":        {`{"request_id":"r","role":"wizard","name":"Rita"}`, http.StatusBadRequest, "invalid_hire"},
		"bad name":            {`{"request_id":"r","role":"validator","name":"Rita/../x"}`, http.StatusBadRequest, "invalid_hire"},
		"reserved name":       {`{"request_id":"r","role":"validator","name":"hire"}`, http.StatusBadRequest, "invalid_hire"},
		"brief too long":      {`{"request_id":"r","role":"validator","name":"Rita","brief":"` + long + `"}`, http.StatusBadRequest, "invalid_hire"},
		"workspace name long": {`{"request_id":"r","role":"validator","name":"Rita","workspace_name":"` + strings.Repeat("n", 81) + `"}`, http.StatusBadRequest, "invalid_hire"},
		"name taken":          {`{"request_id":"r","role":"validator","name":"Keep"}`, http.StatusConflict, "agent_exists"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newHireFixture(t)
			if err := f.agents.CreateAgent("Keep", &store.CreateAgentConfig{Role: types.RoleResearcher}); err != nil {
				t.Fatal(err)
			}
			rr, got := f.hire(t, tc.body)
			if rr.Code != tc.status || got["error"] != tc.code {
				t.Fatalf("(%d) %v; want %d %s", rr.Code, got, tc.status, tc.code)
			}
			if names := f.agents.ListAgents(); len(names) != 1 || f.workspaceCount(t) != 0 {
				t.Fatalf("a refused hire left agents %v and %d workspaces", names, f.workspaceCount(t))
			}
			if keep, ok := f.agents.GetAgent("Keep"); !ok || keep.Role != types.RoleResearcher {
				t.Fatal("the existing agent changed")
			}
		})
	}
}

func TestHire_WorkspaceFailureRemovesTheAgentItCreated(t *testing.T) {
	f := newHireFixture(t)
	f.h.SetWorkspaceStore(failingSaveStore{f.workspaces})
	rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita","brief":"Check it."}`)
	if rr.Code != http.StatusInternalServerError || got["step"] != "workspace" || got["error"] != "hire_failed" {
		t.Fatalf("(%d) %v", rr.Code, got)
	}
	if _, ok := f.agents.GetAgent("Rita"); ok {
		t.Fatal("the agent outlived its failed hire")
	}
	if _, err := f.records.ByRequestID(t.Context(), "req-1"); !errors.Is(err, personalassistant.ErrHireNotFound) {
		t.Fatalf("a failed hire was recorded: %v", err)
	}

	// Try again with the same request id once the disk has room: it hires.
	f.h.SetWorkspaceStore(f.workspaces)
	if rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita","brief":"Check it."}`); rr.Code != http.StatusCreated {
		t.Fatalf("retry (%d) %v", rr.Code, got)
	}
}

func TestHire_RecordFailureRemovesTheWorkspaceAndTheAgent(t *testing.T) {
	f := newHireFixture(t)
	f.h.SetHireRecords(failingCreateRecords{f.records})
	rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita","brief":"Check it."}`)
	if rr.Code != http.StatusInternalServerError || got["step"] != "record" {
		t.Fatalf("(%d) %v", rr.Code, got)
	}
	if _, ok := f.agents.GetAgent("Rita"); ok {
		t.Fatal("the agent outlived its failed hire")
	}
	if n := f.workspaceCount(t); n != 0 {
		t.Fatalf("%d workspaces outlived the failed hire", n)
	}
}

func TestHire_UnwiredRecordsAnswerUnavailable(t *testing.T) {
	f := newHireFixture(t)
	f.h.hireRecords = nil
	if rr, got := f.hire(t, `{"request_id":"req-1","role":"validator","name":"Rita"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("(%d) %v", rr.Code, got)
	}
	rr := httptest.NewRecorder()
	f.h.HandleHire(rr, httptest.NewRequest(http.MethodGet, "/api/agents/hire", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET answered %d", rr.Code)
	}
}

func TestCatalog_CarriesWhatTheHireCardNeedsPerRole(t *testing.T) {
	rr := httptest.NewRecorder()
	CatalogHandler(rr, httptest.NewRequest(http.MethodGet, "/api/agents/catalog", nil))
	var payload struct {
		Entries []struct {
			Slug string `json:"slug"`
		} `json:"entries"`
		Hire struct {
			Roles map[string]struct {
				DefaultBrief   string `json:"default_brief"`
				Blueprint      string `json:"blueprint"`
				BlueprintLabel string `json:"blueprint_label"`
			} `json:"roles"`
			DomainBrief      string `json:"domain_brief"`
			BriefMax         int    `json:"brief_max"`
			WorkspaceNameMax int    `json:"workspace_name_max"`
		} `json:"hire"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Entries) == 0 || len(payload.Hire.Roles) != len(payload.Entries) {
		t.Fatalf("%d roles for %d entries", len(payload.Hire.Roles), len(payload.Entries))
	}
	for _, entry := range payload.Entries {
		role := payload.Hire.Roles[entry.Slug]
		if role.DefaultBrief != personalassistant.HireDefaultBrief(types.AgentRole(entry.Slug), "") ||
			role.Blueprint == "" || role.BlueprintLabel == "" {
			t.Errorf("%s: %+v", entry.Slug, role)
		}
	}
	if payload.Hire.DomainBrief != "Act as the {domain} expert for this workspace." ||
		payload.Hire.BriefMax != 300 || payload.Hire.WorkspaceNameMax != 80 {
		t.Fatalf("hire block %+v", payload.Hire)
	}
}

func TestCreate_RefusesTheNameTheHireRouteAnswers(t *testing.T) {
	h := catalogTestHandler(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(`{"name":"Hire"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("an agent named Hire was created (%d)", rr.Code)
	}
}
