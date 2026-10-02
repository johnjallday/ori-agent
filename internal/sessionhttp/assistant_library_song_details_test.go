package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// songDetailsHome is an initialized, writable Home library: its installed Home
// provider matches the Home's fingerprint exactly.
func songDetailsHome(t *testing.T) (*Handler, *workspace.InMemoryStore, *workspace.Workspace) {
	t.Helper()
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	state := station.GetAssistantProgramState()
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: state.Key.ProgramID,
		StationName: "Portfolio Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "guide", Label: "Guide", Required: true,
			Primary: true, SystemPrompt: "Coordinate reviewed Home work."}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168,
			MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: state.Key.PluginID, PluginVersion: "0.1.0", ProgramID: state.Key.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
		PluginGeneration: 4, ComponentFingerprint: strings.Repeat("a", 64),
	}
	if err := store.Update(station.ID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.HomeProvider = &owner
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installed := plugin.InstalledPlugin{
		Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration,
		ComponentFingerprint: owner.ComponentFingerprint, InstalledAt: station.CreatedAt.Add(-time.Minute),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: owner.PluginID, Version: owner.PluginVersion,
			Protocol:              plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures:  []string{plugin.HostFeatureIndependentProgramHomesV1},
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home},
		},
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{installed}})
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := handler.assistantLibraryStore()
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "song-details-init"); err != nil {
		t.Fatal(err)
	}
	return handler, store, station
}

func postSongDetails(t *testing.T, handler *Handler, homeID, body string) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.SetAssistantLibrarySongDetails(response,
		assistantProgramRequest(http.MethodPost, "/library/song-details", homeID, body))
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %d %s: %v", response.Code, response.Body.String(), err)
	}
	return response.Code, result
}

// songDetailsState reads the state from a response body, success or refusal.
func songDetailsState(result map[string]any) string {
	view, _ := result["song_details"].(map[string]any)
	if view == nil {
		data, _ := result["data"].(map[string]any)
		view, _ = data["song_details"].(map[string]any)
	}
	state, _ := view["state"].(string)
	return state
}

func rootsSongDetails(t *testing.T, handler *Handler, homeID string) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ListAssistantLibraryRoots(response, assistantProgramRequest(http.MethodGet, "/library/roots", homeID, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("roots: %d %s", response.Code, response.Body.String())
	}
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return songDetailsState(result)
}

func TestLibrarySongDetails_AHomeWithoutConsentHasNoSwitch(t *testing.T) {
	handler, _, station := songDetailsHome(t)
	if state := rootsSongDetails(t, handler, station.ID); state != workspace.SongDetailsNone {
		t.Fatalf("roots song_details = %q, want none", state)
	}
	for _, body := range []string{`{"request_id":"r1","enabled":true}`, `{"request_id":"r2","enabled":false}`} {
		code, result := postSongDetails(t, handler, station.ID, body)
		if code != http.StatusConflict || result["reason"] != "song_details_not_granted" ||
			songDetailsState(result) != workspace.SongDetailsNone {
			t.Fatalf("%s: %d %v", body, code, result)
		}
	}
	if consent := mustHome(t, handler, station.ID).GetAssistantProgramState().GetSongDetailsConsent(); consent != nil {
		t.Fatalf("a refused switch wrote a consent: %+v", consent)
	}
}

func TestLibrarySongDetails_TheSwitchTurnsOffAndOnAgain(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	if state := rootsSongDetails(t, handler, station.ID); state != workspace.SongDetailsOn {
		t.Fatalf("roots song_details = %q, want on", state)
	}
	for _, step := range []struct {
		body, want string
	}{
		{`{"request_id":"off-1","enabled":false}`, workspace.SongDetailsOff},
		{`{"request_id":"off-1","enabled":false}`, workspace.SongDetailsOff}, // a retry is a no-op
		{`{"request_id":"on-1","enabled":true}`, workspace.SongDetailsOn},
		{`{"request_id":"on-1","enabled":true}`, workspace.SongDetailsOn},
	} {
		code, result := postSongDetails(t, handler, station.ID, step.body)
		if code != http.StatusOK || songDetailsState(result) != step.want {
			t.Fatalf("%s: %d %v", step.body, code, result)
		}
		if state := rootsSongDetails(t, handler, station.ID); state != step.want {
			t.Fatalf("after %s the roots read %q", step.body, state)
		}
	}
}

func TestLibrarySongDetails_ARefusedOrForeignRequestChangesNothing(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"enabled":false}`, `{"request_id":"  ","enabled":false}`,
		`{"request_id":"` + strings.Repeat("r", 121) + `","enabled":false}`} {
		response := httptest.NewRecorder()
		handler.SetAssistantLibrarySongDetails(response,
			assistantProgramRequest(http.MethodPost, "/library/song-details", station.ID, body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, response.Code, response.Body.String())
		}
	}
	handler.currentUserID = func(context.Context) (string, error) { return "someone-else", nil }
	response := httptest.NewRecorder()
	handler.SetAssistantLibrarySongDetails(response, assistantProgramRequest(http.MethodPost,
		"/library/song-details", station.ID, `{"request_id":"x","enabled":false}`))
	if response.Code != http.StatusNotFound {
		t.Fatalf("another owner: %d %s", response.Code, response.Body.String())
	}
	if state := mustHome(t, handler, station.ID).GetAssistantProgramState().GetSongDetailsConsent().State(); state != workspace.SongDetailsOn {
		t.Fatalf("a refused request changed the switch to %q", state)
	}
}

func TestLibrarySongDetails_AReadOnlyHomeTurnsOffButNotOn(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{})
	if code, result := postSongDetails(t, handler, station.ID, `{"request_id":"off","enabled":false}`); code != http.StatusOK ||
		songDetailsState(result) != workspace.SongDetailsOff {
		t.Fatalf("off on a read-only Home: %d %v", code, result)
	}
	code, result := postSongDetails(t, handler, station.ID, `{"request_id":"on","enabled":true}`)
	if code != http.StatusConflict || result["reason"] != "provider_unavailable" || songDetailsState(result) != workspace.SongDetailsOff {
		t.Fatalf("on on a read-only Home: %d %v", code, result)
	}
}

func mustHome(t *testing.T, handler *Handler, id string) *workspace.Workspace {
	t.Helper()
	home, err := handler.workspaceTaskStore.Get(id)
	if err != nil || home == nil {
		t.Fatalf("home %s: %v", id, err)
	}
	return home
}
