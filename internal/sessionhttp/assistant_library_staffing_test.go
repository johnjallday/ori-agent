package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// library-setup-staffing: the staffing-only finish a confirmed setup runs after
// each song is connected, and its inert reads.

func newStaffingFixture(t *testing.T, songs ...string) *openFixture {
	t.Helper()
	f := newOpenFixture(t)
	f.handler.staffingProviderOverride = func(projectlibrary.Scope, *agentworkspace.Workspace, *agentworkspace.Workspace) bool { return true }
	for _, entry := range songs {
		if _, ok := f.opener.rows[entry]; !ok {
			f.opener.rows[entry] = projectlibrary.ActivationEligibility{State: "review_available", ProjectFiles: []string{entry + ".rpp"}}
		}
		f.connect(t, entry)
	}
	return f
}

// connect links a song the way activation does: no agent, no staffing.
func (f *openFixture) connect(t *testing.T, entry string) {
	t.Helper()
	if _, err := f.opener.Activate(context.Background(), projectlibrary.Scope{}, libraryActivation{EntryID: entry, Key: "connect:" + entry}); err != nil {
		t.Fatal(err)
	}
}

func (f *openFixture) finish(t *testing.T, homeID, entryID, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+homeID+"/assistant-program/library/projects/"+entryID+"/staffing", bytes.NewBufferString(body))
	request.SetPathValue("workspaceID", homeID)
	request.SetPathValue("entryID", entryID)
	recorder := httptest.NewRecorder()
	f.handler.FinishAssistantLibraryProjectStaffing(recorder, request)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

func (f *openFixture) read(t *testing.T, path string, handle func(http.ResponseWriter, *http.Request), entryID string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+openHomeID+"/assistant-program/library"+path, nil)
	request.SetPathValue("workspaceID", openHomeID)
	if entryID != "" {
		request.SetPathValue("entryID", entryID)
	}
	recorder := httptest.NewRecorder()
	handle(recorder, request)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

func finishBody(id string) string {
	return `{"request_id":"` + id + `","team_digest":"` + openDigest + `"}`
}

func staffingState(payload map[string]any) string {
	staffing, _ := payload["staffing"].(map[string]any)
	state, _ := staffing["state"].(string)
	return state
}

func (f *openFixture) consentRole(t *testing.T) agentworkspace.ProjectStaffingConsentRole {
	t.Helper()
	consent, err := f.staffs.Consents().Read(openHomeID)
	if err != nil || consent == nil {
		t.Fatalf("consent = %v %v", consent, err)
	}
	return consent.Roles[0]
}

func (f *openFixture) sharedAgents() []string {
	var names []string
	for _, name := range f.agents.ListAgents() {
		if strings.HasPrefix(name, "REAPER Assistant") {
			names = append(names, name)
		}
	}
	return names
}

// Four Albums placed by activation only: the confirmed finish creates one
// shared assistant on the first and binds that same one on the other three.
func TestFinishStaffing_HiresOnceAndAssignsTheRestUnderTheStandingConsent(t *testing.T) {
	f := newStaffingFixture(t, "album-1", "album-2", "album-3", "album-4")
	f.grant(t)
	before := map[string][]agentworkspace.Task{}
	for _, entry := range []string{"album-1", "album-2", "album-3", "album-4"} {
		song, _ := f.store.Get("song-" + entry)
		before[entry] = song.Tasks
	}
	want := []string{openStaffingAdded, openStaffingJoined, openStaffingJoined, openStaffingJoined}
	for i, entry := range []string{"album-1", "album-2", "album-3", "album-4"} {
		code, payload := f.finish(t, openHomeID, entry, finishBody("r-"+entry))
		staffing, _ := payload["staffing"].(map[string]any)
		if code != http.StatusOK || staffing["state"] != want[i] || staffing["agent_name"] != "REAPER Assistant" ||
			staffing["shared"] != true || payload["workspace_id"] != "song-"+entry {
			t.Fatalf("%s = %d %v", entry, code, payload)
		}
	}
	if got := f.sharedAgents(); len(got) != 1 {
		t.Fatalf("shared definitions = %v, want one", got)
	}
	if role := f.consentRole(t); role.AgentName != "REAPER Assistant" || role.PendingName != "" || len(role.Copies) != 0 && len(role.Copies) > 4 {
		t.Fatalf("consent role = %+v", role)
	}
	for _, entry := range []string{"album-1", "album-2", "album-3", "album-4"} {
		song, _ := f.store.Get("song-" + entry)
		if len(song.GetAgentInstances()) != 1 || song.GetAgentInstances()[0].Name != "REAPER Assistant" {
			t.Fatalf("%s agents = %+v", entry, song.GetAgentInstances())
		}
		// The finish writes no ticket and selects no mode.
		if !reflect.DeepEqual(song.Tasks, before[entry]) {
			t.Fatalf("%s tickets changed: %+v", entry, song.Tasks)
		}
	}
	if len(f.fileOnly) != 0 {
		t.Fatalf("the finish selected a mode: %v", f.fileOnly)
	}
	// A retry of a finished song changes nothing.
	staffed := len(f.staffed)
	if code, payload := f.finish(t, openHomeID, "album-2", finishBody("r-album-2")); code != http.StatusOK || staffingState(payload) != openStaffingAlready {
		t.Fatalf("retry = %d %v", code, payload)
	}
	if len(f.staffed) != staffed {
		t.Fatal("a retry staffed again")
	}
}

func TestFinishStaffing_RefusesWhatItMayNotDo(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	foreign := &agentworkspace.Workspace{ID: "their-home", Name: "Theirs", OwnerUserID: "someone-else", Status: agentworkspace.StatusActive}
	foreign.SetAssistantProgramState(&agentworkspace.AssistantProgramState{SchemaVersion: agentworkspace.AssistantProgramStateSchemaVersion,
		Key: agentworkspace.AssistantProgramKey{OwnerUserID: "someone-else", PluginID: "p", ProgramID: "q"}})
	if err := f.store.Save(foreign); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, home, entry, body, reason string
		code                            int
	}{
		{"another owner's Home", "their-home", "two", finishBody("a"), "", http.StatusNotFound},
		{"a row that does not exist", openHomeID, "missing", finishBody("b"), "", http.StatusNotFound},
		{"an unknown field", openHomeID, "two", `{"request_id":"c","team_digest":"` + openDigest + `","grant":{"source":"x"}}`, "", http.StatusBadRequest},
		{"an agent name", openHomeID, "two", `{"request_id":"d","team_digest":"` + openDigest + `","agent_name":"Mine"}`, "", http.StatusBadRequest},
		{"a duplicate field", openHomeID, "two", `{"request_id":"e","request_id":"f","team_digest":"` + openDigest + `"}`, "", http.StatusBadRequest},
		{"trailing JSON", openHomeID, "two", finishBody("g") + `{}`, "", http.StatusBadRequest},
		{"no request id", openHomeID, "two", `{"team_digest":"` + openDigest + `"}`, "", http.StatusBadRequest},
		{"a short digest", openHomeID, "two", `{"request_id":"h","team_digest":"abc"}`, "", http.StatusBadRequest},
		{"not on the map", openHomeID, "three", finishBody("i"), "not_connected", http.StatusConflict},
		{"another approval", openHomeID, "two", `{"request_id":"j","team_digest":"` + strings.Repeat("c", 64) + `"}`, "sharing_changed", http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, payload := f.finish(t, tc.home, tc.entry, tc.body)
			if code != tc.code || (tc.reason != "" && payload["reason"] != tc.reason) {
				t.Fatalf("= %d %v, want %d %s", code, payload, tc.code, tc.reason)
			}
		})
	}
	if len(f.staffed) != 0 || len(f.sharedAgents()) != 0 {
		t.Fatalf("a refused finish staffed: %v", f.staffed)
	}
	if role := f.consentRole(t); role.PendingName != "" {
		t.Fatalf("a refused finish reserved a name: %+v", role)
	}
	// A request id is one question: reused for another song it is refused.
	f.connect(t, "three")
	if code, _ := f.finish(t, openHomeID, "two", finishBody("same")); code != http.StatusOK {
		t.Fatalf("first use = %d", code)
	}
	if code, payload := f.finish(t, openHomeID, "three", finishBody("same")); code != http.StatusConflict || payload["reason"] != "request_reused" {
		t.Fatalf("reused = %d %v", code, payload)
	}
	if code, payload := f.finish(t, openHomeID, "two", finishBody("same")); code != http.StatusOK || staffingState(payload) != openStaffingAlready {
		t.Fatalf("same input replay = %d %v", code, payload)
	}
}

// Existing consent only: no consent, switched off, an older team, the shared
// assistant deleted, a read-only provider and a trashed song each stop with
// their own reason and add nobody.
func TestFinishStaffing_StopsAreTypedAndAddNobody(t *testing.T) {
	type setup func(t *testing.T, f *openFixture)
	cases := []struct {
		name   string
		setup  setup
		reason string
	}{
		{"no consent", func(*testing.T, *openFixture) {}, openStaffingNone},
		{"switched off", func(t *testing.T, f *openFixture) {
			f.grant(t)
			if _, err := f.staffs.Consents().Revoke(openHomeID); err != nil {
				t.Fatal(err)
			}
		}, openStaffingOff},
		{"older team", func(t *testing.T, f *openFixture) {
			f.grant(t)
			f.withTeam(strings.Repeat("d", 64))
		}, ""}, // the request's digest no longer matches: sharing_changed
		{"assistant deleted", func(t *testing.T, f *openFixture) {
			f.grant(t)
			if err := f.staffs.Consents().ReserveAgent(openHomeID, "reaper-plugin", "reaper-song", openDigest, "reaper-assistant", "REAPER Assistant"); err != nil {
				t.Fatal(err)
			}
			if err := f.staffs.Consents().RecordAgent(openHomeID, "reaper-plugin", "reaper-song", openDigest, "reaper-assistant", "REAPER Assistant"); err != nil {
				t.Fatal(err)
			}
		}, agentworkspace.ProjectStaffingAssistantMissing},
		{"read-only provider", func(t *testing.T, f *openFixture) {
			f.grant(t)
			f.handler.staffingProviderOverride = func(projectlibrary.Scope, *agentworkspace.Workspace, *agentworkspace.Workspace) bool { return false }
		}, staffingReadOnly},
		{"a song another owner holds", func(t *testing.T, f *openFixture) {
			f.grant(t)
			if err := f.store.Update("song-two", func(song *agentworkspace.Workspace) error {
				song.OwnerUserID = "someone-else"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}, staffingUnavailableTarget},
		{"model missing for a create", func(t *testing.T, f *openFixture) {
			f.grant(t)
			f.handler.staffingModelReady = func() bool { return false }
		}, staffingNeedsModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStaffingFixture(t, "two")
			tc.setup(t, f)
			code, payload := f.finish(t, openHomeID, "two", finishBody("r"))
			want := tc.reason
			if want == "" {
				want = "sharing_changed"
			}
			if code != http.StatusConflict || payload["reason"] != want {
				t.Fatalf("= %d %v, want %s", code, payload, want)
			}
			if len(f.staffed) != 0 {
				t.Fatalf("a stop staffed: %v", f.staffed)
			}
			if consent, _ := f.staffs.Consents().Read(openHomeID); consent != nil && consent.Roles[0].PendingName != "" {
				t.Fatalf("a stop reserved a name: %+v", consent.Roles[0])
			}
		})
	}
}

// An older team pinned on the song while the consent and installed blueprint
// moved on is stale, never a create or bind under the newer approval.
func TestFinishStaffing_ASongPinnedToAnOlderTeamIsStale(t *testing.T) {
	f := newStaffingFixture(t, "two")
	newer := strings.Repeat("e", 64)
	f.withTeam(newer)
	if _, err := f.staffs.Consents().Grant(openHomeID, agentworkspace.ProjectStaffingConsentGrant{
		Source: agentworkspace.ProjectStaffingConsentHomeSwitch, PluginID: "reaper-plugin", BlueprintID: "reaper-song",
		TeamDigest: newer, RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
	code, payload := f.finish(t, openHomeID, "two", `{"request_id":"r","team_digest":"`+newer+`"}`)
	if code != http.StatusConflict || payload["reason"] != agentworkspace.ProjectStaffingConsentStale || len(f.staffed) != 0 {
		t.Fatalf("= %d %v staffed=%v", code, payload, f.staffed)
	}
}

// A bind needs no new model: the shared assistant brings its own.
func TestFinishStaffing_JoiningNeedsNoNewModel(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	if code, payload := f.finish(t, openHomeID, "two", finishBody("r2")); code != http.StatusOK || staffingState(payload) != openStaffingAdded {
		t.Fatalf("first = %d %v", code, payload)
	}
	f.handler.staffingModelReady = func() bool { return false }
	if code, payload := f.finish(t, openHomeID, "three", finishBody("r3")); code != http.StatusOK || staffingState(payload) != openStaffingJoined {
		t.Fatalf("join without a model = %d %v", code, payload)
	}
}

// D8: a song staffed with its own agent keeps it and is reported as not shared.
func TestFinishStaffing_LeavesASongsOwnAgentAlone(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	if err := f.handler.assistantWorkspaceRoleStaffer(context.Background(), "song-two", []RoleStaffingFill{{RoleID: "reaper-assistant", Mode: "create", Name: "Mix Buddy"}}); err != nil {
		t.Fatal(err)
	}
	f.staffed = nil
	code, payload := f.finish(t, openHomeID, "two", finishBody("r"))
	staffing, _ := payload["staffing"].(map[string]any)
	if code != http.StatusOK || staffing["state"] != openStaffingAlready || staffing["agent_name"] != "Mix Buddy" || staffing["shared"] != false || len(f.staffed) != 0 {
		t.Fatalf("own agent = %d %v", code, payload)
	}
	if role := f.consentRole(t); role.AgentName != "" {
		t.Fatalf("a song's own agent was adopted: %+v", role)
	}
}

// Spike defect 1: two finishes racing the first create. The Home's turn makes
// the second wait, see the recorded agent and bind it.
func TestFinishStaffing_TwoTabsRacingTheFirstCreateMakeOneAssistant(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	inner := f.handler.assistantWorkspaceRoleStaffer
	var mu sync.Mutex
	f.handler.assistantWorkspaceRoleStaffer = func(ctx context.Context, id string, fills []RoleStaffingFill) error {
		mu.Lock()
		defer mu.Unlock()
		for _, fill := range fills {
			if _, taken := f.agents.GetAgent(fill.Name); fill.Mode == "create" && taken {
				return errors.New("name taken") // what the real adapter does
			}
		}
		return inner(ctx, id, fills)
	}
	results := make([]string, 2)
	var wg sync.WaitGroup
	for i, entry := range []string{"two", "three"} {
		wg.Add(1)
		go func(i int, entry string) {
			defer wg.Done()
			_, payload := f.finish(t, openHomeID, entry, finishBody("r-"+entry))
			results[i] = staffingState(payload)
		}(i, entry)
	}
	wg.Wait()
	got := strings.Join(results, ",")
	if got != "added,joined" && got != "joined,added" {
		t.Fatalf("outcomes = %s", got)
	}
	if names := f.sharedAgents(); len(names) != 1 {
		t.Fatalf("shared definitions = %v", names)
	}
}

// Library Open takes the same turn, so an Open racing a finish cannot make a
// second assistant either.
func TestFinishStaffing_AnOpenRacingASetupUsesTheSameTurn(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	var wg sync.WaitGroup
	var openState, finishState string
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, payload := f.open(t, openHomeID, "three", `{"request_id":"o3"}`)
		openState, _ = payload["staffing"].(string)
	}()
	go func() {
		defer wg.Done()
		_, payload := f.finish(t, openHomeID, "two", finishBody("f2"))
		finishState = staffingState(payload)
	}()
	wg.Wait()
	states := openState + "," + finishState
	if states != "added,joined" && states != "joined,added" {
		t.Fatalf("open,finish = %s", states)
	}
	if names := f.sharedAgents(); len(names) != 1 {
		t.Fatalf("shared definitions = %v", names)
	}
}

// Spike defect 2: a stop after the bind and before Settle. The next finish on
// another song recovers the proven create instead of making "REAPER Assistant 2".
func TestFinishStaffing_RecoversACreateThatWasBoundButNotRecorded(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	fills, err := f.staffs.Fill("song-two", []string{"reaper-assistant"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.handler.assistantWorkspaceRoleStaffer(context.Background(), "song-two", []RoleStaffingFill{{RoleID: "reaper-assistant", Mode: "create", Name: fills[0].Name}}); err != nil {
		t.Fatal(err)
	}
	// (stopped here: Settle never ran)
	code, payload := f.finish(t, openHomeID, "three", finishBody("r3"))
	staffing, _ := payload["staffing"].(map[string]any)
	if code != http.StatusOK || staffing["state"] != openStaffingJoined || staffing["agent_name"] != "REAPER Assistant" {
		t.Fatalf("after the stop = %d %v", code, payload)
	}
	if names := f.sharedAgents(); len(names) != 1 {
		t.Fatalf("shared definitions = %v", names)
	}
	if role := f.consentRole(t); role.AgentName != "REAPER Assistant" || role.PendingName != "" {
		t.Fatalf("consent = %+v", role)
	}
	// The interrupted song itself now reads as staffed by the shared assistant.
	if code, payload := f.finish(t, openHomeID, "two", finishBody("r2")); code != http.StatusOK || staffingState(payload) != openStaffingAlready {
		t.Fatalf("interrupted song = %d %v", code, payload)
	}
}

// Spike defect 3: a stop after the agent was created and before it was bound.
// Nothing proves the consent made it, so nothing adopts it and nothing steps
// around it with a second create: the owner is asked.
func TestFinishStaffing_AnUnboundCreateIsNeverAdoptedNorDuplicated(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	fills, err := f.staffs.Fill("song-two", []string{"reaper-assistant"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.agents.mu.Lock()
	f.agents.names[fills[0].Name] = true // created, then stopped before the bind
	f.agents.mu.Unlock()
	for _, entry := range []string{"two", "three"} {
		code, payload := f.finish(t, openHomeID, entry, finishBody("r-"+entry))
		if code != http.StatusConflict || payload["reason"] != staffingReconcile {
			t.Fatalf("%s = %d %v", entry, code, payload)
		}
	}
	// Open of a new song stops the same way rather than creating "… 2".
	if _, payload := f.open(t, openHomeID, "one", `{"request_id":"o1","selected_file":"Song 001.rpp"}`); payload["staffing"] != staffingReconcile {
		t.Fatalf("open = %v", payload)
	}
	if names := f.sharedAgents(); len(names) != 1 || len(f.staffed) != 0 {
		t.Fatalf("shared definitions = %v staffed = %v", names, f.staffed)
	}
}

// Spike defect 4 and implementation review: a create that fails while an agent
// holds the reserved name is never released and never stepped around (it may be
// another path's create about to be recorded, or this create's own leftover):
// reconcile. Once the owner removes that agent the retry creates the assistant.
func TestFinishStaffing_ANameTakenInBetweenIsReconciledNeverDuplicated(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	inner := f.handler.assistantWorkspaceRoleStaffer
	f.handler.assistantWorkspaceRoleStaffer = func(ctx context.Context, id string, fills []RoleStaffingFill) error {
		f.agents.mu.Lock()
		f.agents.names["REAPER Assistant"] = true // someone else's, made in between
		f.agents.mu.Unlock()
		return errors.New("name taken")
	}
	code, payload := f.finish(t, openHomeID, "two", finishBody("r"))
	if code != http.StatusConflict || payload["reason"] != staffingReconcile {
		t.Fatalf("taken = %d %v", code, payload)
	}
	if role := f.consentRole(t); role.PendingName != "REAPER Assistant" || role.AgentName != "" {
		t.Fatalf("the reservation moved: %+v", role)
	}
	f.handler.assistantWorkspaceRoleStaffer = inner
	if code, payload := f.finish(t, openHomeID, "two", finishBody("r")); code != http.StatusConflict || payload["reason"] != staffingReconcile {
		t.Fatalf("retry while held = %d %v", code, payload)
	}
	f.agents.mu.Lock()
	delete(f.agents.names, "REAPER Assistant") // the owner removed it
	f.agents.mu.Unlock()
	code, payload = f.finish(t, openHomeID, "two", finishBody("r"))
	staffing, _ := payload["staffing"].(map[string]any)
	if code != http.StatusOK || staffing["state"] != openStaffingAdded || staffing["agent_name"] != "REAPER Assistant" {
		t.Fatalf("after the owner resolved it = %d %v", code, payload)
	}
	if names := f.sharedAgents(); len(names) != 1 {
		t.Fatalf("shared definitions = %v", names)
	}
}

// A song bound to the shared assistant after it was deleted is not "already":
// it is the missing-assistant case, with its own repair.
func TestFinishStaffing_ABindingToADeletedSharedAssistantIsMissing(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	if code, _ := f.finish(t, openHomeID, "two", finishBody("r")); code != http.StatusOK {
		t.Fatal("finish failed")
	}
	f.agents.mu.Lock()
	delete(f.agents.names, "REAPER Assistant")
	f.agents.mu.Unlock()
	if code, payload := f.finish(t, openHomeID, "two", finishBody("r2")); code != http.StatusConflict || payload["reason"] != agentworkspace.ProjectStaffingAssistantMissing {
		t.Fatalf("deleted = %d %v", code, payload)
	}
	if _, row := f.read(t, "/projects/two/staffing", f.handler.GetAssistantLibraryProjectStaffing, "two"); row["filled"] != false || row["plan"] != agentworkspace.ProjectStaffingAssistantMissing {
		t.Fatalf("row = %v", row)
	}
}

// The approval checked before the turn is checked again inside it: a regrant
// for another team in between is a new question, and nobody is added.
func TestFinishStaffing_TheConfirmedApprovalIsRecheckedInsideTheTurn(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	release := f.staffs.Hold(openHomeID)
	done := make(chan map[string]any)
	go func() {
		_, payload := f.finish(t, openHomeID, "two", finishBody("r"))
		done <- payload
	}()
	// While the finish waits for the turn, the consent moves to another team
	// (written directly: the switch itself would wait for the turn too).
	if _, err := f.staffs.Consents().Grant(openHomeID, agentworkspace.ProjectStaffingConsentGrant{
		Source: agentworkspace.ProjectStaffingConsentHomeSwitch, PluginID: "reaper-plugin", BlueprintID: "reaper-song",
		TeamDigest: strings.Repeat("f", 64), RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
	release()
	if payload := <-done; payload["reason"] != staffingSharingChanged {
		t.Fatalf("= %v", payload)
	}
	if len(f.staffed) != 0 {
		t.Fatalf("staffed under an approval the person did not see: %v", f.staffed)
	}
}

// Any other write failure is failed, the role stays empty, nothing was made so
// the reservation is given up (a later manual Hire of that name is never
// adopted), and the retry finishes it.
func TestFinishStaffing_AFailedWriteIsReportedAndRetrySafe(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	inner := f.handler.assistantWorkspaceRoleStaffer
	f.handler.assistantWorkspaceRoleStaffer = func(context.Context, string, []RoleStaffingFill) error { return errors.New("disk full") }
	if code, payload := f.finish(t, openHomeID, "two", finishBody("r")); code != http.StatusConflict || payload["reason"] != staffingFailed {
		t.Fatalf("failed = %d %v", code, payload)
	}
	if role := f.consentRole(t); role.PendingName != "" || role.ReservedAt != nil {
		t.Fatalf("a failed create kept its reservation: %+v", role)
	}
	f.handler.assistantWorkspaceRoleStaffer = inner
	if code, payload := f.finish(t, openHomeID, "two", finishBody("r")); code != http.StatusOK || staffingState(payload) != openStaffingAdded {
		t.Fatalf("retry = %d %v", code, payload)
	}
	if names := f.sharedAgents(); len(names) != 1 {
		t.Fatalf("shared definitions = %v", names)
	}
}

// An approval for another blueprint is not this song's approval: not_shared, and
// nothing is added or reserved.
func TestFinishStaffing_AnApprovalForAnotherBlueprintIsNotShared(t *testing.T) {
	f := newStaffingFixture(t, "two")
	if _, err := f.staffs.Consents().Grant(openHomeID, agentworkspace.ProjectStaffingConsentGrant{
		Source: agentworkspace.ProjectStaffingConsentFolderOffer, PluginID: "reaper-plugin", BlueprintID: "another-blueprint",
		TeamDigest: openDigest, RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
	code, payload := f.finish(t, openHomeID, "two", finishBody("r"))
	if code != http.StatusConflict || payload["reason"] != openStaffingNotShare {
		t.Fatalf("other blueprint = %d %v", code, payload)
	}
	if len(f.staffed) != 0 || len(f.sharedAgents()) != 0 {
		t.Fatalf("staffed under another blueprint's approval: %v", f.staffed)
	}
}

// A manual Hire (another tab) fills the role while the finish is creating the
// shared assistant: the person's own agent stays, the song is reported as
// having its own, and the abandoned reservation is given up, so that agent is
// never adopted as the shared one later.
func TestFinishStaffing_AManualHireRacingTheCreateWinsAndLeavesNoReservation(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	inner := f.handler.assistantWorkspaceRoleStaffer
	f.handler.assistantWorkspaceRoleStaffer = func(ctx context.Context, id string, fills []RoleStaffingFill) error {
		// The person's own Hire lands first...
		if err := inner(ctx, id, []RoleStaffingFill{{RoleID: "reaper-assistant", Mode: "create", Name: "Mix Buddy"}}); err != nil {
			return err
		}
		// ...so the shared create is refused by the role binding.
		return errors.New("role already filled")
	}
	code, payload := f.finish(t, openHomeID, "two", finishBody("r"))
	staffing, _ := payload["staffing"].(map[string]any)
	if code != http.StatusOK || staffing["state"] != openStaffingAlready || staffing["agent_name"] != "Mix Buddy" || staffing["shared"] != false {
		t.Fatalf("race = %d %v", code, payload)
	}
	if role := f.consentRole(t); role.PendingName != "" || role.AgentName != "" || role.ReservedAt != nil {
		t.Fatalf("the abandoned reservation was kept: %+v", role)
	}
	// A later manual Hire of the label's own name is never mistaken for the
	// shared assistant.
	f.handler.assistantWorkspaceRoleStaffer = inner
	if err := inner(context.Background(), "song-three", []RoleStaffingFill{{RoleID: "reaper-assistant", Mode: "create", Name: "REAPER Assistant"}}); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.finish(t, openHomeID, "three", finishBody("r3")); code != http.StatusOK {
		t.Fatal("finish after a manual hire failed")
	}
	if role := f.consentRole(t); role.AgentName != "" {
		t.Fatalf("a manual hire was adopted as the shared assistant: %+v", role)
	}
}

// If the consent's record of the create is lost while the assistant is being
// added (a concurrent change outside the turn), the finish says so: bound, but
// not recorded as the shared one. It is never reported as added.
func TestFinishStaffing_ACreateThatCannotBeRecordedIsUnsettled(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	inner := f.handler.assistantWorkspaceRoleStaffer
	f.handler.assistantWorkspaceRoleStaffer = func(ctx context.Context, id string, fills []RoleStaffingFill) error {
		if err := inner(ctx, id, fills); err != nil {
			return err
		}
		return f.staffs.Consents().ReleaseReservation(openHomeID, "reaper-assistant", fills[0].Name)
	}
	code, payload := f.finish(t, openHomeID, "two", finishBody("r"))
	if code != http.StatusConflict || payload["reason"] != staffingUnsettled {
		t.Fatalf("unsettled = %d %v", code, payload)
	}
	if staffing, _ := payload["staffing"].(map[string]any); staffing["agent_name"] != "REAPER Assistant" {
		t.Fatalf("the bound agent was not named: %v", payload)
	}
}

// The reads never write: no reservation, no agent, no consent change.
func TestFinishStaffing_StatusReadsAreInert(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	homeBefore, _ := f.store.Get(openHomeID)
	songBefore, _ := f.store.Get("song-two")
	code, plan := f.read(t, "/staffing", f.handler.GetAssistantLibraryStaffing, "")
	if code != http.StatusOK || plan["plan"] != staffingCreate || plan["agent_name"] != "REAPER Assistant" || plan["role_label"] != "REAPER Assistant" {
		t.Fatalf("home plan = %d %v", code, plan)
	}
	code, row := f.read(t, "/projects/two/staffing", f.handler.GetAssistantLibraryProjectStaffing, "two")
	if code != http.StatusOK || row["connected"] != true || row["filled"] != false || row["plan"] != staffingCreate || row["workspace_id"] != "song-two" {
		t.Fatalf("row = %d %v", code, row)
	}
	code, row = f.read(t, "/projects/three/staffing", f.handler.GetAssistantLibraryProjectStaffing, "three")
	if code != http.StatusOK || row["connected"] != false || row["plan"] != staffingCreate {
		t.Fatalf("unconnected row = %d %v", code, row)
	}
	homeAfter, _ := f.store.Get(openHomeID)
	songAfter, _ := f.store.Get("song-two")
	if !reflect.DeepEqual(homeBefore.GetAssistantProgramState().GetProjectStaffingConsent(), homeAfter.GetAssistantProgramState().GetProjectStaffingConsent()) ||
		!reflect.DeepEqual(songBefore.GetAgentInstances(), songAfter.GetAgentInstances()) || !reflect.DeepEqual(songBefore.Tasks, songAfter.Tasks) {
		t.Fatal("a status read changed state")
	}
	if len(f.staffed) != 0 || len(f.agents.ListAgents()) != 0 {
		t.Fatalf("a status read staffed: %v", f.staffed)
	}
	// After the first create, the plan joins the recorded assistant.
	if code, _ := f.finish(t, openHomeID, "two", finishBody("r")); code != http.StatusOK {
		t.Fatal("finish failed")
	}
	if _, plan := f.read(t, "/staffing", f.handler.GetAssistantLibraryStaffing, ""); plan["plan"] != staffingJoin || plan["agent_name"] != "REAPER Assistant" {
		t.Fatalf("plan after create = %v", plan)
	}
	if _, row := f.read(t, "/projects/two/staffing", f.handler.GetAssistantLibraryProjectStaffing, "two"); row["filled"] != true || row["shared"] != true {
		t.Fatalf("row after create = %v", row)
	}
	// Blockers are disclosed before anything is confirmed.
	f.handler.staffingModelReady = func() bool { return false }
	if _, err := f.staffs.Consents().Revoke(openHomeID); err != nil {
		t.Fatal(err)
	}
	if _, plan := f.read(t, "/staffing", f.handler.GetAssistantLibraryStaffing, ""); plan["plan"] != openStaffingOff || plan["message"] == "" {
		t.Fatalf("plan switched off = %v", plan)
	}
}
