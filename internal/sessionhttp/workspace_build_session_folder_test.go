package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeBuildFolders stands in for the composer's observation service: it
// answers the chooser, hands back one scripted observation, and remembers
// how it was asked.
type fakeBuildFolders struct {
	view        personalassistant.FolderDigestView
	observation *foldercontext.Observation
	observeErr  error
	root        string
	rootErr     error
	observed    []foldercontext.Target
	modes       []string
	choices     []string
	resolved    []string
	// recognition is what Recognize says about the observation; nil with no
	// error is a folder service that has nothing to say.
	recognition  *personalassistant.BuildFolderRecognition
	recognizeErr error
	recognized   []string
	// plan is what Plan reads for a recognized folder; planned records the
	// workspace name of every read.
	plan    *personalassistant.BuildFolderPlan
	planErr error
	planned []string
}

// Plan answers with plan (nil with no error: nothing to plan) and records the
// workspace name each read was asked for.
func (f *fakeBuildFolders) Plan(_ context.Context, target foldercontext.Target, observationID, candidateID, workspaceName string) (*personalassistant.BuildFolderPlan, error) {
	f.planned = append(f.planned, workspaceName)
	if f.planErr != nil {
		return nil, f.planErr
	}
	if f.plan == nil {
		return nil, nil
	}
	copied := *f.plan
	copied.Lines = append([]personalassistant.FolderPlanLine(nil), f.plan.Lines...)
	copied.Needs = append([]personalassistant.BuildFolderNeed(nil), f.plan.Needs...)
	return &copied, nil
}

func (f *fakeBuildFolders) Recognize(_ context.Context, _ foldercontext.Target, id string) (*personalassistant.BuildFolderRecognition, error) {
	f.recognized = append(f.recognized, id)
	if f.recognizeErr != nil {
		return nil, f.recognizeErr
	}
	if f.recognition == nil {
		return nil, nil
	}
	copied := *f.recognition
	return &copied, nil
}

// reaperObservation is a REAPER song folder as the composer observes it: the
// root is the project, three session files mark it.
func reaperObservation() *foldercontext.Observation {
	return &foldercontext.Observation{
		Version: foldercontext.Version, ID: "obs-reaper", Folder: "Ori Music Demo",
		ScannedAt: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), Entries: 5, Files: 5,
		Kinds:    []foldercontext.Kind{{Name: "rpp", Count: 3}, {Name: "wav", Count: 2}},
		Projects: []foldercontext.Project{{ID: "candidate-0", Name: "Ori Music Demo", Files: 5, Root: true, Marker: "REAPER session", LastChanged: "2026-10-18"}},
		Coverage: foldercontext.Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3},
	}
}

// reaperRecognition is what the folder digest says about reaperObservation.
func reaperRecognition() *personalassistant.BuildFolderRecognition {
	return &personalassistant.BuildFolderRecognition{
		Verdict: personalassistant.BuildFolderProject, Shape: "audio", ProjectCount: 1, CandidateID: "candidate-0",
		Label: "REAPER project", MarkerNoun: "session", MarkerCount: 3,
		BlueprintID: "reaper-song", BlueprintLabel: "REAPER song",
		IntegrationKey: "ori_reaper", IntegrationName: "REAPER",
		HomeProviderKey: "music_project_management", HomeProviderName: "Music Project Management", HomeName: "Music Production Home",
	}
}

func (f *fakeBuildFolders) Choices(context.Context, string) (personalassistant.FolderDigestView, error) {
	return f.view, nil
}

func (f *fakeBuildFolders) Observe(_ context.Context, target foldercontext.Target, mode, choice string) (*foldercontext.Observation, error) {
	f.observed = append(f.observed, target)
	f.modes = append(f.modes, mode)
	f.choices = append(f.choices, choice)
	if f.observeErr != nil {
		return nil, f.observeErr
	}
	if f.observation == nil {
		return nil, nil
	}
	copied := *f.observation
	return &copied, nil
}

func (f *fakeBuildFolders) ResolveRoot(_ context.Context, _ foldercontext.Target, id string) (string, error) {
	f.resolved = append(f.resolved, id)
	if f.rootErr != nil {
		return "", f.rootErr
	}
	return f.root, nil
}

// testObservation is a valid, path-free snapshot of a folder called Elite School.
func testObservation() *foldercontext.Observation {
	return &foldercontext.Observation{
		Version: foldercontext.Version, ID: "obs-1", Folder: "Elite School",
		ScannedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), Entries: 3, Files: 2,
		Kinds:    []foldercontext.Kind{{Name: "md", Count: 2}},
		Projects: []foldercontext.Project{{ID: "candidate-0", Name: "Elite School", Files: 2, Root: true, Marker: "package.json", LastChanged: "2026-10-10"}},
		Coverage: foldercontext.Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3},
	}
}

func (f *buildFixture) withFolders(t *testing.T, folders *fakeBuildFolders) {
	t.Helper()
	f.handler.workspaceBuild.Folders = folders
	f.assistant.HQWorkspaceID = "hq-1"
}

func (f *buildFixture) attachFolder(t *testing.T, build personalassistant.WorkspaceBuildSession, body map[string]any) (int, map[string]any) {
	t.Helper()
	body["version"] = build.Version
	return f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/folder", body)
}

func lastEntry(session personalassistant.WorkspaceBuildSession) personalassistant.BuildTranscriptEntry {
	if len(session.Transcript) == 0 {
		return personalassistant.BuildTranscriptEntry{}
	}
	return session.Transcript[len(session.Transcript)-1]
}

func TestWorkspaceBuildFolder_TheChooserIsTheComposersAndOnlyWhenWired(t *testing.T) {
	f := newBuildFixture(t, true)
	if _, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil); body["folders"] != false {
		t.Fatalf("unwired folders must not be offered: %v", body)
	}
	if status, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/folder-choices", nil); status != http.StatusOK || body["available"] != false {
		t.Fatalf("unwired choices: %d %v", status, body)
	}
	f.withFolders(t, &fakeBuildFolders{view: personalassistant.FolderDigestView{
		Chips:           []personalassistant.FolderChip{{ID: "documents", Label: "Documents"}, {ID: "desktop", Label: "Desktop"}},
		PickerAvailable: false, PickerNote: "Pick a folder from the list for now.",
	}})
	if _, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil); body["folders"] != true {
		t.Fatalf("wired folders are offered: %v", body)
	}
	status, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/folder-choices", nil)
	chips, _ := body["chips"].([]any)
	if status != http.StatusOK || body["available"] != true || len(chips) != 2 || body["picker_available"] != false || body["picker_note"] != "Pick a folder from the list for now." {
		t.Fatalf("choices: %d %v", status, body)
	}
	if status, _ := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/folder-choices", nil); status != http.StatusMethodNotAllowed {
		t.Fatalf("choices are read only: %d", status)
	}
}

func TestWorkspaceBuildFolder_AttachKeepsTheObservationAndTheModelReadsIt(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	folders := &fakeBuildFolders{observation: testObservation()}
	f.withFolders(t, folders)
	build := f.start(t)

	status, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("attach: %d %v", status, body)
	}
	attached := sessionOf(t, body)
	if attached.Folder == nil || attached.Folder.Observation.Folder != "Elite School" || attached.Version != build.Version+1 {
		t.Fatalf("attached session %+v", attached)
	}
	if last := lastEntry(attached); last.Role != personalassistant.BuildRoleForm || !strings.Contains(last.Text, "You attached the folder “Elite School”") {
		t.Fatalf("the attach is said as the user's own act: %+v", last)
	}
	// The selection is scoped to this build as the draft, under the
	// assistant's HQ and profile, so Create resolves exactly what was observed.
	if len(folders.observed) != 1 || folders.observed[0] != (foldercontext.Target{UserID: "local", WorkspaceID: "hq-1", AgentName: "luna", DraftID: "build:" + build.ID}) ||
		folders.modes[0] != "chip" || folders.choices[0] != "documents" {
		t.Fatalf("observed %+v %v %v", folders.observed, folders.modes, folders.choices)
	}
	// A stale caller is refused before any dialog opens.
	if status, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "desktop"}); status != http.StatusConflict || body["code"] != "version" {
		t.Fatalf("stale version: %d %v", status, body)
	}

	_, body = f.turn(t, attached, map[string]any{"text": "a workspace for this"})
	got := sessionOf(t, body)
	if got.Folder == nil || got.Draft.Name != "Newsletter Desk" {
		t.Fatalf("the folder survives a turn: %+v", got)
	}
	system := f.provider.systems[0]
	for _, want := range []string{"<folder_observation>", `"folder":"Elite School"`, `"marker":"package.json"`, `"last_changed":"2026-10-10"`, `"attached_folder":"Elite School"`, "Never set ask_folder"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, `"id":"obs-1"`) || strings.Contains(system, "candidate-0") {
		t.Fatalf("observation ids never reach the model:\n%s", system)
	}
	var heard bool
	for _, message := range f.provider.messages[0] {
		if strings.Contains(message.Content, "[I edited the form myself] You attached the folder “Elite School”") {
			heard = true
		}
	}
	if !heard {
		t.Fatalf("the model hears the attach as a form edit: %+v", f.provider.messages[0])
	}
}

// The verdict the folder digest reached on the same scan is kept with the
// folder: a REAPER song folder is a recognized project, anything else says it
// is not one, and a folder that cannot be recognized right now still attaches.
func TestWorkspaceBuildFolder_AttachKeepsWhatTheFolderWasRecognizedAs(t *testing.T) {
	cases := []struct {
		name        string
		observation *foldercontext.Observation
		recognition *personalassistant.BuildFolderRecognition
		err         error
		want        string
	}{
		{"a REAPER song folder", reaperObservation(), reaperRecognition(), nil, personalassistant.BuildFolderProject},
		{"an unrecognized folder", testObservation(), &personalassistant.BuildFolderRecognition{Verdict: personalassistant.BuildFolderUnrecognized}, nil, personalassistant.BuildFolderUnrecognized},
		{"a recognition that failed", testObservation(), nil, personalassistant.ErrFolderSelection, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBuildFixture(t, true)
			folders := &fakeBuildFolders{observation: tc.observation, recognition: tc.recognition, recognizeErr: tc.err}
			f.withFolders(t, folders)
			build := f.start(t)
			status, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
			if status != http.StatusOK {
				t.Fatalf("attach: %d %v", status, body)
			}
			if len(folders.recognized) != 1 || folders.recognized[0] != tc.observation.ID {
				t.Fatalf("recognized %v, want the observation just made", folders.recognized)
			}
			attached := sessionOf(t, body)
			if attached.Folder == nil {
				t.Fatalf("the folder is attached whatever it was recognized as: %+v", attached)
			}
			got := attached.Folder.Recognition
			switch {
			case tc.want == "" && got != nil:
				t.Fatalf("a failed recognition is not guessed: %+v", got)
			case tc.want != "" && (got == nil || got.Verdict != tc.want):
				t.Fatalf("recognition = %+v, want %s", got, tc.want)
			}
			stored, err := f.store.Read(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			if kept := stored.Session(build.ID).Folder.Recognition; (kept == nil) != (tc.want == "") || (kept != nil && *kept != *got) {
				t.Fatalf("stored recognition %+v, served %+v", kept, got)
			}
			if tc.want == personalassistant.BuildFolderProject {
				session, _ := body["session"].(map[string]any)
				folder, _ := session["folder"].(map[string]any)
				served, _ := folder["recognition"].(map[string]any)
				if served["label"] != "REAPER project" || served["marker_count"] != float64(3) || served["blueprint_id"] != "reaper-song" || served["home_name"] != "Music Production Home" {
					t.Fatalf("the pane reads the recognition from the session: %v", folder)
				}
			}
		})
	}
}

func TestWorkspaceBuildFolder_TheModelsFolderQuestionIsRefusedOnceOneIsAttached(t *testing.T) {
	ask := `{"say":"Code Project fits.","ask":{"question":"Link the repository you already have?","choices":[],"allow_free_text":true},
"patch":{"set":["blueprint_id","name","description","ask_folder"],"blueprint_id":"code-project","name":"Field Notes","description":"Tracks the code.","inputs":[],"parent_id":"","ask_folder":true,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`
	f := newBuildFixture(t, true, ask)
	f.withFolders(t, &fakeBuildFolders{observation: testObservation()})
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "picker"})
	attached := sessionOf(t, body)
	_, body = f.turn(t, attached, map[string]any{"text": "my code"})
	got := sessionOf(t, body)
	if got.AskFolder || got.PendingQuestion == nil || len(got.PendingQuestion.Choices) != 0 {
		t.Fatalf("no folder chips once a folder is attached: ask=%v %+v", got.AskFolder, got.PendingQuestion)
	}
	if len(got.Rejections) != 1 || got.Rejections[0].Field != "ask_folder" || got.Rejections[0].Reason != buildReasonFolderAttached {
		t.Fatalf("rejections %+v", got.Rejections)
	}
	if last := lastEntry(got); strings.Contains(last.Text, "Explore a folder") || strings.Contains(last.Text, "didn’t take") {
		t.Fatalf("the refusal needs no note in the transcript: %q", last.Text)
	}
	if got.Draft.TemplateID != "code-project" || got.Draft.Name != "Field Notes" {
		t.Fatalf("the rest of the reply still applies: %+v", got.Draft)
	}
}

func TestWorkspaceBuildFolder_ACancelledDialogChangesNothingAndDetachClearsIt(t *testing.T) {
	f := newBuildFixture(t, true)
	folders := &fakeBuildFolders{}
	f.withFolders(t, folders)
	build := f.start(t)

	status, body := f.attachFolder(t, build, map[string]any{"mode": "picker"})
	cancelled := sessionOf(t, body)
	if status != http.StatusOK || body["cancelled"] != true || cancelled.Folder != nil || cancelled.Version != build.Version || len(cancelled.Transcript) != 0 {
		t.Fatalf("cancelled pick: %d %v %+v", status, body, cancelled)
	}

	folders.observation = testObservation()
	_, body = f.attachFolder(t, build, map[string]any{"mode": "selection", "selection_token": "tok-1"})
	attached := sessionOf(t, body)
	if attached.Folder == nil || folders.choices[1] != "tok-1" || folders.modes[1] != "selection" {
		t.Fatalf("typed path goes by its token: %+v %v", attached.Folder, folders.choices)
	}

	status, body = f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/folder/detach", map[string]any{"version": attached.Version})
	detached := sessionOf(t, body)
	if status != http.StatusOK || detached.Folder != nil || lastEntry(detached).Text != buildCopyFolderRemoved || detached.Version != attached.Version+1 {
		t.Fatalf("detach: %d %+v", status, detached)
	}
	status, body = f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/folder/detach", map[string]any{"version": detached.Version})
	again := sessionOf(t, body)
	if status != http.StatusOK || len(again.Transcript) != len(detached.Transcript) || again.Version != detached.Version {
		t.Fatalf("a second detach says nothing: %d %+v", status, again)
	}
	if status, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/folder/detach", map[string]any{"version": 1}); status != http.StatusConflict || body["code"] != "version" {
		t.Fatalf("stale detach: %d %v", status, body)
	}
}

func TestWorkspaceBuildFolder_RefusalsAreSaidInTheUsersWords(t *testing.T) {
	f := newBuildFixture(t, true)
	folders := &fakeBuildFolders{observeErr: &personalassistant.FolderRootError{Message: "That is your whole home folder."}}
	f.withFolders(t, folders)
	build := f.start(t)
	for _, bad := range []map[string]any{
		{"mode": "chip"},
		{"mode": "chip", "chip": "documents", "selection_token": "tok"},
		{"mode": "picker", "chip": "documents"},
		{"mode": "selection"},
		{"mode": "path", "path": "/Users/me"},
	} {
		if status, _ := f.attachFolder(t, build, bad); status != http.StatusBadRequest {
			t.Fatalf("%v must be a bad request, got %d", bad, status)
		}
	}
	if len(folders.observed) != 0 {
		t.Fatal("a refused request never reaches the folder service")
	}
	status, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "home"})
	if status != http.StatusConflict || body["code"] != "folder" || body["error"] != "That is your whole home folder." {
		t.Fatalf("root refusal: %d %v", status, body)
	}
	folders.observeErr = personalassistant.ErrFolderScanBusy
	if _, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"}); !strings.Contains(body["error"].(string), "still being looked at") {
		t.Fatalf("busy: %v", body)
	}
	folders.observeErr = personalassistant.ErrFolderPickerUnavailable
	if _, body := f.attachFolder(t, build, map[string]any{"mode": "picker"}); !strings.Contains(body["error"].(string), "dialog is unavailable") {
		t.Fatalf("picker off: %v", body)
	}
	if status, body := f.attachFolder(t, personalassistant.WorkspaceBuildSession{ID: "unknown", Version: 1}, map[string]any{"mode": "picker"}); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("unknown build: %d %v", status, body)
	}
	f.handler.workspaceBuild.Folders = nil
	if status, body := f.attachFolder(t, build, map[string]any{"mode": "picker"}); status != http.StatusConflict || body["code"] != "folder" {
		t.Fatalf("unwired: %d %v", status, body)
	}
}

func TestWorkspaceBuildFolder_CreatePlansTheHeldRootAndRefusesALostPick(t *testing.T) {
	f := newBuildFixture(t, true)
	folders := &fakeBuildFolders{observation: testObservation(), root: "/Users/me/elite_school"}
	f.withFolders(t, folders)
	ctx := context.Background()
	build := f.start(t)

	// No build, a build without a folder, and a group are never refused.
	if plan, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{}, session.WorkspaceKindWorkspace); plan != nil || err != nil {
		t.Fatalf("no build: %+v %v", plan, err)
	}
	if plan, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{BuildSessionID: build.ID}, session.WorkspaceKindWorkspace); plan != nil || err != nil {
		t.Fatalf("no folder: %+v %v", plan, err)
	}
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	attached := sessionOf(t, body)
	if plan, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{BuildSessionID: build.ID}, session.WorkspaceKindGroup); plan != nil || err != nil {
		t.Fatalf("a group never links: %+v %v", plan, err)
	}
	plan, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{BuildSessionID: build.ID}, session.WorkspaceKindWorkspace)
	if err != nil || plan == nil || plan.root != "/Users/me/elite_school" || plan.name != "Elite School" || plan.sessionID != build.ID {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if len(folders.resolved) != 1 || folders.resolved[0] != "obs-1" {
		t.Fatalf("resolved %v", folders.resolved)
	}
	folders.rootErr = personalassistant.ErrFolderSelection
	if plan, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{BuildSessionID: build.ID}, session.WorkspaceKindWorkspace); plan != nil || err == nil || err.kind != "build_folder" || err.status != http.StatusConflict {
		t.Fatalf("a lost pick refuses the create: %+v %+v", plan, err)
	}
	// The record of how the workspace was set up names the folder.
	summary := buildSummaryFor(attached, personalassistant.BuildDraft{})
	if len(summary.Decisions) != 1 || summary.Decisions[0].Section != "folder" || summary.Decisions[0].Text != "Linked the folder “Elite School” you attached." {
		t.Fatalf("summary %+v", summary.Decisions)
	}
}

func TestWorkspaceBuildFolder_RecordLinksTheFolderInPlaceAsThePrimaryDirectory(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	files, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler.SetWorkspaceStore(files)
	linked := filepath.Join(t.TempDir(), "elite_school")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	newWorkspace := func(name string) (*session.Workspace, *agentworkspace.Workspace, string) {
		ws := &session.Workspace{Name: name, Kind: session.WorkspaceKindWorkspace, FolderSlug: agentworkspace.Slugify(name)}
		if err := handler.store.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
		folderWS := &agentworkspace.Workspace{ID: ws.ID, Name: ws.Name, Kind: string(ws.Kind), FolderSlug: ws.FolderSlug, Status: agentworkspace.StatusActive}
		if err := files.Save(folderWS); err != nil {
			t.Fatal(err)
		}
		folder, err := files.GetFolderPath(ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ws, folderWS, folder
	}

	ws, folderWS, folder := newWorkspace("Elite School")
	plan := &createWorkspaceBuildFolderPlan{sessionID: "build-1", name: "Elite School", root: linked}
	if err := handler.recordCreateWorkspaceBuildFolder(ctx, ws, folderWS, folder, plan); err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := files.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.DirectoryReferences) != 1 || filepath.Clean(stored.DirectoryReferences[0].Path) != linked || stored.DirectoryReferences[0].Name != "Elite School" {
		t.Fatalf("directory references %+v", stored.DirectoryReferences)
	}
	if primary, _ := stored.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); primary != stored.DirectoryReferences[0].ID {
		t.Fatalf("primary %q refs %+v", primary, stored.DirectoryReferences)
	}
	row, err := handler.store.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	var refs []agentworkspace.DirectoryReference
	if err := json.Unmarshal(row.DirectoryReferencesJSON, &refs); err != nil || len(refs) != 1 || filepath.Clean(refs[0].Path) != linked {
		t.Fatalf("session row refs %s: %v", row.DirectoryReferencesJSON, err)
	}
	if primary, _ := row.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); primary != refs[0].ID {
		t.Fatalf("session row primary %q", primary)
	}
	// The user's folder is only referenced: nothing was written into it.
	if entries, _ := os.ReadDir(linked); len(entries) != 0 {
		t.Fatalf("the linked folder was touched: %v", entries)
	}

	// A scaffold the blueprint made inside the workspace folder is superseded;
	// a project folder the user chose outside it keeps the primary.
	ws2, folderWS2, folder2 := newWorkspace("Scaffolded")
	scaffold := filepath.Join(folder2, "project")
	if err := os.MkdirAll(scaffold, 0o755); err != nil {
		t.Fatal(err)
	}
	scaffoldID, err := projecttemplates.AttachLinkedDirectory(folderWS2, "project", scaffold)
	if err != nil {
		t.Fatal(err)
	}
	folderWS2.SharedData = map[string]any{}
	projecttemplates.SetPrimaryDirectoryID(folderWS2.SharedData, scaffoldID)
	if err := handler.recordCreateWorkspaceBuildFolder(ctx, ws2, folderWS2, folder2, plan); err != nil {
		t.Fatalf("record over a scaffold: %v", err)
	}
	stored2, _ := files.Get(ws2.ID)
	if primary, _ := stored2.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); primary == scaffoldID || primary == "" {
		t.Fatalf("the attached folder supersedes a scaffold: primary %q", primary)
	}

	ws3, folderWS3, folder3 := newWorkspace("Attached Project")
	outside := filepath.Join(t.TempDir(), "song")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideID, err := projecttemplates.AttachLinkedDirectory(folderWS3, "song", outside)
	if err != nil {
		t.Fatal(err)
	}
	folderWS3.SharedData = map[string]any{}
	projecttemplates.SetPrimaryDirectoryID(folderWS3.SharedData, outsideID)
	if err := handler.recordCreateWorkspaceBuildFolder(ctx, ws3, folderWS3, folder3, plan); err != nil {
		t.Fatalf("record beside an attached project: %v", err)
	}
	stored3, _ := files.Get(ws3.ID)
	if primary, _ := stored3.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); primary != outsideID || len(stored3.DirectoryReferences) != 2 {
		t.Fatalf("an attached project keeps the primary: %q %+v", primary, stored3.DirectoryReferences)
	}

	// A folder that is gone cannot be linked, and says so as a build-folder
	// refusal, never as a project-connection one.
	missing := &createWorkspaceBuildFolderPlan{name: "Gone", root: filepath.Join(t.TempDir(), "gone")}
	ws4, folderWS4, folder4 := newWorkspace("Gone")
	var attachErr *createWorkspaceAttachError
	if err := handler.recordCreateWorkspaceBuildFolder(ctx, ws4, folderWS4, folder4, missing); !errors.As(err, &attachErr) || attachErr.kind != "build_folder" {
		t.Fatalf("missing folder: %v", err)
	}
	unwired := &Handler{}
	if err := unwired.recordCreateWorkspaceBuildFolder(ctx, ws4, folderWS4, folder4, plan); !errors.As(err, &attachErr) || attachErr.kind != "build_folder" {
		t.Fatalf("no file store: %v", err)
	}
}

// The template's tool step reads and saves the workspace itself; the link must
// already be on disk when it runs, or the tool step's save would be undone by
// a later save of the stale in-memory record.
func TestWorkspaceBuildFolder_TheLinkIsOnDiskBeforeTheTemplatesToolStep(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	files, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler.SetWorkspaceStore(files)
	linked := filepath.Join(t.TempDir(), "elite_school")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ws := &session.Workspace{Name: "Elite School", Kind: session.WorkspaceKindWorkspace, FolderSlug: "elite-school"}
	if err := handler.store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	folderWS := &agentworkspace.Workspace{ID: ws.ID, Name: ws.Name, Kind: string(ws.Kind), FolderSlug: ws.FolderSlug, Status: agentworkspace.StatusActive}
	if err := files.Save(folderWS); err != nil {
		t.Fatal(err)
	}
	folder, err := files.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	toolsRan := false
	handler.SetTemplateToolApplier(func(workspaceID string, _ projecttemplates.ToolDefaults) ([]string, []string) {
		toolsRan = true
		stored, err := files.Get(workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if primary, _ := stored.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); primary == "" || len(stored.DirectoryReferences) != 1 {
			t.Errorf("the tool step ran before the link was on disk: %+v", stored.SharedData)
		}
		return []string{"filesystem"}, nil
	})
	// A metadata-only template with declared tools: no scaffold, then the
	// link, then the tool step.
	tc := createTemplateContext{
		wantsProject: true, resolved: true,
		template:    projecttemplates.Template{ID: "meta-tools", Name: "Meta Tools", Tools: projecttemplates.ToolDefaults{MCPServers: []string{"filesystem"}}},
		buildFolder: &createWorkspaceBuildFolderPlan{sessionID: "build-1", name: "elite_school", root: linked},
	}
	warning, attachErr := handler.applyCreateWorkspaceTemplate(ctx, createWorkspaceRequest{TemplateID: "meta-tools"}, ws, folderWS, folder, tc)
	if attachErr != nil || warning != "" || !toolsRan {
		t.Fatalf("apply: warning=%q err=%v tools=%v", warning, attachErr, toolsRan)
	}
	// A link that fails stops the create before the tool step.
	toolsRan = false
	tc.buildFolder = &createWorkspaceBuildFolderPlan{name: "gone", root: filepath.Join(t.TempDir(), "gone")}
	if _, attachErr := handler.applyCreateWorkspaceTemplate(ctx, createWorkspaceRequest{TemplateID: "meta-tools"}, ws, folderWS, folder, tc); attachErr == nil || toolsRan {
		t.Fatalf("a failed link must stop before tools: err=%v tools=%v", attachErr, toolsRan)
	}
}

func TestPathInsideFolder(t *testing.T) {
	for _, tc := range []struct {
		parent, child string
		want          bool
	}{
		{"/w/ws", "/w/ws/project", true},
		{"/w/ws", "/w/ws", true},
		{"/w/ws", "/w/ws2", false},
		{"/w/ws", "/w", false},
		{"/w/ws", "/elsewhere/ws/project", false},
		{"", "/w/ws", false},
		{"/w/ws", "", false},
	} {
		if got := pathInsideFolder(tc.parent, tc.child); got != tc.want {
			t.Errorf("pathInsideFolder(%q, %q) = %v, want %v", tc.parent, tc.child, got, tc.want)
		}
	}
}
