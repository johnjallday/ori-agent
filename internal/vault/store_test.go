package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

	return NewStore(db, StoreOptions{}), db
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

func openTestVaultFileDB(t *testing.T, ctx context.Context, store *Store, vaultID string) *sql.DB {
	t.Helper()

	vaultItem, err := store.getVault(ctx, vaultID)
	if err != nil {
		t.Fatalf("get vault %q: %v", vaultID, err)
	}

	vaultDB, err := openVaultFile(ctx, store.resolveVaultFileAbsolutePath(vaultItem.FilePath))
	if err != nil {
		t.Fatalf("open vault file for %q: %v", vaultID, err)
	}
	return vaultDB
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
		FolderPath:  "Travel",
		Label:       "Primary Inbox",
		Tags:        []string{"Email", "Private"},
		Source:      "manual",
		Payload:     json.RawMessage(`{"email":"user@example.com","subject":"Quarterly taxes"}`),
	}
	if err := store.CreateRecord(ctx, record, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}
	vaultDB := openTestVaultFileDB(t, ctx, store, primaryVault.ID)
	defer func() { _ = vaultDB.Close() }()

	var metadataCiphertext, payloadCiphertext string
	if err := vaultDB.QueryRowContext(ctx, `
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
	if items[0].FolderPath != "Travel" {
		t.Fatalf("expected decrypted folder path, got %q", items[0].FolderPath)
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
	folderPath := "Travel/Passports"
	tags := []string{"Credentials", "Personal"}
	source := "import"
	retention := "until_rotated"
	payload := json.RawMessage(`{"email":"user@example.com","subject":"Filed"}`)
	updated, err := store.UpdateRecord(ctx, record.ID, RecordUpdate{
		Type:            &recordType,
		WorkspaceID:     &workspaceID,
		FolderPath:      &folderPath,
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
	if updated.FolderPath != "Travel/Passports" {
		t.Fatalf("expected updated folder path, got %q", updated.FolderPath)
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

	folders, err := store.ListFolders(ctx, primaryVault.ID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if len(folders) != 2 || folders[0].Path != "Travel" || folders[1].Path != "Travel/Passports" {
		t.Fatalf("unexpected folders: %#v", folders)
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

func TestStoreCreateFolderPersistsEmptyFolders(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	created, err := store.CreateFolder(ctx, &Folder{
		VaultID: primaryVault.ID,
		Path:    "Family/Passports",
	})
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if created.Path != "Family/Passports" {
		t.Fatalf("unexpected created folder path: %q", created.Path)
	}

	folders, err := store.ListFolders(ctx, primaryVault.ID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("expected 2 persisted folders including ancestor, got %d", len(folders))
	}
	if folders[0].Path != "Family" || folders[1].Path != "Family/Passports" {
		t.Fatalf("unexpected folders: %#v", folders)
	}
}

func TestStoreDeleteFolderSupportsRecursiveDelete(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	primaryVault := createTestVault(t, ctx, store, "Primary Vault")

	if _, err := store.CreateFolder(ctx, &Folder{
		VaultID: primaryVault.ID,
		Path:    "Family/Passports",
	}); err != nil {
		t.Fatalf("create folder: %v", err)
	}

	if err := store.DeleteFolder(ctx, primaryVault.ID, "Family", false); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("expected ErrFolderNotEmpty for parent folder, got %v", err)
	}

	if err := store.DeleteFolder(ctx, primaryVault.ID, "Family/Passports", false); err != nil {
		t.Fatalf("delete leaf folder: %v", err)
	}

	folders, err := store.ListFolders(ctx, primaryVault.ID)
	if err != nil {
		t.Fatalf("list folders after leaf delete: %v", err)
	}
	if len(folders) != 1 || folders[0].Path != "Family" {
		t.Fatalf("unexpected folders after leaf delete: %#v", folders)
	}

	record := &Record{
		VaultID:    primaryVault.ID,
		Type:       "personal_note",
		FolderPath: "Family",
		Label:      "Passport",
		Payload:    json.RawMessage(`{"note":"Emergency contact"}`),
	}
	if err := store.CreateRecord(ctx, record, AccessContext{}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	if err := store.DeleteFolder(ctx, primaryVault.ID, "Family", false); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("expected ErrFolderNotEmpty for folder containing a record, got %v", err)
	}

	if err := store.DeleteFolder(ctx, primaryVault.ID, "Family", true); err != nil {
		t.Fatalf("delete folder recursively: %v", err)
	}

	folders, err = store.ListFolders(ctx, primaryVault.ID)
	if err != nil {
		t.Fatalf("list folders after delete: %v", err)
	}
	if len(folders) != 0 {
		t.Fatalf("expected no folders after delete, got %#v", folders)
	}
	if _, err := store.GetRecord(ctx, record.ID, AccessContext{}); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound after recursive folder delete, got %v", err)
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
	vaultDB := openTestVaultFileDB(t, ctx, store, primaryVault.ID)
	defer func() { _ = vaultDB.Close() }()

	if !refreshed.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("expected refreshed grant created_at %v, got %v", original.CreatedAt, refreshed.CreatedAt)
	}
	if !refreshed.UpdatedAt.Equal(currentTime) {
		t.Fatalf("expected refreshed grant updated_at %v, got %v", currentTime, refreshed.UpdatedAt)
	}

	var createdAt time.Time
	var updatedAt time.Time
	if err := vaultDB.QueryRowContext(ctx, `
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
	vaultDB := openTestVaultFileDB(t, ctx, store, primaryVault.ID)
	defer func() { _ = vaultDB.Close() }()

	if _, err := vaultDB.ExecContext(ctx, `
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

func TestStorePersistsVaultFilePathAndKeyMaterial(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	item := Vault{
		Name:        "Archive Vault",
		Description: "Records that will move to their own SQLite file",
		FilePath:    "vaults/archive-vault.db",
	}
	if err := store.CreateVault(ctx, &item, testVaultPassword); err != nil {
		t.Fatalf("create vault: %v", err)
	}

	vaults, err := store.ListVaults(ctx)
	if err != nil {
		t.Fatalf("list vaults: %v", err)
	}
	if len(vaults) != 1 {
		t.Fatalf("expected 1 vault, got %d", len(vaults))
	}
	if vaults[0].FilePath != item.FilePath {
		t.Fatalf("expected file path %q, got %q", item.FilePath, vaults[0].FilePath)
	}

	stored, err := store.getVault(ctx, item.ID)
	if err != nil {
		t.Fatalf("get vault: %v", err)
	}
	if stored.FilePath != item.FilePath {
		t.Fatalf("expected stored file path %q, got %q", item.FilePath, stored.FilePath)
	}

	rows, err := db.QueryContext(ctx, "PRAGMA table_info(vaults)")
	if err != nil {
		t.Fatalf("inspect main-db vault columns: %v", err)
	}
	defer func() { _ = rows.Close() }()

	hasKeyColumns := false
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan main-db vault column: %v", err)
		}
		if name == "key_salt" || name == "key_nonce" || name == "key_ciphertext" {
			hasKeyColumns = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate main-db vault columns: %v", err)
	}
	if hasKeyColumns {
		t.Fatalf("expected main-db vault catalog to drop wrapped key columns")
	}

	vaultFileDB, err := openVaultFile(ctx, store.resolveVaultFileAbsolutePath(item.FilePath))
	if err != nil {
		t.Fatalf("open vault file: %v", err)
	}
	defer func() { _ = vaultFileDB.Close() }()

	metadata, err := loadVaultFileMetadata(ctx, vaultFileDB, item.ID)
	if err != nil {
		t.Fatalf("load vault file metadata: %v", err)
	}
	if metadata.VaultID != item.ID {
		t.Fatalf("expected vault metadata id %q, got %q", item.ID, metadata.VaultID)
	}
	if metadata.Name != item.Name || metadata.Description != item.Description {
		t.Fatalf("unexpected vault metadata: %+v", metadata)
	}
	if metadata.KeySalt == "" || metadata.KeyNonce == "" || metadata.KeyCiphertext == "" {
		t.Fatalf("expected wrapped key material in vault file metadata, got %+v", metadata)
	}
}

func TestStoreListRecordsFailsWhenVaultFileIsMissing(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	item := createTestVault(t, ctx, store, "Missing File Vault")
	if err := os.Remove(store.resolveVaultFileAbsolutePath(item.FilePath)); err != nil {
		t.Fatalf("remove vault file: %v", err)
	}

	_, err := store.ListRecords(ctx, RecordFilter{VaultID: item.ID}, AccessContext{})
	if !errors.Is(err, ErrVaultFileMissing) {
		t.Fatalf("expected ErrVaultFileMissing, got %v", err)
	}
}

func TestStoreListRecordsFailsWhenVaultFileIsCorrupt(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	item := createTestVault(t, ctx, store, "Corrupt File Vault")
	if err := os.WriteFile(store.resolveVaultFileAbsolutePath(item.FilePath), []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("corrupt vault file: %v", err)
	}

	_, err := store.ListRecords(ctx, RecordFilter{VaultID: item.ID}, AccessContext{})
	if !errors.Is(err, ErrVaultFileCorrupt) {
		t.Fatalf("expected ErrVaultFileCorrupt, got %v", err)
	}
}

func TestStoreListVaultsIgnoresLegacyCatalogRowsWithoutFilePath(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO vaults (id, name, description, file_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "legacy-vault", "Legacy Vault", "unsupported shared-db vault", "", time.Now(), time.Now()); err != nil {
		t.Fatalf("insert legacy vault row: %v", err)
	}

	vaults, err := store.ListVaults(ctx)
	if err != nil {
		t.Fatalf("list vaults: %v", err)
	}
	if len(vaults) != 0 {
		t.Fatalf("expected legacy blank-file-path vault row to be ignored, got %#v", vaults)
	}
}

func TestStoreListVaultsSkipsMissingVaultFiles(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	validVault := createTestVault(t, ctx, store, "Valid Vault")
	missingVault := createTestVault(t, ctx, store, "Missing Vault")
	if err := os.Remove(store.resolveVaultFileAbsolutePath(missingVault.FilePath)); err != nil {
		t.Fatalf("remove missing vault file: %v", err)
	}

	vaults, err := store.ListVaults(ctx)
	if err != nil {
		t.Fatalf("list vaults: %v", err)
	}
	if len(vaults) != 2 {
		t.Fatalf("expected both vault catalog rows to remain in the list, got %#v", vaults)
	}

	var listedValid Vault
	var listedMissing Vault
	for _, item := range vaults {
		switch item.ID {
		case validVault.ID:
			listedValid = item
		case missingVault.ID:
			listedMissing = item
		}
	}

	if listedValid.ID != validVault.ID || listedValid.FileMissing {
		t.Fatalf("expected valid vault to remain healthy, got %#v", listedValid)
	}
	if listedMissing.ID != missingVault.ID || !listedMissing.FileMissing {
		t.Fatalf("expected missing vault to remain listed with FileMissing=true, got %#v", listedMissing)
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

	if err := store.DeleteVaultWithOptions(ctx, financeVault.ID, DeleteVaultOptions{DeleteFile: true}); err != nil {
		t.Fatalf("delete finance vault: %v", err)
	}

	if _, err := store.ListRecords(ctx, RecordFilter{VaultID: financeVault.ID}, AccessContext{}); !errors.Is(err, ErrVaultNotFound) {
		t.Fatalf("expected ErrVaultNotFound after delete, got %v", err)
	}

	if _, err := os.Stat(store.resolveVaultFileAbsolutePath(financeVault.FilePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected vault file to be removed, got err=%v", err)
	}
}

func TestStoreCreateVaultInCustomDirectory(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	customDir := t.TempDir()
	item := Vault{
		Name:        "Custom Vault",
		Description: "Stored outside the managed vault directory",
	}
	if err := store.CreateVaultWithOptions(ctx, &item, testVaultPassword, CreateVaultOptions{
		Storage: VaultStorage{
			Mode:      VaultStorageModeCustomDir,
			Directory: customDir,
		},
	}); err != nil {
		t.Fatalf("create custom vault: %v", err)
	}

	if !filepath.IsAbs(item.FilePath) {
		t.Fatalf("expected absolute custom file path, got %q", item.FilePath)
	}
	if filepath.Dir(item.FilePath) != customDir {
		t.Fatalf("expected custom directory %q, got %q", customDir, filepath.Dir(item.FilePath))
	}
	if item.StorageMode != VaultStorageModeCustomDir {
		t.Fatalf("expected custom storage mode, got %#v", item)
	}
	if item.LocationSummary != customDir {
		t.Fatalf("expected location summary %q, got %#v", customDir, item)
	}
	if _, err := os.Stat(item.FilePath); err != nil {
		t.Fatalf("expected custom vault file to exist: %v", err)
	}
}

func TestStoreCreateVaultUsesManagedVaultRoot(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(context.Background(), &database.Config{
		InMemory: true,
		WALMode:  false,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	vaultFilesBaseDir := t.TempDir()
	managedRoot := filepath.Join(t.TempDir(), "managed-vaults")
	store := NewStore(db, StoreOptions{
		VaultFilesBaseDir: vaultFilesBaseDir,
		ManagedVaultRoot:  managedRoot,
	})

	item := Vault{Name: "Managed Root Vault"}
	if err := store.CreateVault(ctx, &item, testVaultPassword); err != nil {
		t.Fatalf("create vault: %v", err)
	}

	expectedPath := filepath.Join(managedRoot, defaultVaultFileName(item.ID))
	if item.FilePath != expectedPath {
		t.Fatalf("expected managed vault file path %q, got %q", expectedPath, item.FilePath)
	}
	if item.StorageMode != VaultStorageModeManaged {
		t.Fatalf("expected managed storage mode, got %#v", item)
	}
	if item.LocationSummary != managedRoot {
		t.Fatalf("expected location summary %q, got %#v", managedRoot, item)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected managed vault file to exist: %v", err)
	}
}

func TestStoreSetManagedVaultRootAffectsNewVaults(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(context.Background(), &database.Config{
		InMemory: true,
		WALMode:  false,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	vaultFilesBaseDir := t.TempDir()
	initialRoot := filepath.Join(t.TempDir(), "initial-vaults")
	store := NewStore(db, StoreOptions{
		VaultFilesBaseDir: vaultFilesBaseDir,
		ManagedVaultRoot:  initialRoot,
	})

	firstVault := createTestVault(t, ctx, store, "First Managed Vault")
	firstPath := filepath.Join(initialRoot, defaultVaultFileName(firstVault.ID))
	if firstVault.FilePath != firstPath {
		t.Fatalf("expected first managed vault path %q, got %q", firstPath, firstVault.FilePath)
	}

	nextRoot := filepath.Join(t.TempDir(), "next-vaults")
	if err := store.SetManagedVaultRoot(nextRoot); err != nil {
		t.Fatalf("SetManagedVaultRoot: %v", err)
	}

	secondVault := createTestVault(t, ctx, store, "Second Managed Vault")
	secondPath := filepath.Join(nextRoot, defaultVaultFileName(secondVault.ID))
	if secondVault.FilePath != secondPath {
		t.Fatalf("expected second managed vault path %q, got %q", secondPath, secondVault.FilePath)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("expected second managed vault file to exist: %v", err)
	}
}

func TestStoreRelinkVaultRestoresMissingVault(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	item := createTestVault(t, ctx, store, "Relink Me")
	originalPath := store.resolveVaultFileAbsolutePath(item.FilePath)
	relinkedDir := t.TempDir()
	relinkedPath := filepath.Join(relinkedDir, filepath.Base(originalPath))

	if err := os.Rename(originalPath, relinkedPath); err != nil {
		t.Fatalf("move vault file: %v", err)
	}

	vaults, err := store.ListVaults(ctx)
	if err != nil {
		t.Fatalf("list vaults after move: %v", err)
	}
	if len(vaults) != 1 || !vaults[0].FileMissing {
		t.Fatalf("expected moved vault to be marked missing, got %#v", vaults)
	}

	status, err := store.Status(ctx, item.ID)
	if err != nil {
		t.Fatalf("status for missing vault: %v", err)
	}
	if status.Available || !status.FileMissing {
		t.Fatalf("expected missing-file status, got %+v", status)
	}

	relinkedVault, err := store.RelinkVault(ctx, item.ID, VaultStorage{
		Mode:      VaultStorageModeCustomDir,
		Directory: relinkedDir,
	})
	if err != nil {
		t.Fatalf("relink vault: %v", err)
	}
	if relinkedVault.FileMissing {
		t.Fatalf("expected relinked vault to be available again, got %#v", relinkedVault)
	}
	if relinkedVault.StorageMode != VaultStorageModeCustomDir {
		t.Fatalf("expected custom storage mode after relink, got %#v", relinkedVault)
	}
	if relinkedVault.LocationSummary != relinkedDir {
		t.Fatalf("expected relinked location summary %q, got %#v", relinkedDir, relinkedVault)
	}
	if relinkedVault.FilePath != relinkedPath {
		t.Fatalf("expected relinked file path %q, got %q", relinkedPath, relinkedVault.FilePath)
	}
}

func TestStoreDeleteVaultCanKeepBackingFile(t *testing.T) {
	ctx := context.Background()
	store, db := newTestVaultStore(t)
	defer func() { _ = db.Close() }()

	item := createTestVault(t, ctx, store, "Keep File Vault")
	absolutePath := store.resolveVaultFileAbsolutePath(item.FilePath)

	if err := store.DeleteVault(ctx, item.ID); err != nil {
		t.Fatalf("delete vault safely: %v", err)
	}

	if _, err := os.Stat(absolutePath); err != nil {
		t.Fatalf("expected safe delete to keep the file on disk: %v", err)
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
