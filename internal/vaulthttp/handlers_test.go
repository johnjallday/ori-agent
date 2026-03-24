package vaulthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/vault"
)

func newTestHandler(t *testing.T, secretStore vault.SecretStore, fallbackPath string) (*Handler, *vault.Store, *database.DB) {
	t.Helper()

	db, err := database.Open(context.Background(), &database.Config{
		InMemory: true,
		WALMode:  false,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	store := vault.NewStore(db, vault.StoreOptions{
		SecretStore:        secretStore,
		FallbackSecretPath: fallbackPath,
	})
	return NewHandler(store), store, db
}

func performJSONRequest(t *testing.T, handler http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
}

func TestHandlerRecordLifecycle(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"type":         "personal_note",
		"workspace_id": "ws-1",
		"label":        "Passport",
		"tags":         []string{"Travel", "Personal"},
		"payload": map[string]any{
			"number": "X1234567",
		},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Record vault.Record `json:"record"`
	}
	decodeJSONBody(t, createRec, &created)
	if created.Record.ID == "" {
		t.Fatal("expected created record id")
	}

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?workspace_id=ws-1", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from list, got %d: %s", listRec.Code, listRec.Body.String())
	}

	getRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records/"+created.Record.ID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from get, got %d: %s", getRec.Code, getRec.Body.String())
	}

	updateRec := performJSONRequest(t, handler, http.MethodPatch, "/api/vault/records/"+created.Record.ID, map[string]any{
		"label": "Passport Updated",
		"payload": map[string]any{
			"number": "X7654321",
		},
	})
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from update, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	deleteRec := performJSONRequest(t, handler, http.MethodDelete, "/api/vault/records/"+created.Record.ID, nil)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from delete, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestHandlerDeniesActorWithoutGrant(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"type":         "email_snippet",
		"workspace_id": "ws-finance",
		"label":        "Tax Email",
		"payload": map[string]any{
			"body": "1099 attached",
		},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Record vault.Record `json:"record"`
	}
	decodeJSONBody(t, createRec, &created)

	denied := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records/"+created.Record.ID+"?workspace_id=ws-finance&actor_type=agent&actor_id=finance-agent", nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without grant, got %d: %s", denied.Code, denied.Body.String())
	}

	grantRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/grants", map[string]any{
		"workspace_id": "ws-finance",
		"actor_type":   "agent",
		"actor_id":     "finance-agent",
		"capability":   "vault.email.read_saved",
		"record_type":  "email_snippet",
	})
	if grantRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating grant, got %d: %s", grantRec.Code, grantRec.Body.String())
	}

	allowed := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records/"+created.Record.ID+"?workspace_id=ws-finance&actor_type=agent&actor_id=finance-agent", nil)
	if allowed.Code != http.StatusOK {
		t.Fatalf("expected 200 with grant, got %d: %s", allowed.Code, allowed.Body.String())
	}
}

func TestHandlerExportRequiresConfirmationAndPassword(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"type":         "personal_note",
		"workspace_id": "ws-1",
		"label":        "Address",
		"payload": map[string]any{
			"city": "Seoul",
		},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d: %s", createRec.Code, createRec.Body.String())
	}

	noConfirm := performJSONRequest(t, handler, http.MethodPost, "/api/vault/export", map[string]any{
		"workspace_id":   "ws-1",
		"vault_password": "export-pass",
		"confirm":        false,
	})
	if noConfirm.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without confirm, got %d: %s", noConfirm.Code, noConfirm.Body.String())
	}

	noPassword := performJSONRequest(t, handler, http.MethodPost, "/api/vault/export", map[string]any{
		"workspace_id": "ws-1",
		"confirm":      true,
	})
	if noPassword.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without password, got %d: %s", noPassword.Code, noPassword.Body.String())
	}

	exportRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/export", map[string]any{
		"workspace_id":   "ws-1",
		"vault_password": "export-pass",
		"confirm":        true,
	})
	if exportRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from export, got %d: %s", exportRec.Code, exportRec.Body.String())
	}

	var bundle vault.ExportBundle
	decodeJSONBody(t, exportRec, &bundle)
	if bundle.RecordCount != 1 {
		t.Fatalf("expected 1 exported record, got %d", bundle.RecordCount)
	}
}

func TestHandlerUnlockAndLockFallbackVault(t *testing.T) {
	tempDir := t.TempDir()
	secretStore := vault.NewAutoSecretStore(vault.AutoSecretStoreOptions{GOOS: "plan9"})
	handler, _, db := newTestHandler(t, secretStore, filepath.Join(tempDir, "vault-secrets.json"))
	defer func() { _ = db.Close() }()

	statusRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/status", nil)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from status, got %d: %s", statusRec.Code, statusRec.Body.String())
	}
	var status vault.VaultStatus
	decodeJSONBody(t, statusRec, &status)
	if !status.Locked || !status.RequiresPassphrase {
		t.Fatalf("expected locked fallback vault, got %+v", status)
	}

	unlockRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/unlock", map[string]any{
		"vault_password": "fallback-pass",
	})
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from unlock, got %d: %s", unlockRec.Code, unlockRec.Body.String())
	}

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"type":         "personal_note",
		"workspace_id": "ws-1",
		"label":        "Unlocked",
		"payload": map[string]any{
			"value": "ok",
		},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 after unlock, got %d: %s", createRec.Code, createRec.Body.String())
	}

	lockRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/lock", map[string]any{})
	if lockRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from lock, got %d: %s", lockRec.Code, lockRec.Body.String())
	}

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?workspace_id=ws-1", nil)
	if listRec.Code != http.StatusLocked {
		t.Fatalf("expected 423 after lock, got %d: %s", listRec.Code, listRec.Body.String())
	}
}
