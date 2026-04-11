package vaulthttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/vault"
	"golang.org/x/oauth2"
)

const testVaultPassword = "test-vault-password"

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

func performMultipartRequest(t *testing.T, handler http.Handler, method string, path string, fields map[string]string, fileField string, fileName string, fileContent []byte) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %q: %v", key, err)
		}
	}

	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatalf("create form file %q: %v", fileField, err)
		}
		if _, err := part.Write(fileContent); err != nil {
			t.Fatalf("write form file %q: %v", fileField, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
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
	if err := store.CreateVault(context.Background(), &item, testVaultPassword); err != nil {
		t.Fatalf("create handler vault %q: %v", name, err)
	}
	return item
}

func insertLegacyHandlerVault(t *testing.T, db *database.DB, name string) vault.Vault {
	t.Helper()

	item := vault.Vault{
		ID:   "legacy-" + strings.ReplaceAll(strings.ToLower(name), " ", "-"),
		Name: name,
	}
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO vaults (id, name, description, key_salt, key_nonce, key_ciphertext, created_at, updated_at)
		VALUES (?, ?, '', '', '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, item.ID, item.Name); err != nil {
		t.Fatalf("insert legacy handler vault %q: %v", name, err)
	}
	return item
}

func configureTestGoogleOAuth(t *testing.T, tokenURL string) string {
	t.Helper()

	authURL := "https://accounts.google.test/o/oauth2/v2/auth"
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_ID", "google-client-id")
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_SECRET", "google-client-secret")
	t.Setenv("ORI_EMAIL_GOOGLE_AUTH_URL", authURL)
	t.Setenv("ORI_EMAIL_GOOGLE_TOKEN_URL", tokenURL)
	t.Setenv("ORI_EMAIL_OAUTH_BASE_URL", "http://ori.test")
	return authURL
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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
		"type":             "secret",
		"workspace_id":     "ws-secure",
		"label":            "Passport Updated",
		"tags":             []string{"Travel", "Docs"},
		"source":           "import",
		"retention_policy": "until_rotated",
		"payload": map[string]any{
			"number": "X7654321",
		},
	})
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from update, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated struct {
		Record vault.Record `json:"record"`
	}
	if err := json.Unmarshal(updateRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated record: %v", err)
	}
	if updated.Record.Type != "secret" || updated.Record.WorkspaceID != "ws-secure" {
		t.Fatalf("unexpected updated location: %#v", updated.Record)
	}
	if updated.Record.Source != "import" || updated.Record.RetentionPolicy != "until_rotated" {
		t.Fatalf("unexpected updated metadata: %#v", updated.Record)
	}
	if len(updated.Record.Tags) != 2 || updated.Record.Tags[0] != "travel" || updated.Record.Tags[1] != "docs" {
		t.Fatalf("unexpected updated tags: %#v", updated.Record.Tags)
	}

	deleteRec := performJSONRequest(t, handler, http.MethodDelete, "/api/vault/records/"+created.Record.ID, nil)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from delete, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestHandlerRecordAttachmentLifecycle(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/records", map[string]any{
		"vault_id": primaryVault.ID,
		"type":     "personal_note",
		"label":    "Passport",
		"payload": map[string]any{
			"note": "Emergency contact",
		},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Record vault.Record `json:"record"`
	}
	decodeJSONBody(t, createRec, &created)

	uploadRec := performMultipartRequest(t, handler, http.MethodPost, "/api/vault/records/"+created.Record.ID+"/attachments", nil, "file", "passport.txt", []byte("scan-data"))
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from attachment upload, got %d: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploaded struct {
		Attachment vault.RecordAttachment `json:"attachment"`
	}
	decodeJSONBody(t, uploadRec, &uploaded)
	if uploaded.Attachment.ID == "" {
		t.Fatal("expected created attachment id")
	}
	if uploaded.Attachment.DownloadURL == "" {
		t.Fatalf("expected attachment download URL, got %+v", uploaded.Attachment)
	}

	getRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records/"+created.Record.ID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from record get, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var record vault.Record
	decodeJSONBody(t, getRec, &record)
	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		t.Fatalf("unmarshal record payload: %v", err)
	}
	attachments, _ := payload["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("expected 1 payload attachment, got %#v", payload["attachments"])
	}
	attachmentPayload, _ := attachments[0].(map[string]any)
	if strings.TrimSpace(fmt.Sprintf("%v", attachmentPayload["download_url"])) == "" {
		t.Fatalf("expected payload attachment download URL, got %#v", attachmentPayload)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, uploaded.Attachment.DownloadURL, nil)
	downloadRec := httptest.NewRecorder()
	handler.ServeHTTP(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("expected 200 downloading attachment, got %d: %s", downloadRec.Code, downloadRec.Body.String())
	}
	if got := downloadRec.Body.String(); got != "scan-data" {
		t.Fatalf("unexpected attachment bytes %q", got)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, uploaded.Attachment.DownloadURL, nil)
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting attachment, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, uploaded.Attachment.DownloadURL, nil)
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after attachment delete, got %d: %s", missingRec.Code, missingRec.Body.String())
	}
}

func TestHandlerEmailAccountLifecycle(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	createRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/email-accounts", map[string]any{
		"vault_id":      primaryVault.ID,
		"label":         "Support Inbox",
		"provider":      "gmail",
		"email_address": "support@example.com",
		"display_name":  "Support",
		"auth_type":     "oauth2",
		"credentials": map[string]any{
			"refresh_token": "refresh-token",
			"client_id":     "client-id",
			"client_secret": "client-secret",
		},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create email account, got %d: %s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), "refresh-token") || strings.Contains(createRec.Body.String(), "client-secret") {
		t.Fatalf("expected create response to redact credentials, got %s", createRec.Body.String())
	}

	var created struct {
		Account vault.EmailAccount `json:"account"`
	}
	decodeJSONBody(t, createRec, &created)
	if created.Account.ID == "" {
		t.Fatal("expected created email account id")
	}
	if created.Account.Provider != vault.EmailProviderGmail {
		t.Fatalf("expected gmail provider, got %q", created.Account.Provider)
	}
	if !created.Account.CredentialsStatus.HasRefreshToken || !created.Account.CredentialsStatus.HasClientSecret {
		t.Fatalf("expected credential state to be surfaced, got %#v", created.Account.CredentialsStatus)
	}

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/email-accounts?vault_id="+primaryVault.ID, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from list email accounts, got %d: %s", listRec.Code, listRec.Body.String())
	}

	getRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/email-accounts/"+created.Account.ID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from get email account, got %d: %s", getRec.Code, getRec.Body.String())
	}

	updateRec := performJSONRequest(t, handler, http.MethodPatch, "/api/vault/email-accounts/"+created.Account.ID, map[string]any{
		"display_name": "Support Team",
		"access_token": "access-token",
	})
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from update email account, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	var updated struct {
		Account vault.EmailAccount `json:"account"`
	}
	decodeJSONBody(t, updateRec, &updated)
	if updated.Account.DisplayName != "Support Team" {
		t.Fatalf("expected updated display name, got %#v", updated.Account)
	}
	if !updated.Account.CredentialsStatus.HasAccessToken {
		t.Fatalf("expected access token state after update, got %#v", updated.Account.CredentialsStatus)
	}

	deleteRec := performJSONRequest(t, handler, http.MethodDelete, "/api/vault/email-accounts/"+created.Account.ID, nil)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from delete email account, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestHandlerEmailOAuthProviders(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_ID", "google-client-id")
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_SECRET", "google-client-secret")

	rec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/email-oauth/providers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from provider status, got %d: %s", rec.Code, rec.Body.String())
	}

	var response struct {
		Providers []struct {
			Provider         string `json:"provider"`
			ConnectSupported bool   `json:"connect_supported"`
			Enabled          bool   `json:"enabled"`
			Reason           string `json:"reason"`
		} `json:"providers"`
	}
	decodeJSONBody(t, rec, &response)
	if len(response.Providers) != 3 {
		t.Fatalf("expected 3 provider entries, got %d", len(response.Providers))
	}

	if response.Providers[0].Provider != "gmail" || !response.Providers[0].Enabled {
		t.Fatalf("expected gmail provider to be enabled, got %#v", response.Providers[0])
	}
	if response.Providers[1].Provider != "microsoft" || response.Providers[1].Enabled {
		t.Fatalf("expected microsoft provider to be disabled without env, got %#v", response.Providers[1])
	}
	if response.Providers[2].Provider != "imap_smtp" || response.Providers[2].ConnectSupported {
		t.Fatalf("expected imap_smtp to be manual only, got %#v", response.Providers[2])
	}
}

func TestHandlerEmailOAuthConnectCreatesAccount(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	tokenCalls := 0
	tokenClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		if r.Method != http.MethodPost {
			t.Fatalf("expected token endpoint POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		if r.Form.Get("code") != "oauth-code-1" {
			t.Fatalf("expected code oauth-code-1, got %q", r.Form.Get("code"))
		}
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Fatalf("expected authorization_code grant, got %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("redirect_uri") != "http://ori.test/api/vault/email-oauth/callback" {
			t.Fatalf("unexpected redirect URI %q", r.Form.Get("redirect_uri"))
		}
		if strings.TrimSpace(r.Form.Get("code_verifier")) == "" {
			t.Fatal("expected PKCE code verifier on token exchange")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-token-1","refresh_token":"refresh-token-1","token_type":"Bearer","expires_in":3600}`)),
		}, nil
	})}

	authURL := configureTestGoogleOAuth(t, "https://oauth.google.test/token")

	startRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/email-oauth/start?vault_id="+url.QueryEscape(primaryVault.ID)+"&provider=gmail&label=Support%20Inbox&email_address=support@example.com&display_name=Support%20Team&tags=finance,priority", nil)
	if startRec.Code != http.StatusFound {
		t.Fatalf("expected 302 from oauth start, got %d: %s", startRec.Code, startRec.Body.String())
	}

	redirectLocation := startRec.Header().Get("Location")
	if !strings.HasPrefix(redirectLocation, authURL) {
		t.Fatalf("expected redirect to auth URL %q, got %q", authURL, redirectLocation)
	}

	redirectURL, err := url.Parse(redirectLocation)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	state := strings.TrimSpace(redirectURL.Query().Get("state"))
	if state == "" {
		t.Fatal("expected oauth state in redirect URL")
	}
	if redirectURL.Query().Get("login_hint") != "support@example.com" {
		t.Fatalf("expected login_hint, got %q", redirectURL.Query().Get("login_hint"))
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/vault/email-oauth/callback?state="+url.QueryEscape(state)+"&code=oauth-code-1", nil)
	callbackReq.Host = "ori.test"
	callbackReq = callbackReq.WithContext(context.WithValue(callbackReq.Context(), oauth2.HTTPClient, tokenClient))
	callbackRec := httptest.NewRecorder()
	handler.ServeHTTP(callbackRec, callbackReq)

	if callbackRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from oauth callback, got %d: %s", callbackRec.Code, callbackRec.Body.String())
	}
	if !strings.Contains(callbackRec.Body.String(), emailOAuthPopupEventType) {
		t.Fatalf("expected popup event payload in callback HTML, got %s", callbackRec.Body.String())
	}
	if strings.Contains(callbackRec.Body.String(), "google-client-secret") {
		t.Fatalf("expected callback HTML to avoid leaking client secret, got %s", callbackRec.Body.String())
	}
	if tokenCalls != 1 {
		t.Fatalf("expected one token exchange, got %d", tokenCalls)
	}

	accounts, err := store.ListEmailAccounts(context.Background(), primaryVault.ID, "")
	if err != nil {
		t.Fatalf("list email accounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected one email account after oauth connect, got %d", len(accounts))
	}

	account := accounts[0]
	if account.Label != "Support Inbox" || account.EmailAddress != "support@example.com" {
		t.Fatalf("unexpected created account: %#v", account)
	}
	if account.AuthType != vault.EmailAuthTypeOAuth2 || account.Provider != vault.EmailProviderGmail {
		t.Fatalf("expected gmail oauth account, got %#v", account)
	}
	if !account.CredentialsStatus.HasRefreshToken || !account.CredentialsStatus.HasClientSecret {
		t.Fatalf("expected stored oauth credentials, got %#v", account.CredentialsStatus)
	}
}

func TestHandlerEmailOAuthReconnectReplacesPasswordAuth(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()
	primaryVault := createHandlerVault(t, store, "Primary Vault")

	account, err := store.CreateEmailAccount(context.Background(), vault.EmailAccountInput{
		VaultID:      primaryVault.ID,
		Label:        "Support Inbox",
		Provider:     vault.EmailProviderGmail,
		EmailAddress: "support@example.com",
		AuthType:     vault.EmailAuthTypeAppPassword,
		Credentials: vault.EmailAccountCredentials{
			Password: "old-app-password",
		},
	})
	if err != nil {
		t.Fatalf("create seed email account: %v", err)
	}

	tokenClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-token-2","refresh_token":"refresh-token-2","token_type":"Bearer","expires_in":3600}`)),
		}, nil
	})}

	configureTestGoogleOAuth(t, "https://oauth.google.test/token")

	startRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/email-oauth/start?vault_id="+url.QueryEscape(primaryVault.ID)+"&provider=gmail&account_id="+url.QueryEscape(account.ID), nil)
	if startRec.Code != http.StatusFound {
		t.Fatalf("expected 302 from reconnect start, got %d: %s", startRec.Code, startRec.Body.String())
	}

	redirectURL, err := url.Parse(startRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse reconnect redirect URL: %v", err)
	}
	state := strings.TrimSpace(redirectURL.Query().Get("state"))
	if state == "" {
		t.Fatal("expected reconnect state")
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/vault/email-oauth/callback?state="+url.QueryEscape(state)+"&code=oauth-code-2", nil)
	callbackReq.Host = "ori.test"
	callbackReq = callbackReq.WithContext(context.WithValue(callbackReq.Context(), oauth2.HTTPClient, tokenClient))
	callbackRec := httptest.NewRecorder()
	handler.ServeHTTP(callbackRec, callbackReq)

	if callbackRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from reconnect callback, got %d: %s", callbackRec.Code, callbackRec.Body.String())
	}

	updatedAccount, err := store.GetEmailAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("get updated email account: %v", err)
	}
	if updatedAccount.AuthType != vault.EmailAuthTypeOAuth2 {
		t.Fatalf("expected oauth2 auth after reconnect, got %#v", updatedAccount)
	}
	if updatedAccount.CredentialsStatus.HasPassword {
		t.Fatalf("expected password credential to be cleared after reconnect, got %#v", updatedAccount.CredentialsStatus)
	}
	if !updatedAccount.CredentialsStatus.HasRefreshToken {
		t.Fatalf("expected refresh token after reconnect, got %#v", updatedAccount.CredentialsStatus)
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
	handler, _, db := newTestHandler(t, secretStore, filepath.Join(tempDir, "vault-secrets.json"))
	defer func() { _ = db.Close() }()
	primaryVault := insertLegacyHandlerVault(t, db, "Primary Vault")
	_ = insertLegacyHandlerVault(t, db, "Archive Vault")

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
		"vault_id":       primaryVault.ID,
		"vault_password": "fallback-pass",
	})
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from unlock, got %d: %s", unlockRec.Code, unlockRec.Body.String())
	}
	var unlocked vault.VaultStatus
	decodeJSONBody(t, unlockRec, &unlocked)
	if unlocked.VaultID != primaryVault.ID {
		t.Fatalf("expected unlock response for vault %q, got %q", primaryVault.ID, unlocked.VaultID)
	}
	if unlocked.Locked {
		t.Fatalf("expected unlocked status after unlock, got %+v", unlocked)
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

	lockRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/lock?vault_id="+primaryVault.ID, map[string]any{})
	if lockRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from lock, got %d: %s", lockRec.Code, lockRec.Body.String())
	}

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+primaryVault.ID+"&workspace_id=ws-1", nil)
	if listRec.Code != http.StatusLocked {
		t.Fatalf("expected 423 after lock, got %d: %s", listRec.Code, listRec.Body.String())
	}
}

func TestHandlerImportCreatesVaultAndRestoresBundle(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	sourceVault := createHandlerVault(t, store, "Finance Vault")

	if err := store.CreateRecord(context.Background(), &vault.Record{
		VaultID:     sourceVault.ID,
		Type:        "secret",
		WorkspaceID: "ws-import",
		Label:       "Brokerage Login",
		Payload:     json.RawMessage(`{"username":"jjdev"}`),
	}, vault.AccessContext{}); err != nil {
		t.Fatalf("create source record: %v", err)
	}

	if err := store.CreateGrant(context.Background(), &vault.Grant{
		VaultID:     sourceVault.ID,
		WorkspaceID: "ws-import",
		ActorType:   vault.ActorTypeAgent,
		ActorID:     "finance-agent",
		Capability:  vault.CapabilitySecretsRead,
		RecordType:  "secret",
	}); err != nil {
		t.Fatalf("create source grant: %v", err)
	}

	bundle, err := store.Export(context.Background(), vault.ExportRequest{
		VaultID:  sourceVault.ID,
		Password: "bundle-pass",
	})
	if err != nil {
		t.Fatalf("export bundle: %v", err)
	}

	importRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/import", map[string]any{
		"import_password": "bundle-pass",
		"bundle":          bundle,
		"create_vault": map[string]any{
			"name":           "Imported Finance Vault",
			"vault_password": "imported-vault-pass",
		},
	})
	if importRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from import, got %d: %s", importRec.Code, importRec.Body.String())
	}

	var importBody struct {
		Result vault.ImportResult `json:"result"`
	}
	decodeJSONBody(t, importRec, &importBody)
	if !importBody.Result.CreatedVault || importBody.Result.RecordCount != 1 || importBody.Result.GrantCount != 1 {
		t.Fatalf("unexpected import result: %+v", importBody.Result)
	}

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+importBody.Result.Vault.ID, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing imported vault records, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Records []vault.RecordListItem `json:"records"`
	}
	decodeJSONBody(t, listRec, &listed)
	if len(listed.Records) != 1 || listed.Records[0].Label != "Brokerage Login" {
		t.Fatalf("unexpected imported records: %#v", listed.Records)
	}

	grantsRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/grants?vault_id="+importBody.Result.Vault.ID, nil)
	if grantsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing imported vault grants, got %d: %s", grantsRec.Code, grantsRec.Body.String())
	}
	var importedGrants struct {
		Grants []vault.Grant `json:"grants"`
	}
	decodeJSONBody(t, grantsRec, &importedGrants)
	if len(importedGrants.Grants) != 1 || importedGrants.Grants[0].ActorID != "finance-agent" {
		t.Fatalf("unexpected imported grants: %#v", importedGrants.Grants)
	}
}

func TestHandlerImportAcceptsMultipartBundleUpload(t *testing.T) {
	handler, store, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	sourceVault := createHandlerVault(t, store, "Travel Vault")

	if err := store.CreateRecord(context.Background(), &vault.Record{
		VaultID: sourceVault.ID,
		Type:    "personal_note",
		Label:   "Passport copy",
		Payload: json.RawMessage(fmt.Sprintf(`{
			"note":"Scan attached",
			"attachments":[{"name":"passport.txt","mime_type":"text/plain","size_bytes":9,"content_base64":"%s"}]
		}`, base64.StdEncoding.EncodeToString([]byte("scan-data")))),
	}, vault.AccessContext{}); err != nil {
		t.Fatalf("create source record with attachment: %v", err)
	}

	bundle, err := store.Export(context.Background(), vault.ExportRequest{
		VaultID:  sourceVault.ID,
		Password: "bundle-pass",
	})
	if err != nil {
		t.Fatalf("export bundle: %v", err)
	}

	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	importRec := performMultipartRequest(t, handler, http.MethodPost, "/api/vault/import", map[string]string{
		"import_password":       "bundle-pass",
		"restore_grants":        "false",
		"create_vault_name":     "Imported Travel Vault",
		"create_vault_password": "imported-vault-pass",
	}, "file", "vault-export.json", bundleJSON)
	if importRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from multipart import, got %d: %s", importRec.Code, importRec.Body.String())
	}

	var importBody struct {
		Result vault.ImportResult `json:"result"`
	}
	decodeJSONBody(t, importRec, &importBody)
	if !importBody.Result.CreatedVault || importBody.Result.RecordCount != 1 {
		t.Fatalf("unexpected multipart import result: %+v", importBody.Result)
	}

	listRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records?vault_id="+importBody.Result.Vault.ID, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing imported records, got %d: %s", listRec.Code, listRec.Body.String())
	}

	var listed struct {
		Records []vault.RecordListItem `json:"records"`
	}
	decodeJSONBody(t, listRec, &listed)
	if len(listed.Records) != 1 {
		t.Fatalf("unexpected imported record count: %#v", listed.Records)
	}

	getRec := performJSONRequest(t, handler, http.MethodGet, "/api/vault/records/"+listed.Records[0].ID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 loading imported record, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var importedRecord vault.Record
	decodeJSONBody(t, getRec, &importedRecord)
	var payload map[string]any
	if err := json.Unmarshal(importedRecord.Payload, &payload); err != nil {
		t.Fatalf("unmarshal imported payload: %v", err)
	}
	attachments, _ := payload["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("expected imported attachment metadata, got %#v", payload["attachments"])
	}
}

func TestHandlerSupportsNamedVaults(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createPrimaryVaultRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/vaults", map[string]any{
		"name":           "Personal Vault",
		"description":    "Personal encrypted records",
		"vault_password": "personal-vault-pass",
	})
	if createPrimaryVaultRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating primary vault, got %d: %s", createPrimaryVaultRec.Code, createPrimaryVaultRec.Body.String())
	}

	var primaryVault struct {
		Vault vault.Vault `json:"vault"`
	}
	decodeJSONBody(t, createPrimaryVaultRec, &primaryVault)

	createVaultRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/vaults", map[string]any{
		"name":           "Client Vault",
		"description":    "Per-client encrypted records",
		"vault_password": "client-vault-pass",
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
		"name":           "Client Vault",
		"description":    "Per-client encrypted records",
		"vault_password": "client-vault-pass",
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

func TestHandlerCreateVaultRequiresPassword(t *testing.T) {
	handler, _, db := newTestHandler(t, vault.NewMemorySecretStore(), "")
	defer func() { _ = db.Close() }()

	createVaultRec := performJSONRequest(t, handler, http.MethodPost, "/api/vault/vaults", map[string]any{
		"name": "Passwordless Vault",
	})
	if createVaultRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 creating vault without a password, got %d: %s", createVaultRec.Code, createVaultRec.Body.String())
	}
}
