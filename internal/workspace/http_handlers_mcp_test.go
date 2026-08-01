package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/vault"
)

func newTestWorkspaceMCPHandler(t *testing.T) (*HTTPHandler, *InMemoryStore, *vault.Store, *database.DB) {
	t.Helper()

	db, err := database.Open(context.Background(), &database.Config{
		InMemory: true,
		WALMode:  false,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	vaultStore := vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: t.TempDir()})
	handler.SetEmailAccountStore(vaultStore)
	return handler, store, vaultStore, db
}

func performWorkspaceJSONRequest(t *testing.T, handler http.Handler, method string, path string, workspaceID string, body any) *httptest.ResponseRecorder {
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
	req.SetPathValue("workspaceID", workspaceID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func createTestWorkspace(t *testing.T, store *InMemoryStore, id string) {
	t.Helper()

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Email Workspace"})
	ws.ID = id
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
}

func createTestVault(t *testing.T, store *vault.Store, name string) vault.Vault {
	t.Helper()

	item := vault.Vault{Name: name}
	if err := store.CreateVault(context.Background(), &item, "test-vault-password"); err != nil {
		t.Fatalf("create vault: %v", err)
	}
	return item
}

func TestCreateMCPBindingWithEmailAccount(t *testing.T) {
	handler, store, vaultStore, db := newTestWorkspaceMCPHandler(t)
	defer func() { _ = db.Close() }()
	createTestWorkspace(t, store, "ws-email")
	primaryVault := createTestVault(t, vaultStore, "Primary Vault")

	account, err := vaultStore.CreateEmailAccount(context.Background(), vault.EmailAccountInput{
		VaultID:      primaryVault.ID,
		Label:        "Support Inbox",
		Provider:     vault.EmailProviderGmail,
		EmailAddress: "support@example.com",
		AuthType:     vault.EmailAuthTypeOAuth2,
		Credentials: vault.EmailAccountCredentials{
			RefreshToken: "refresh-token",
		},
	})
	if err != nil {
		t.Fatalf("create email account: %v", err)
	}

	rec := performWorkspaceJSONRequest(t, http.HandlerFunc(handler.CreateMCPBinding), http.MethodPost, "/api/workspaces/ws-email/mcp-bindings", "ws-email", map[string]any{
		"server_name": "gmail",
		"config": map[string]any{
			"account_id":      account.ID,
			"allowed_actions": []string{"read", "draft", "send"},
			"mailboxes":       []string{"INBOX"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create mcp binding, got %d: %s", rec.Code, rec.Body.String())
	}

	var response struct {
		Binding map[string]any `json:"binding"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	config, ok := response.Binding["config"].(map[string]any)
	if !ok {
		t.Fatalf("expected binding config in response, got %#v", response.Binding)
	}
	if got := config["account_id"]; got != account.ID {
		t.Fatalf("expected normalized account_id %q, got %#v", account.ID, got)
	}
	if got := config["require_send_confirmation"]; got != true {
		t.Fatalf("expected send confirmation to default true, got %#v", got)
	}

	emailAccount, ok := response.Binding["email_account"].(map[string]any)
	if !ok {
		t.Fatalf("expected email_account summary in response, got %#v", response.Binding)
	}
	if got := emailAccount["id"]; got != account.ID {
		t.Fatalf("expected email account summary id %q, got %#v", account.ID, got)
	}

	listRec := performWorkspaceJSONRequest(t, http.HandlerFunc(handler.ListMCPBindings), http.MethodGet, "/api/workspaces/ws-email/mcp-bindings", "ws-email", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from list mcp bindings, got %d: %s", listRec.Code, listRec.Body.String())
	}

	var listResponse struct {
		Bindings []map[string]any `json:"bindings"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResponse.Bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(listResponse.Bindings))
	}
	if _, ok := listResponse.Bindings[0]["email_account"].(map[string]any); !ok {
		t.Fatalf("expected email account summary in list response, got %#v", listResponse.Bindings[0])
	}
}

func TestCreateMCPBindingRejectsEmailAccountFromDifferentWorkspace(t *testing.T) {
	handler, store, vaultStore, db := newTestWorkspaceMCPHandler(t)
	defer func() { _ = db.Close() }()
	createTestWorkspace(t, store, "ws-email")
	primaryVault := createTestVault(t, vaultStore, "Primary Vault")

	account, err := vaultStore.CreateEmailAccount(context.Background(), vault.EmailAccountInput{
		VaultID:      primaryVault.ID,
		WorkspaceID:  "ws-other",
		Label:        "Finance Inbox",
		Provider:     vault.EmailProviderGmail,
		EmailAddress: "finance@example.com",
		AuthType:     vault.EmailAuthTypeOAuth2,
		Credentials:  vault.EmailAccountCredentials{RefreshToken: "refresh-token"},
	})
	if err != nil {
		t.Fatalf("create workspace-scoped email account: %v", err)
	}

	rec := performWorkspaceJSONRequest(t, http.HandlerFunc(handler.CreateMCPBinding), http.MethodPost, "/api/workspaces/ws-email/mcp-bindings", "ws-email", map[string]any{
		"server_name": "gmail",
		"config": map[string]any{
			"account_id": account.ID,
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when binding foreign workspace email account, got %d: %s", rec.Code, rec.Body.String())
	}
}
