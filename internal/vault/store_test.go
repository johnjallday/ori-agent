package vault

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestVaultStore(t *testing.T) (*Store, *database.DB) {
	t.Helper()

	db, err := database.Open(context.Background(), &database.Config{
		InMemory: true,
		WALMode:  false,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	return NewStore(db, StoreOptions{
		SecretStore: NewMemorySecretStore(),
	}), db
}

func TestStoreRecordCRUDEncryptsPayload(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	record := &Record{
		Type:        "email_snippet",
		WorkspaceID: "ws-1",
		Label:       "Primary Inbox",
		Tags:        []string{"Email", "Private"},
		Source:      "manual",
		Payload:     json.RawMessage(`{"email":"user@example.com","subject":"Quarterly taxes"}`),
	}
	if err := store.CreateRecord(ctx, record, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	var metadataCiphertext, payloadCiphertext string
	if err := db.QueryRowContext(ctx, `
		SELECT metadata_ciphertext, payload_ciphertext
		FROM vault_records
		WHERE id = ?
	`, record.ID).Scan(&metadataCiphertext, &payloadCiphertext); err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}

	for _, leaked := range []string{"Primary Inbox", "user@example.com", "quarterly taxes", "private"} {
		if strings.Contains(strings.ToLower(metadataCiphertext), leaked) || strings.Contains(strings.ToLower(payloadCiphertext), leaked) {
			t.Fatalf("ciphertext unexpectedly contains plaintext fragment %q", leaked)
		}
	}

	items, err := store.ListRecords(ctx, RecordFilter{WorkspaceID: "ws-1"}, AccessContext{})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 record, got %d", len(items))
	}
	if items[0].Label != "Primary Inbox" {
		t.Fatalf("expected decrypted label, got %q", items[0].Label)
	}
	if len(items[0].Tags) != 2 || items[0].Tags[0] != "email" || items[0].Tags[1] != "private" {
		t.Fatalf("expected normalized tags, got %#v", items[0].Tags)
	}

	got, err := store.GetRecord(ctx, record.ID, AccessContext{})
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if string(got.Payload) != `{"email":"user@example.com","subject":"Quarterly taxes"}` {
		t.Fatalf("unexpected payload: %s", got.Payload)
	}

	label := "Updated Inbox"
	payload := json.RawMessage(`{"email":"user@example.com","subject":"Filed"}`)
	updated, err := store.UpdateRecord(ctx, record.ID, RecordUpdate{
		Label:   &label,
		Payload: &payload,
	}, AccessContext{})
	if err != nil {
		t.Fatalf("update record: %v", err)
	}
	if updated.Label != "Updated Inbox" {
		t.Fatalf("expected updated label, got %q", updated.Label)
	}
	if string(updated.Payload) != `{"email":"user@example.com","subject":"Filed"}` {
		t.Fatalf("unexpected updated payload: %s", updated.Payload)
	}

	if err := store.DeleteRecord(ctx, record.ID, AccessContext{}); err != nil {
		t.Fatalf("delete record: %v", err)
	}
	if _, err := store.GetRecord(ctx, record.ID, AccessContext{}); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound after delete, got %v", err)
	}
}

func TestStoreGrantEnforcement(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	record := &Record{
		Type:        "email_snippet",
		WorkspaceID: "ws-finance",
		Label:       "Tax Email",
		Payload:     json.RawMessage(`{"body":"1099 attached"}`),
	}
	if err := store.CreateRecord(ctx, record, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	actor := AccessContext{
		WorkspaceID: "ws-finance",
		ActorType:   ActorTypeAgent,
		ActorID:     "finance-agent",
	}

	if _, err := store.GetRecord(ctx, record.ID, actor); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied without grant, got %v", err)
	}

	if err := store.CreateGrant(ctx, &Grant{
		WorkspaceID: "ws-finance",
		ActorType:   ActorTypeAgent,
		ActorID:     "finance-agent",
		Capability:  CapabilityEmailRead,
		RecordType:  "email_snippet",
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}

	got, err := store.GetRecord(ctx, record.ID, actor)
	if err != nil {
		t.Fatalf("get record with grant: %v", err)
	}
	if got.Label != "Tax Email" {
		t.Fatalf("expected decrypted record after grant, got %q", got.Label)
	}
}

func TestStoreExportRequiresPasswordAndEncryptsBundle(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	if err := store.CreateRecord(ctx, &Record{
		Type:        "personal_note",
		WorkspaceID: "ws-1",
		Label:       "Passport",
		Payload:     json.RawMessage(`{"number":"X1234567"}`),
	}, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	if _, err := store.Export(ctx, ExportRequest{WorkspaceID: "ws-1"}); !errors.Is(err, ErrExportPasswordEmpty) {
		t.Fatalf("expected ErrExportPasswordEmpty, got %v", err)
	}

	bundle, err := store.Export(ctx, ExportRequest{
		WorkspaceID: "ws-1",
		Password:    "vault-export-pass",
	})
	if err != nil {
		t.Fatalf("export vault: %v", err)
	}
	if bundle.RecordCount != 1 {
		t.Fatalf("expected 1 exported record, got %d", bundle.RecordCount)
	}

	decrypted, err := DecryptExportBundle(*bundle, "vault-export-pass")
	if err != nil {
		t.Fatalf("decrypt export bundle: %v", err)
	}
	if !strings.Contains(string(decrypted), `"Passport"`) {
		t.Fatalf("expected decrypted export to contain record label, got %s", decrypted)
	}
}

func TestStoreRejectsMalformedEncryptedRecord(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	record := &Record{
		Type:        "personal_note",
		WorkspaceID: "ws-1",
		Label:       "Medical",
		Payload:     json.RawMessage(`{"note":"allergic"}`),
	}
	if err := store.CreateRecord(ctx, record, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE vault_records
		SET payload_ciphertext = 'not-base64'
		WHERE id = ?
	`, record.ID); err != nil {
		t.Fatalf("corrupt record: %v", err)
	}

	if _, err := store.GetRecord(ctx, record.ID, AccessContext{}); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("expected ErrMalformedRecord, got %v", err)
	}
}
