package sessionhttp

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// This deliberately characterizes the legacy import, not the future modern
// checkpoint path. Source records are saved through real owners. The destination
// receives only a workspace directory, never the DB, global roster or uploads.
func TestWorkspaceContinuityCharacterization_LegacyFolderOmitsDatabaseHistory(t *testing.T) {
	sourceEnv := resetfixture.New(t)
	source, closeSource := newPersonalHQImportHarness(t)
	t.Cleanup(closeSource)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	seedWorkspace := func(name string) *workspace.Workspace {
		t.Helper()
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name, Agents: []string{"Assistant"}})
		ws.OwnerUserID = "local"
		must(ws.SetEntryAgentName("Assistant"))
		composed := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(source.handler.store), source.fileStore)
		must(composed.Save(ws))
		must(source.fileStore.SaveWorkspaceAgent(ws.ID, "Assistant", &agent.Agent{Role: types.RoleOrchestrator}))
		return ws
	}
	hq := seedWorkspace("Portable HQ")
	unrelated := seedWorkspace("Unrelated Project")
	must(source.fileStore.SaveWorkspaceAgent(unrelated.ID, "Assistant", &agent.Agent{Role: types.RoleResearcher}))
	_, err := source.service.Designate(ctx, "local", hq.ID)
	must(err)
	folder, err := source.fileStore.GetFolderPath(hq.ID)
	must(err)
	must(workspace.NewMemoryStore(source.fileStore).Append(hq.ID, workspace.MemoryEntry{
		Text: "Synthetic workspace memory", Date: "2026-09-01", Provenance: "manual",
	}))

	hiredAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	state := personalassistant.NewState("local")
	state.Status = personalassistant.StatusPaused
	state.DisplayName, state.GlobalAgentProfileName = "Assistant", "Assistant"
	state.HQWorkspaceID, state.HQEntryAgentInstanceID = hq.ID, hq.AgentInstances[0].ID
	state.HiredAt, state.Mandate = &hiredAt, "Preserve the synthetic agreement"
	_, err = personalassistant.NewSQLiteStore(source.handler.store.DB()).CreateState(ctx, state)
	must(err)

	seedSession := func(owner, text string) string {
		t.Helper()
		sess := &session.Session{Title: "Synthetic conversation", AgentName: "Assistant", FolderID: owner}
		must(source.handler.store.CreateSession(ctx, sess))
		must(source.handler.store.AddMessage(ctx, sess.ID, &session.Message{Role: session.RoleUser, Content: text}))
		return sess.ID
	}
	ownedID := seedSession(hq.ID, "Owned history sentinel")
	foreignID := seedSession(unrelated.ID, "Unrelated history sentinel")
	globalID := seedSession("", "Unassigned history sentinel")
	owned := hq.ID
	listed, err := source.handler.store.ListSessions(ctx, &session.SessionFilter{FolderID: &owned}, nil)
	must(err)
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != ownedID {
		t.Fatal("canonical membership must not select same-name foreign or global sessions")
	}

	uploads, err := sessionfiles.NewStore(filepath.Join(sourceEnv.Paths().WorkDir, "session_files"))
	must(err)
	_, err = uploads.AddFileFromReader(ownedID, strings.NewReader("synthetic upload"), "owned.txt", 16)
	must(err)
	commitments := followup.NewService(followup.NewSQLiteStore(source.handler.store.DB()))
	captured, err := commitments.Capture(ctx, followup.CaptureInput{
		UserID: "local", WorkspaceID: hq.ID, Category: followup.CategoryIOwe, Title: "Synthetic completed commitment",
	})
	must(err)
	_, err = commitments.Complete(ctx, "local", captured.ID)
	must(err)
	briefs := dailybrief.NewSQLiteStore(source.handler.store.DB())
	_, err = dailybrief.NewService(briefs, nil).UpdateConfig(ctx, dailybrief.Config{
		WorkspaceID: hq.ID, UserID: "local", Timezone: "UTC", Scope: dailybrief.ScopeSelected,
		SelectedWorkspaceIDs: []string{hq.ID},
	})
	must(err)
	revision := &dailybrief.Revision{
		WorkspaceID: hq.ID, UserID: "local", LocalDate: "2026-09-01", RevisionNumber: 1,
		Trigger: dailybrief.TriggerManual, Status: dailybrief.GenerationSucceeded, ConfigRevision: 1,
		ContentJSON: `{"summary":"Synthetic historical brief"}`, GeneratedAt: hiredAt,
	}
	must(briefs.CreateRevision(ctx, revision))
	must(briefs.SetCurrentRevision(ctx, hq.ID, revision.ID))

	// Change HOME, data, CWD, secret backend and every inherited provider/root
	// override before constructing the destination. No full server is booted.
	destinationEnv := resetfixture.New(t)
	destination, closeDestination := newPersonalHQImportHarness(t)
	t.Cleanup(closeDestination)
	must(destination.handler.agentStore.CreateAgent("Assistant", nil))
	must(destination.handler.agentStore.UpdateAgent("Assistant", func(ag *agent.Agent) error {
		ag.Role = types.RoleAnalyzer
		return nil
	}))
	code, _ := destination.importFolder(t, folder)
	if code != http.StatusCreated {
		t.Fatalf("legacy import status = %d", code)
	}
	imported, err := destination.fileStore.Get(hq.ID)
	must(err)
	if imported.ID != hq.ID || imported.AgentInstances[0].ID != hq.AgentInstances[0].ID {
		t.Fatal("legacy import changed workspace or instance identity")
	}
	ag, found, err := destination.fileStore.GetWorkspaceAgent(hq.ID, "Assistant")
	must(err)
	if !found || ag.Role != types.RoleOrchestrator {
		t.Fatal("imported scoped agent was replaced by a same-name profile")
	}
	globalAgent, found := destination.handler.agentStore.GetAgent("Assistant")
	if !found || globalAgent.Role != types.RoleAnalyzer {
		t.Fatal("legacy import overwrote destination global agent")
	}
	memory, err := workspace.NewMemoryStore(destination.fileStore).ReadRaw(hq.ID)
	must(err)
	if !strings.Contains(memory, "Synthetic workspace memory") {
		t.Fatal("folder-owned memory did not travel")
	}
	if status := destination.status(t); !status.Valid || status.WorkspaceID != hq.ID {
		t.Fatal("fixture must demonstrate designation without the relationship row")
	}
	if _, err := personalassistant.NewSQLiteStore(destination.handler.store.DB()).GetState(ctx, "local"); !errors.Is(err, personalassistant.ErrNotFound) {
		t.Fatalf("legacy folder unexpectedly restored relationship: %v", err)
	}
	for _, id := range []string{ownedID, foreignID, globalID} {
		if _, err := destination.handler.store.GetSession(ctx, id); !errors.Is(err, session.ErrSessionNotFound) {
			t.Fatalf("legacy folder unexpectedly restored a database session: %v", err)
		}
	}
	if _, err := followup.NewSQLiteStore(destination.handler.store.DB()).Get(ctx, "local", captured.ID); !errors.Is(err, followup.ErrNotFound) {
		t.Fatalf("legacy folder unexpectedly restored follow-up: %v", err)
	}
	destinationBriefs := dailybrief.NewSQLiteStore(destination.handler.store.DB())
	if _, err := destinationBriefs.GetConfig(ctx, hq.ID); !errors.Is(err, dailybrief.ErrConfigNotFound) {
		t.Fatalf("legacy folder unexpectedly restored brief config: %v", err)
	}
	if _, err := destinationBriefs.GetRevision(ctx, revision.ID); !errors.Is(err, dailybrief.ErrRevisionNotFound) {
		t.Fatalf("legacy folder unexpectedly restored brief history: %v", err)
	}
	destinationUploads, err := sessionfiles.NewStore(filepath.Join(destinationEnv.Paths().WorkDir, "session_files"))
	must(err)
	files, err := destinationUploads.ListFiles(ownedID)
	must(err)
	if len(files) != 0 {
		t.Fatal("legacy directory unexpectedly carried external session uploads")
	}
	workspaces, err := destination.handler.store.ListWorkspaces(ctx)
	must(err)
	if len(workspaces) != 1 || workspaces[0].ID != hq.ID {
		t.Fatal("directory import leaked unrelated workspace")
	}
}
