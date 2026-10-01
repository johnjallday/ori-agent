package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func (f *openFixture) withTeam(digest string) *projectlibrary.ProjectTeam {
	team := &projectlibrary.ProjectTeam{PluginID: "reaper-plugin", BlueprintID: "reaper-song", Digest: digest,
		Roles: []agentworkspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant", Required: true}}}
	f.handler.projectTeamOverride = func(projectlibrary.Scope, *agentworkspace.Workspace) (projectlibrary.ProjectTeam, bool) {
		return *team, true
	}
	return team
}

func (f *openFixture) sharing(t *testing.T) librarySharingView {
	t.Helper()
	home, err := f.store.Get(openHomeID)
	if err != nil {
		t.Fatal(err)
	}
	return f.handler.librarySharing(projectlibrary.Scope{OwnerUserID: "local", HomeID: openHomeID}, home)
}

func (f *openFixture) post(t *testing.T, path, body string, handle http.HandlerFunc) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.SetPathValue("workspaceID", openHomeID)
	recorder := httptest.NewRecorder()
	handle(recorder, request)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

func sharingState(payload map[string]any) string {
	sharing, _ := payload["sharing"].(map[string]any)
	state, _ := sharing["state"].(string)
	return state
}

// D7, and how a Home built before this work opts in: the switch turns the
// shared assistant on (a fresh consent from the Home) and off (revoked).
func TestLibrarySharing_TheSwitchTurnsTheSharedAssistantOnAndOff(t *testing.T) {
	f := newOpenFixture(t)
	f.withTeam(openDigest)
	if view := f.sharing(t); view.State != sharingNone || view.RoleLabel != "REAPER Assistant" || view.TeamDigest != openDigest {
		t.Fatalf("a Home with no consent = %+v", view)
	}
	code, payload := f.post(t, "/sharing", `{"request_id":"on","enabled":true,"team_digest":"`+openDigest+`"}`, f.handler.SetAssistantLibrarySharing)
	if code != http.StatusOK || sharingState(payload) != sharingOn {
		t.Fatalf("on = %d %v", code, payload)
	}
	consent, _ := f.staffs.Consents().Read(openHomeID)
	if consent.Source != agentworkspace.ProjectStaffingConsentHomeSwitch || !consent.Active() {
		t.Fatalf("consent = %+v", consent)
	}
	code, payload = f.post(t, "/sharing", `{"request_id":"off","enabled":false}`, f.handler.SetAssistantLibrarySharing)
	if code != http.StatusOK || sharingState(payload) != sharingOff {
		t.Fatalf("off = %d %v", code, payload)
	}
	// Turning it on agrees to the exact team the switch showed.
	code, payload = f.post(t, "/sharing", `{"request_id":"again","enabled":true,"team_digest":"`+strings.Repeat("f", 64)+`"}`, f.handler.SetAssistantLibrarySharing)
	if code != http.StatusConflict || payload["reason"] != "sharing_changed" || sharingState(payload) != sharingOff {
		t.Fatalf("another team = %d %v", code, payload)
	}
	// Strict body: no path, no unknown field.
	if code, _ := f.post(t, "/sharing", `{"request_id":"x","enabled":true,"folder":"Songs"}`, f.handler.SetAssistantLibrarySharing); code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", code)
	}
}

// D6: a plugin update changes the team. Open stops once and points at the
// Home; the review renews the consent with the same assistant; songs already
// open keep what they have.
func TestLibrarySharing_AChangedTeamIsReviewedOnceOnTheHome(t *testing.T) {
	f := newOpenFixture(t)
	team := f.withTeam(openDigest)
	f.grant(t)
	if code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`); code != http.StatusOK || payload["staffing"] != "added" {
		t.Fatalf("first open = %d %v", code, payload)
	}
	team.Digest = strings.Repeat("c", 64)
	if view := f.sharing(t); view.State != sharingStale || view.AgentName != "REAPER Assistant" {
		t.Fatalf("after the update = %+v", view)
	}
	for _, entry := range []string{"three", "one"} {
		if code, payload := f.open(t, openHomeID, entry, `{"request_id":"s-`+entry+`"}`); code != http.StatusConflict || payload["reason"] != "consent_stale" {
			t.Fatalf("%s while stale = %d %v", entry, code, payload)
		}
	}
	code, payload := f.post(t, "/sharing", `{"request_id":"renew","enabled":true,"team_digest":"`+team.Digest+`"}`, f.handler.SetAssistantLibrarySharing)
	if code != http.StatusOK || sharingState(payload) != sharingOn {
		t.Fatalf("review = %d %v", code, payload)
	}
	consent, _ := f.staffs.Consents().Read(openHomeID)
	if consent.TeamDigest != team.Digest || consent.Roles[0].AgentName != "REAPER Assistant" {
		t.Fatalf("renewed consent = %+v", consent)
	}
	// The song already open is untouched.
	song, _ := f.store.Get("song-two")
	if len(song.GetAgentInstances()) != 1 || song.GetAgentInstances()[0].Name != "REAPER Assistant" {
		t.Fatalf("an open song changed: %+v", song.GetAgentInstances())
	}
}

// 4.4: the assistant the consent created was deleted. Open still makes the
// song (no agent), and the Home's one action lets the next song get a new one.
func TestLibrarySharing_AMissingAssistantIsAddedAgainOnTheNextOpen(t *testing.T) {
	f := newOpenFixture(t)
	f.withTeam(openDigest)
	f.grant(t)
	if code, _ := f.open(t, openHomeID, "two", `{"request_id":"r2"}`); code != http.StatusOK {
		t.Fatal("first open failed")
	}
	f.agents.mu.Lock()
	delete(f.agents.names, "REAPER Assistant")
	f.agents.mu.Unlock()
	if view := f.sharing(t); !view.AssistantMissing || view.State != sharingOn {
		t.Fatalf("after the delete = %+v", view)
	}
	code, payload := f.open(t, openHomeID, "three", `{"request_id":"r3"}`)
	if code != http.StatusOK || payload["staffing"] != agentworkspace.ProjectStaffingAssistantMissing || payload["created"] != true {
		t.Fatalf("open with the assistant gone = %d %v", code, payload)
	}
	code, payload = f.post(t, "/sharing/assistant", `{"request_id":"again"}`, f.handler.ReAddAssistantLibrarySharing)
	sharing, _ := payload["sharing"].(map[string]any)
	if code != http.StatusOK || sharing["assistant_missing"] == true || sharing["agent_name"] != nil {
		t.Fatalf("add again = %d %v", code, payload)
	}
	code, payload = f.open(t, openHomeID, "one", `{"request_id":"r1","selected_file":"Song 001.rpp"}`)
	if code != http.StatusOK || payload["staffing"] != "added" || payload["agent_name"] != "REAPER Assistant" {
		t.Fatalf("the next song = %d %v", code, payload)
	}
	consent, _ := f.staffs.Consents().Read(openHomeID)
	if consent.Roles[0].AgentName != "REAPER Assistant" {
		t.Fatalf("the new assistant was not recorded: %+v", consent.Roles[0])
	}
	// With nothing missing the action is an answer, not a change.
	if code, payload := f.post(t, "/sharing/assistant", `{"request_id":"noop"}`, f.handler.ReAddAssistantLibrarySharing); code != http.StatusOK || sharingState(payload) != sharingOn {
		t.Fatalf("nothing missing = %d %v", code, payload)
	}
}

// D5: no code on this branch selects anything but File-only or grants live
// control. The open path's only mode selection is SelectFileOnlyMode, and no
// changed file names a live-control grant.
func TestPortfolioPathsSelectOnlyFileOnly(t *testing.T) {
	root := filepath.Join("..", "..")
	files := []string{
		"internal/sessionhttp/assistant_library_open.go", "internal/sessionhttp/assistant_library_sharing.go",
		"internal/sessionhttp/portfolio_setup.go", "internal/foldersetup/portfolio.go", "internal/projectstaffing/service.go",
		"internal/server/folder_digest_portfolio.go",
	}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- fixed repository paths
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, forbidden := range []string{"assisted", "live_control", "LiveControlConfigured: true", "GrantLive", "ActionGrant"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s mentions %q", name, forbidden)
			}
		}
	}
	open, err := os.ReadFile(filepath.Join(root, "internal/sessionhttp/assistant_library_open.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(open), "selectFileOnly(") {
		t.Fatal("opening a song must record File-only")
	}
}
