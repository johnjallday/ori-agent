package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/trigger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func nativeCreationFixture(t *testing.T) (*database.DB, *WorkspaceStoreAdapter, *workspace.FileStore, *resetstate.WorkGate) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("AGENT_STORE_PATH", filepath.Join(root, "global", "agents.json"))
	t.Setenv("WORKSPACE_DIR", filepath.Join(root, "forbidden-shadow"))
	t.Setenv("ORI_SECRET_STORE_BACKEND", "memory")
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "data", "sessions.db")})
	nativeCreationMust(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gate := &resetstate.WorkGate{}
	private, err := workspace.NewLocalConfigStoreWithWorkGate(db, vault.NewMemorySecretStore(), gate)
	nativeCreationMust(t, err)
	files, err := workspace.NewFileStoreWithLocalConfig(filepath.Join(root, "canonical"), private)
	nativeCreationMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	return db, NewWorkspaceStoreAdapter(NewHybridStoreWithDB(db, 10)), files, gate
}

func nativeCreationMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeWorkspaceCreationPublishesPrivateCanonicalStateBeforeAdmission(t *testing.T) {
	db, adapter, files, gate := nativeCreationFixture(t)
	globals, err := store.NewFileStore(os.Getenv("AGENT_STORE_PATH"), types.Settings{})
	nativeCreationMust(t, err)
	source := &agent.Agent{Role: types.RoleOrchestrator, Settings: types.Settings{APIKey: "synthetic-native-key", Model: "offline"}}
	nativeCreationMust(t, globals.SetAgent("Guide", source))
	composed := workspace.NewAgentSnapshotStore(workspace.NewSyncStore(adapter, files), globals)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "New native", Agents: []string{"Guide"}})
	ws.MCPBindings = []workspace.MCPBinding{{ID: "connector", ServerName: "synthetic", Config: map[string]any{"secret": "synthetic-native-connector"}}}
	ws.AllowNativeMCPCLI = true
	ws.SharedData = map[string]any{"authored_id": json.Number("9007199254740993")}
	nativeCreationMust(t, workspace.CreateNativeWorkspace(t.Context(), composed, ws))
	local := workspacecontinuity.NewLocalStore(db)
	a, err := local.Attachment(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if a.State != workspacecontinuity.Native || a.Provisioning || !a.AllowsAutomatic() || !a.AllowsManual() {
		t.Fatal("successful native creation was not admitted")
	}
	barriers, err := local.FileMutationBarriers(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if len(barriers) != 0 {
		t.Fatal("successful native creation left barriers")
	}
	folder, err := files.GetFolderPath(ws.ID)
	nativeCreationMust(t, err)
	for _, path := range []string{"workspace.json", "agents/guide/config.json"} {
		data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, path, workspacecontinuity.MaxChunkBytes)
		nativeCreationMust(t, err)
		if bytes.Contains(data, []byte("synthetic-native-key")) || bytes.Contains(data, []byte("synthetic-native-connector")) {
			t.Fatal("new native creation published plaintext configuration")
		}
	}
	profile, exists, err := composed.GetWorkspaceAgent(ws.ID, "Guide")
	nativeCreationMust(t, err)
	if !exists || profile.Settings.APIKey != source.Settings.APIKey || profile.WorkspaceLocalConfigID == "" {
		t.Fatal("native profile lost its installation-local settings")
	}
	loaded, err := files.Get(ws.ID)
	nativeCreationMust(t, err)
	if !loaded.AllowNativeMCPCLI || loaded.MCPBindings[0].Config["secret"] != "synthetic-native-connector" {
		t.Fatal("new native grants were lost")
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, "workspace.json", workspacecontinuity.MaxChunkBytes)
	nativeCreationMust(t, err)
	if !bytes.Contains(canonical, []byte("9007199254740993")) {
		t.Fatal("private separation rounded an authored numeric identity")
	}
	nativeCreationMust(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := NewSQLiteStore(db).SnapshotContinuityWorkspace(t.Context(), tx, canonical)
		return err
	}))
	loaded.Name = "Edited native"
	nativeCreationMust(t, composed.Save(loaded))
	canonical, err = workspacecontinuity.ReadCanonicalFile(t.Context(), folder, "workspace.json", workspacecontinuity.MaxChunkBytes)
	nativeCreationMust(t, err)
	if !bytes.Contains(canonical, []byte("9007199254740993")) {
		t.Fatal("hydrating a separated native file rounded its authored identity on save")
	}
	nativeCreationMust(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := NewSQLiteStore(db).SnapshotContinuityWorkspace(t.Context(), tx, canonical)
		return err
	}))
	status, err := local.Preparation(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if status.Ready() {
		t.Fatal("creation fabricated a completed checkpoint")
	}
	// New native work is not an upsert, even when supplied the original ID.
	if err := workspace.CreateNativeWorkspace(t.Context(), composed, ws); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("creation adopted an existing row", err)
	}
	nativeCreationMust(t, gate.TryFence(t.Context()))
	other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Fenced"})
	if err := workspace.CreateNativeWorkspace(t.Context(), composed, other); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("fenced creation", err)
	}
	var rows int
	nativeCreationMust(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspaces WHERE id=?`, other.ID).Scan(&rows))
	if rows != 0 {
		t.Fatal("reset fence allowed a canonical insertion")
	}
}

func TestNativeWorkspaceCreationThroughWorkspaceHTTP(t *testing.T) {
	db, adapter, files, _ := nativeCreationFixture(t)
	globals, err := store.NewFileStore(os.Getenv("AGENT_STORE_PATH"), types.Settings{})
	nativeCreationMust(t, err)
	nativeCreationMust(t, globals.SetAgent("Guide", &agent.Agent{}))
	composed := workspace.NewAgentSnapshotStore(workspace.NewSyncStore(adapter, files), globals)
	handler := workspace.NewHTTPHandler(composed, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"name":"HTTP native","agents":["Guide"]}`))
	response := httptest.NewRecorder()
	handler.CreateWorkspace(response, request)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code, response.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	nativeCreationMust(t, json.Unmarshal(response.Body.Bytes(), &created))
	a, err := workspacecontinuity.NewLocalStore(db).Attachment(t.Context(), created.ID)
	nativeCreationMust(t, err)
	if !a.AllowsManual() || a.Provisioning {
		t.Fatal("HTTP success did not finish native provisioning")
	}
	folder, err := files.GetFolderPath(created.ID)
	nativeCreationMust(t, err)
	data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, "workspace.json", workspacecontinuity.MaxChunkBytes)
	nativeCreationMust(t, err)
	if !bytes.Contains(data, []byte(`"ori_local_config_id"`)) {
		t.Fatal("HTTP creation used legacy plaintext persistence")
	}
}

// A fake canonical owner changes the row between durable file publication and
// final validation. The real SQL validator must detect it, not just versions.
type divergentNativeOwner struct {
	*WorkspaceStoreAdapter
	db *database.DB
}

func (s *divergentNativeOwner) Save(ws *workspace.Workspace) error {
	if err := s.WorkspaceStoreAdapter.Save(ws); err != nil {
		return err
	}
	_, err := s.db.ExecContext(context.Background(), `UPDATE workspaces SET tasks_json='[{"id":"unexpected"}]' WHERE id=?`, ws.ID)
	return err
}

func TestNativeWorkspaceCreationFailureRetainsInactiveDurableWork(t *testing.T) {
	db, adapter, files, _ := nativeCreationFixture(t)
	owner := &divergentNativeOwner{WorkspaceStoreAdapter: adapter, db: db}
	composed := workspace.NewSyncStore(owner, files)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Interrupted native"})
	if err := workspace.CreateNativeWorkspace(t.Context(), composed, ws); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("mixed SQL/file native creation was admitted", err)
	}
	local := workspacecontinuity.NewLocalStore(db)
	a, err := local.Attachment(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if !a.Provisioning || a.AllowsManual() || a.AllowsAutomatic() {
		t.Fatal("failed native creation gained authority")
	}
	barriers, err := local.FileMutationBarriers(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if len(barriers) != 1 || barriers[0].Target != "native-create" || !barriers[0].Failed {
		t.Fatal("failed creation lost durable recovery evidence", barriers)
	}
	if _, err := files.GetFolderPath(ws.ID); err != nil {
		t.Fatal("failure erased newly saved work", err)
	}
	if err := local.RegisterNative(t.Context(), ws.ID); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("upgrade bypassed failed provisioning", err)
	}
}

type blockedOwnedProfileSource struct {
	store.Store
	started chan struct{}
	resume  chan struct{}
}

func (s *blockedOwnedProfileSource) GetOwnedAgent(name string) (*agent.Agent, bool) {
	close(s.started)
	<-s.resume
	return s.GetAgent(name)
}

func TestNativeWorkspaceCreationHoldsAdmissionAndResetPermitThroughProfiles(t *testing.T) {
	db, adapter, files, gate := nativeCreationFixture(t)
	globals, err := store.NewFileStore(os.Getenv("AGENT_STORE_PATH"), types.Settings{})
	nativeCreationMust(t, err)
	nativeCreationMust(t, globals.SetAgent("Guide", &agent.Agent{Settings: types.Settings{APIKey: "synthetic-only"}}))
	source := &blockedOwnedProfileSource{Store: globals, started: make(chan struct{}), resume: make(chan struct{})}
	composed := workspace.NewAgentSnapshotStore(workspace.NewSyncStore(adapter, files), source)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Held creation", Agents: []string{"Guide"}})
	done := make(chan error, 1)
	// Release even when an assertion fails so a test cannot strand a writer.
	defer func() {
		select {
		case <-source.resume:
		default:
			close(source.resume)
		}
	}()
	go func() { done <- workspace.CreateNativeWorkspace(t.Context(), composed, ws) }()
	select {
	case <-source.started:
	case err := <-done:
		t.Fatal("creation returned before profile publication", err)
	case <-time.After(10 * time.Second):
		t.Fatal("creation did not reach profile publication")
	}
	a, err := workspacecontinuity.NewLocalStore(db).Attachment(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if a.AllowsManual() || a.AllowsAutomatic() || !a.Provisioning {
		t.Fatal("native work became runnable before its profile callback returned")
	}
	for _, consumer := range []workspace.Store{composed, adapter, files} {
		for _, automatic := range []bool{false, true} {
			if err := workspace.RequireWorkspaceExecution(t.Context(), consumer, ws.ID, automatic); !errors.Is(err, workspace.ErrWorkspaceExecutionInactive) {
				t.Fatal("canonical consumer bypassed native provisioning", err)
			}
		}
	}
	triggers, err := trigger.NewService(trigger.ServiceConfig{Source: files, WorkspaceStore: composed})
	nativeCreationMust(t, err)
	triggers.SetAdmissionGate(gate)
	defer triggers.Close()
	nativeCreationMust(t, triggers.Start())
	triggerInput := trigger.Trigger{WorkspaceID: ws.ID, Name: "Synthetic creation probe", Type: trigger.TypeWebhook,
		Enabled: true, Action: trigger.Action{Kind: trigger.ActionMissionRun}}
	if _, err := triggers.Create(triggerInput); !errors.Is(err, workspace.ErrWorkspaceExecutionInactive) {
		t.Fatal("trigger service bypassed provisional canonical owner", err)
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatal("reset passed a creation still publishing profiles", err)
	}
	barriers, err := workspacecontinuity.NewLocalStore(db).FileMutationBarriers(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if len(barriers) != 1 || barriers[0].Target != "native-create" {
		t.Fatal("test did not isolate the enclosing creation lifetime", barriers)
	}
	close(source.resume)
	nativeCreationMust(t, <-done)
	if _, err := triggers.Create(triggerInput); err != nil {
		t.Fatal("completed native owner could not save a trigger", err)
	}
	nativeCreationMust(t, gate.TryFence(t.Context()))
}

func TestNativeWorkspaceCreationRefusesMissingProfileAndGenericUpsert(t *testing.T) {
	db, adapter, files, _ := nativeCreationFixture(t)
	composed := workspace.NewSyncStore(adapter, files)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Not an upsert"})
	if err := composed.Save(ws); err == nil {
		t.Fatal("ordinary Save admitted an unknown native identity")
	}
	var rows int
	nativeCreationMust(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspaces WHERE id=?`, ws.ID).Scan(&rows))
	if rows != 0 {
		t.Fatal("ordinary Save inserted a provisional row")
	}
	ws = workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Missing definition", Agents: []string{"Missing"}})
	if err := workspace.CreateNativeWorkspace(t.Context(), composed, ws); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("missing canonical profile was treated as complete", err)
	}
	a, err := workspacecontinuity.NewLocalStore(db).Attachment(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if !a.Provisioning || a.AllowsManual() {
		t.Fatal("profile-free native work was admitted")
	}
}

func TestNativeWorkspaceCreationCannotAdoptDiscoveredFolderIdentity(t *testing.T) {
	db, adapter, files, _ := nativeCreationFixture(t)
	legacy, err := workspace.NewFileStore(files.BasePath())
	nativeCreationMust(t, err)
	t.Cleanup(func() { _ = legacy.Close() })
	retained := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Retained copy"})
	nativeCreationMust(t, legacy.Save(retained))
	folder, err := legacy.GetFolderPath(retained.ID)
	nativeCreationMust(t, err)
	before, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, "workspace.json", workspacecontinuity.MaxChunkBytes)
	nativeCreationMust(t, err)
	nativeCreationMust(t, files.Reload())
	attempt := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Different name"})
	attempt.ID = retained.ID
	if err := workspace.CreateNativeWorkspace(t.Context(), workspace.NewSyncStore(adapter, files), attempt); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("creation adopted a discovered disk identity", err)
	}
	after, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, "workspace.json", workspacecontinuity.MaxChunkBytes)
	nativeCreationMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("refused creation changed retained source bytes")
	}
	var rows int
	nativeCreationMust(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspaces WHERE id=?`, retained.ID).Scan(&rows))
	if rows != 0 {
		t.Fatal("refused creation registered the retained copy")
	}
}

func TestNativeWorkspaceCreationDoesNotUseCheckpointSizeAsLiveLimit(t *testing.T) {
	db, adapter, files, _ := nativeCreationFixture(t)
	composed := workspace.NewSyncStore(adapter, files)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Large native"})
	ws.SharedData = map[string]any{"native_history": strings.Repeat("x", workspacecontinuity.MaxChunkBytes+1)}
	nativeCreationMust(t, workspace.CreateNativeWorkspace(t.Context(), composed, ws))
	a, err := workspacecontinuity.NewLocalStore(db).Attachment(t.Context(), ws.ID)
	nativeCreationMust(t, err)
	if !a.AllowsManual() {
		t.Fatal("large native work was left unusable")
	}
	if err := files.PrepareNativeLocalConfig(t.Context(), ws.ID); !errors.Is(err, workspacecontinuity.ErrLimit) {
		t.Fatal("oversized checkpoint preparation was not reported separately", err)
	}
}
