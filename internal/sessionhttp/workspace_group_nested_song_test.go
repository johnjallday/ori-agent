package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// saveFolderGroupInHome saves an ordinary group (a folder workspace) inside
// the Home, the place a song can now sit instead of directly under the Home.
func saveFolderGroupInHome(t *testing.T, store agentworkspace.Store, homeID, name string) string {
	t.Helper()
	group := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: name})
	group.Kind, group.OwnerUserID, group.ParentID = "group", "local", homeID
	if err := store.Save(group); err != nil {
		t.Fatal(err)
	}
	return group.ID
}

type nestedGroupedSong struct {
	handler  *Handler
	store    agentworkspace.Store
	homeID   string
	albumsID string
	songID   string
	payload  map[string]any
}

// newNestedGroupedSong sets up a required grouped project on a workspace that
// already sits inside the Albums folder group inside the Home.
func newNestedGroupedSong(t *testing.T) *nestedGroupedSong {
	t.Helper()
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	t.Cleanup(cleanup)
	homeID := preparePolicyHome(t, handler)
	albumsID := saveFolderGroupInHome(t, store, homeID, "Albums")
	code, body := postPolicyWorkspace(t, handler, map[string]any{"name": "Album-2"})
	if code != http.StatusCreated {
		t.Fatalf("workspace create status=%d body=%v", code, body)
	}
	songID := body["folder"].(map[string]any)["id"].(string)
	if _, err := store.(interface {
		MoveWorkspaceFolder(string, string) ([]agentworkspace.MovedWorkspace, error)
	}).MoveWorkspaceFolder(songID, albumsID); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"template_id": template.ID, "project_name": "Album-2", "group_composition": "grouped",
		"group_requirement_review": true,
	}
	code, body = postPolicyProject(t, handler, songID, payload)
	if code != http.StatusOK {
		t.Fatalf("project review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	delete(payload, "group_requirement_review")
	payload["group_review_token"] = review["review_token"]
	payload["idempotency_key"] = "nested-project"
	code, body = postPolicyProject(t, handler, songID, payload)
	if code != http.StatusCreated {
		t.Fatalf("project commit of a nested workspace status=%d body=%v", code, body)
	}
	return &nestedGroupedSong{handler: handler, store: store, homeID: homeID, albumsID: albumsID, songID: songID, payload: payload}
}

func (f *nestedGroupedSong) homeName(t *testing.T) string {
	t.Helper()
	home, err := f.store.Get(f.homeID)
	if err != nil {
		t.Fatal(err)
	}
	return home.Name
}

func (f *nestedGroupedSong) request(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.HandleWorkspaces(recorder, req)
	return recorder
}

// A grouped project set up on a workspace that already sits inside a folder
// group inside the Home stays there, finalizes, replays, and resolves its Home.
func TestGroupedProjectOnANestedWorkspaceFinalizesInPlace(t *testing.T) {
	f := newNestedGroupedSong(t)
	project, err := f.store.Get(f.songID)
	if err != nil {
		t.Fatal(err)
	}
	if link := project.GetAssistantProjectLink(); project.ParentID != f.albumsID || link == nil || link.StationWorkspaceID != f.homeID {
		t.Fatalf("nested project placement = parent %q link %#v", project.ParentID, link)
	}
	code, body := postPolicyProject(t, f.handler, f.songID, f.payload)
	if code != http.StatusCreated || body["idempotent_replay"] != true {
		t.Fatalf("nested project replay status=%d body=%v", code, body)
	}
	station, _, err := f.handler.assistantProgramStation(f.songID)
	if err != nil || station == nil || station.ID != f.homeID {
		t.Fatalf("a nested required song resolved station %#v err=%v", station, err)
	}
	recorder := httptest.NewRecorder()
	f.handler.GetAssistantProgram(recorder, assistantProgramRequest(http.MethodGet, "/", f.songID, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("assistant program for a nested song = %d %s", recorder.Code, recorder.Body.String())
	}
}

// FR10: a folder workspace that holds the Home's songs can't be deleted,
// trashed or moved out of the Home, and the refusal says so in one sentence.
func TestFolderHoldingHomeSongs_RefusesRemovalInPlainWords(t *testing.T) {
	f := newNestedGroupedSong(t)
	want := "Albums holds songs from your " + f.homeName(t) + ". Remove them from the map first."
	for _, query := range []string{"", "?confirm=true", "?confirm=true&delete_mode=contents", "?confirm=true&delete_mode=group_only"} {
		recorder := f.request(t, http.MethodDelete, "/api/workspaces/"+f.albumsID+query, nil)
		var failure orihttp.APIError
		if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusConflict || failure.Code != assistantRemovalReviewRequired || failure.Message != want {
			t.Fatalf("delete%s = %d %+v, want %q", query, recorder.Code, failure, want)
		}
	}
	recorder := f.request(t, http.MethodPatch, "/api/workspaces/"+f.albumsID, map[string]any{"parent_id": ""})
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), want) {
		t.Fatalf("moving the folder out of the Home = %d %s", recorder.Code, recorder.Body.String())
	}
	if albums, err := f.store.Get(f.albumsID); err != nil || albums.ParentID != f.homeID {
		t.Fatalf("a refused move changed the folder: %+v %v", albums, err)
	}
	// The Home's own protection is unchanged.
	recorder = f.request(t, http.MethodDelete, "/api/workspaces/"+f.homeID+"?confirm=true", nil)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "is an Assistant Home") {
		t.Fatalf("deleting the Home = %d %s", recorder.Code, recorder.Body.String())
	}
}

// Open question 3: the protection lasts only while the folder holds songs.
// Once its song leaves the map, the folder is an ordinary workspace again.
func TestFolderHoldingHomeSongs_ProtectionEndsWhenItsSongsLeave(t *testing.T) {
	f := newNestedGroupedSong(t)
	programs := agentworkspace.NewAssistantProgramStore(f.store)
	home, err := f.store.Get(f.homeID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := programs.ReviewDisconnect(f.homeID, f.songID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitDisconnect(f.homeID, review.Token, "remove-from-map"); err != nil {
		t.Fatal(err)
	}
	if song, err := f.store.Get(f.songID); err != nil || song.GetAssistantProjectLink() != nil || song.ParentID == f.albumsID {
		t.Fatalf("disconnect left the song in the folder: %+v %v", song, err)
	}
	recorder := f.request(t, http.MethodPatch, "/api/workspaces/"+f.albumsID, map[string]any{"parent_id": ""})
	if recorder.Code != http.StatusOK {
		t.Fatalf("moving an empty folder out of the Home = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = f.request(t, http.MethodDelete, "/api/workspaces/"+f.albumsID+"?confirm=true", nil)
	if recorder.Code != http.StatusNoContent && recorder.Code != http.StatusOK {
		t.Fatalf("deleting (trashing) an empty folder = %d %s", recorder.Code, recorder.Body.String())
	}
}

// PRD non-goal: a nested song is not moved between groups of the same Home.
func TestNestedSong_MoveBetweenGroupsOfTheHomeIsRefused(t *testing.T) {
	f := newNestedGroupedSong(t)
	labelID := saveFolderGroupInHome(t, f.store, f.homeID, "Label 2026")
	recorder := f.request(t, http.MethodPatch, "/api/workspaces/"+f.songID, map[string]any{"parent_id": labelID})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("moving a nested song to another group of the Home = %d %s", recorder.Code, recorder.Body.String())
	}
	if song, err := f.store.Get(f.songID); err != nil || song.ParentID != f.albumsID {
		t.Fatalf("a refused move changed the song: %+v %v", song, err)
	}
}
