package sessionhttp

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// sourceHQ builds a Personal HQ through the source installation's own stores:
// a designated workspace, its entry assistant profile (with a native key that
// must not travel), the relationship and a Daily Brief with history.
func sourceHQ(t *testing.T, source *continuityInstallation, at time.Time) *agentworkspace.Workspace {
	t.Helper()
	ctx := t.Context()
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "My HQ"})
	ws.OwnerUserID, ws.FolderSlug, ws.Designation = "local", "my-hq", "personal_hq"
	ws.CreatedAt, ws.UpdatedAt = at, at
	ws.AgentInstances = []agentworkspace.AgentInstance{{ID: "entry-1", Name: "Ada", EntryPoint: true}}
	ws.SharedData = map[string]any{"personal_assistant_presentation": map[string]any{"version": 1, "assistant_id": "assistant-1", "request_id": "hq-request"}}
	if err := source.sync.Save(ws); err != nil {
		t.Fatal(err)
	}
	profile := &agent.Agent{Role: types.RoleOrchestrator, Appearance: types.NewAgentAppearance(), Metadata: &types.AgentMetadata{
		Tags: []string{personalassistant.ProfileAssistantMarker("assistant-1"), personalassistant.ProfileHireMarker("hire-request")}}}
	profile.Settings.Model = "gpt-test"
	profile.Settings.APIKey = "synthetic-native-key-must-not-travel"
	if err := source.files.SaveWorkspaceAgent(ws.ID, "Ada", profile); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, `UPDATE users SET personal_workspace_id=? WHERE id='local'`, ws.ID); err != nil {
		t.Fatal(err)
	}
	state := personalassistant.NewState("local")
	state.AssistantID, state.Status, state.DisplayName = "assistant-1", personalassistant.StatusActive, "Ada"
	state.Appearance = profile.Appearance.Clone()
	state.HQWorkspaceID, state.HQEntryAgentInstanceID, state.GlobalAgentProfileName = ws.ID, "entry-1", "Ada"
	state.Mandate = "Keep the important work visible."
	state.FocusAreas = []personalassistant.FocusArea{personalassistant.FocusPlanMyDay}
	state.SpecialistOfferState = personalassistant.SpecialistOfferDeclined
	state.LastHireRequestID, state.LastHQRequestID = "hire-request", "hq-request"
	state.HiredAt = &at
	if _, err := personalassistant.NewSQLiteStore(source.db).CreateState(ctx, state); err != nil {
		t.Fatal(err)
	}
	briefs := dailybrief.NewSQLiteStore(source.db)
	cfg := &dailybrief.Config{WorkspaceID: ws.ID, UserID: "local", Timezone: "UTC", ScheduleDays: []string{"mon", "tue"},
		ScheduleTime: "08:00", ScheduleEnabled: true, Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true, NotifyOnReady: true}
	if err := briefs.UpsertConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for i, date := range []string{"2026-08-30", "2026-08-31"} {
		rev := &dailybrief.Revision{ID: "rev-" + date, WorkspaceID: ws.ID, UserID: "local", LocalDate: date, RevisionNumber: 1,
			Trigger: dailybrief.TriggerScheduled, Status: dailybrief.GenerationSucceeded, ConfigRevision: 1,
			ContentJSON: `{"headline":"Brief ` + date + `"}`, GeneratedAt: at.Add(time.Duration(i) * time.Hour), CreatedAt: at.Add(time.Duration(i) * time.Hour)}
		if err := briefs.CreateRevision(ctx, rev); err != nil {
			t.Fatal(err)
		}
	}
	if err := briefs.SetCurrentRevision(ctx, ws.ID, "rev-2026-08-31"); err != nil {
		t.Fatal(err)
	}
	return ws
}

func copyContinuityFolder(t *testing.T, folder string) string {
	t.Helper()
	copied := filepath.Join(t.TempDir(), "transfer", filepath.Base(folder))
	if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copied, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}
	return copied
}

func reviewContinuityFolder(t *testing.T, dest *continuityInstallation, path string) map[string]any {
	t.Helper()
	code, payload := dest.request(t, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(path), nil)
	if code != http.StatusOK {
		t.Fatal(code, payload)
	}
	review, _ := payload["continuity"].(map[string]any)
	return review
}

// "Import and continue": the one incoming assistant becomes this
// installation's assistant with its agreement, paused, and its HQ designated,
// with brief history readable and brief scheduling off.
func TestContinuityImportAndContinueAdoptsAssistant(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := sourceHQ(t, source, at)
	status, err := source.worker.PrepareNow(ctx, ws.ID)
	if err != nil || status.State != continuityprep.StateReady {
		prep, _ := source.local.Preparation(ctx, ws.ID)
		t.Fatalf("source HQ not ready: %+v %v %+v", status, err, prep)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)

	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	if review["recommended_action"] != "continue" {
		t.Fatalf("clean destination should offer Import and continue: %v", review)
	}
	code, payload := dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}
	report, _ := payload["import"].(map[string]any)
	if report["adopted"] != true || report["assistant_name"] != "Ada" {
		t.Fatalf("report: %v", report)
	}
	state, err := personalassistant.NewSQLiteStore(dest.db).GetState(ctx, "local")
	if err != nil || state.AssistantID != "assistant-1" || state.HQWorkspaceID != ws.ID || state.HQEntryAgentInstanceID != "entry-1" ||
		state.Status != personalassistant.StatusPaused || state.Mandate != "Keep the important work visible." || !state.HiredAt.Equal(at) {
		t.Fatalf("assistant not adopted with its agreement: %+v %v", state, err)
	}
	var designated string
	if err := dest.db.QueryRowContext(ctx, `SELECT personal_workspace_id FROM users WHERE id='local'`).Scan(&designated); err != nil || designated != ws.ID {
		t.Fatalf("HQ not designated: %q %v", designated, err)
	}
	briefs := dailybrief.NewSQLiteStore(dest.db)
	cfg, err := briefs.GetConfig(ctx, ws.ID)
	if err != nil || cfg.ScheduleEnabled || cfg.NotifyOnReady || cfg.IncludeFutureWorkspaces {
		t.Fatalf("brief config must arrive with scheduling and notifications off: %+v %v", cfg, err)
	}
	current, err := briefs.GetCurrentRevision(ctx, ws.ID)
	if err != nil || current.ID != "rev-2026-08-31" {
		t.Fatalf("current brief lost: %+v %v", current, err)
	}
	// A finished scheduled brief is completion evidence: activating routines
	// later must not regenerate it.
	if claim, err := briefs.GetLatestClaim(ctx, ws.ID, "2026-08-31"); err != nil || claim == nil {
		t.Fatalf("completion evidence missing: %v", err)
	}
	// The installed profile carries no key from the source machine.
	installed, _ := dest.files.GetFolderPath(ws.ID)
	profile, err := os.ReadFile(filepath.Join(installed, "agents", "ada", "config.json"))
	if err != nil || strings.Contains(string(profile), "synthetic-native-key") {
		t.Fatalf("source key travelled into the installed profile: %v", err)
	}
	if stored, _ := os.ReadFile(filepath.Join(installed, "workspace.json")); !strings.Contains(string(stored), `"designation": "personal_hq"`) &&
		!strings.Contains(string(stored), `"designation":"personal_hq"`) {
		t.Fatal("adopted HQ folder lost its designation")
	}
	policy, _ := dest.local.Policy(ctx, ws.ID)
	if policy.Automatic {
		t.Fatal("adopted HQ started with background routines allowed")
	}
	// The source's copy of this folder still names the source machine's key
	// only in its own file; the copied folder never became readable to Ori
	// as settings, and the source is unchanged.
	if _, err := workspacecontinuity.Inspect(ctx, copied); err != nil {
		t.Fatal(err)
	}
}

// Adoption is bound to the reviewed workspace instance, never to a name: a
// destination agent that happens to be called "Ada" — in another workspace or
// as an unowned chat — is neither a conflict nor a target of the import.
func TestContinuityImportLeavesSameNameAgentsAlone(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := sourceHQ(t, source, at)
	if status, err := source.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source HQ not ready: %+v %v", status, err)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)

	dest := newContinuityInstallation(t, "destination")
	studio := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Studio"})
	studio.OwnerUserID, studio.FolderSlug = "local", "studio"
	studio.AgentInstances = []agentworkspace.AgentInstance{{ID: "studio-ada", Name: "Ada", EntryPoint: true}}
	if err := dest.sync.Save(studio); err != nil {
		t.Fatal(err)
	}
	local := &agent.Agent{Role: types.RoleOrchestrator, Appearance: types.NewAgentAppearance()}
	local.Settings.Model = "local-model"
	if err := dest.files.SaveWorkspaceAgent(studio.ID, "Ada", local); err != nil {
		t.Fatal(err)
	}
	unowned := &session.Session{ID: "global-ada", Title: "Unowned", AgentName: "Ada", CreatedAt: at, UpdatedAt: at}
	if err := dest.store.CreateSession(ctx, unowned); err != nil {
		t.Fatal(err)
	}
	if err := dest.store.AddMessage(ctx, unowned.ID, &session.Message{ID: "global-msg", Role: session.RoleUser, Content: "hello", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}

	review := reviewContinuityFolder(t, dest, copied)
	if review["recommended_action"] != "continue" {
		t.Fatalf("a same-name destination agent must not block adoption: %v", review)
	}
	code, payload := dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}

	state, err := personalassistant.NewSQLiteStore(dest.db).GetState(ctx, "local")
	if err != nil || state.HQWorkspaceID != ws.ID || state.HQEntryAgentInstanceID != "entry-1" {
		t.Fatalf("adoption not bound to the imported instance: %+v %v", state, err)
	}
	kept, found, err := dest.files.GetWorkspaceAgent(studio.ID, "Ada")
	if err != nil || !found || kept.Settings.Model != "local-model" {
		t.Fatalf("same-name agent in another workspace changed: %+v %v", kept, err)
	}
	imported, found, err := dest.files.GetWorkspaceAgent(ws.ID, "Ada")
	if err != nil || !found || imported.Settings.Model != "gpt-test" || imported.Settings.APIKey != "" {
		t.Fatalf("imported profile wrong: %+v %v", imported, err)
	}
	chat, err := dest.store.GetSession(ctx, unowned.ID)
	if err != nil || chat.FolderID != "" || len(chat.Messages) != 1 {
		t.Fatalf("unowned same-name chat was claimed by the import: %+v %v", chat, err)
	}
	if policy, _ := dest.local.Policy(ctx, studio.ID); !policy.Automatic {
		t.Fatal("import changed an unrelated native workspace's routines")
	}
}

// FR-36: after an app-record reset that kept the workspace folders, nothing
// comes back on its own — not the HQ, not any other retained folder. Importing
// the retained HQ folder explicitly restores it where it is, with Ada and her
// agreement, routines off; the folder the user did not import stays detached.
func TestContinuityExplicitRestoreAfterAppRecordReset(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	before := newContinuityInstallation(t, "before-reset")
	ws := sourceHQ(t, before, at)
	other := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Side Project"})
	other.OwnerUserID, other.FolderSlug, other.CreatedAt, other.UpdatedAt = "local", "side-project", at, at
	if err := before.sync.Save(other); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ws.ID, other.ID} {
		if status, err := before.worker.PrepareNow(ctx, id); err != nil || status.State != continuityprep.StateReady {
			t.Fatalf("not ready before reset: %s %+v %v", id, status, err)
		}
	}
	hqFolder, _ := before.files.GetFolderPath(ws.ID)
	otherFolder, _ := before.files.GetFolderPath(other.ID)

	// The same machine after the reset: app records gone, folders retained.
	after := newContinuityInstallationAt(t, "after-reset", before.root)
	if err := after.files.Reload(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ws.ID, other.ID} {
		if _, err := after.files.Get(id); err == nil {
			t.Fatalf("a retained folder came back on its own: %s", id)
		}
	}
	if _, err := personalassistant.NewSQLiteStore(after.db).GetState(ctx, "local"); err == nil {
		t.Fatal("the assistant came back without an import")
	}
	// The empty installation never prepares over a retained checkpoint.
	pointer := func(folder string) []byte {
		data, err := os.ReadFile(filepath.Join(folder, ".ori", "continuity", "current.json"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	hqPointer, otherPointer := pointer(hqFolder), pointer(otherFolder)
	after.worker.Pass(ctx)
	if !bytes.Equal(pointer(hqFolder), hqPointer) || !bytes.Equal(pointer(otherFolder), otherPointer) {
		t.Fatal("a reset installation published over a retained checkpoint")
	}

	review := reviewContinuityFolder(t, after, hqFolder)
	if review["recommended_action"] != "continue" {
		t.Fatalf("retained HQ should offer Import and continue: %v", review)
	}
	code, payload := after.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": hqFolder, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusCreated {
		t.Fatalf("explicit restore: %d %v", code, payload)
	}
	installed, err := after.files.GetFolderPath(ws.ID)
	if err != nil || mustEval(t, installed) != mustEval(t, hqFolder) {
		t.Fatalf("restored somewhere other than where it was retained: %q %v", installed, err)
	}
	state, err := personalassistant.NewSQLiteStore(after.db).GetState(ctx, "local")
	if err != nil || state.AssistantID != "assistant-1" || state.Status != personalassistant.StatusPaused ||
		state.Mandate != "Keep the important work visible." {
		t.Fatalf("assistant not restored with its agreement: %+v %v", state, err)
	}
	if policy, _ := after.local.Policy(ctx, ws.ID); policy.Automatic || !policy.Manual {
		t.Fatalf("restored HQ admitted background routines: %+v", policy)
	}
	// Reloading after the import still leaves the other retained folder alone.
	if _, err := after.files.Get(other.ID); err == nil {
		t.Fatal("restoring one folder reattached another retained folder")
	}
	if _, err := workspacecontinuity.Inspect(ctx, otherFolder); err != nil {
		t.Fatal("the folder the user did not import was changed", err)
	}
}

// A destination that already has an assistant keeps it: the incoming HQ is
// imported as a workspace only, with no change to the incumbent.
func TestContinuityImportPreservesIncumbentAssistant(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := sourceHQ(t, source, at)
	if status, err := source.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source HQ not ready: %+v %v", status, err)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)

	dest := newContinuityInstallation(t, "destination")
	incumbent := personalassistant.NewState("local")
	incumbent.AssistantID, incumbent.Status, incumbent.DisplayName = "incumbent", personalassistant.StatusAwaitingHQ, "Bea"
	incumbent.Appearance = types.NewAgentAppearance()
	incumbent.GlobalAgentProfileName = "Bea"
	incumbent.HiredAt = &at
	if _, err := personalassistant.NewSQLiteStore(dest.db).CreateState(ctx, incumbent); err != nil {
		t.Fatal(err)
	}
	review := reviewContinuityFolder(t, dest, copied)
	if review["adoption_blocked"] != "existing_assistant" || review["recommended_action"] != "workspace_only" {
		t.Fatalf("an incumbent must block adoption: %v", review)
	}
	code, payload := dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusConflict {
		t.Fatalf("continue must be refused with an incumbent: %d %v", code, payload)
	}
	code, payload = dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "workspace_only"})
	if code != http.StatusCreated {
		t.Fatalf("workspace-only import: %d %v", code, payload)
	}
	state, err := personalassistant.NewSQLiteStore(dest.db).GetState(ctx, "local")
	if err != nil || state.AssistantID != "incumbent" || state.DisplayName != "Bea" {
		t.Fatalf("incumbent changed: %+v %v", state, err)
	}
	var designated string
	_ = dest.db.QueryRowContext(ctx, `SELECT COALESCE(personal_workspace_id,'') FROM users WHERE id='local'`).Scan(&designated)
	if designated != "" {
		t.Fatal("workspace-only import designated an HQ")
	}
	// Brief history of the non-adopted HQ is still reachable by its workspace.
	if current, err := dailybrief.NewSQLiteStore(dest.db).GetCurrentRevision(ctx, ws.ID); err != nil || current == nil {
		t.Fatalf("brief history not reachable: %v", err)
	}
	attachment, _ := dest.local.Attachment(ctx, ws.ID)
	if attachment.Disposition != workspacecontinuity.WorkspaceOnlyHQ {
		t.Fatalf("non-adoption not recorded: %+v", attachment)
	}

	// The non-adopted HQ keeps the agreement it arrived with: this machine can
	// prepare its own checkpoint for it, and a later move to a machine without
	// an assistant can still continue with Ada.
	if status, err := dest.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		prep, _ := dest.local.Preparation(ctx, ws.ID)
		t.Fatalf("workspace-only HQ not re-preparable: %+v %v %+v", status, err, prep)
	}
	if state, _ := personalassistant.NewSQLiteStore(dest.db).GetState(ctx, "local"); state.AssistantID != "incumbent" {
		t.Fatal("preparing the imported HQ touched the incumbent")
	}
	installed, _ := dest.files.GetFolderPath(ws.ID)
	onward := copyContinuityFolder(t, installed)
	third := newContinuityInstallation(t, "third")
	review = reviewContinuityFolder(t, third, onward)
	if review["recommended_action"] != "continue" {
		t.Fatalf("onward copy lost its assistant: %v", review)
	}
	code, payload = third.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": onward, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusCreated {
		t.Fatalf("onward import: %d %v", code, payload)
	}
	adopted, err := personalassistant.NewSQLiteStore(third.db).GetState(ctx, "local")
	if err != nil || adopted.AssistantID != "assistant-1" || adopted.Mandate != "Keep the important work visible." || !adopted.HiredAt.Equal(at) {
		t.Fatalf("onward adoption lost the agreement: %+v %v", adopted, err)
	}
}
