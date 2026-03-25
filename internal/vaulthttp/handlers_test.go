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

func createHandlerVault(t *testing.T, store *vault.Store, name string) vault.Vault {
	t.Helper()

	item := vault.Vault{Name: name}
	if err := store.CreateVault(context.Background(), &item); err != nil {
		t.Fatalf("create handler vault %q: %v", name, err)
	}
	return item
}

func TestHandlerRecordLifecycle(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id":     primaryVault.ID,
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

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+primaryVault.ID+"&workspace_id=ws-1", nil)
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
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id":     primaryVault.ID,
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
		"vault_id":     primaryVault.ID,
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
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id":     primaryVault.ID,
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
		"vault_id":       primaryVault.ID,
		"workspace_id":   "ws-1",
		"vault_password": "export-pass",
		"confirm":        false,
	})
	if noConfirm.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without confirm, got %d: %s", noConfirm.Code, noConfirm.Body.String())
	}

	noPassword := performJSONRequest(t, handler, http.MethodPost, "/api/vault/export", map[string]any{
		"vault_id":     primaryVault.ID,
		"workspace_id": "ws-1",
		"confirm":      true,
	})
	if noPassword.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without password, got %d: %s", noPassword.Code, noPassword.Body.String())
	}

	exportRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/export", map[string]any{
		"vault_id":       primaryVault.ID,
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
	handler, store, db := newTestHandler(t, secretStore, filepath.Join(tempDir, "vault-secrets.json"))
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	statusRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/status?vault_id="+primaryVault.ID, nil)
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
		"vault_id":     primaryVault.ID,
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

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+primaryVault.ID+"&workspace_id=ws-1", nil)
	if listRec.Code != http.StatusLocked {
		t.Fatalf("expected 423 after lock, got %d: %s", listRec.Code, listRec.Body.String())
	}
}

func TestHandlerSupportsNamedVaults(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createPrimaryVaultRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/vaults", map[string]any{
		"name":        "Personal Vault",
		"description": "Personal encrypted records",
	})
	if createPrimaryVaultRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating primary vault, got %d: %s", createPrimaryVaultRec.Code, createPrimaryVaultRec.Body.String())
	}

	var primaryVault struct {
		Vault vault.Vault `json:"vault"`
	}
	decodeJSONBody(t, createPrimaryVaultRec, &primaryVault)

	createVaultRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/vaults", map[string]any{
		"name":        "Client Vault",
		"description": "Per-client encrypted records",
	})
	if createVaultRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating vault, got %d: %s", createVaultRec.Code, createVaultRec.Body.String())
	}

	var createdVault struct {
		Vault vault.Vault `json:"vault"`
	}
	decodeJSONBody(t, createVaultRec, &createdVault)
	if createdVault.Vault.ID == "" {
		t.Fatal("expected created vault id")
	}

	listVaultsRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/vaults", nil)
	if listVaultsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing vaults, got %d: %s", listVaultsRec.Code, listVaultsRec.Body.String())
	}

	primaryRecordRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id": primaryVault.Vault.ID,
		"type":     "personal_note",
		"label":    "Personal vault record",
		"payload": map[string]any{
			"value": "default",
		},
	})
	if primaryRecordRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating personal record, got %d: %s", primaryRecordRec.Code, primaryRecordRec.Body.String())
	}

	secondRecordRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id": createdVault.Vault.ID,
		"type":     "secret",
		"label":    "Client secret",
		"payload": map[string]any{
			"token": "vault-two",
		},
	})
	if secondRecordRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating second-vault record, got %d: %s", secondRecordRec.Code, secondRecordRec.Body.String())
	}

	statusRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/status?vault_id="+createdVault.Vault.ID, nil)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for named vault status, got %d: %s", statusRec.Code, statusRec.Body.String())
	}
	var status vault.VaultStatus
	decodeJSONBody(t, statusRec, &status)
	if status.VaultID != createdVault.Vault.ID || status.RecordCount != 1 {
		t.Fatalf("unexpected named vault status: %+v", status)
	}

	primaryListRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+primaryVault.Vault.ID, nil)
	if primaryListRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for personal vault list, got %d: %s", primaryListRec.Code, primaryListRec.Body.String())
	}
	var primaryList struct {
		Records []vault.RecordListItem `json:"records"`
	}
	decodeJSONBody(t, primaryListRec, &primaryList)
	if len(primaryList.Records) != 1 || primaryList.Records[0].Label != "Personal vault record" {
		t.Fatalf("unexpected personal vault records: %#v", primaryList.Records)
	}

	secondListRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+createdVault.Vault.ID, nil)
	if secondListRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for named vault list, got %d: %s", secondListRec.Code, secondListRec.Body.String())
	}
	var secondList struct {
		Records []vault.RecordListItem `json:"records"`
	}
	decodeJSONBody(t, secondListRec, &secondList)
	if len(secondList.Records) != 1 || secondList.Records[0].Label != "Client secret" {
		t.Fatalf("unexpected second vault records: %#v", secondList.Records)
	}
	if secondList.Records[0].VaultID != createdVault.Vault.ID {
		t.Fatalf("expected named vault id %q, got %q", createdVault.Vault.ID, secondList.Records[0].VaultID)
	}
}

func TestHandlerRenamesAndDeletesNamedVaults(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createVaultRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/vaults", map[string]any{
		"name":        "Client Vault",
		"description": "Per-client encrypted records",
	})
	if createVaultRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating vault, got %d: %s", createVaultRec.Code, createVaultRec.Body.String())
	}

	var createdVault struct {
		Vault vault.Vault `json:"vault"`
	}
	decodeJSONBody(t, createVaultRec, &createdVault)

	updateVaultRec := performJSONRequest(t, handler, http.MethodPatch, "/api/vault/vaults/"+createdVault.Vault.ID, map[string]any{
		"name":        "Client Archive",
		"description": "Archived client materials",
	})
	if updateVaultRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating vault, got %d: %s", updateVaultRec.Code, updateVaultRec.Body.String())
	}

	var updatedVault struct {
		Vault vault.Vault `json:"vault"`
	}
	decodeJSONBody(t, updateVaultRec, &updatedVault)
	if updatedVault.Vault.Name != "Client Archive" || updatedVault.Vault.Description != "Archived client materials" {
		t.Fatalf("unexpected updated vault: %+v", updatedVault.Vault)
	}

	createRecordRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id": createdVault.Vault.ID,
		"type":     "secret",
		"label":    "Client secret",
		"payload": map[string]any{
			"token": "vault-two",
		},
	})
	if createRecordRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating named-vault record, got %d: %s", createRecordRec.Code, createRecordRec.Body.String())
	}

	deleteVaultRec := performJSONRequest(t, handler, http.MethodDelete, "/api/vault/vaults/"+createdVault.Vault.ID, nil)
	if deleteVaultRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting vault, got %d: %s", deleteVaultRec.Code, deleteVaultRec.Body.String())
	}

	listVaultsRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/vaults", nil)
	if listVaultsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing vaults, got %d: %s", listVaultsRec.Code, listVaultsRec.Body.String())
	}

	var listed struct {
		Vaults []vault.Vault `json:"vaults"`
	}
	decodeJSONBody(t, listVaultsRec, &listed)
	if len(listed.Vaults) != 0 {
		t.Fatalf("expected no vaults to remain, got %#v", listed.Vaults)
	}

	missingListRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+createdVault.Vault.ID, nil)
	if missingListRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 listing deleted vault, got %d: %s", missingListRec.Code, missingListRec.Body.String())
	}
}

func TestHandlerRequiresVaultWhenNoneExist(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	statusRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/status", nil)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from empty status, got %d: %s", statusRec.Code, statusRec.Body.String())
	}

	var status vault.VaultStatus
	decodeJSONBody(t, statusRec, &status)
	if status.Available {
		t.Fatalf("expected unavailable status without vaults, got %+v", status)
	}

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"type":  "personal_note",
		"label": "Missing vault",
		"payload": map[string]any{
			"value": "nope",
		},
	})
	if createRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 creating record without a vault, got %d: %s", createRec.Code, createRec.Body.String())
	}
}
