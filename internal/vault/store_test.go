package vault

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

const testVaultPassword = "test-vault-password"

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

func createTestVault(t *testing.T, ctx context.Context, store *Store, name string) Vault {
	return createTestVaultWithPassword(t, ctx, store, name, testVaultPassword)
}

func createTestVaultWithPassword(t *testing.T, ctx context.Context, store *Store, name string, password string) Vault {
	t.Helper()

	item := Vault{Name: name}
	if err := store.CreateVault(ctx, &item, password); err != nil {
		t.Fatalf("create test vault %q: %v", name, err)
	}
	return item
}

func TestStoreRecordCRUDEncryptsPayload(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	record := &Record{
		VaultID:     primaryVault.ID,
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

	items, err := store.ListRecords(ctx, RecordFilter{VaultID: primaryVault.ID, WorkspaceID: "ws-1"}, AccessContext{})
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
	recordType := "secret"
	workspaceID := "ws-secure"
	tags := []string{"Credentials", "Personal"}
	source := "import"
	retention := "until_rotated"
	payload := json.RawMessage(`{"email":"user@example.com","subject":"Filed"}`)
	updated, err := store.UpdateRecord(ctx, record.ID, RecordUpdate{
		Type:            &recordType,
		WorkspaceID:     &workspaceID,
		Label:           &label,
		Tags:            &tags,
		Source:          &source,
		RetentionPolicy: &retention,
		Payload:         &payload,
	}, AccessContext{})
	if err != nil {
		t.Fatalf("update record: %v", err)
	}
	if updated.Type != "secret" {
		t.Fatalf("expected updated type, got %q", updated.Type)
	}
	if updated.WorkspaceID != "ws-secure" {
		t.Fatalf("expected updated workspace, got %q", updated.WorkspaceID)
	}
	if updated.Label != "Updated Inbox" {
		t.Fatalf("expected updated label, got %q", updated.Label)
	}
	if updated.Source != "import" || updated.RetentionPolicy != "until_rotated" {
		t.Fatalf("unexpected updated metadata: source=%q retention=%q", updated.Source, updated.RetentionPolicy)
	}
	if len(updated.Tags) != 2 || updated.Tags[0] != "credentials" || updated.Tags[1] != "personal" {
		t.Fatalf("unexpected updated tags: %#v", updated.Tags)
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

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	record := &Record{
		VaultID:     primaryVault.ID,
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
		VaultID:     primaryVault.ID,
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

func TestStoreCreateGrantRefreshPreservesCreatedAt(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{
		InMemory: true,
		WALMode:  false,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	currentTime := time.Date(2026, time.April, 3, 9, 0, 0, 0, time.UTC)
	store := NewStore(db, StoreOptions{
		SecretStore: NewMemorySecretStore(),
		Clock: func() time.Time {
			return currentTime
		},
	})

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	original := &Grant{
		VaultID:     primaryVault.ID,
		WorkspaceID: "ws-finance",
		ActorType:   ActorTypeAgent,
		ActorID:     "finance-agent",
		Capability:  CapabilityEmailRead,
		RecordType:  "email_snippet",
	}
	if err := store.CreateGrant(ctx, original); err != nil {
		t.Fatalf("create original grant: %v", err)
	}

	currentTime = currentTime.Add(2 * time.Hour)
	refreshed := &Grant{
		VaultID:     primaryVault.ID,
		WorkspaceID: "ws-finance",
		ActorType:   ActorTypeAgent,
		ActorID:     "finance-agent",
		Capability:  CapabilityEmailRead,
		RecordType:  "email_snippet",
	}
	if err := store.CreateGrant(ctx, refreshed); err != nil {
		t.Fatalf("refresh grant: %v", err)
	}

	if !refreshed.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("expected refreshed grant created_at %v, got %v", original.CreatedAt, refreshed.CreatedAt)
	}
	if !refreshed.UpdatedAt.Equal(currentTime) {
		t.Fatalf("expected refreshed grant updated_at %v, got %v", currentTime, refreshed.UpdatedAt)
	}

	var createdAt time.Time
	var updatedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT created_at, updated_at
		FROM vault_grants
		WHERE id = ?
	`, refreshed.ID).Scan(&createdAt, &updatedAt); err != nil {
		t.Fatalf("query refreshed grant timestamps: %v", err)
	}
	if !createdAt.Equal(original.CreatedAt) {
		t.Fatalf("expected persisted created_at %v, got %v", original.CreatedAt, createdAt)
	}
	if !updatedAt.Equal(currentTime) {
		t.Fatalf("expected persisted updated_at %v, got %v", currentTime, updatedAt)
	}
}

func TestStoreExportRequiresPasswordAndEncryptsBundle(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	if err := store.CreateRecord(ctx, &Record{
		VaultID:     primaryVault.ID,
		Type:        "personal_note",
		WorkspaceID: "ws-1",
		Label:       "Passport",
		Payload:     json.RawMessage(`{"number":"X1234567"}`),
	}, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	if _, err := store.Export(ctx, ExportRequest{VaultID: primaryVault.ID, WorkspaceID: "ws-1"}); !errors.Is(err, ErrExportPasswordEmpty) {
		t.Fatalf("expected ErrExportPasswordEmpty, got %v", err)
	}

	bundle, err := store.Export(ctx, ExportRequest{
		VaultID:     primaryVault.ID,
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

func TestStoreImportCreatesVaultAndRestoresBundle(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	sourceVault := createTestVault(t, ctx, store, "Travel Vault")

	if err := store.CreateRecord(ctx, &Record{
		VaultID:     sourceVault.ID,
		Type:        "secret",
		WorkspaceID: "ws-import",
		Label:       "Flight backup code",
		Source:      "manual",
		Payload:     json.RawMessage(`{"code":"ZX-42"}`),
	}, AccessContext{}); err != nil {
		t.Fatalf("create source record: %v", err)
	}

	if err := store.CreateGrant(ctx, &Grant{
		VaultID:     sourceVault.ID,
		WorkspaceID: "ws-import",
		ActorType:   ActorTypeAgent,
		ActorID:     "travel-agent",
		Capability:  CapabilitySecretsRead,
		RecordType:  "secret",
	}); err != nil {
		t.Fatalf("create source grant: %v", err)
	}

	bundle, err := store.Export(ctx, ExportRequest{
		VaultID:  sourceVault.ID,
		Password: "bundle-import-pass",
	})
	if err != nil {
		t.Fatalf("export source vault: %v", err)
	}

	result, err := store.Import(ctx, ImportRequest{
		Password:         "bundle-import-pass",
		Bundle:           *bundle,
		NewVaultName:     "Imported Travel Vault",
		NewVaultPassword: "imported-vault-pass",
		RestoreGrants:    true,
	})
	if err != nil {
		t.Fatalf("import vault bundle: %v", err)
	}
	if !result.CreatedVault {
		t.Fatal("expected import to create a new vault")
	}
	if result.RecordCount != 1 || result.GrantCount != 1 {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	if result.Vault.Name != "Imported Travel Vault" {
		t.Fatalf("unexpected imported vault name: %+v", result.Vault)
	}

	records, err := store.ListRecords(ctx, RecordFilter{VaultID: result.Vault.ID}, AccessContext{})
	if err != nil {
		t.Fatalf("list imported records: %v", err)
	}
	if len(records) != 1 || records[0].Label != "Flight backup code" {
		t.Fatalf("unexpected imported records: %#v", records)
	}

	grants, err := store.ListGrants(ctx, result.Vault.ID, "")
	if err != nil {
		t.Fatalf("list imported grants: %v", err)
	}
	if len(grants) != 1 || grants[0].ActorID != "travel-agent" {
		t.Fatalf("unexpected imported grants: %#v", grants)
	}
}

func TestStoreRejectsMalformedEncryptedRecord(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	record := &Record{
		VaultID:     primaryVault.ID,
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

func TestStoreSupportsMultipleVaults(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	personalVault := createTestVaultWithPassword(t, ctx, store, "Personal Vault", "personal-vault-pass")
	if err := store.CreateVault(ctx, &Vault{
		Name:        "Finance Vault",
		Description: "Quarterly and tax records",
	}, "finance-vault-pass"); err != nil {
		t.Fatalf("create second vault: %v", err)
	}

	vaults, err := store.ListVaults(ctx)
	if err != nil {
		t.Fatalf("list vaults: %v", err)
	}
	if len(vaults) != 2 {
		t.Fatalf("expected 2 vaults, got %d", len(vaults))
	}

	var financeVault Vault
	for _, item := range vaults {
		if item.Name == "Finance Vault" {
			financeVault = item
			break
		}
	}
	if financeVault.ID == "" {
		t.Fatal("expected Finance Vault to be returned")
	}

	if err := store.CreateRecord(ctx, &Record{
		VaultID:     personalVault.ID,
		Type:        "personal_note",
		WorkspaceID: "ws-home",
		Label:       "Personal note",
		Payload:     json.RawMessage(`{"value":"default"}`),
	}, AccessContext{}); err != nil {
		t.Fatalf("create personal vault record: %v", err)
	}

	if err := store.CreateRecord(ctx, &Record{
		VaultID:     financeVault.ID,
		Type:        "secret",
		WorkspaceID: "ws-finance",
		Label:       "Finance secret",
		Payload:     json.RawMessage(`{"token":"abc123"}`),
	}, AccessContext{}); err != nil {
		t.Fatalf("create finance vault record: %v", err)
	}

	personalRecords, err := store.ListRecords(ctx, RecordFilter{VaultID: personalVault.ID}, AccessContext{})
	if err != nil {
		t.Fatalf("list personal vault records: %v", err)
	}
	if len(personalRecords) != 1 || personalRecords[0].Label != "Personal note" {
		t.Fatalf("unexpected personal vault records: %#v", personalRecords)
	}

	financeRecords, err := store.ListRecords(ctx, RecordFilter{VaultID: financeVault.ID}, AccessContext{})
	if err != nil {
		t.Fatalf("list finance vault records: %v", err)
	}
	if len(financeRecords) != 1 || financeRecords[0].Label != "Finance secret" {
		t.Fatalf("unexpected finance vault records: %#v", financeRecords)
	}
	if financeRecords[0].VaultID != financeVault.ID {
		t.Fatalf("expected finance record vault id %q, got %q", financeVault.ID, financeRecords[0].VaultID)
	}

	memoryStore, ok := store.primarySecretStore.(*MemorySecretStore)
	if !ok {
		t.Fatal("expected memory secret store")
	}
	if _, ok := memoryStore.secrets[vaultDEKSecretKey(personalVault.ID)]; ok {
		t.Fatal("did not expect personal vault DEK in the shared secret store")
	}
	if _, ok := memoryStore.secrets[vaultDEKSecretKey(financeVault.ID)]; ok {
		t.Fatal("did not expect finance vault DEK in the shared secret store")
	}

	if err := store.Lock(ctx, financeVault.ID); err != nil {
		t.Fatalf("lock finance vault: %v", err)
	}
	if _, err := store.ListRecords(ctx, RecordFilter{VaultID: financeVault.ID}, AccessContext{}); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("expected finance vault to be locked, got %v", err)
	}
	if _, err := store.ListRecords(ctx, RecordFilter{VaultID: personalVault.ID}, AccessContext{}); err != nil {
		t.Fatalf("expected personal vault to remain available, got %v", err)
	}
	if err := store.Unlock(ctx, financeVault.ID, "personal-vault-pass"); !errors.Is(err, ErrVaultPasswordInvalid) {
		t.Fatalf("expected ErrVaultPasswordInvalid unlocking with the wrong password, got %v", err)
	}
	if err := store.Unlock(ctx, financeVault.ID, "finance-vault-pass"); err != nil {
		t.Fatalf("unlock finance vault: %v", err)
	}
	if _, err := store.ListRecords(ctx, RecordFilter{VaultID: financeVault.ID}, AccessContext{}); err != nil {
		t.Fatalf("expected finance vault to unlock, got %v", err)
	}
}

func TestStoreRenamesAndDeletesVaults(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	if err := store.CreateVault(ctx, &Vault{
		Name:        "Finance Vault",
		Description: "Quarterly and tax records",
	}, testVaultPassword); err != nil {
		t.Fatalf("create finance vault: %v", err)
	}

	vaults, err := store.ListVaults(ctx)
	if err != nil {
		t.Fatalf("list vaults: %v", err)
	}

	var financeVault Vault
	for _, item := range vaults {
		if item.Name == "Finance Vault" {
			financeVault = item
			break
		}
	}
	if financeVault.ID == "" {
		t.Fatal("expected finance vault")
	}

	if err := store.CreateRecord(ctx, &Record{
		VaultID: financeVault.ID,
		Type:    "secret",
		Label:   "Tax token",
		Payload: json.RawMessage(`{"token":"abc123"}`),
	}, AccessContext{}); err != nil {
		t.Fatalf("create finance record: %v", err)
	}

	if err := store.CreateGrant(ctx, &Grant{
		VaultID:     financeVault.ID,
		WorkspaceID: "ws-finance",
		ActorType:   ActorTypeAgent,
		ActorID:     "finance-agent",
		Capability:  CapabilitySecretsRead,
		RecordType:  "secret",
	}); err != nil {
		t.Fatalf("create finance grant: %v", err)
	}

	updatedVault, err := store.UpdateVault(ctx, financeVault.ID, "Family Archive", "Household records")
	if err != nil {
		t.Fatalf("update finance vault: %v", err)
	}
	if updatedVault.Name != "Family Archive" || updatedVault.Description != "Household records" {
		t.Fatalf("unexpected updated vault: %+v", updatedVault)
	}

	if err := store.DeleteVault(ctx, financeVault.ID); err != nil {
		t.Fatalf("delete finance vault: %v", err)
	}

	if _, err := store.ListRecords(ctx, RecordFilter{VaultID: financeVault.ID}, AccessContext{}); !errors.Is(err, ErrVaultNotFound) {
		t.Fatalf("expected ErrVaultNotFound after delete, got %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_records WHERE vault_id = ?`, financeVault.ID).Scan(&count); err != nil {
		t.Fatalf("count deleted vault records: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected deleted vault records to be removed, got %d", count)
	}

	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_grants WHERE vault_id = ?`, financeVault.ID).Scan(&count); err != nil {
		t.Fatalf("count deleted vault grants: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected deleted vault grants to be removed, got %d", count)
	}

	memoryStore, ok := store.primarySecretStore.(*MemorySecretStore)
	if !ok {
		t.Fatal("expected memory secret store")
	}
	if _, ok := memoryStore.secrets[vaultDEKSecretKey(financeVault.ID)]; ok {
		t.Fatal("did not expect deleted password-protected vault to use a shared secret-store DEK")
	}
}

func TestStoreRequiresVaultWhenNoneExist(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	status, err := store.Status(ctx, "")
	if err != nil {
		t.Fatalf("status without vaults: %v", err)
	}
	if status.Available {
		t.Fatalf("expected unavailable status without vaults, got %+v", status)
	}

	if err := store.CreateRecord(ctx, &Record{
		Type:    "personal_note",
		Label:   "Missing vault",
		Payload: json.RawMessage(`{"ok":true}`),
	}, AccessContext{}); !errors.Is(err, ErrVaultRequired) {
		t.Fatalf("expected ErrVaultRequired creating record without vaults, got %v", err)
	}
}

func TestStoreCreateVaultRequiresPassword(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	if err := store.CreateVault(ctx, &Vault{Name: "No Password Vault"}, ""); !errors.Is(err, ErrVaultPasswordRequired) {
		t.Fatalf("expected ErrVaultPasswordRequired, got %v", err)
	}
}
