package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

const openHomeID = "music-home"

var openDigest = strings.Repeat("a", 64)

// fakeOpener stands in for the library's reviewed creator: rows, their
// eligibility, and an Activate that makes a linked song the way the creator does.
type fakeOpener struct {
	mu          sync.Mutex
	store       agentworkspace.Store
	rows        map[string]projectlibrary.ActivationEligibility
	activations []string
	keys        []string
	activateErr error
}

func (f *fakeOpener) Exists(_ projectlibrary.Scope, entryID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.rows[entryID]
	return ok, nil
}

func (f *fakeOpener) Eligibility(_ context.Context, _ projectlibrary.Scope, entryID string) (projectlibrary.ActivationEligibility, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[entryID], nil
}

func (f *fakeOpener) Activate(_ context.Context, _ projectlibrary.Scope, entryID, selectedFile, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, key)
	if f.activateErr != nil {
		return "", f.activateErr
	}
	f.activations = append(f.activations, entryID+":"+selectedFile)
	id := "song-" + entryID
	song := &agentworkspace.Workspace{ID: id, Name: "Song " + entryID, FolderSlug: id, OwnerUserID: "local",
		Status: agentworkspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	song.SetAssistantProjectLink(&agentworkspace.AssistantProjectLink{
		StationWorkspaceID: openHomeID, ProjectProvider: openProvider(),
		ProjectRoles: []agentworkspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant",
			Scope: agentworkspace.AssistantRoleScopeProject, Required: true, Primary: true}},
	})
	if err := f.store.Save(song); err != nil {
		return "", err
	}
	// The creator has linked it: the next read says so.
	row := f.rows[entryID]
	row.State, row.WorkspaceID = "connected", id
	f.rows[entryID] = row
	return id, nil
}

func (f *fakeOpener) Format(projectlibrary.Scope, string) string { return "reaper" }

func openProvider() *agentworkspace.AssistantProjectProviderOwner {
	return &agentworkspace.AssistantProjectProviderOwner{
		PluginID: "reaper-plugin", PluginVersion: "0.9.0", BlueprintID: "reaper-song", BlueprintVersion: 1,
		ProjectTeamID: "team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: openDigest,
		PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64),
	}
}

type openAgents struct {
	mu    sync.Mutex
	names map[string]bool
}

func (a *openAgents) ListAgents() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var names []string
	for name := range a.names {
		names = append(names, name)
	}
	return names
}

func (a *openAgents) GetAgent(name string) (*agent.Agent, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.names[name] {
		return &agent.Agent{}, true
	}
	return nil, false
}

type openFixture struct {
	handler  *Handler
	store    agentworkspace.Store
	opener   *fakeOpener
	agents   *openAgents
	staffed  []RoleStaffingFill
	staffs   *projectstaffing.Service
	fileOnly []string
}

func newOpenFixture(t *testing.T) *openFixture {
	t.Helper()
	store := agentworkspace.NewInMemoryStore()
	home := &agentworkspace.Workspace{ID: openHomeID, Name: "Music Production Home", OwnerUserID: "local",
		Status: agentworkspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&agentworkspace.AssistantProgramState{
		SchemaVersion: agentworkspace.AssistantProgramStateSchemaVersion, PluginAvailable: true,
		Key: agentworkspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
	})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	agents := &openAgents{names: map[string]bool{}}
	f := &openFixture{store: store, agents: agents}
	f.opener = &fakeOpener{store: store, rows: map[string]projectlibrary.ActivationEligibility{
		"two":     {State: "review_available", ProjectFiles: []string{"Song 002.rpp"}},
		"three":   {State: "review_available", ProjectFiles: []string{"Song 003.rpp"}},
		"one":     {State: "file_choice_required", ProjectFiles: []string{"Song 001.rpp", "Song 001 alt.rpp"}},
		"ableton": {State: "project_provider_unavailable", Reason: "A compatible installed project integration is required."},
	}}
	f.staffs = projectstaffing.New(store, agents)
	f.handler = &Handler{
		workspaceTaskStore: store, currentUserID: func(context.Context) (string, error) { return "local", nil },
		projectStaffing: f.staffs, staffingModelReady: func() bool { return true },
		libraryOpenerOverride: f.opener,
	}
	f.handler.selectFileOnly = func(_ context.Context, songID string) error {
		f.fileOnly = append(f.fileOnly, songID)
		return nil
	}
	// The staffing seam: a create makes the agent, then the role is bound in the
	// song exactly as the staffing adapter records it.
	f.handler.assistantWorkspaceRoleStaffer = func(_ context.Context, songID string, fills []RoleStaffingFill) error {
		f.staffed = append(f.staffed, fills...)
		for _, fill := range fills {
			source := agentworkspace.RoleSourceAssigned
			if fill.Mode != "assign" {
				agents.mu.Lock()
				agents.names[fill.Name] = true
				agents.mu.Unlock()
				source = agentworkspace.RoleSourceCreated
			}
			if err := store.Update(songID, func(song *agentworkspace.Workspace) error {
				song.AgentInstances = append(song.AgentInstances, agentworkspace.AgentInstance{ID: "i-" + songID, Name: fill.Name, RoleSource: source, EntryPoint: true})
				link := song.GetAssistantProjectLink()
				link.ProjectBindings.Bindings = append(link.ProjectBindings.Bindings, agentworkspace.AssistantRoleBinding{RoleID: fill.RoleID, AgentInstanceID: "i-" + songID, AgentName: fill.Name})
				song.SetAssistantProjectLink(link)
				return song.SetEntryAgentName(fill.Name)
			}); err != nil {
				return err
			}
		}
		return nil
	}
	return f
}

func (f *openFixture) grant(t *testing.T) {
	t.Helper()
	if _, err := f.staffs.Consents().Grant(openHomeID, agentworkspace.ProjectStaffingConsentGrant{
		Source: agentworkspace.ProjectStaffingConsentFolderOffer, PluginID: "reaper-plugin", BlueprintID: "reaper-song",
		TeamDigest: openDigest, RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *openFixture) open(t *testing.T, homeID, entryID, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+homeID+"/assistant-program/library/projects/"+entryID+"/open", bytes.NewBufferString(body))
	request.SetPathValue("workspaceID", homeID)
	request.SetPathValue("entryID", entryID)
	recorder := httptest.NewRecorder()
	f.handler.OpenAssistantLibraryProject(recorder, request)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

func TestOpenLibrarySong_CreatesTheWorkspaceAndTheSharedAssistantThenJoinsIt(t *testing.T) {
	f := newOpenFixture(t)
	f.grant(t)
	code, first := f.open(t, openHomeID, "two", `{"request_id":"r2"}`)
	if code != http.StatusOK || first["created"] != true || first["staffing"] != "added" ||
		first["agent_name"] != "REAPER Assistant" || first["route"] != "/workspaces/song-two" || first["first_task"] != true {
		t.Fatalf("first open = %d %v", code, first)
	}
	code, second := f.open(t, openHomeID, "three", `{"request_id":"r3"}`)
	if code != http.StatusOK || second["staffing"] != "joined" || second["agent_name"] != "REAPER Assistant" {
		t.Fatalf("second open = %d %v", code, second)
	}
	if len(f.staffed) != 2 || f.staffed[0].Mode != "create" || f.staffed[1].Mode != "assign" {
		t.Fatalf("staffing = %+v", f.staffed)
	}
	// Each opened song works File-only, so opening asks no mode question (D5).
	if strings.Join(f.fileOnly, ",") != "song-two,song-three" {
		t.Fatalf("File-only recorded for %v", f.fileOnly)
	}
	// The activation commit is keyed by the request, so a retry replays it.
	if f.opener.keys[0] != "open:r2" {
		t.Fatalf("activation keys = %v", f.opener.keys)
	}
	consent, _ := f.staffs.Consents().Read(openHomeID)
	if consent.Roles[0].AgentName != "REAPER Assistant" {
		t.Fatalf("the created assistant was not recorded: %+v", consent.Roles[0])
	}
	// The first task is seeded, assigned to the assistant, and has not started.
	song, _ := f.store.Get("song-two")
	found := false
	for i := range song.Tasks {
		if isFolderFirstTask(&song.Tasks[i]) {
			found = true
			if song.Tasks[i].Status != agentworkspace.TaskStatusPending {
				t.Fatalf("the first task started on its own: %+v", song.Tasks[i])
			}
		}
	}
	if !found {
		t.Fatalf("no first task in %+v", song.Tasks)
	}
}

func TestOpenLibrarySong_SeveralProjectFilesAskWhichOne(t *testing.T) {
	f := newOpenFixture(t)
	f.grant(t)
	code, payload := f.open(t, openHomeID, "one", `{"request_id":"r1"}`)
	if code != http.StatusConflict || payload["reason"] != "needs_choice" || payload["needs_choice"] != true {
		t.Fatalf("no choice = %d %v", code, payload)
	}
	if files, _ := payload["project_files"].([]any); len(files) != 2 {
		t.Fatalf("files = %v", payload["project_files"])
	}
	if len(f.opener.activations) != 0 {
		t.Fatal("a question made a workspace")
	}
	// A name that was not offered is refused; an offered one opens.
	if code, _ := f.open(t, openHomeID, "one", `{"request_id":"r1","selected_file":"Other.rpp"}`); code != http.StatusBadRequest {
		t.Fatalf("unoffered file = %d", code)
	}
	code, payload = f.open(t, openHomeID, "one", `{"request_id":"r1","selected_file":"Song 001 alt.rpp"}`)
	if code != http.StatusOK || f.opener.activations[0] != "one:Song 001 alt.rpp" {
		t.Fatalf("chosen file = %d %v %v", code, payload, f.opener.activations)
	}
}

// A retry after a half-finished open (the workspace exists, its role is empty)
// finishes the staffing and never makes a second workspace.
func TestOpenLibrarySong_ARetryFinishesAHalfFinishedOpen(t *testing.T) {
	f := newOpenFixture(t)
	f.grant(t)
	staffer := f.handler.assistantWorkspaceRoleStaffer
	f.handler.assistantWorkspaceRoleStaffer = func(context.Context, string, []RoleStaffingFill) error {
		return errors.New("crashed before the role was filled")
	}
	if code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`); code != http.StatusOK || payload["staffing"] != "none" {
		t.Fatalf("half-finished = %d %v", code, payload)
	}
	f.handler.assistantWorkspaceRoleStaffer = staffer
	code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`)
	if code != http.StatusOK || payload["created"] != false || payload["staffing"] != "added" || payload["workspace_id"] != "song-two" {
		t.Fatalf("retry = %d %v", code, payload)
	}
	if len(f.opener.activations) != 1 {
		t.Fatalf("a second workspace was made: %v", f.opener.activations)
	}
	// Opening an already-open, staffed song just goes there.
	code, payload = f.open(t, openHomeID, "two", `{"request_id":"again"}`)
	if code != http.StatusOK || payload["staffing"] != "already" || len(f.opener.activations) != 1 {
		t.Fatalf("already open = %d %v", code, payload)
	}
}

func TestOpenLibrarySong_RefusesWhatItMayNotDo(t *testing.T) {
	f := newOpenFixture(t)
	f.grant(t)
	foreign := &agentworkspace.Workspace{ID: "their-home", Name: "Theirs", OwnerUserID: "someone-else", Status: agentworkspace.StatusActive}
	foreign.SetAssistantProgramState(&agentworkspace.AssistantProgramState{SchemaVersion: agentworkspace.AssistantProgramStateSchemaVersion,
		Key: agentworkspace.AssistantProgramKey{OwnerUserID: "someone-else", PluginID: "p", ProgramID: "q"}})
	if err := f.store.Save(foreign); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, home, entry, body string
		code                    int
	}{
		{"another owner's Home", "their-home", "two", `{"request_id":"x"}`, http.StatusNotFound},
		{"a row that does not exist", openHomeID, "missing", `{"request_id":"x"}`, http.StatusNotFound},
		{"a path in the body", openHomeID, "two", `{"request_id":"x","path":"/Users/me/Songs"}`, http.StatusBadRequest},
		{"a folder in the body", openHomeID, "two", `{"request_id":"x","folder":"Songs"}`, http.StatusBadRequest},
		{"no request id", openHomeID, "two", `{}`, http.StatusBadRequest},
		{"a path as the file", openHomeID, "one", `{"request_id":"x","selected_file":"../Song 001.rpp"}`, http.StatusBadRequest},
		{"the integration is missing", openHomeID, "ableton", `{"request_id":"x"}`, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, payload := f.open(t, tc.home, tc.entry, tc.body); code != tc.code {
				t.Fatalf("code = %d (%v), want %d", code, payload, tc.code)
			}
		})
	}
	if len(f.opener.activations) != 0 {
		t.Fatalf("a refused open made a workspace: %v", f.opener.activations)
	}
}

// D7: with no consent, or the consent switched off, the song still opens,
// File-only, with no agent; the first task waits unassigned.
func TestOpenLibrarySong_WithoutAConsentAddsNoAgent(t *testing.T) {
	f := newOpenFixture(t)
	code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`)
	if code != http.StatusOK || payload["staffing"] != "none" || payload["created"] != true || len(f.staffed) != 0 {
		t.Fatalf("no consent = %d %v", code, payload)
	}
	f.grant(t)
	if _, err := f.staffs.Consents().Revoke(openHomeID); err != nil {
		t.Fatal(err)
	}
	code, payload = f.open(t, openHomeID, "three", `{"request_id":"r3"}`)
	if code != http.StatusOK || payload["staffing"] != "off" || len(f.staffed) != 0 {
		t.Fatalf("switched off = %d %v", code, payload)
	}
	song, _ := f.store.Get("song-three")
	if len(song.GetAgentInstances()) != 0 {
		t.Fatalf("an agent was added with the switch off: %+v", song.GetAgentInstances())
	}
}

// D8: a song already staffed with its own per-song agent keeps it.
func TestOpenLibrarySong_LeavesASongsOwnAgentAlone(t *testing.T) {
	f := newOpenFixture(t)
	f.grant(t)
	if _, err := f.opener.Activate(context.Background(), projectlibrary.Scope{}, "two", "", "earlier"); err != nil {
		t.Fatal(err)
	}
	if err := f.handler.assistantWorkspaceRoleStaffer(context.Background(), "song-two", []RoleStaffingFill{{RoleID: "reaper-assistant", Mode: "create", Name: "REAPER Assistant · Song 002"}}); err != nil {
		t.Fatal(err)
	}
	f.staffed = nil
	code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`)
	if code != http.StatusOK || payload["staffing"] != "already" || payload["agent_name"] != "REAPER Assistant · Song 002" || len(f.staffed) != 0 {
		t.Fatalf("own agent = %d %v staffed=%v", code, payload, f.staffed)
	}
	consent, _ := f.staffs.Consents().Read(openHomeID)
	if consent.Roles[0].AgentName != "" {
		t.Fatalf("a per-song agent was adopted as the shared one: %+v", consent.Roles[0])
	}
}

// needs_model: opening a song that would create the assistant without a model
// makes no workspace at all.
func TestOpenLibrarySong_NeedsAModelBeforeMakingAHalfStaffedSong(t *testing.T) {
	f := newOpenFixture(t)
	f.grant(t)
	ready := false
	f.handler.staffingModelReady = func() bool { return ready }
	team := projectlibrary.ProjectTeam{PluginID: "reaper-plugin", BlueprintID: "reaper-song", Digest: openDigest,
		Roles: []agentworkspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant", Required: true}}}
	f.handler.projectTeamOverride = func(projectlibrary.Scope, *agentworkspace.Workspace) (projectlibrary.ProjectTeam, bool) {
		return team, true
	}
	code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`)
	if code != http.StatusConflict || payload["reason"] != "needs_model" || len(f.opener.activations) != 0 {
		t.Fatalf("no model = %d %v activations=%v", code, payload, f.opener.activations)
	}
	// Once there is a model the same request finishes.
	ready = true
	if code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`); code != http.StatusOK || payload["staffing"] != "added" {
		t.Fatalf("with a model = %d %v", code, payload)
	}
	// A bind needs no new model: the shared assistant brings its own.
	ready = false
	if code, payload := f.open(t, openHomeID, "three", `{"request_id":"r3"}`); code != http.StatusOK || payload["staffing"] != "joined" {
		t.Fatalf("bind without a system model = %d %v", code, payload)
	}
	// D6: a plugin update changed the team: Open stops, asks on the Home, and
	// makes nothing.
	team.Digest = strings.Repeat("c", 64)
	f.opener.rows["four"] = projectlibrary.ActivationEligibility{State: "review_available", ProjectFiles: []string{"Song 004.rpp"}}
	before := len(f.opener.activations)
	code, payload = f.open(t, openHomeID, "four", `{"request_id":"r4"}`)
	if code != http.StatusConflict || payload["reason"] != "consent_stale" || len(f.opener.activations) != before {
		t.Fatalf("stale team = %d %v", code, payload)
	}
}
