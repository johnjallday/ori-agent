package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type libraryOfferResolverFunc func(context.Context, string, string, string) (string, string, error)

func (fn libraryOfferResolverFunc) PortfolioRoot(ctx context.Context, userID, offerID, homeID string) (string, string, error) {
	return fn(ctx, userID, offerID, homeID)
}

type libraryTestPicker struct{ path string }

func (p libraryTestPicker) Available() bool { return true }
func (p libraryTestPicker) Choose(context.Context, string) (string, bool, error) {
	return p.path, true, nil
}

func libraryAction(t *testing.T, handler func(http.ResponseWriter, *http.Request), homeID, rootID, body string) (int, map[string]any) {
	t.Helper()
	request := assistantProgramRequest(http.MethodPost, "/library/action", homeID, body)
	request.SetPathValue("rootID", rootID)
	response := httptest.NewRecorder()
	handler(response, request)
	result := map[string]any{}
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode %s: %v", response.Body.String(), err)
		}
	}
	return response.Code, result
}

func TestAssistantLibraryActions_ExactInstalledProviderAndReviewedConsent(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	state := station.GetAssistantProgramState()
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: state.Key.ProgramID,
		StationName: "Portfolio Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "guide", Label: "Guide", Required: true,
			Primary: true, SystemPrompt: "Coordinate reviewed Home work.", Skills: []string{"music-project-management"}}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168,
			MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
			ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
		}},
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
		Skills: []string{"music-project-management"}, ComponentFingerprint: owner.ComponentFingerprint, InstalledAt: station.CreatedAt.Add(-time.Minute),
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
	if !handler.assistantLibraryProviderEvidence(scope, station) {
		fresh, _ := store.Get(station.ID)
		if !handler.assistantLibraryProviderEvidence(scope, fresh) {
			t.Fatal("exact installed package and Home fingerprint did not match")
		}
	}
	rootPath := filepath.Join(t.TempDir(), "Songs")
	if err := os.MkdirAll(filepath.Join(rootPath, "Track"), 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(rootPath, "Track", "Track.rpp")
	deeper := filepath.Join(rootPath, "Track", "Extra")
	if err := os.Mkdir(deeper, 0o750); err != nil {
		t.Fatal(err)
	}
	deepMarker := filepath.Join(deeper, "Extra.rpp")
	if err := os.WriteFile(deepMarker, []byte("test-only deep marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("test-only marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootPath, _ = filepath.EvalSymlinks(rootPath)
	handler.ConfigureAssistantLibraryRoots(libraryTestPicker{rootPath}, pathselection.NewStore(),
		filejanitor.RootGuards{DataDir: t.TempDir()})
	if handler.assistantLibraryRoots == nil {
		t.Fatal("host failed to wire native picker")
	}
	handler.SetAssistantLibraryPortfolioResolver(libraryOfferResolverFunc(func(_ context.Context, user, offer, homeID string) (string, string, error) {
		if user != station.OwnerUserID || homeID != station.ID || offer != "saved-offer" {
			return "", "", projectlibrary.ErrUnavailable
		}
		info, err := os.Lstat(rootPath)
		if err != nil {
			return "", "", err
		}
		identity, err := projectlibrary.DirectoryIdentity(info)
		return rootPath, identity, err
	}))
	if status, _ := libraryAction(t, handler.ReviewAssistantLibraryInitialize, project.ID, "", `{}`); status != http.StatusNotFound {
		t.Fatalf("linked project URL initialized owner catalog: %d", status)
	}
	if status, _ := libraryAction(t, handler.ReviewAssistantLibraryInitialize, station.ID, "", `{}`); status != http.StatusOK {
		t.Fatalf("initialize review = %d", status)
	}
	_, initReview := libraryAction(t, handler.ReviewAssistantLibraryInitialize, station.ID, "", `{}`)
	initToken, _ := initReview["token"].(string)
	if initToken == "" {
		t.Fatalf("missing reviewed initialization token: %+v", initReview)
	}
	if status, _ := libraryAction(t, handler.CommitAssistantLibraryInitialize, station.ID, "",
		`{"review_token":"`+initToken+`","idempotency_key":"init-http","confirm":true}`); status != http.StatusOK {
		t.Fatalf("reviewed initialize = %d", status)
	}
	if status, _ := libraryAction(t, handler.PickAssistantLibraryOfferRoot, station.ID, "", `{"offer_id":"foreign-offer"}`); status != http.StatusConflict {
		t.Fatalf("foreign offer provided root authority: %d", status)
	}
	pickedStatus, picked := libraryAction(t, handler.PickAssistantLibraryOfferRoot, station.ID, "", `{"offer_id":"saved-offer"}`)
	pickToken, _ := picked["selection_token"].(string)
	if pickedStatus != http.StatusOK || pickToken == "" {
		t.Fatalf("native picker: %d %+v", pickedStatus, picked)
	}
	doc, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := libraryAction(t, handler.ReviewAssistantLibraryRoot, station.ID, "",
		`{"selection_token":"`+pickToken+`","if_revision":`+strconv.FormatInt(doc.Revision, 10)+`,"path":"/tmp/foreign"}`); status != http.StatusBadRequest {
		t.Fatalf("typed path accepted by root review: %d", status)
	}
	status, rootReview := libraryAction(t, handler.ReviewAssistantLibraryRoot, station.ID, "",
		`{"selection_token":"`+pickToken+`","if_revision":`+strconv.FormatInt(doc.Revision, 10)+`}`)
	if status != http.StatusOK || rootReview["root_path"] != rootPath {
		t.Fatalf("root review did not disclose exact source: %d %+v", status, rootReview)
	}
	rootToken, _ := rootReview["token"].(string)
	if status, _ := libraryAction(t, handler.CommitAssistantLibraryRoot, station.ID, "",
		`{"review_token":"`+rootToken+`","idempotency_key":"root-http","confirm":false}`); status != http.StatusBadRequest {
		t.Fatalf("unconfirmed root granted: %d", status)
	}
	changed := installed
	changed.ComponentFingerprint = strings.Repeat("b", 64)
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{changed}})
	if status, _ := libraryAction(t, handler.CommitAssistantLibraryRoot, station.ID, "",
		`{"review_token":"`+rootToken+`","idempotency_key":"root-http","confirm":true}`); status != http.StatusConflict {
		t.Fatalf("changed fingerprint granted root: %d", status)
	}
	reinstalled := installed
	reinstalled.InstalledAt = station.CreatedAt.Add(time.Minute) // same fingerprint/generation, new install epoch
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{reinstalled}})
	if status, _ := libraryAction(t, handler.CommitAssistantLibraryRoot, station.ID, "",
		`{"review_token":"`+rootToken+`","idempotency_key":"root-http","confirm":true}`); status != http.StatusConflict {
		t.Fatalf("same-content reinstall inherited an old root review: %d", status)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{installed}})
	status, rootResult := libraryAction(t, handler.CommitAssistantLibraryRoot, station.ID, "",
		`{"review_token":"`+rootToken+`","idempotency_key":"root-http","confirm":true}`)
	rootID, _ := rootResult["root_id"].(string)
	if status != http.StatusOK || rootID == "" {
		t.Fatalf("reviewed root commit: %d %+v", status, rootResult)
	}
	doc, err = projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := libraryAction(t, handler.ReviewAssistantLibraryScan, station.ID, "foreign-root",
		`{"if_revision":`+strconv.FormatInt(doc.Revision, 10)+`}`); status != http.StatusNotFound {
		t.Fatalf("foreign root ID was not hidden: %d", status)
	}
	status, scanReview := libraryAction(t, handler.ReviewAssistantLibraryScan, station.ID, rootID,
		`{"if_revision":`+strconv.FormatInt(doc.Revision, 10)+`}`)
	scanToken, _ := scanReview["token"].(string)
	if status != http.StatusOK || scanToken == "" || scanReview["includes_scan"] != true {
		t.Fatalf("scan review: %d %+v", status, scanReview)
	}
	status, scan := libraryAction(t, handler.CommitAssistantLibraryScan, station.ID, rootID,
		`{"review_token":"`+scanToken+`","idempotency_key":"scan-http","confirm":true}`)
	if status != http.StatusOK || scan["status"] != "complete" || scan["entries_seen"].(float64) < 1 {
		t.Fatalf("scoped metadata scan: %d %+v", status, scan)
	}
	doc, err = projectlibrary.NewStore(store).Read(scope)
	if err != nil || len(doc.Entries) < 1 {
		t.Fatalf("scan not persisted: %+v %v", doc, err)
	}
	entryID := doc.Entries[0].ID
	activationEntryID := ""
	for _, entry := range doc.Entries {
		for _, observation := range entry.Observations {
			if observation.RelativeFolder == "Track" {
				activationEntryID = entry.ID
			}
		}
	}
	if activationEntryID == "" {
		t.Fatal("scanned Track was not cataloged")
	}
	folderStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler.SetWorkspaceStore(folderStore) // production supplies the canonical disk-folder resolver
	readActivation := func(homeID, id, query string) (int, string) {
		request := assistantProgramRequest(http.MethodGet, "/library/projects/"+id+"/activation"+query, homeID, "")
		request.SetPathValue("entryID", id)
		response := httptest.NewRecorder()
		handler.GetAssistantLibraryActivation(response, request)
		return response.Code, response.Body.String()
	}
	if code, _ := readActivation(project.ID, activationEntryID, ""); code != http.StatusNotFound {
		t.Fatalf("child project URL inspected Home eligibility: %d", code)
	}
	if code, _ := readActivation(station.ID, "absent", ""); code != http.StatusNotFound {
		t.Fatalf("foreign catalog entry inspected setup: %d", code)
	}
	if code, _ := readActivation(station.ID, activationEntryID, "?path=/tmp/foreign"); code != http.StatusNotFound {
		t.Fatalf("query path reached setup inspector: %d", code)
	}
	if code, body := readActivation(station.ID, activationEntryID, ""); code != http.StatusOK ||
		!strings.Contains(body, `"state":"project_provider_unavailable"`) || strings.Contains(body, rootPath) {
		t.Fatalf("inert missing-REAPER status: %d %s", code, body)
	}
	if current, readErr := projectlibrary.NewStore(store).Read(scope); readErr != nil || current.Revision != doc.Revision {
		t.Fatalf("eligibility mutated library revision: %d %v", current.Revision, readErr)
	}
	projectTeam := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: 1, Version: 1, ID: "reaper-song-team",
		Home: projecttemplates.AssistantProjectHomeReference{ProviderPluginID: scope.ProviderID,
			ProgramID: scope.ProgramID, HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "reaper-assistant", Label: "REAPER Assistant"}},
	}
	reaper := plugin.InstalledPlugin{Name: "reaper-plugin", Version: "0.9.0", Enabled: true, Generation: 1,
		ComponentFingerprint: strings.Repeat("b", 64), InstalledAt: station.CreatedAt.Add(-time.Minute),
		WorkspaceSurfaces: &plugin.SurfaceContribution{Protocol: plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures: []string{plugin.HostFeatureIndependentProgramHomesV1}},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "reaper-song", QualifiedID: "plugin:reaper-plugin:reaper-song", Version: 10,
			Template: projecttemplates.Template{ID: "plugin:reaper-plugin:reaper-song",
				PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", PluginVersion: "0.9.0",
					BlueprintID: "reaper-song", BlueprintVersion: 10},
				GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: projecttemplates.SplitGroupRequirementSchemaVersion,
					Policy: projecttemplates.GroupPolicyRequired, AssistantProjectID: projectTeam.ID},
				AssistantProject: projectTeam,
				ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{SchemaVersion: 1,
					SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionExistingProject},
					AttachExisting: &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}}}},
		}},
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{installed, reaper}})
	if code, body := readActivation(station.ID, activationEntryID, ""); code != http.StatusOK ||
		!strings.Contains(body, `"state":"review_available"`) || !strings.Contains(body, `"project_files":["Track.rpp"]`) ||
		!strings.Contains(body, `"project_role_labels":["REAPER Assistant"]`) || strings.Contains(body, rootPath) {
		t.Fatalf("reviewable project eligibility: %d %s", code, body)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{installed}})
	studio := func(handle http.HandlerFunc, homeID, catalogID, sessionID, body string) (int, string) {
		method := http.MethodPost
		if body == "" {
			method = http.MethodGet
		}
		request := assistantProgramRequest(method, "/library/studio", homeID, body)
		request.SetPathValue("entryID", catalogID)
		request.SetPathValue("sessionID", sessionID)
		response := httptest.NewRecorder()
		handle(response, request)
		return response.Code, response.Body.String()
	}
	goalBody := `{"if_entry_revision":1,"goal":{"goal":"Listen to Album-3","desired_outcome":"Choose a vocal take","time_minutes":30}}`
	if code, _ := studio(handler.ReviewAssistantStudioGoal, project.ID, activationEntryID, "", goalBody); code != http.StatusNotFound {
		t.Fatalf("linked project URL created a Home session: %d", code)
	}
	if code, _ := studio(handler.ReviewAssistantStudioGoal, station.ID, activationEntryID, "", `{"if_entry_revision":1,"goal":{"goal":"a","goal":"b"}}`); code != http.StatusBadRequest {
		t.Fatalf("duplicate JSON key accepted in goal: %d", code)
	}
	code, reviewedGoal := studio(handler.ReviewAssistantStudioGoal, station.ID, activationEntryID, "", goalBody)
	var goalReview projectlibrary.SessionReview
	if json.Unmarshal([]byte(reviewedGoal), &goalReview) != nil || code != http.StatusOK || goalReview.Token == "" {
		t.Fatalf("review goal: %d %s", code, reviewedGoal)
	}
	if code, _ := studio(handler.ListAssistantStudioSessions, station.ID, activationEntryID, "", ""); code != http.StatusOK {
		t.Fatalf("review hid catalog-only sessions: %d", code)
	}
	commitGoal := `{"if_entry_revision":1,"goal":{"goal":"Listen to Album-3","desired_outcome":"Choose a vocal take","time_minutes":30},"review_token":"` + goalReview.Token + `","idempotency_key":"http-goal","confirm":true}`
	code, savedGoal := studio(handler.CommitAssistantStudioGoal, station.ID, activationEntryID, "", commitGoal)
	if code != http.StatusOK || !strings.Contains(savedGoal, `"state":"accepted"`) || !strings.Contains(savedGoal, `"author":"local"`) {
		t.Fatalf("accepted user goal: %d %s", code, savedGoal)
	}
	if code, replay := studio(handler.CommitAssistantStudioGoal, station.ID, activationEntryID, "", commitGoal); code != http.StatusOK || !strings.Contains(replay, `"replay":true`) {
		t.Fatalf("goal replay: %d %s", code, replay)
	}
	recapBody := `{"if_session_revision":1,"if_fields_revision":0,"recap":{"recap":"Listened to the chorus","next_action":"Record scratch vocal","update_project_next_action":true}}`
	code, reviewedRecap := studio(handler.ReviewAssistantStudioRecap, station.ID, activationEntryID, goalReview.Session.ID, recapBody)
	var recapReview projectlibrary.SessionReview
	if json.Unmarshal([]byte(reviewedRecap), &recapReview) != nil || code != http.StatusOK || recapReview.Token == "" {
		t.Fatalf("review recap: %d %s", code, reviewedRecap)
	}
	commitRecap := `{"if_session_revision":1,"if_fields_revision":0,"recap":{"recap":"Listened to the chorus","next_action":"Record scratch vocal","update_project_next_action":true},"review_token":"` + recapReview.Token + `","idempotency_key":"http-recap","confirm":true}`
	if code, saved := studio(handler.CommitAssistantStudioRecap, station.ID, activationEntryID, goalReview.Session.ID, commitRecap); code != http.StatusOK || !strings.Contains(saved, `"next_action":"Record scratch vocal"`) {
		t.Fatalf("atomic reviewed recap: %d %s", code, saved)
	}
	if code, replay := studio(handler.CommitAssistantStudioRecap, station.ID, activationEntryID, goalReview.Session.ID, commitRecap); code != http.StatusOK || !strings.Contains(replay, `"replay":true`) {
		t.Fatalf("recap replay: %d %s", code, replay)
	}
	if code, _ := studio(handler.ReviewAssistantStudioRecap, station.ID, activationEntryID, "foreign-session", recapBody); code != http.StatusNotFound {
		t.Fatalf("foreign session ID inspected: %d", code)
	}
	if code, body := studio(handler.GetAssistantStudioSession, station.ID, activationEntryID, goalReview.Session.ID, ""); code != http.StatusOK || !strings.Contains(body, `"recap":"Listened to the chorus"`) {
		t.Fatalf("owner full session detail: %d %s", code, body)
	}
	if code, _ := studio(handler.GetAssistantStudioSession, station.ID, entryID, goalReview.Session.ID, ""); code != http.StatusNotFound {
		t.Fatalf("another catalog entry read this session: %d", code)
	}
	resume := func(homeID string) (int, string) {
		response := httptest.NewRecorder()
		handler.GetAssistantStudioResume(response, assistantProgramRequest(http.MethodGet, "/library/resume", homeID, ""))
		return response.Code, response.Body.String()
	}
	if code, _ := resume(project.ID); code != http.StatusNotFound {
		t.Fatalf("child project URL read Home resume: %d", code)
	}
	if code, body := resume(station.ID); code != http.StatusOK ||
		!strings.Contains(body, `"project_next_action":"Record scratch vocal"`) ||
		!strings.Contains(body, `"recap":"Listened to the chorus"`) || strings.Contains(body, rootPath) {
		t.Fatalf("bounded Home resume: %d %s", code, body)
	}
	fieldRevision := doc.Entries[0].Fields.Revision
	patch := `{"stage":"mixing","next_action":"Print stems","priority":0}`
	reviewBody := `{"if_fields_revision":` + strconv.FormatInt(fieldRevision, 10) + `,"patch":` + patch + `}`
	fieldRequest := assistantProgramRequest(http.MethodPost, "/library/fields/review", station.ID, reviewBody)
	fieldRequest.SetPathValue("entryID", entryID)
	fieldRecorder := httptest.NewRecorder()
	handler.ReviewAssistantLibraryFields(fieldRecorder, fieldRequest)
	var fieldReview map[string]any
	if err := json.Unmarshal(fieldRecorder.Body.Bytes(), &fieldReview); err != nil || fieldRecorder.Code != http.StatusOK {
		t.Fatalf("manual review: %d %+v %v", fieldRecorder.Code, fieldReview, err)
	}
	fieldToken, _ := fieldReview["token"].(string)
	commitBody := `{"if_fields_revision":` + strconv.FormatInt(fieldRevision, 10) + `,"patch":` + patch + `,"review_token":"` + fieldToken + `","idempotency_key":"fields-http","confirm":true}`
	for i := 0; i < 2; i++ {
		commit := assistantProgramRequest(http.MethodPost, "/library/fields/commit", station.ID, commitBody)
		commit.SetPathValue("entryID", entryID)
		result := httptest.NewRecorder()
		handler.CommitAssistantLibraryFields(result, commit)
		if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"next_action":"Print stems"`) ||
			!strings.Contains(result.Body.String(), `"replay":`+strconv.FormatBool(i != 0)) {
			t.Fatalf("reviewed field edit/replay %d: %d %s", i, result.Code, result.Body.String())
		}
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "test-only marker" {
		t.Fatalf("source file changed: %q %v", contents, err)
	}
	// A bound Manager can suggest a Home note but cannot confirm it. The
	// owner-only HTTP routes must recheck current proposal and use exactly
	// the same field review/commit receipts as the manual editor above.
	if err := store.Update(station.ID, func(home *workspace.Workspace) error {
		home.AgentInstances = append(home.AgentInstances, workspace.AgentInstance{ID: "http-manager", Name: "Guide", RoleID: "guide"})
		state := home.GetAssistantProgramState()
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{
			RoleID: "guide", AgentInstanceID: "http-manager", AgentName: "Guide",
		}}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceAgent(station.ID, "Guide", &agent.Agent{}); err != nil {
		t.Fatal(err)
	}
	proposalStore := projectlibrary.NewStore(store).WithProviderEvidence(handler.assistantLibraryProviderEvidence)
	current, err := proposalStore.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	var proposalFieldsRevision int64
	for _, row := range current.Entries {
		if row.ID == entryID {
			proposalFieldsRevision = row.Fields.Revision
		}
	}
	proposal, replay, err := proposalStore.ProposeNextAction(projectlibrary.ManagerAuthority{
		HomeID: station.ID, AgentInstanceID: "http-manager", AgentName: "Guide",
	}, entryID, proposalFieldsRevision, "Check the chorus edit", "Untrusted artist note", "http-manager-suggestion")
	if err != nil || replay {
		t.Fatalf("bound Manager proposal: %+v %v %v", proposal, replay, err)
	}
	proposalList := httptest.NewRecorder()
	handler.ListAssistantLibraryProposals(proposalList, assistantProgramRequest(http.MethodGet, "/library/proposals", station.ID, ""))
	if proposalList.Code != http.StatusOK || !strings.Contains(proposalList.Body.String(), `"status":"ready"`) ||
		!strings.Contains(proposalList.Body.String(), "Check the chorus edit") {
		t.Fatalf("owner could not list an inert Manager suggestion: %d %s", proposalList.Code, proposalList.Body.String())
	}
	proposalID := proposal.ID
	proposeReview := func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("proposalID", proposalID)
		handler.ReviewAssistantLibraryProposal(w, r)
	}
	proposeCommit := func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("proposalID", proposalID)
		handler.CommitAssistantLibraryProposal(w, r)
	}
	if code, _ := libraryAction(t, proposeReview, project.ID, "", `{}`); code != http.StatusNotFound {
		t.Fatalf("child workspace accessed Home proposal review: %d", code)
	}
	if code, _ := libraryAction(t, proposeReview, station.ID, "", `{"next_action":"Forged from browser"}`); code != http.StatusBadRequest {
		t.Fatalf("browser changed stored suggestion in owner review: %d", code)
	}
	if code, _ := libraryAction(t, proposeCommit, station.ID, "", `{"confirm":true,"review_token":"forged","idempotency_key":"forged","patch":{"next_action":"Forged"}}`); code != http.StatusBadRequest {
		t.Fatalf("browser supplied a different proposal patch: %d", code)
	}
	if code, _ := libraryAction(t, proposeCommit, station.ID, "", `{"confirm":true,"review_token":"forged","idempotency_key":"forged"}`); code != http.StatusConflict {
		t.Fatalf("forged owner commit accepted: %d", code)
	}
	code, ownerReview := libraryAction(t, proposeReview, station.ID, "", `{}`)
	if code != http.StatusOK || ownerReview["before"].(map[string]any)["next_action"] != "Print stems" ||
		ownerReview["after"].(map[string]any)["next_action"] != "Check the chorus edit" {
		t.Fatalf("owner field review was not proposal-derived: %d %+v", code, ownerReview)
	}
	if code, _ := libraryAction(t, proposeCommit, station.ID, "", `{"confirm":false,"review_token":"`+ownerReview["token"].(string)+`","idempotency_key":"http-proposal"}`); code != http.StatusBadRequest {
		t.Fatalf("unconfirmed proposal committed: %d", code)
	}
	for i := 0; i < 2; i++ {
		code, result := libraryAction(t, proposeCommit, station.ID, "", `{"confirm":true,"review_token":"`+ownerReview["token"].(string)+`","idempotency_key":"http-proposal"}`)
		if code != http.StatusOK || result["replay"] != (i != 0) ||
			result["fields"].(map[string]any)["next_action"] != "Check the chorus edit" {
			t.Fatalf("owner proposal commit/replay %d: %d %+v", i, code, result)
		}
	}
	rootsResponse := httptest.NewRecorder()
	handler.ListAssistantLibraryRoots(rootsResponse,
		assistantProgramRequest(http.MethodGet, "/library/roots?offset=0", station.ID, ""))
	if rootsResponse.Code != http.StatusOK || !strings.Contains(rootsResponse.Body.String(), rootPath) ||
		!strings.Contains(rootsResponse.Body.String(), `"has_scopes":true`) ||
		!strings.Contains(rootsResponse.Body.String(), `"last_scan"`) || len(rootsResponse.Body.Bytes()) > 128<<10 {
		t.Fatalf("bounded owner roots: %d %s", rootsResponse.Code, rootsResponse.Body.String())
	}
	scopeList := func(homeID, rootID, query string) (int, map[string]any, int) {
		request := assistantProgramRequest(http.MethodGet, "/library/scopes?"+query, homeID, "")
		request.SetPathValue("rootID", rootID)
		response := httptest.NewRecorder()
		handler.ListAssistantLibraryScanScopes(response, request)
		var body map[string]any
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code, body, response.Body.Len()
	}
	beforeScopes, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	query := "if_revision=" + strconv.FormatInt(beforeScopes.Revision, 10)
	if code, _, _ := scopeList(project.ID, rootID, query); code != http.StatusNotFound {
		t.Fatalf("foreign project inspected scopes: %d", code)
	}
	if code, _, _ := scopeList(station.ID, "foreign-root", query); code != http.StatusNotFound {
		t.Fatalf("foreign root inspected scopes: %d", code)
	}
	for _, invalid := range []string{"", query + "&offset=-1", query + "&if_revision=2", query + "&path=Track"} {
		if code, _, _ := scopeList(station.ID, rootID, invalid); code != http.StatusBadRequest {
			t.Fatalf("malformed scope page %q: %d", invalid, code)
		}
	}
	code, scopePage, size := scopeList(station.ID, rootID, query)
	if code != http.StatusOK || size >= 128<<10 || scopePage["total"].(float64) < 2 {
		t.Fatalf("known scopes not bounded and readable: %d %+v size=%d", code, scopePage, size)
	}
	var deepScope string
	for _, item := range scopePage["rows"].([]any) {
		choice := item.(map[string]any)
		if choice["relative_folder"] == filepath.Join("Track", "Extra") {
			deepScope = choice["id"].(string)
		}
		if _, exposed := choice["file_identity"]; exposed {
			t.Fatal("scope page disclosed private filesystem identity")
		}
	}
	if deepScope == "" {
		t.Fatalf("deeper verified scope missing: %+v", scopePage)
	}
	if code, _ := libraryAction(t, handler.ReviewAssistantLibraryScan, station.ID, rootID,
		`{"if_revision":`+strconv.FormatInt(beforeScopes.Revision, 10)+`,"scope_id":"../typed-path"}`); code != http.StatusConflict {
		t.Fatalf("typed relative path accepted as scope: %d", code)
	}
	code, deeperReview := libraryAction(t, handler.ReviewAssistantLibraryScan, station.ID, rootID,
		`{"if_revision":`+strconv.FormatInt(beforeScopes.Revision, 10)+`,"scope_id":"`+deepScope+`"}`)
	if code != http.StatusOK || deeperReview["relative_folder"] != filepath.Join("Track", "Extra") {
		t.Fatalf("follow-up review did not name selected folder: %d %+v", code, deeperReview)
	}
	if code, _, _ := scopeList(station.ID, rootID, query); code != http.StatusConflict {
		t.Fatalf("stale scope page accepted after a review: %d", code)
	}
	reviewedButNotScanned, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil || len(reviewedButNotScanned.Entries) != 2 {
		t.Fatalf("scope listing or review scanned without approval: %+v %v", reviewedButNotScanned, err)
	}
	code, scopedScan := libraryAction(t, handler.CommitAssistantLibraryScan, station.ID, rootID,
		`{"review_token":"`+deeperReview["token"].(string)+`","scope_id":"`+deepScope+`","idempotency_key":"scoped-http","confirm":true}`)
	if code != http.StatusOK || scopedScan["status"] != "complete" {
		t.Fatalf("selected follow-up was not scanned: %d %+v", code, scopedScan)
	}
	if data, err := os.ReadFile(deepMarker); err != nil || string(data) != "test-only deep marker" {
		t.Fatalf("follow-up changed source content: %q %v", data, err)
	}
	doc, err = projectlibrary.NewStore(store).Read(scope)
	if err != nil || len(doc.Entries) != 3 {
		t.Fatalf("follow-up did not add only the deeper song: %+v %v", doc, err)
	}
	var queueCatalogIDs []string
	for _, entry := range doc.Entries {
		if entry.Link == nil {
			queueCatalogIDs = append(queueCatalogIDs, entry.ID)
		}
	}
	if len(queueCatalogIDs) < 2 {
		t.Fatalf("queue fixture needs two catalog-only entries: %+v", doc.Entries)
	}
	queueIDs, err := json.Marshal(queueCatalogIDs[:2])
	if err != nil {
		t.Fatal(err)
	}
	queueBody := `{"ids":` + string(queueIDs) + `,"request_key":"owner-queue"}`
	if code, _ := libraryAction(t, handler.StartAssistantLibraryQueue, project.ID, "", queueBody); code != http.StatusNotFound {
		t.Fatalf("child URL wrote Home queue: %d", code)
	}
	code, started := libraryAction(t, handler.StartAssistantLibraryQueue, station.ID, "", queueBody)
	queueResult, _ := started["queue"].(map[string]any)
	queueID, _ := queueResult["id"].(string)
	if code != http.StatusOK || queueID == "" {
		t.Fatalf("owner queue start: %d %+v", code, started)
	}
	getQueue := httptest.NewRecorder()
	handler.GetAssistantLibraryQueue(getQueue, assistantProgramRequest(http.MethodGet, "/library/queue", station.ID, ""))
	if getQueue.Code != http.StatusOK || !strings.Contains(getQueue.Body.String(), queueID) {
		t.Fatalf("owner queue read: %d %s", getQueue.Code, getQueue.Body.String())
	}
	queueAction := func(fn func(http.ResponseWriter, *http.Request), body string) (int, string) {
		t.Helper()
		request := assistantProgramRequest(http.MethodPost, "/library/queue/"+queueID, station.ID, body)
		request.SetPathValue("queueID", queueID)
		response := httptest.NewRecorder()
		fn(response, request)
		return response.Code, response.Body.String()
	}
	if code, _ := queueAction(handler.ProgressAssistantLibraryQueue, `{"entry_id":"`+queueCatalogIDs[0]+`","action":"connected","if_revision":1,"request_key":"forged-child"}`); code != http.StatusConflict {
		t.Fatalf("HTTP caller fabricated connected child: %d", code)
	}
	if code, _ := queueAction(handler.DiscardAssistantLibraryQueue, `{"if_revision":1,"request_key":"discard","confirm":false}`); code != http.StatusBadRequest {
		t.Fatalf("queue discard without owner confirmation: %d", code)
	}
	if code, _ := queueAction(handler.DiscardAssistantLibraryQueue, `{"if_revision":1,"request_key":"discard","confirm":true}`); code != http.StatusOK {
		t.Fatalf("owner could not discard navigation-only queue: %d", code)
	}
	doc, err = projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := scopeList(station.ID, rootID, "if_revision="+strconv.FormatInt(doc.Revision, 10)+"&offset=100"); code != http.StatusOK {
		t.Fatalf("out-of-page read should be inert: %d", code)
	}
	doc, err = projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{reinstalled}})
	if _, err := handler.assistantLibraryRoots.VerifyConnectedRoot(scope, rootID); !errors.Is(err, projectlibrary.ErrUnavailable) {
		t.Fatalf("same-content reinstall regained an approved root: %v", err)
	}
	if status, _ := libraryAction(t, handler.ReviewAssistantLibraryScan, station.ID, rootID,
		`{"if_revision":`+strconv.FormatInt(doc.Revision, 10)+`}`); status != http.StatusConflict {
		t.Fatalf("same-content reinstall could start a scan: %d", status)
	}
	if saved, err := projectlibrary.NewStore(store).Read(scope); err != nil || len(saved.Entries) != len(doc.Entries) {
		t.Fatalf("provider reinstall hid historical projects: %+v %v", saved, err)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{installed}})
	status, revoke := libraryAction(t, handler.ReviewAssistantLibraryRevoke, station.ID, rootID,
		`{"if_revision":`+strconv.FormatInt(doc.Revision, 10)+`}`)
	revokeToken, _ := revoke["token"].(string)
	if status != http.StatusOK || revokeToken == "" || revoke["entry_count"] != float64(2) {
		t.Fatalf("revoke impact: %d %+v", status, revoke)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{}) // Revocation remains available without provider.
	status, revoked := libraryAction(t, handler.CommitAssistantLibraryRevoke, station.ID, rootID,
		`{"review_token":"`+revokeToken+`","idempotency_key":"revoke-http","confirm":true}`)
	if status != http.StatusOK || revoked["root_id"] != rootID {
		t.Fatalf("providerless root revocation: %d %+v", status, revoked)
	}
}

func TestAssistantLibraryActions_StrictRequestJSON(t *testing.T) {
	handler, _, station, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	for _, input := range []string{`{"selection_token":"one","selection_token":"two","if_revision":1}`,
		`{"selection_token":"x","if_revision":1}{}`, `null`, `[]`, `{"path":"/tmp/x"}`,
		strings.Repeat("x", maxLibraryActionBytes+1)} {
		code, _ := libraryAction(t, handler.ReviewAssistantLibraryRoot, station.ID, "", input)
		if code != http.StatusBadRequest {
			t.Fatalf("unsafe JSON %q accepted with status %d", input[:min(len(input), 60)], code)
		}
	}
}
