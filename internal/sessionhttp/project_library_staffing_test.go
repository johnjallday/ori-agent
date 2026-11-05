package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// library-setup-staffing group 3: a song already on the map before this fix
// gets the Home's shared assistant from its own page. The browser names only
// the workspace; Home and catalog row are resolved on the server.

func (f *openFixture) projectStaffing(t *testing.T, method, workspaceID, body string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Buffer
	if body != "" {
		reader = bytes.NewBufferString(body)
	} else {
		reader = &bytes.Buffer{}
	}
	request := httptest.NewRequest(method, "/api/workspaces/"+workspaceID+"/library-staffing", reader)
	request.SetPathValue("workspaceID", workspaceID)
	recorder := httptest.NewRecorder()
	if method == http.MethodGet {
		f.handler.GetProjectLibraryStaffing(recorder, request)
	} else {
		f.handler.FinishProjectLibraryStaffing(recorder, request)
	}
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

// Four Albums connected before the fix (no assistant). Repairing them one by
// one from their own pages creates one shared assistant and binds the rest,
// keeps every identity and ticket, and leaves a song with its own agent alone.
func TestProjectStaffing_RepairsPreFixSongsWithOneSharedAssistant(t *testing.T) {
	f := newStaffingFixture(t, "album-1", "album-2", "album-3", "album-4", "album-5")
	f.grant(t)
	if err := f.handler.assistantWorkspaceRoleStaffer(t.Context(), "song-album-5", []RoleStaffingFill{{RoleID: "reaper-assistant", Mode: "create", Name: "Album 5 Engineer"}}); err != nil {
		t.Fatal(err)
	}
	f.staffed = nil
	ticket := agentworkspace.Task{ID: "starter-a1", WorkspaceID: "song-album-1", Description: "Sketch the song's arrangement",
		Priority: 1, Status: agentworkspace.TaskStatusPending, Context: map[string]any{"template_starter_task": true}, CreatedAt: time.Now()}
	if err := f.store.Update("song-album-1", func(song *agentworkspace.Workspace) error { return song.AddTasks([]agentworkspace.Task{ticket}) }); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Get("song-album-1")

	code, status := f.projectStaffing(t, http.MethodGet, "song-album-1", "")
	if code != http.StatusOK || status["eligible"] != true || status["home_id"] != openHomeID || status["entry_id"] != "album-1" ||
		status["plan"] != staffingCreate || status["team_digest"] != openDigest || status["filled"] != false {
		t.Fatalf("status = %d %v", code, status)
	}
	if len(f.staffed) != 0 {
		t.Fatal("a status read staffed")
	}
	want := map[string]string{"song-album-1": openStaffingAdded, "song-album-2": openStaffingJoined, "song-album-3": openStaffingJoined, "song-album-4": openStaffingJoined}
	for _, id := range []string{"song-album-1", "song-album-2", "song-album-3", "song-album-4"} {
		code, payload := f.projectStaffing(t, http.MethodPost, id, finishBody("p-"+id))
		if code != http.StatusOK || staffingState(payload) != want[id] || payload["workspace_id"] != id {
			t.Fatalf("%s = %d %v", id, code, payload)
		}
	}
	if names := f.sharedAgents(); len(names) != 1 {
		t.Fatalf("shared definitions = %v", names)
	}
	// The song with its own agent is reported as such and never touched.
	code, own := f.projectStaffing(t, http.MethodPost, "song-album-5", finishBody("p-5"))
	staffing, _ := own["staffing"].(map[string]any)
	if code != http.StatusOK || staffing["state"] != openStaffingAlready || staffing["shared"] != false || staffing["agent_name"] != "Album 5 Engineer" {
		t.Fatalf("own agent = %d %v", code, own)
	}
	// Identity and tickets are unchanged; only the role was filled.
	after, _ := f.store.Get("song-album-1")
	if after.ID != before.ID || after.FolderSlug != before.FolderSlug || !reflect.DeepEqual(after.Tasks, before.Tasks) ||
		after.GetAssistantProjectLink().ID != before.GetAssistantProjectLink().ID {
		t.Fatalf("repair changed the song: %+v", after.Tasks)
	}
	// A repeated repair is a no-op.
	staffed := len(f.staffed)
	if code, payload := f.projectStaffing(t, http.MethodPost, "song-album-2", finishBody("p-again")); code != http.StatusOK || staffingState(payload) != openStaffingAlready || len(f.staffed) != staffed {
		t.Fatalf("again = %d %v", code, payload)
	}
	if len(f.fileOnly) != 0 {
		t.Fatalf("repair selected a mode: %v", f.fileOnly)
	}
}

// Only a library project of the caller's own Home is eligible; anything else is
// not guessed at.
func TestProjectStaffing_RefusesWhatItCannotResolveExactly(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	plain := &agentworkspace.Workspace{ID: "plain", Name: "Plain", OwnerUserID: "local", Status: agentworkspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	theirs := &agentworkspace.Workspace{ID: "theirs", Name: "Theirs", OwnerUserID: "someone-else", Status: agentworkspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, ws := range []*agentworkspace.Workspace{plain, theirs} {
		if err := f.store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := f.projectStaffing(t, http.MethodGet, "theirs", ""); code != http.StatusNotFound {
		t.Fatalf("another owner's workspace = %d", code)
	}
	if code, _ := f.projectStaffing(t, http.MethodPost, "theirs", finishBody("x")); code != http.StatusNotFound {
		t.Fatalf("another owner's workspace POST = %d", code)
	}
	if code, payload := f.projectStaffing(t, http.MethodGet, "plain", ""); code != http.StatusOK || payload["eligible"] != false {
		t.Fatalf("plain workspace = %d %v", code, payload)
	}
	if code, payload := f.projectStaffing(t, http.MethodPost, "plain", finishBody("y")); code != http.StatusConflict || payload["reason"] != "not_eligible" {
		t.Fatalf("plain workspace POST = %d %v", code, payload)
	}
	// Two catalog rows claiming one workspace: ambiguous, never guessed.
	f.opener.mu.Lock()
	f.opener.rows["ghost"] = projectlibrary.ActivationEligibility{State: "connected", WorkspaceID: "song-two"}
	f.opener.mu.Unlock()
	if code, payload := f.projectStaffing(t, http.MethodGet, "song-two", ""); code != http.StatusOK || payload["eligible"] != false {
		t.Fatalf("ambiguous = %d %v", code, payload)
	}
	if code, payload := f.projectStaffing(t, http.MethodPost, "song-two", finishBody("z")); code != http.StatusConflict || payload["reason"] != "not_eligible" {
		t.Fatalf("ambiguous POST = %d %v", code, payload)
	}
	// Strict body, as on the Home.
	if code, _ := f.projectStaffing(t, http.MethodPost, "song-three", `{"request_id":"a","team_digest":"`+openDigest+`","home_id":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", code)
	}
	if len(f.staffed) != 0 {
		t.Fatalf("a refused repair staffed: %v", f.staffed)
	}
}

// Between the status read and the repair the catalog row was relinked to
// another workspace: the repair stops instead of staffing what was not shown.
func TestProjectStaffing_AChangedLinkStopsTheRepair(t *testing.T) {
	f := newStaffingFixture(t, "two")
	f.grant(t)
	if code, payload := f.projectStaffing(t, http.MethodGet, "song-two", ""); code != http.StatusOK || payload["eligible"] != true {
		t.Fatalf("status = %d %v", code, payload)
	}
	f.opener.mu.Lock()
	row := f.opener.rows["two"]
	row.State = "link_needs_review"
	f.opener.rows["two"] = row
	f.opener.mu.Unlock()
	if code, payload := f.projectStaffing(t, http.MethodPost, "song-two", finishBody("r")); code != http.StatusConflict || payload["reason"] != "not_eligible" {
		t.Fatalf("relinked = %d %v", code, payload)
	}
	if len(f.staffed) != 0 {
		t.Fatalf("staffed after the link changed: %v", f.staffed)
	}
}

// Sharing off, an older team, or a deleted shared assistant: the project shows
// the reason and its owner review, and nothing is added.
func TestProjectStaffing_TheHomesStopsComeBackToTheProject(t *testing.T) {
	f := newStaffingFixture(t, "two", "three")
	f.grant(t)
	if code, _ := f.projectStaffing(t, http.MethodPost, "song-two", finishBody("r2")); code != http.StatusOK {
		t.Fatal("first repair failed")
	}
	f.agents.mu.Lock()
	delete(f.agents.names, "REAPER Assistant")
	f.agents.mu.Unlock()
	if _, status := f.projectStaffing(t, http.MethodGet, "song-three", ""); status["plan"] != agentworkspace.ProjectStaffingAssistantMissing || status["message"] == "" {
		t.Fatalf("missing = %v", status)
	}
	if code, payload := f.projectStaffing(t, http.MethodPost, "song-three", finishBody("r3")); code != http.StatusConflict || payload["reason"] != agentworkspace.ProjectStaffingAssistantMissing {
		t.Fatalf("missing POST = %d %v", code, payload)
	}
	if _, err := f.staffs.Consents().Revoke(openHomeID); err != nil {
		t.Fatal(err)
	}
	if _, status := f.projectStaffing(t, http.MethodGet, "song-three", ""); status["plan"] != openStaffingOff {
		t.Fatalf("off = %v", status)
	}
	if names := f.sharedAgents(); len(names) != 0 {
		t.Fatalf("a replacement was created: %v", names)
	}
}
