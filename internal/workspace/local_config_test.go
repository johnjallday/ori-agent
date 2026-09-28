package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func localConfigMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func localConfigFixture(t *testing.T) (*LocalConfigStore, *FileStore, *Workspace, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("ORI_SECRET_STORE_BACKEND", "memory")
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "data", "sessions.db"), WALMode: true})
	localConfigMust(t, err)
	t.Cleanup(func() { _ = db.Close() })
	files, err := NewFileStore(filepath.Join(root, "workspaces"))
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Synthetic", Agents: []string{"Guide"}})
	ws.OwnerUserID = "local"
	ws.MCPBindings = []MCPBinding{{ID: "connector", ServerName: "synthetic-connector", Enabled: true,
		Config: map[string]any{"arbitrary_name": "synthetic-connector-secret"}, Scope: map[string]any{"account": "source-local-account"}}}
	ws.SkillBindings = []SkillBinding{{ID: "skill", SkillName: "synthetic-skill", Enabled: true, Trusted: true,
		Config: map[string]any{"opaque": map[string]any{"value": "synthetic-skill-secret"}}}}
	ws.AllowNativeMCPCLI = true
	ws.RuntimeState = &WorkspaceRuntimeState{SelectedModeID: "assisted", Grants: []RuntimeCapabilityGrant{{
		CapabilityKey: "synthetic-capability", AgentInstanceID: ws.AgentInstances[0].ID, GrantedAt: time.Now().UTC()}},
		RequirementStates: []RuntimeRequirementState{{RequirementKey: "connection", ConfigurationState: "configured"}}}
	ws.Messages = []AgentMessage{{ID: "message-one", From: "Guide", Content: "Private authored prose stays exact", Timestamp: time.Now().UTC()}}
	localConfigMust(t, files.Save(ws))
	_, err = db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?,'Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, ws.ID)
	localConfigMust(t, err)
	localConfigMust(t, workspacecontinuity.NewLocalStore(db).RegisterNative(t.Context(), ws.ID))
	folder, err := files.GetFolderPath(ws.ID)
	localConfigMust(t, err)
	return NewLocalConfigStore(db, vault.NewMemorySecretStore()), files, ws, folder
}

func syntheticPrivateAgent() *agent.Agent {
	yes := true
	return &agent.Agent{Role: types.RoleOrchestrator, Appearance: types.NewAgentAppearance(),
		Metadata:     &types.AgentMetadata{Description: "Authored description", Tags: []string{"stable-provenance"}},
		Capabilities: []string{"draft"}, Settings: types.Settings{APIKey: "synthetic-agent-secret", Model: "offline-model",
			SystemPrompt: "User-authored content, not a secret config field", AllowWebSearch: &yes, AllowNativeMCPTools: &yes, FallbackAllowCloud: &yes}}
}

func TestLocalConfigSeparatesPhysicalFilesAndPreservesNativeUse(t *testing.T) {
	local, files, ws, folder := localConfigFixture(t)
	original := syntheticPrivateAgent()
	localConfigMust(t, files.SaveWorkspaceAgent(ws.ID, "Guide", original))
	localConfigMust(t, local.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"))
	localConfigMust(t, local.MigrateBindingsFile(t.Context(), folder, ws.ID))
	for _, name := range []string{"agents/guide/config.json", "workspace.json"} {
		data, err := os.ReadFile(filepath.Join(folder, name))
		localConfigMust(t, err)
		for _, forbidden := range []string{"synthetic-agent-secret", "synthetic-connector-secret", "synthetic-skill-secret", "source-local-account"} {
			if bytes.Contains(data, []byte(forbidden)) {
				t.Fatalf("private configuration remained in %s", name)
			}
		}
		info, err := os.Stat(filepath.Join(folder, name))
		localConfigMust(t, err)
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("new projection is not private")
		}
	}
	got, err := local.ReadAgentFile(t.Context(), folder, ws.ID, "Guide")
	localConfigMust(t, err)
	if !reflect.DeepEqual(got.Settings, original.Settings) || !reflect.DeepEqual(got.Metadata, original.Metadata) || !reflect.DeepEqual(got.Appearance, original.Appearance) {
		t.Fatal("native agent lost configuration or authored data")
	}
	loaded, err := local.ReadBindingsFile(t.Context(), folder, ws.ID)
	localConfigMust(t, err)
	for name, pair := range map[string][2]any{
		"connectors": {loaded.MCPBindings, ws.MCPBindings}, "skills": {loaded.SkillBindings, ws.SkillBindings},
		"runtime": {loaded.RuntimeState, ws.RuntimeState}, "messages": {loaded.Messages, ws.Messages},
		"instances": {loaded.AgentInstances, ws.AgentInstances},
	} {
		gotJSON, err := json.Marshal(pair[0])
		localConfigMust(t, err)
		wantJSON, err := json.Marshal(pair[1])
		localConfigMust(t, err)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("native workspace lost %s", name)
		}
	}
	if !loaded.AllowNativeMCPCLI || !loaded.CreatedAt.Equal(ws.CreatedAt) {
		t.Fatal("native workspace lost grant or creation date")
	}
	var ciphertext []byte
	localConfigMust(t, local.db.QueryRowContext(t.Context(), `SELECT ciphertext FROM workspace_local_config WHERE kind='agent'`).Scan(&ciphertext))
	if bytes.Contains(ciphertext, []byte(original.Settings.APIKey)) || json.Valid(ciphertext) {
		t.Fatal("private SQL payload is plaintext")
	}
	// Idempotent migration does not replace the reference or disable native use.
	before, err := os.ReadFile(filepath.Join(folder, "agents/guide/config.json"))
	localConfigMust(t, err)
	localConfigMust(t, local.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"))
	after, err := os.ReadFile(filepath.Join(folder, "agents/guide/config.json"))
	localConfigMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("repeat migration rewrote a prepared agent")
	}
	// A normal scoped edit carries the private key forward and prunes old slots.
	oldCopy := *got
	got.Settings.Model = "new-offline-model"
	localConfigMust(t, local.WriteAgentFile(t.Context(), folder, ws.ID, "Guide", got))
	got, err = local.ReadAgentFile(t.Context(), folder, ws.ID, "Guide")
	localConfigMust(t, err)
	if got.Settings.APIKey != original.Settings.APIKey || got.Settings.Model != "new-offline-model" {
		t.Fatal("native edit lost its local key")
	}
	if err := local.WriteAgentFile(t.Context(), folder, ws.ID, "Guide", &oldCopy); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatalf("stale private settings restored: %v", err)
	}
	var count int
	localConfigMust(t, local.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_local_config WHERE kind='agent'`).Scan(&count))
	if count != 1 {
		t.Fatal("superseded private configuration retained")
	}
}

func TestLocalConfigCopyAndNewAttachmentCannotReuseAuthority(t *testing.T) {
	local, files, ws, folder := localConfigFixture(t)
	localConfigMust(t, files.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	localConfigMust(t, local.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"))
	localConfigMust(t, local.MigrateBindingsFile(t.Context(), folder, ws.ID))
	// Give the same physical file a NEW local import attachment, leaving old
	// ciphertext present deliberately. Matching workspace/slot IDs are not consent.
	_, err := local.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state='imported_inactive',operation_id=? WHERE workspace_id=?`, "native", ws.ID)
	localConfigMust(t, err)
	ag, err := local.ReadAgentFile(t.Context(), folder, ws.ID, "Guide")
	localConfigMust(t, err)
	if ag.Settings.APIKey != "" || ag.Settings.IsWebSearchAllowed() || ag.Settings.IsNativeMCPToolsAllowed() || *ag.Settings.FallbackAllowCloud {
		t.Fatal("copied reference reused old credentials/grants")
	}
	copied, err := local.ReadBindingsFile(t.Context(), folder, ws.ID)
	localConfigMust(t, err)
	if len(copied.MCPBindings[0].Config) > 0 || len(copied.MCPBindings[0].Scope) > 0 || len(copied.SkillBindings[0].Config) > 0 ||
		copied.SkillBindings[0].Trusted || copied.AllowNativeMCPCLI || len(copied.RuntimeState.Grants) > 0 || len(copied.RuntimeState.RequirementStates) > 0 {
		t.Fatal("copied workspace activated private configuration")
	}
	if copied.RuntimeState.SelectedModeID != "assisted" || !copied.MCPBindings[0].Enabled || !reflect.DeepEqual(copied.Messages, ws.Messages) {
		t.Fatal("inert intent/history was removed")
	}
	if err := local.MigrateBindingsFile(t.Context(), folder, ws.ID); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("imported attachment was migrated as native")
	}
	// No attachment after reset/discovery means not even manual private reads.
	_, err = local.db.ExecContext(t.Context(), `DELETE FROM continuity_attachments WHERE workspace_id=?`, ws.ID)
	localConfigMust(t, err)
	if _, err := local.ReadAgentFile(t.Context(), folder, ws.ID, "Guide"); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("unreviewed folder was given private runtime access")
	}
}

type unavailableLocalSecrets struct{ vault.SecretStore }

func (unavailableLocalSecrets) Get(vault.SecretKey) (string, error) {
	return "", errors.New("synthetic-private-backend-detail")
}

func TestLocalConfigUnavailableBackendAndWriteFailurePreserveSource(t *testing.T) {
	local, files, ws, folder := localConfigFixture(t)
	localConfigMust(t, files.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	path := filepath.Join(folder, "agents/guide/config.json")
	before, err := os.ReadFile(path)
	localConfigMust(t, err)
	failed := NewLocalConfigStore(local.db, unavailableLocalSecrets{})
	err = failed.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide")
	if !errors.Is(err, ErrLocalConfigUnavailable) || strings.Contains(err.Error(), "backend-detail") {
		t.Fatal("backend failure was not bounded")
	}
	after, err := os.ReadFile(path)
	localConfigMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("failed secure storage silently stripped native configuration")
	}
	// An interrupted encrypted stage never becomes the canonical configuration.
	_, err = local.Stage(t.Context(), ws.ID, "bindings", "workspace", []byte(`{"staged":"unpublished"}`))
	localConfigMust(t, err)
	// A source change between inspection and publication preserves the external
	// edit, even with an unpublished encrypted slot left by an earlier attempt.
	err = local.WriteBindingsFile(t.Context(), folder, ws, workspacecontinuity.Digest([]byte("stale")))
	if !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatalf("stale source accepted: %v", err)
	}
	loaded, err := local.ReadBindingsFile(t.Context(), folder, ws.ID)
	localConfigMust(t, err)
	if !reflect.DeepEqual(loaded.MCPBindings, ws.MCPBindings) {
		t.Fatal("failed projection lost the legacy source configuration")
	}
	localConfigMust(t, local.MigrateBindingsFile(t.Context(), folder, ws.ID))
	var count int
	localConfigMust(t, local.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_local_config WHERE kind='bindings'`).Scan(&count))
	if count != 1 {
		t.Fatal("retry did not prune unpublished private slots")
	}
}

func TestLocalConfigCiphertextIsBoundAndResetClassified(t *testing.T) {
	local, _, ws, _ := localConfigFixture(t)
	ctx := t.Context()
	slot, err := local.Stage(ctx, ws.ID, "agent", "guide", []byte(`{"version":1,"api_key":"synthetic"}`))
	localConfigMust(t, err)
	_, err = local.db.ExecContext(ctx, `UPDATE workspace_local_config SET item_id='other' WHERE slot_id=?`, slot)
	localConfigMust(t, err)
	if _, _, err := local.Load(ctx, ws.ID, "agent", "other", slot); !errors.Is(err, ErrLocalConfigInvalid) {
		t.Fatal("ciphertext was reassigned to another item")
	}
	inspection, err := database.InspectReset(ctx, local.db.DB)
	localConfigMust(t, err)
	if len(inspection.Problems) != 0 || inspection.Counts["workspace_local_config"] == nil || *inspection.Counts["workspace_local_config"] != 1 {
		t.Fatal("private configuration is not reset-classified")
	}
	path := local.db.Path()
	localConfigMust(t, local.db.Close())
	counts, err := database.ResetAppRecords(context.Background(), path)
	localConfigMust(t, err)
	if counts["workspace_local_config"] != 1 {
		t.Fatal("reset retained private workspace configuration")
	}
	if _, err := local.secrets.Get(vault.SecretKeyWorkspaceConfigDEK); err != nil {
		t.Fatal("app-record reset removed installation encryption material")
	}
}

func TestLocalConfigDirectoryOnlyCopyHasNoPrivateDependency(t *testing.T) {
	source, files, ws, folder := localConfigFixture(t)
	localConfigMust(t, files.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	localConfigMust(t, source.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"))
	localConfigMust(t, source.MigrateBindingsFile(t.Context(), folder, ws.ID))
	destination := t.TempDir()
	copyRoot := filepath.Join(destination, "copy")
	for _, name := range []string{"workspace.json", "agents/guide/config.json"} {
		data, err := os.ReadFile(filepath.Join(folder, name))
		localConfigMust(t, err)
		localConfigMust(t, os.MkdirAll(filepath.Dir(filepath.Join(copyRoot, name)), 0750))
		localConfigMust(t, os.WriteFile(filepath.Join(copyRoot, name), data, 0600))
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(destination, "fresh.db")})
	localConfigMust(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?,'Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, ws.ID)
	localConfigMust(t, err)
	// This is an isolated private-adapter fixture, not the reviewed importer.
	_, err = db.ExecContext(t.Context(), `INSERT INTO continuity_attachments(workspace_id,state,disposition,operation_id,updated_at)
		VALUES (?,'imported_inactive','ordinary',?,CURRENT_TIMESTAMP)`, ws.ID, uuid.NewString())
	localConfigMust(t, err)
	// Any secure-backend read would fail. An absent local slot must not need one.
	reader := NewLocalConfigStore(db, unavailableLocalSecrets{})
	ag, err := reader.ReadAgentFile(t.Context(), copyRoot, ws.ID, "Guide")
	localConfigMust(t, err)
	if ag.Settings.APIKey != "" || ag.Settings.IsWebSearchAllowed() || ag.Settings.IsNativeMCPToolsAllowed() || *ag.Settings.FallbackAllowCloud {
		t.Fatal("directory copy transferred agent credentials or grants")
	}
	copied, err := reader.ReadBindingsFile(t.Context(), copyRoot, ws.ID)
	localConfigMust(t, err)
	if len(copied.MCPBindings[0].Config) != 0 || len(copied.SkillBindings[0].Config) != 0 || copied.AllowNativeMCPCLI || len(copied.RuntimeState.Grants) != 0 {
		t.Fatal("directory copy transferred connector configuration or authority")
	}
	if !reflect.DeepEqual(copied.Messages, ws.Messages) || ag.Metadata.Description != "Authored description" {
		t.Fatal("private separation changed authored history")
	}
	// Explicit local setup can subsequently publish new, destination-owned keys.
	reader = NewLocalConfigStore(db, vault.NewMemorySecretStore())
	ag.Settings.APIKey = "destination-only-key"
	localConfigMust(t, reader.WriteAgentFile(t.Context(), copyRoot, ws.ID, "Guide", ag))
	ag, err = reader.ReadAgentFile(t.Context(), copyRoot, ws.ID, "Guide")
	localConfigMust(t, err)
	if ag.Settings.APIKey != "destination-only-key" || ag.Settings.IsNativeMCPToolsAllowed() {
		t.Fatal("explicit local setup failed or enabled an unrelated grant")
	}
	original, err := source.ReadAgentFile(t.Context(), folder, ws.ID, "Guide")
	localConfigMust(t, err)
	if original.Settings.APIKey != "synthetic-agent-secret" {
		t.Fatal("destination edit changed source configuration")
	}
}

func TestLocalConfigBoundsMalformedDataAndDeletion(t *testing.T) {
	local, files, ws, folder := localConfigFixture(t)
	localConfigMust(t, files.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	for i := 0; i < 32; i++ {
		_, err := local.Stage(t.Context(), ws.ID, "agent", "guide", []byte(`{"unused":true}`))
		localConfigMust(t, err)
	}
	if _, err := local.Stage(t.Context(), ws.ID, "agent", "guide", []byte(`{"unused":true}`)); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("unbounded unpublished slot accumulation")
	}
	// A legitimate retry reads the legacy file, discards only unpublished slots,
	// and can still complete after the bounded interrupted-stage allowance fills.
	localConfigMust(t, local.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"))
	if _, err := local.Stage(t.Context(), ws.ID, "agent", "guide", []byte(`{"blob":"`+strings.Repeat("x", maxLocalConfigBytes)+`"}`)); !errors.Is(err, ErrLocalConfigInvalid) {
		t.Fatal("unbounded private payload")
	}
	if err := applyAgentLocalConfig(&agent.Agent{}, []byte(`{"version":1,"api_key":"x"}`)); !errors.Is(err, ErrLocalConfigInvalid) {
		t.Fatal("missing private grant fields fabricated legacy allow defaults")
	}
	data, err := os.ReadFile(filepath.Join(folder, "agents/guide/config.json"))
	localConfigMust(t, err)
	var portable agent.Agent
	localConfigMust(t, json.Unmarshal(data, &portable))
	// Read-only inspection never normalizes or stages this ambiguous source.
	malformed := append([]byte(`{"Settings":{"api_key":"x"},`), data[1:]...)
	localConfigMust(t, os.WriteFile(filepath.Join(folder, "agents/guide/config.json"), malformed, 0600))
	if err := local.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"); err == nil {
		t.Fatal("ambiguous source was rewritten")
	}
	after, err := os.ReadFile(filepath.Join(folder, "agents/guide/config.json"))
	localConfigMust(t, err)
	if !bytes.Equal(after, malformed) {
		t.Fatal("failed inspection mutated the source")
	}
	_, err = local.db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id=?`, ws.ID)
	localConfigMust(t, err)
	var count int
	localConfigMust(t, local.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_local_config WHERE workspace_id=?`, ws.ID).Scan(&count))
	if count != 0 {
		t.Fatal("workspace deletion retained private configuration")
	}
}

func TestLocalConfigParallelStagesShareOneEncryptionKey(t *testing.T) {
	local, _, ws, _ := localConfigFixture(t)
	const workers = 12
	done := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			store := NewLocalConfigStore(local.db, local.secrets)
			item := uuid.NewString()
			data := []byte(`{"synthetic":"parallel-local-value"}`)
			slot, err := store.Stage(t.Context(), ws.ID, "agent", item, data)
			if err == nil {
				var loaded []byte
				var found bool
				loaded, found, err = store.Load(t.Context(), ws.ID, "agent", item, slot)
				if err == nil && (!found || !bytes.Equal(loaded, data)) {
					err = errors.New("parallel stage lost local configuration")
				}
			}
			done <- err
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

type privateSnapshotSpy struct {
	Store
	owner         Store
	fallbackCalls int
}

func (s *privateSnapshotSpy) SetWorkspaceAgentStore(owner Store) { s.owner = owner }
func (s *privateSnapshotSpy) GetWorkspaceAgent(id, name string) (*agent.Agent, bool, error) {
	s.fallbackCalls++
	return syntheticPrivateAgent(), true, nil
}
func (s *privateSnapshotSpy) SaveWorkspaceAgent(id, name string, ag *agent.Agent) error {
	s.fallbackCalls++
	return nil
}

func TestLocalConfigCanonicalFileStorePreservesPrivateNativeEdits(t *testing.T) {
	local, legacy, ws, folder := localConfigFixture(t)
	localConfigMust(t, legacy.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	localConfigMust(t, legacy.Close())
	files, err := NewFileStoreWithLocalConfig(filepath.Dir(folder), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	localConfigMust(t, files.PrepareNativeLocalConfig(t.Context(), ws.ID))
	primary := &privateSnapshotSpy{Store: NewInMemoryStore()}
	localConfigMust(t, primary.Save(ws))
	synced := NewSyncStore(primary, files)
	if primary.owner != files {
		t.Fatal("primary adapter did not receive the canonical snapshot owner")
	}
	ag, found, err := synced.GetWorkspaceAgent(ws.ID, "Guide")
	localConfigMust(t, err)
	if !found || ag.Settings.APIKey != "synthetic-agent-secret" {
		t.Fatal("canonical agent reader lost its native key")
	}
	ag.Settings.Model = "edited-offline-model"
	localConfigMust(t, synced.SaveWorkspaceAgent(ws.ID, "Guide", ag))
	if primary.fallbackCalls != 0 {
		t.Fatal("private-aware sync touched the legacy plaintext snapshot path")
	}
	localConfigMust(t, files.Update(ws.ID, func(current *Workspace) error {
		current.MCPBindings[0].Config["arbitrary_name"] = "synthetic-updated-secret"
		current.Name = "Edited label"
		return nil
	}))
	loaded, err := files.Get(ws.ID)
	localConfigMust(t, err)
	if loaded.MCPBindings[0].Config["arbitrary_name"] != "synthetic-updated-secret" || !loaded.AllowNativeMCPCLI {
		t.Fatal("canonical workspace update lost local configuration")
	}
	// A rename must load full history/private state, not write the lean cache.
	localConfigMust(t, files.Rename(ws.ID, "Renamed workspace"))
	folder, err = files.GetFolderPath(ws.ID)
	localConfigMust(t, err)
	loaded, err = files.Get(ws.ID)
	localConfigMust(t, err)
	if !reflect.DeepEqual(loaded.Messages, ws.Messages) || loaded.MCPBindings[0].Config["arbitrary_name"] != "synthetic-updated-secret" {
		t.Fatal("rename lost history or private configuration")
	}
	before, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
	localConfigMust(t, err)
	if bytes.Contains(before, []byte("synthetic-updated-secret")) || bytes.Contains(before, []byte("synthetic-skill-secret")) {
		t.Fatal("canonical save leaked local configuration back into the folder")
	}
	// Reopening without the local owner cannot silently strip native keys or
	// overwrite a marked workspace from a marker-less partial projection.
	localConfigMust(t, files.Close())
	unconfigured, err := NewFileStore(filepath.Dir(folder))
	localConfigMust(t, err)
	t.Cleanup(func() { _ = unconfigured.Close() })
	if _, _, err := unconfigured.GetWorkspaceAgent(ws.ID, "Guide"); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("legacy reader returned an unusable projection as a native agent")
	}
	loaded.WorkspaceLocalConfigID = ""
	if err := unconfigured.Save(loaded); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatalf("unconfigured writer bypassed the private owner: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
	localConfigMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected legacy write changed the prepared file")
	}
	// Revocation fails closed, without a same-name primary fallback.
	_, err = local.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state='detached' WHERE workspace_id=?`, ws.ID)
	localConfigMust(t, err)
	if _, _, err := synced.GetWorkspaceAgent(ws.ID, "Guide"); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("detached private agent was readable")
	}
	if primary.fallbackCalls != 0 {
		t.Fatal("revocation fell back to an unrelated primary agent")
	}
}

func TestLocalConfigPreparedLiveStateCanOutgrowCheckpointLimit(t *testing.T) {
	local, legacy, ws, folder := localConfigFixture(t)
	localConfigMust(t, legacy.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	localConfigMust(t, legacy.SaveWorkspaceAgent(ws.ID, "Unused profile", syntheticPrivateAgent()))
	localConfigMust(t, legacy.Close())
	files, err := NewFileStoreWithLocalConfig(filepath.Dir(folder), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	localConfigMust(t, files.PrepareNativeLocalConfig(t.Context(), ws.ID))
	unused, err := os.ReadFile(filepath.Join(folder, "agents", "unused-profile", WorkspaceAgentConfigFile))
	localConfigMust(t, err)
	if bytes.Contains(unused, []byte("synthetic-agent-secret")) {
		t.Fatal("unused canonical profile still leaked credentials into the copy")
	}
	history := strings.Repeat("synthetic-history ", 1<<19)
	localConfigMust(t, files.Update(ws.ID, func(current *Workspace) error {
		current.Messages[0].Content = history
		return nil
	}))
	localConfigMust(t, files.Update(ws.ID, func(current *Workspace) error {
		current.Name = "Large but live"
		return nil
	}))
	got, err := files.Get(ws.ID)
	localConfigMust(t, err)
	if got.Messages[0].Content != history || got.Name != "Large but live" || !got.AllowNativeMCPCLI {
		t.Fatal("checkpoint limit affected live canonical work")
	}
	data, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
	localConfigMust(t, err)
	if bytes.Contains(data, []byte("synthetic-connector-secret")) || bytes.Contains(data, []byte("synthetic-skill-secret")) {
		t.Fatal("large live save fell back to plaintext configuration")
	}
	if err := files.PrepareNativeLocalConfig(t.Context(), ws.ID); !errors.Is(err, workspacecontinuity.ErrLimit) {
		t.Fatalf("oversized source did not report a separate preparation limit: %v", err)
	}
}

func TestLocalConfigUnavailablePreparationDoesNotBlockLegacyLiveSaves(t *testing.T) {
	local, legacy, ws, folder := localConfigFixture(t)
	localConfigMust(t, legacy.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	localConfigMust(t, legacy.Close())
	unavailable := NewLocalConfigStore(local.db, unavailableLocalSecrets{})
	files, err := NewFileStoreWithLocalConfig(filepath.Dir(folder), unavailable)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	if err := files.PrepareNativeLocalConfig(t.Context(), ws.ID); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatalf("unavailable backend did not refuse preparation: %v", err)
	}
	history := strings.Repeat("synthetic-history ", 1<<19) // Larger than the checkpoint canonical-file limit.
	localConfigMust(t, files.Update(ws.ID, func(current *Workspace) error {
		current.Messages[0].Content = history
		return nil
	}))
	// Exercise the ownership-marker guard on an already-large legacy file too.
	localConfigMust(t, files.Update(ws.ID, func(current *Workspace) error {
		current.Name = "Still editable"
		return nil
	}))
	got, err := files.Get(ws.ID)
	localConfigMust(t, err)
	if got.Messages[0].Content != history || got.Name != "Still editable" || !got.AllowNativeMCPCLI {
		t.Fatal("preparation limit or unavailable backend lost canonical native work")
	}
	if err := files.PrepareNativeLocalConfig(t.Context(), ws.ID); !errors.Is(err, workspacecontinuity.ErrLimit) {
		t.Fatalf("oversized checkpoint was not reported separately: %v", err)
	}
}

func TestLocalConfigFolderMovePreservesNativeConfiguration(t *testing.T) {
	local, legacy, ws, folder := localConfigFixture(t)
	localConfigMust(t, legacy.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	parent := NewWorkspace(CreateWorkspaceParams{Name: "Parent", Agents: []string{}})
	parent.OwnerUserID = "local"
	_, err := local.db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES(?,?,?,?,?)`, parent.ID, parent.Name, parent.OwnerUserID, parent.CreatedAt, parent.UpdatedAt)
	localConfigMust(t, err)
	localConfigMust(t, workspacecontinuity.NewLocalStore(local.db).RegisterNative(t.Context(), parent.ID))
	localConfigMust(t, legacy.Save(parent))
	localConfigMust(t, legacy.Close())
	files, err := NewFileStoreWithLocalConfig(filepath.Dir(folder), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	localConfigMust(t, files.PrepareNativeLocalConfig(t.Context(), ws.ID))
	before, err := files.Get(ws.ID)
	localConfigMust(t, err)
	moved, err := files.MoveWorkspaceFolder(ws.ID, parent.ID)
	localConfigMust(t, err)
	if len(moved) != 1 || moved[0].OldPath == moved[0].NewPath {
		t.Fatal("native folder was not moved")
	}
	after, err := files.Get(ws.ID)
	localConfigMust(t, err)
	if after.ParentID != parent.ID || !after.AllowNativeMCPCLI ||
		!reflect.DeepEqual(after.MCPBindings, before.MCPBindings) ||
		!reflect.DeepEqual(after.SkillBindings, before.SkillBindings) ||
		!reflect.DeepEqual(after.Messages, before.Messages) {
		t.Fatal("folder move saved the denied projection over native configuration/history")
	}
	profile, found, err := files.GetWorkspaceAgent(ws.ID, "Guide")
	localConfigMust(t, err)
	if !found || profile.Settings.APIKey != "synthetic-agent-secret" {
		t.Fatal("folder move lost scoped native credentials")
	}
}

func TestLocalConfigNativeReadersRefuseLinksAndAmbiguousReferences(t *testing.T) {
	for _, data := range []string{
		`{"id":"legacy","ori_local_config_id":null}`,
		`{"id":"legacy","ORI_LOCAL_CONFIG_ID":"copied"}`,
		`{"id":"legacy","ori_local_config_id":"copied","ori_local_config_id":""}`,
	} {
		folder := t.TempDir()
		localConfigMust(t, os.WriteFile(filepath.Join(folder, WorkspaceConfigFile), []byte(data), 0600))
		if _, err := legacyWorkspaceReference(folder); err == nil {
			t.Fatal("ambiguous marker bypassed the canonical private owner")
		}
	}
	folder, outside := t.TempDir(), t.TempDir()
	localConfigMust(t, os.WriteFile(filepath.Join(outside, WorkspaceConfigFile), []byte(`{"id":"outside"}`), 0600))
	if err := os.Symlink(filepath.Join(outside, WorkspaceConfigFile), filepath.Join(folder, WorkspaceConfigFile)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readNativeWorkspaceFile(folder); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("native reader followed a workspace.json symlink")
	}
	if _, err := legacyWorkspaceReference(folder); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("native ownership guard followed a workspace.json symlink")
	}
}

func TestLocalConfigToolLimitsAndClassificationStayLocal(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Synthetic"})
	ws.MCPBindings = []MCPBinding{
		{ID: "deny", ServerName: "deny", AllowedTools: []string{}, DefaultSideEffect: SideEffectRead},
		{ID: "limited", ServerName: "limited", AllowedTools: []string{"synthetic-tool"}, ToolOverrides: map[string]SideEffect{"synthetic-tool": SideEffectRead}},
		{ID: "all", ServerName: "all", AllowedTools: nil},
	}
	ws.SkillBindings = []SkillBinding{{ID: "skill", SkillName: "synthetic-skill", Trusted: true, DefaultSideEffect: SideEffectRead}}
	portable, private, err := splitWorkspaceLocalConfig(ws)
	localConfigMust(t, err)
	for _, binding := range portable.MCPBindings {
		if binding.AllowedTools == nil || len(binding.AllowedTools) != 0 || binding.DefaultSideEffect != "" || len(binding.ToolOverrides) > 0 {
			t.Fatal("projection granted tools or classifications")
		}
	}
	if portable.SkillBindings[0].Trusted || portable.SkillBindings[0].DefaultSideEffect != "" {
		t.Fatal("projection granted skill trust")
	}
	data, err := portable.ToJSON()
	localConfigMust(t, err)
	var roundTrip Workspace
	localConfigMust(t, workspacecontinuity.DecodeDocument(data, &roundTrip, workspacecontinuity.MaxChunkBytes))
	for _, binding := range roundTrip.MCPBindings {
		if binding.AllowsAllTools() || binding.ToolAllowed("synthetic-tool") {
			t.Fatal("serialization widened deny-all into inherited/all-tools access")
		}
	}
	localConfigMust(t, applyBindingsLocalConfig(&roundTrip, private))
	if !reflect.DeepEqual(roundTrip.MCPBindings, ws.MCPBindings) || !reflect.DeepEqual(roundTrip.SkillBindings, ws.SkillBindings) {
		t.Fatal("native private read changed a tool restriction or classification")
	}
	// Required nested fields are not optional legacy permission defaults.
	malformed := bytes.Replace(private, []byte(`"allowed_tools":null,`), nil, 1)
	if err := applyBindingsLocalConfig(&Workspace{}, malformed); !errors.Is(err, ErrLocalConfigInvalid) {
		t.Fatal("missing nested tool choice became all-tools permission")
	}
	roundTrip.MCPBindings[0].ServerName = "another-connector"
	if err := applyBindingsLocalConfig(&roundTrip, private); !errors.Is(err, ErrLocalConfigInvalid) {
		t.Fatal("connector identity edit redirected stored configuration")
	}
}
