//go:build darwin || linux

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/backuprecoveryhttp"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type blockedBackupPicker struct{ entered, finish chan struct{} }

func (*blockedBackupPicker) Available() bool { return true }
func (p *blockedBackupPicker) Choose(context.Context, string) (string, bool, error) {
	close(p.entered)
	<-p.finish
	return "", false, nil
}

func TestBackupRecoveryUnavailableCompositionRegistersHonestEndpoint(t *testing.T) {
	builder := &ServerBuilder{}
	builder.wireBackupRecovery()
	mux := http.NewServeMux()
	registerBackupRecoveryRoutes(mux, &Server{Handlers: &HandlerFacade{BackupRecovery: builder.backupRecoveryHandler}})
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8765/api/workspaces/invented/backup-recovery", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || builder.backupRecoveryService != nil {
		t.Fatal("nil dependencies invented a runtime", response.Code, response.Body.String())
	}
}

// Real server composition and stored domain HTTP, with no backup executable,
// model invocation, selected personal path, credential, or provider fixture.
func TestBackupRecoveryServerCompositionWithoutToolAndRetryableClose(t *testing.T) {
	isolateServerBuildEnv(t)
	t.Setenv("WORKSPACE_DIR", filepath.Join(t.TempDir(), "workspaces"))
	t.Setenv("ORI_BACKUP_RESTIC_EXECUTABLE", "")
	t.Setenv("ORI_DISABLE_NATIVE_SECRET_STORE", "1")
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "GROK_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	manager := config.NewManagerWithSecretStore(filepath.Join(config.DefaultDataDir(), "settings.json"), vault.NewMemorySecretStore())
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatal(err)
	}
	builder.WithConfigManager(manager).WithDesktopOpener(discardDesktopOpener{})
	srv, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Shutdown()
		if err := builder.sessionStore.Close(); err != nil {
			t.Error(err)
		}
	})
	if builder.backupRecoveryService == nil || srv.backupRecoveryService != builder.backupRecoveryService || srv.Handlers.BackupRecovery == nil || builder.setupWizardService == nil {
		t.Fatal("incomplete startup composition")
	}
	handler := srv.Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8765"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:8765")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(backuprecoveryhttp.ActionHeader, backuprecoveryhttp.ActionValue)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := call(http.MethodPost, "/api/workspaces", `{"name":"Ordinary workspace without backup tool"}`)
	var result struct {
		Folder struct {
			ID string `json:"id"`
		} `json:"folder"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &result) != nil || result.Folder.ID == "" {
		t.Fatal("ordinary creator failed", created.Code, created.Body.String())
	}
	ordinary := result.Folder
	catalog := "/api/workspaces/" + ordinary.ID + "/capabilities"
	if response := call(http.MethodGet, catalog, ""); response.Code != 200 {
		t.Fatal("ordinary catalog failed", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, catalog+"/backup-recovery/install", `{"source":"in-place"}`); response.Code != 200 {
		t.Fatal("inert install failed", response.Code, response.Body.String())
	}
	base := "/api/workspaces/" + ordinary.ID + "/backup-recovery"
	response := call(http.MethodGet, base, "")
	var stored struct {
		Status backuprecovery.Status `json:"status"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &stored) != nil || stored.Status.RuntimeAvailable || stored.Status.Connection != "not_configured" {
		var owner, kind, activity, installs, attachment, operation, source, digest string
		var provisional int
		err := builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT w.owner_user_id,w.kind,w.status,w.installed_capabilities_json,COALESCE(a.state,''),COALESCE(a.operation_id,''),COALESCE(a.source_generation,''),COALESCE(a.source_digest,''),(SELECT COUNT(*) FROM continuity_file_mutations m WHERE m.workspace_id=w.id AND m.target='native-create') FROM workspaces w LEFT JOIN continuity_attachments a ON a.workspace_id=w.id WHERE w.id=?`, ordinary.ID).Scan(&owner, &kind, &activity, &installs, &attachment, &operation, &source, &digest, &provisional)
		t.Fatalf("stored setup: HTTP=%d body=%s; invented fixture owner=%q kind=%q status=%q installs=%s attachment=%q operation=%q source=%q digest=%q provisional=%d err=%v", response.Code, response.Body.String(), owner, kind, activity, installs, attachment, operation, source, digest, provisional, err)
	}
	if response := call(http.MethodGet, base+"/guide", ""); response.Code != 200 || !strings.Contains(response.Body.String(), "0.19.1") {
		t.Fatal("model-free guide unavailable", response.Code, response.Body.String())
	}
	var count int
	if err := builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM backup_recovery_local`).Scan(&count); err != nil || count != 0 {
		t.Fatal("install/GET created configuration", count, err)
	}

	control := backuprecovery.ControlRequest{Generation: stored.Status.Generation, Revision: stored.Status.Revision, OperationID: uuid.NewString()}
	payload, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if response := call(http.MethodDelete, catalog+"/backup-recovery", string(payload)); response.Code != 200 {
		t.Fatal("guarded removal failed", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, catalog+"/backup-recovery/install", `{"source":"in-place"}`); response.Code != 200 {
		t.Fatal("inert reinstallation failed", response.Code, response.Body.String())
	}
	if response := call(http.MethodDelete, catalog+"/backup-recovery", string(payload)); response.Code != 409 {
		t.Fatal("old removal targeted reinstallation", response.Code, response.Body.String())
	}

	// A user prompt that has not drained must keep stores and once-only
	// shutdown open for a safe retry, rather than closing them behind its work.
	if err := srv.backupRecoveryService.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	picker := &blockedBackupPicker{entered: make(chan struct{}), finish: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(picker.finish) }) }
	defer release()
	service, err := backuprecovery.NewService(backuprecovery.ServiceOptions{Database: builder.sessionStore.DB(), Secrets: builder.configManager.SecretStore(), DataDir: config.DefaultDataDir(), WorkGate: builder.resetWork, Picker: picker, Selections: builder.pathSelectionStore})
	if err != nil {
		t.Fatal(err)
	}
	srv.backupRecoveryService = service
	pickDone := make(chan error, 1)
	go func() { _, err := service.Pick(context.Background(), "local", ordinary.ID, "source"); pickDone <- err }()
	<-picker.entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := srv.shutdownBackground(ctx); !errors.Is(err, backuprecovery.ErrBusy) {
		t.Fatal("ambiguous close released ownership", err)
	}
	if err := builder.sessionStore.DB().PingContext(t.Context()); err != nil {
		t.Fatal("store closed under live work", err)
	}
	if owner, err := builder.workspaceStore.Get(ordinary.ID); err != nil || owner == nil {
		t.Fatal("workspace owner closed under live work", err)
	}
	release()
	<-pickDone
	if err := srv.shutdownBackground(t.Context()); err != nil {
		t.Fatal("shutdown could not retry after drain", err)
	}
}
