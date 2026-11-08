package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// musicProjectCard is the words section the Music 0.5.0 candidate declares.
func musicProjectCard() *projecttemplates.ProjectCardDeclaration {
	return &projecttemplates.ProjectCardDeclaration{
		SchemaVersion: 1, Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs and assistants",
		Facts: []projecttemplates.ProjectCardFact{
			{Kind: "track_count", Unit: "tracks", UnitOne: "track"},
			{Kind: "tempo", Unit: "BPM"},
			{Kind: "length"},
		},
		Fields: []projecttemplates.ProjectCardField{{ID: "key", Label: "Key", Kind: "text"}},
	}
}

// pinProjectCardPackage pins the Home to an installed package whose Home
// declaration carries card (nil: no section) and returns the installed list
// the handler will see, which the caller may change.
func pinProjectCardPackage(t *testing.T, handler *Handler, store *workspace.InMemoryStore, home *workspace.Workspace,
	card *projecttemplates.ProjectCardDeclaration) *[]plugin.InstalledPlugin {
	t.Helper()
	state := home.GetAssistantProgramState()
	declaration := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: state.Key.ProgramID,
		StationName: "Music Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "guide", Label: "Guide", Required: true,
			Primary: true, SystemPrompt: "Coordinate reviewed Home work."}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168,
			MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
		ProjectCard: card,
	}
	features := []string{plugin.HostFeatureIndependentProgramHomesV1}
	if card != nil {
		features = append(features, plugin.HostFeatureProjectCardWordsV1)
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(&declaration); err != nil {
		t.Fatal(err)
	}
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: state.Key.PluginID, PluginVersion: "0.5.0", ProgramID: state.Key.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(declaration),
		PluginGeneration: 4, ComponentFingerprint: strings.Repeat("a", 64),
	}
	if err := store.Update(home.ID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.HomeProvider = &owner
		// A Home created under a declaration with the section carries the
		// words snapshot agents read (PRD #792 FR9).
		state.ProjectCardWords = projectlibrary.WordsSnapshot(owner.PluginID, owner.PluginVersion, card)
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installed := []plugin.InstalledPlugin{{
		Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration,
		ComponentFingerprint: owner.ComponentFingerprint,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: owner.PluginID, Version: owner.PluginVersion,
			Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: features,
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{declaration},
		},
	}}
	handler.SetInstalledPluginLister(installedPluginListerFunc(func() ([]plugin.InstalledPlugin, error) { return installed, nil }))
	return &installed
}

func TestProjectCardWordsHTTP_PackageNeutralDisabledAndNotInstalled(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	seedSongFacts(t, store, scope, project.ID)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	installed := pinProjectCardPackage(t, handler, store, station, musicProjectCard())

	// Pinned release with the section: the #791 wording, from the package.
	body := decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if body.Words == nil || body.Words.Source != projectlibrary.WordsSourcePackage || body.Words.Noun != "Song" ||
		body.Words.RosterTitle != "Songs and assistants" || len(body.Words.Facts) != 3 || len(body.Words.Fields) != 1 {
		t.Fatalf("package words: %+v", body.Words)
	}
	if !strings.HasPrefix(body.StatusLine, "14 tracks · 92 BPM · 3:41; saved") {
		t.Fatalf("package status line: %q", body.StatusLine)
	}
	roster := decodeRoster(t, rosterRequest(t, handler, station.ID, ""))
	if roster.Words.Noun != "Song" || roster.Words.RosterTitle != "Songs and assistants" {
		t.Fatalf("roster words: %+v", roster.Words)
	}
	if words := librarySummaryWords(t, handler, station.ID); words.Noun != "Song" || words.Source != "package" {
		t.Fatalf("summary words: %+v", words)
	}

	// A disabled package keeps the words the Home was set up under.
	(*installed)[0].Enabled = false
	if body := decodeSongCard(t, songCardRequest(t, handler, project.ID, "")); body.Words.Noun != "Song" {
		t.Fatalf("disabled package lost its words: %+v", body.Words)
	}

	// Not installed: neutral.
	*installed = nil
	body = decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if body.Words.Source != projectlibrary.WordsSourceNeutral || body.Words.Noun != "Project" ||
		!strings.HasPrefix(body.StatusLine, "Tracks 14 · Tempo 92 · 3:41; saved") {
		t.Fatalf("not installed: %+v %q", body.Words, body.StatusLine)
	}
	if words := librarySummaryWords(t, handler, station.ID); words.Source != "neutral" {
		t.Fatalf("summary words not installed: %+v", words)
	}
}

func TestProjectCardWordsHTTP_PinnedReleaseWithoutTheSection(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	pinProjectCardPackage(t, handler, store, station, nil)
	body := decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if body.Words == nil || body.Words.Source != projectlibrary.WordsSourceNeutral || body.Words.NounPlural != "Projects" {
		t.Fatalf("no section: %+v", body.Words)
	}
	if roster := decodeRoster(t, rosterRequest(t, handler, station.ID, "")); roster.Words.RosterTitle != "Projects and assistants" {
		t.Fatalf("roster words: %+v", roster.Words)
	}
	// A non-song workspace's card stays `{"linked":false}`: no words.
	plain := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain"})
	plain.OwnerUserID = station.OwnerUserID
	if err := store.Save(plain); err != nil {
		t.Fatal(err)
	}
	if response := songCardRequest(t, handler, plain.ID, ""); strings.TrimSpace(response.Body.String()) != `{"linked":false}` {
		t.Fatalf("non-song card: %s", response.Body.String())
	}
}

// setLinkedEntryFields writes fields onto the linked entry of projectID,
// straight into the Home's stored document.
func setLinkedEntryFields(t *testing.T, store *workspace.InMemoryStore, scope projectlibrary.Scope, projectID string, fields projectlibrary.Fields) {
	t.Helper()
	doc, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	for i := range doc.Entries {
		if doc.Entries[i].Link != nil && doc.Entries[i].Link.WorkspaceID == projectID {
			fields.DisplayName = doc.Entries[i].Fields.DisplayName
			doc.Entries[i].Fields = fields
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSongCardHTTP_FieldsAndRosterValues(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	pinProjectCardPackage(t, handler, store, station, musicProjectCard())
	setLinkedEntryFields(t, store, scope, project.ID, projectlibrary.Fields{Stage: "mixing", NextAction: "Send stems",
		Blockers: []string{"Vocal comp"}, Extra: map[string][]string{"key": {"F minor"}, "retired": {"hidden"}}, Revision: 2})
	response := songCardRequest(t, handler, project.ID, "")
	var body struct {
		Fields *projectlibrary.SongCardFields `json:"fields"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Fields == nil {
		t.Fatalf("card: %s", response.Body.String())
	}
	if body.Fields.Stage != "mixing" || body.Fields.NextAction != "Send stems" || len(body.Fields.Blockers) != 1 ||
		body.Fields.Revision != 2 || len(body.Fields.Extra) != 1 || body.Fields.Extra["key"][0] != "F minor" {
		t.Fatalf("card fields: %+v", body.Fields)
	}
	if strings.Contains(response.Body.String(), "retired") || strings.Contains(response.Body.String(), "hidden") {
		t.Fatalf("an undeclared plugin field reached the client: %s", response.Body.String())
	}
	raw := rosterRequest(t, handler, station.ID, "")
	var roster struct {
		Rows []struct {
			Stage      string `json:"stage"`
			NextAction string `json:"next_action"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw.Body.Bytes(), &roster); err != nil || len(roster.Rows) != 1 ||
		roster.Rows[0].Stage != "mixing" || roster.Rows[0].NextAction != "Send stems" {
		t.Fatalf("roster: %s", raw.Body.String())
	}
	if strings.Contains(raw.Body.String(), "F minor") {
		t.Fatalf("the roster carries plugin fields: %s", raw.Body.String())
	}
}

// PRD #792 FR29, FR30: the owner edits declared plugin fields on the shelf;
// a stored id the package no longer declares is kept, never shown, never
// editable, and the patch is checked against the declaration.
func TestLibraryFieldsHTTP_OwnerEditsDeclaredExtrasAndKeepsRetiredOnes(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	pinProjectCardPackage(t, handler, store, station, musicProjectCard())
	setLinkedEntryFields(t, store, scope, project.ID, projectlibrary.Fields{
		Extra: map[string][]string{"key": {"F minor"}, "retired": {"old words"}}, Revision: 2})
	doc, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	entryID := ""
	for _, entry := range doc.Entries {
		if entry.Link != nil && entry.Link.WorkspaceID == project.ID {
			entryID = entry.ID
		}
	}
	call := func(action func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
		t.Helper()
		request := assistantProgramRequest(http.MethodPost, "/library/fields", station.ID, body)
		request.SetPathValue("entryID", entryID)
		recorder := httptest.NewRecorder()
		action(recorder, request)
		return recorder
	}

	// The shelf's detail shows the declared field only.
	detailRequest := assistantProgramRequest(http.MethodGet, "/library/projects/x", station.ID, "")
	detailRequest.SetPathValue("entryID", entryID)
	detail := httptest.NewRecorder()
	handler.GetAssistantLibraryProject(detail, detailRequest)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"key":["F minor"]`) ||
		strings.Contains(detail.Body.String(), "retired") {
		t.Fatalf("detail: %d %s", detail.Code, detail.Body.String())
	}

	patch := `{"extra":{"key":["G minor"]}}`
	review := call(handler.ReviewAssistantLibraryFields, `{"if_fields_revision":2,"patch":`+patch+`}`)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), `"G minor"`) ||
		strings.Contains(review.Body.String(), "retired") {
		t.Fatalf("review: %d %s", review.Code, review.Body.String())
	}
	var reviewed struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(review.Body.Bytes(), &reviewed); err != nil {
		t.Fatal(err)
	}
	commitBody := `{"if_fields_revision":2,"patch":` + patch + `,"review_token":"` + reviewed.Token +
		`","idempotency_key":"extra-http","confirm":true}`
	for i := 0; i < 2; i++ {
		commit := call(handler.CommitAssistantLibraryFields, commitBody)
		if commit.Code != http.StatusOK || !strings.Contains(commit.Body.String(), `"G minor"`) ||
			strings.Contains(commit.Body.String(), "retired") {
			t.Fatalf("commit %d: %d %s", i, commit.Code, commit.Body.String())
		}
	}
	after, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range after.Entries {
		if entry.ID == entryID && (entry.Fields.Extra["key"][0] != "G minor" || entry.Fields.Extra["retired"][0] != "old words") {
			t.Fatalf("stored extra: %+v", entry.Fields.Extra)
		}
	}

	// Undeclared ids and a second line on a text field are refused.
	for _, bad := range []string{`{"extra":{"retired":["new"]}}`, `{"extra":{"mood":["dark"]}}`, `{"extra":{"key":["F","G"]}}`} {
		if response := call(handler.ReviewAssistantLibraryFields, `{"if_fields_revision":3,"patch":`+bad+`}`); response.Code == http.StatusOK {
			t.Fatalf("%s accepted: %s", bad, response.Body.String())
		}
	}
}

func librarySummaryWords(t *testing.T, handler *Handler, homeID string) projectlibrary.Words {
	t.Helper()
	response := httptest.NewRecorder()
	handler.GetAssistantLibrarySummary(response, assistantProgramRequest(http.MethodGet, "/library/summary", homeID, ""))
	var body struct {
		Words projectlibrary.Words `json:"words"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("summary: %d %s", response.Code, response.Body.String())
	}
	return body.Words
}
