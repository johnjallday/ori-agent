package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type rosterBody struct {
	Rows []struct {
		WorkspaceID     string `json:"workspace_id"`
		EntryID         string `json:"entry_id"`
		Title           string `json:"title"`
		KeeperAgentName string `json:"keeper_agent_name"`
		LastSession     *struct {
			FirstSentence string `json:"first_sentence"`
		} `json:"last_session"`
		Facts *struct {
			TrackCount int `json:"track_count"`
		} `json:"facts"`
		OpenWork       workspace.TicketCounts `json:"open_work_counts"`
		WorkspaceRoute string                 `json:"workspace_route"`
	} `json:"rows"`
	Total int `json:"total"`
}

func rosterRequest(t *testing.T, handler *Handler, homeID, query string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.GetAssistantLibraryRoster(response, assistantProgramRequest(http.MethodGet,
		"/api/workspaces/"+homeID+"/assistant-program/library/roster"+query, homeID, ""))
	return response
}

func decodeRoster(t *testing.T, response *httptest.ResponseRecorder) rosterBody {
	t.Helper()
	var body rosterBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("roster: %d %s", response.Code, response.Body.String())
	}
	return body
}

func TestRosterHTTP_ListsTheConnectedSongWithKeeperSessionFactsAndRoute(t *testing.T) {
	handler, store, station, project, scope, lister := songCardHTTPFixture(t)
	if err := store.Update(project.ID, func(song *workspace.Workspace) error {
		song.FolderSlug = "night-drive"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	initializeSongCardLibrary(t, store, scope)
	seedSongFacts(t, store, scope, project.ID)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	response := rosterRequest(t, handler, station.ID, "")
	body := decodeRoster(t, response)
	if body.Total != 1 || len(body.Rows) != 1 {
		t.Fatalf("roster rows: %+v", body)
	}
	row := body.Rows[0]
	if row.WorkspaceID != project.ID || row.Title != project.Name || row.KeeperAgentName != "Studio Keeper" ||
		row.LastSession == nil || row.LastSession.FirstSentence != "Tracked the bass" ||
		row.Facts == nil || row.Facts.TrackCount != 14 || row.OpenWork.InProgress != 1 ||
		row.WorkspaceRoute != "/workspaces/night-drive" {
		t.Fatalf("roster row: %+v", row)
	}
	// No agent record, prompt or memory crosses the link: only the name.
	for _, leak := range []string{"agent_instances", "system_prompt", "memory", "Card Song.rpp", "card-root"} {
		if strings.Contains(response.Body.String(), leak) {
			t.Fatalf("roster leaked %q: %s", leak, response.Body.String())
		}
	}
	if lister.calls != 0 {
		t.Fatalf("the roster asked for the installed provider %d times", lister.calls)
	}
}

func TestRosterHTTP_EmptyHomeQueryAndForeignOwner(t *testing.T) {
	handler, _, station, project, _, _ := songCardHTTPFixture(t)
	if body := decodeRoster(t, rosterRequest(t, handler, station.ID, "")); body.Total != 0 || body.Rows == nil || len(body.Rows) != 0 {
		t.Fatalf("a Home without a library has an empty roster: %+v", body)
	}
	if response := rosterRequest(t, handler, station.ID, "?all=1"); response.Code != http.StatusBadRequest {
		t.Fatalf("query accepted: %d", response.Code)
	}
	// A song's own ID does not open its Home's roster.
	if response := rosterRequest(t, handler, project.ID, ""); response.Code != http.StatusNotFound {
		t.Fatalf("a song read the roster: %d %s", response.Code, response.Body.String())
	}
	handler.currentUserID = func(context.Context) (string, error) { return "other-owner", nil }
	if response := rosterRequest(t, handler, station.ID, ""); response.Code != http.StatusNotFound {
		t.Fatalf("foreign owner: %d", response.Code)
	}
}
