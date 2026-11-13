package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects 6.4 (FR44-FR46): the switch names the app plugin that
// reads the project files, and a Home that approved before is told once.

func songDetailsView(result map[string]any) map[string]any {
	view, _ := result["song_details"].(map[string]any)
	if view == nil {
		data, _ := result["data"].(map[string]any)
		view, _ = data["song_details"].(map[string]any)
	}
	return view
}

// installReader adds a reviewed app plugin that declares a project reader to the
// plugins the handler lists.
func installReader(t *testing.T, handler *Handler) string {
	t.Helper()
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("no reviewed integration fixture")
	}
	lister, ok := handler.installedPluginLister.(assistantInstalledPluginLister)
	if !ok {
		t.Fatalf("lister = %T", handler.installedPluginLister)
	}
	reader := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true, WorkspaceSurfaces: &plugin.SurfaceContribution{
		Services: []plugin.ContributedService{{ID: "svc", Operations: []plugin.ContributedOperation{{
			ID: "project.facts.read", Policy: "read_only", TimeoutClass: "long", MaxOutputBytes: 262144}}}},
		ProjectFacts: &plugin.ProjectFactsRef{ServiceID: "svc", Operation: "project.facts.read", Formats: []string{"zzfmt"}},
	}}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: append(append([]plugin.InstalledPlugin{}, lister.installed...), reader)})
	return entry.DisplayName
}

// olderApproval makes the Home's consent one that was granted before the app
// plugin read the files: it carries no notice time.
func olderApproval(t *testing.T, store *workspace.InMemoryStore, homeID string) {
	t.Helper()
	if err := store.Update(homeID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.SongDetailsConsent.ReaderNoticeAt = nil
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func postReaderNotice(t *testing.T, handler *Handler, homeID, body string) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.AcknowledgeSongDetailsReaderNotice(response,
		assistantProgramRequest(http.MethodPost, "/library/song-details/notice", homeID, body))
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %d %s: %v", response.Code, response.Body.String(), err)
	}
	return response.Code, result
}

func rootsSongDetailsView(t *testing.T, handler *Handler, homeID string) map[string]any {
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
	return songDetailsView(result)
}

func TestLibrarySongDetails_NamesThePluginThatReadsAndNoticesOnlyAnOlderApproval(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	if err := workspace.NewSongDetailsConsents(store).GrantScope(station.ID, workspace.SongDetailsConsentLearnSongs, "offer-1", workspace.SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	// No plugin reads the songs: no name, and nothing to be told about.
	view := rootsSongDetailsView(t, handler, station.ID)
	if _, named := view["reader_name"]; named || view["reader_notice"] != nil {
		t.Fatalf("no reader installed, yet the view says %v", view)
	}
	olderApproval(t, store, station.ID)
	if view := rootsSongDetailsView(t, handler, station.ID); view["reader_notice"] != nil {
		t.Fatalf("a notice about a reader that is not installed: %v", view)
	}

	name := installReader(t, handler)
	view = rootsSongDetailsView(t, handler, station.ID)
	if view["reader_name"] != name || view["reader_notice"] != true || view["state"] != workspace.SongDetailsOn {
		t.Fatalf("an older approval with a reader installed: %v", view)
	}

}

// FR49: a reviewed plugin that is installed without a reader is told as an update
// where the Home's song details would be; once a plugin reads, it goes away.
func TestLibrarySongDetails_APluginWithoutAReaderIsToldAsAnUpdate(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	if err := workspace.NewSongDetailsConsents(store).GrantScope(station.ID, workspace.SongDetailsConsentLearnSongs, "offer-1", workspace.SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	if view := rootsSongDetailsView(t, handler, station.ID); view["reader_update"] != nil || view["reader_name"] != nil {
		t.Fatalf("no plugin installed, yet the view says %v", view)
	}
	entry, _ := reviewedintegration.Get("ori_reaper")
	lister, ok := handler.installedPluginLister.(assistantInstalledPluginLister)
	if !ok {
		t.Fatalf("lister = %T", handler.installedPluginLister)
	}
	withoutReader := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: append(append([]plugin.InstalledPlugin{}, lister.installed...), withoutReader)})
	view := rootsSongDetailsView(t, handler, station.ID)
	if view["reader_update"] != entry.DisplayName || view["reader_name"] != nil || view["reader_notice"] != nil {
		t.Fatalf("a reviewed plugin without a reader: %v", view)
	}
	name := installReader(t, handler)
	view = rootsSongDetailsView(t, handler, station.ID)
	if view["reader_update"] != nil || view["reader_name"] != name {
		t.Fatalf("once a plugin reads: %v", view)
	}
}

// A Home that approves now is told in the question itself, so it gets no notice.
func TestLibrarySongDetails_ANewApprovalIsNotFollowedByTheNotice(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	name := installReader(t, handler)
	if err := workspace.NewSongDetailsConsents(store).GrantScope(station.ID, workspace.SongDetailsConsentLearnSongs, "offer-2", workspace.SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	view := rootsSongDetailsView(t, handler, station.ID)
	if view["reader_name"] != name || view["reader_notice"] != nil || view["state"] != workspace.SongDetailsOn {
		t.Fatalf("the Home that approved under the new words: %v", view)
	}
}

func TestLibrarySongDetails_AcknowledgingTheNoticeRecordsItAndChangesNothingElse(t *testing.T) {
	handler, store, station := songDetailsHome(t)
	if err := workspace.NewSongDetailsConsents(store).GrantScope(station.ID, workspace.SongDetailsConsentLearnSongs, "offer-1", workspace.SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	name := installReader(t, handler)
	olderApproval(t, store, station.ID)
	before := mustHome(t, handler, station.ID).GetAssistantProgramState().GetSongDetailsConsent()

	code, result := postReaderNotice(t, handler, station.ID, `{"request_id":"notice-1"}`)
	view := songDetailsView(result)
	if code != http.StatusOK || view["reader_notice"] != nil || view["reader_name"] != name || view["state"] != workspace.SongDetailsOn || view["names"] != true {
		t.Fatalf("acknowledge: %d %v", code, result)
	}
	after := mustHome(t, handler, station.ID).GetAssistantProgramState().GetSongDetailsConsent()
	if after.ReaderNoticeAt == nil || after.Scope != before.Scope || after.Source != before.Source ||
		after.OfferID != before.OfferID || after.RevokedAt != nil {
		t.Fatalf("the consent changed beyond the notice: %+v → %+v", before, after)
	}
	// A retried request is the same answer; the next page load shows no notice.
	if code, _ := postReaderNotice(t, handler, station.ID, `{"request_id":"notice-1"}`); code != http.StatusOK {
		t.Fatalf("retry: %d", code)
	}
	if view := rootsSongDetailsView(t, handler, station.ID); view["reader_notice"] != nil {
		t.Fatalf("the notice came back: %v", view)
	}
}

func TestLibrarySongDetails_TheNoticeRefusesABadRequestAndGivesAHomeWithoutConsentNothing(t *testing.T) {
	handler, _, station := songDetailsHome(t)
	installReader(t, handler)
	for _, body := range []string{`{}`, `{"request_id":"  "}`, `{"request_id":"x"`, `not json`} {
		if code, _ := postReaderNotice(t, handler, station.ID, body); code == http.StatusOK {
			t.Fatalf("%q was accepted", body)
		}
	}
	// No consent: acknowledging records nothing, and the Home gets no consent.
	code, result := postReaderNotice(t, handler, station.ID, `{"request_id":"notice-1"}`)
	if code != http.StatusOK || songDetailsView(result)["state"] != workspace.SongDetailsNone {
		t.Fatalf("no consent: %d %v", code, result)
	}
	if consent := mustHome(t, handler, station.ID).GetAssistantProgramState().GetSongDetailsConsent(); consent != nil {
		t.Fatalf("a notice wrote a consent: %+v", consent)
	}
}
