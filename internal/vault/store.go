package vault

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type StoreOptions struct {
	SecretStore        SecretStore
	FallbackSecretPath string
	Clock              func() time.Time
}

type Store struct {
	db                 *database.DB
	primarySecretStore SecretStore
	fallbackSecretPath string
	now                func() time.Time

	mu                 sync.RWMutex
	sessionSecretStore SecretStore
	cachedDEK          []byte
}

type encryptedRecordMetadata struct {
	Label string   `json:"label"`
	Tags  []string `json:"tags,omitempty"`
}

type recordRow struct {
	ID                 string
	Type               string
	WorkspaceID        string
	Source             string
	RetentionPolicy    string
	MetadataNonce      string
	MetadataCiphertext string
	PayloadNonce       string
	PayloadCiphertext  string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type exportEnvelope struct {
	ExportedAt  time.Time `json:"exported_at"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Records     []Record  `json:"records"`
	Grants      []Grant   `json:"grants"`
}

func NewStore(db *database.DB, opts StoreOptions) *Store {
	secretStore := opts.SecretStore
	if secretStore == nil {
		secretStore = NewDefaultSecretStore()
	}

	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	return &Store{
		db:                 db,
		primarySecretStore: secretStore,
		fallbackSecretPath: resolveFallbackPath(opts.FallbackSecretPath),
		now:                clock,
	}
}

func (s *Store) Status(ctx context.Context) (VaultStatus, error) {
	if s.db == nil {
		return VaultStatus{
			Available: false,
			Locked:    true,
			Writable:  false,
			Message:   "vault database is unavailable",
			SecretStore: StoreStatus{
				Backend:   BackendUnavailable,
				Available: false,
				Writable:  false,
				Locked:    true,
				Message:   "vault database is unavailable",
			},
		}, nil
	}

	storeStatus := s.effectiveSecretStoreStatus()
	locked := s.currentSecretStore() == nil
	recordCount, err := s.recordCount(ctx)
	if err != nil {
		return VaultStatus{}, fmt.Errorf("count vault records: %w", err)
	}

	status := VaultStatus{
		Available:          true,
		Locked:             locked,
		Writable:           !locked && storeStatus.Writable,
		RequiresPassphrase: locked && !storeStatus.Available,
		Message:            strings.TrimSpace(storeStatus.Message),
		RecordCount:        recordCount,
		SecretStore:        storeStatus,
	}
	if status.RequiresPassphrase {
		status.Message = "vault locked until unlocked with a passphrase"
	}
	return status, nil
}

func (s *Store) Unlock(passphrase string) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	if primary := s.primarySecretStore; primary != nil && primary.Status().Available {
		s.mu.Lock()
		s.cachedDEK = nil
		s.mu.Unlock()
		return nil
	}

	passphrase = strings.TrimSpace(passphrase)
	if passphrase == "" {
		return ErrVaultPasswordRequired
	}

	secretStore, err := NewPassphraseSecretStore(s.fallbackSecretPath, passphrase)
	if err != nil {
		return err
	}

	if _, err := secretStore.Get(SecretKeyVaultDEK); err != nil && !errors.Is(err, ErrSecretNotFound) {
		return fmt.Errorf("unlock vault: %w", err)
	}

	s.mu.Lock()
	s.sessionSecretStore = secretStore
	s.cachedDEK = nil
	s.mu.Unlock()
	return nil
}

func (s *Store) Lock() error {
	s.mu.Lock()
	s.cachedDEK = nil
	s.sessionSecretStore = nil
	s.mu.Unlock()
	return nil
}

func (s *Store) ListRecords(ctx context.Context, filter RecordFilter, access AccessContext) ([]RecordListItem, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	access = access.normalized()
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	filter.Type = normalizeRecordType(filter.Type)
	if filter.Type == "*" {
		filter.Type = ""
	}

	if err := s.authorizeList(ctx, access, filter); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, false)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, type, workspace_id, source, retention_policy,
		       metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext,
		       created_at, updated_at
		FROM vault_records
		WHERE (? = '' OR workspace_id = ?)
		  AND (? = '' OR type = ?)
		ORDER BY updated_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, filter.WorkspaceID, filter.WorkspaceID, filter.Type, filter.Type)
	if err != nil {
		return nil, fmt.Errorf("query vault records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]RecordListItem, 0)
	for rows.Next() {
		row, err := scanRecordRow(rows)
		if err != nil {
			return nil, err
		}
		readCapability, _ := capabilitiesForRecordType(row.Type)
		if access.requiresGrant() && !s.hasGrant(ctx, access, row.WorkspaceID, readCapability, row.Type) {
			continue
		}

		metadata, err := decryptRecordMetadata(dek, row.MetadataNonce, row.MetadataCiphertext)
		if err != nil {
			return nil, err
		}

		items = append(items, RecordListItem{
			ID:              row.ID,
			Type:            row.Type,
			WorkspaceID:     row.WorkspaceID,
			Label:           metadata.Label,
			Tags:            metadata.Tags,
			Source:          row.Source,
			RetentionPolicy: row.RetentionPolicy,
			CreatedAt:       row.CreatedAt,
			UpdatedAt:       row.UpdatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vault records: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: access.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "list",
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"count":%d}`, len(items)),
	})

	return items, nil
}

func (s *Store) GetRecord(ctx context.Context, id string, access AccessContext) (*Record, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrRecordNotFound
	}

	row, err := s.getRecordRow(ctx, id)
	if err != nil {
		return nil, err
	}

	access = access.normalized()
	readCapability, _ := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.WorkspaceID, readCapability, row.Type, "read", id); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, false)
	if err != nil {
		return nil, err
	}

	record, err := decryptRecordRow(dek, row)
	if err != nil {
		return nil, err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: row.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "read",
		RecordID:    record.ID,
		RecordType:  record.Type,
		Outcome:     "allowed",
	})

	return record, nil
}

func (s *Store) CreateRecord(ctx context.Context, record *Record, access AccessContext) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}
	if record == nil {
		return fmt.Errorf("vault: record is required")
	}

	access = access.normalized()
	record.Type = normalizeRecordType(record.Type)
	record.WorkspaceID = strings.TrimSpace(record.WorkspaceID)
	record.Label = strings.TrimSpace(record.Label)
	record.Source = strings.TrimSpace(record.Source)
	record.RetentionPolicy = strings.TrimSpace(record.RetentionPolicy)
	record.Tags = normalizeTags(record.Tags)
	if record.ID == "" {
		record.ID = uuid.New().String()
	}
	if record.Label == "" {
		record.Label = "Untitled Vault Entry"
	}
	if len(record.Payload) == 0 {
		record.Payload = json.RawMessage(`{}`)
	}

	_, writeCapability := capabilitiesForRecordType(record.Type)
	if err := s.authorizeAccess(ctx, access, record.WorkspaceID, writeCapability, record.Type, "create", record.ID); err != nil {
		return err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, true)
	if err != nil {
		return err
	}

	now := s.now()
	record.CreatedAt = now
	record.UpdatedAt = now

	metadataNonce, metadataCiphertext, err := encryptRecordMetadata(dek, encryptedRecordMetadata{
		Label: record.Label,
		Tags:  record.Tags,
	})
	if err != nil {
		return err
	}
	payloadNonce, payloadCiphertext, err := encryptBytes(dek, record.Payload)
	if err != nil {
		return fmt.Errorf("encrypt record payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO vault_records (
			id, type, workspace_id, source, retention_policy,
			metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.Type, record.WorkspaceID, record.Source, record.RetentionPolicy,
		metadataNonce, metadataCiphertext, payloadNonce, payloadCiphertext, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert vault record: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: record.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "create",
		RecordID:    record.ID,
		RecordType:  record.Type,
		Outcome:     "allowed",
	})

	return nil
}

func (s *Store) UpdateRecord(ctx context.Context, id string, update RecordUpdate, access AccessContext) (*Record, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	row, err := s.getRecordRow(ctx, id)
	if err != nil {
		return nil, err
	}

	access = access.normalized()
	_, writeCapability := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.WorkspaceID, writeCapability, row.Type, "update", id); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, false)
	if err != nil {
		return nil, err
	}

	record, err := decryptRecordRow(dek, row)
	if err != nil {
		return nil, err
	}

	if update.Label != nil {
		record.Label = strings.TrimSpace(*update.Label)
		if record.Label == "" {
			record.Label = "Untitled Vault Entry"
		}
	}
	if update.Tags != nil {
		record.Tags = normalizeTags(*update.Tags)
	}
	if update.Source != nil {
		record.Source = strings.TrimSpace(*update.Source)
	}
	if update.RetentionPolicy != nil {
		record.RetentionPolicy = strings.TrimSpace(*update.RetentionPolicy)
	}
	if update.Payload != nil {
		record.Payload = *update.Payload
		if len(record.Payload) == 0 {
			record.Payload = json.RawMessage(`{}`)
		}
	}
	record.UpdatedAt = s.now()

	metadataNonce, metadataCiphertext, err := encryptRecordMetadata(dek, encryptedRecordMetadata{
		Label: record.Label,
		Tags:  record.Tags,
	})
	if err != nil {
		return nil, err
	}
	payloadNonce, payloadCiphertext, err := encryptBytes(dek, record.Payload)
	if err != nil {
		return nil, fmt.Errorf("encrypt record payload: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE vault_records
		SET source = ?, retention_policy = ?, metadata_nonce = ?, metadata_ciphertext = ?,
		    payload_nonce = ?, payload_ciphertext = ?, updated_at = ?
		WHERE id = ?
	`, record.Source, record.RetentionPolicy, metadataNonce, metadataCiphertext,
		payloadNonce, payloadCiphertext, record.UpdatedAt, id)
	if err != nil {
		return nil, fmt.Errorf("update vault record: %w", err)
	}
	if err := database.CheckRowsAffectedWithError(result, "vault_record", ErrRecordNotFound); err != nil {
		return nil, err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: record.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "update",
		RecordID:    record.ID,
		RecordType:  record.Type,
		Outcome:     "allowed",
	})

	return record, nil
}

func (s *Store) DeleteRecord(ctx context.Context, id string, access AccessContext) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	row, err := s.getRecordRow(ctx, id)
	if err != nil {
		return err
	}

	access = access.normalized()
	_, writeCapability := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.WorkspaceID, writeCapability, row.Type, "delete", id); err != nil {
		return err
	}

	result, err := s.db.ExecContext(ctx, `DELETE FROM vault_records WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vault record: %w", err)
	}
	if err := database.CheckRowsAffectedWithError(result, "vault_record", ErrRecordNotFound); err != nil {
		return err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: row.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "delete",
		RecordID:    row.ID,
		RecordType:  row.Type,
		Outcome:     "allowed",
	})

	return nil
}

func (s *Store) ListGrants(ctx context.Context, workspaceID string) ([]Grant, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	workspaceID = strings.TrimSpace(workspaceID)
	query := `
		SELECT id, workspace_id, actor_type, actor_id, capability, record_type, created_at, updated_at
		FROM vault_grants
		WHERE (? = '' OR workspace_id = ?)
		ORDER BY updated_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, workspaceID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query vault grants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	grants := make([]Grant, 0)
	for rows.Next() {
		var grant Grant
		if err := rows.Scan(
			&grant.ID,
			&grant.WorkspaceID,
			&grant.ActorType,
			&grant.ActorID,
			&grant.Capability,
			&grant.RecordType,
			&grant.CreatedAt,
			&grant.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan vault grant: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vault grants: %w", err)
	}
	return grants, nil
}

func (s *Store) CreateGrant(ctx context.Context, grant *Grant) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}
	if grant == nil {
		return fmt.Errorf("vault: grant is required")
	}

	grant.WorkspaceID = strings.TrimSpace(grant.WorkspaceID)
	grant.ActorType = normalizeActorType(grant.ActorType)
	grant.ActorID = strings.TrimSpace(grant.ActorID)
	grant.Capability = normalizeCapability(grant.Capability)
	grant.RecordType = normalizeRecordType(grant.RecordType)
	if grant.WorkspaceID == "" || grant.ActorID == "" || (grant.ActorType != ActorTypeAgent && grant.ActorType != ActorTypePlugin) || grant.Capability == "" {
		return fmt.Errorf("vault: workspace_id, actor_type, actor_id, and capability are required")
	}

	now := s.now()

	var existingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM vault_grants
		WHERE workspace_id = ? AND actor_type = ? AND actor_id = ? AND capability = ? AND record_type = ?
	`, grant.WorkspaceID, grant.ActorType, grant.ActorID, grant.Capability, grant.RecordType).Scan(&existingID)
	switch {
	case err == nil:
		grant.ID = existingID
		grant.CreatedAt = now
		grant.UpdatedAt = now
		_, err = s.db.ExecContext(ctx, `
			UPDATE vault_grants
			SET updated_at = ?
			WHERE id = ?
		`, now, existingID)
		if err != nil {
			return fmt.Errorf("refresh vault grant: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		if grant.ID == "" {
			grant.ID = uuid.New().String()
		}
		grant.CreatedAt = now
		grant.UpdatedAt = now
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO vault_grants (
				id, workspace_id, actor_type, actor_id, capability, record_type, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, grant.ID, grant.WorkspaceID, grant.ActorType, grant.ActorID, grant.Capability, grant.RecordType, grant.CreatedAt, grant.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert vault grant: %w", err)
		}
	default:
		return fmt.Errorf("query existing vault grant: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: grant.WorkspaceID,
		Action:      "grant.create",
		RecordType:  grant.RecordType,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"actor_type":%q,"actor_id":%q,"capability":%q}`, grant.ActorType, grant.ActorID, grant.Capability),
	})

	return nil
}

func (s *Store) DeleteGrant(ctx context.Context, id string) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	var grant Grant
	err := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, actor_type, actor_id, capability, record_type, created_at, updated_at
		FROM vault_grants
		WHERE id = ?
	`, strings.TrimSpace(id)).Scan(
		&grant.ID,
		&grant.WorkspaceID,
		&grant.ActorType,
		&grant.ActorID,
		&grant.Capability,
		&grant.RecordType,
		&grant.CreatedAt,
		&grant.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGrantNotFound
	}
	if err != nil {
		return fmt.Errorf("get vault grant: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `DELETE FROM vault_grants WHERE id = ?`, grant.ID)
	if err != nil {
		return fmt.Errorf("delete vault grant: %w", err)
	}
	if err := database.CheckRowsAffectedWithError(result, "vault_grant", ErrGrantNotFound); err != nil {
		return err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: grant.WorkspaceID,
		Action:      "grant.delete",
		RecordType:  grant.RecordType,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"actor_type":%q,"actor_id":%q,"capability":%q}`, grant.ActorType, grant.ActorID, grant.Capability),
	})

	return nil
}

func (s *Store) Export(ctx context.Context, req ExportRequest) (*ExportBundle, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		return nil, ErrExportPasswordEmpty
	}

	records, err := s.ListRecords(ctx, RecordFilter{WorkspaceID: req.WorkspaceID}, AccessContext{})
	if err != nil {
		return nil, err
	}

	fullRecords := make([]Record, 0, len(records))
	for _, item := range records {
		record, err := s.GetRecord(ctx, item.ID, AccessContext{})
		if err != nil {
			return nil, err
		}
		fullRecords = append(fullRecords, *record)
	}

	grants, err := s.ListGrants(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	envelope := exportEnvelope{
		ExportedAt:  s.now(),
		WorkspaceID: req.WorkspaceID,
		Records:     fullRecords,
		Grants:      grants,
	}

	salt, err := randomBytes(16)
	if err != nil {
		return nil, fmt.Errorf("generate export salt: %w", err)
	}
	derivedKey := derivePassphraseKey(req.Password, salt)
	nonce, ciphertext, err := encryptJSON(derivedKey, envelope)
	if err != nil {
		return nil, fmt.Errorf("encrypt vault export: %w", err)
	}

	bundle := &ExportBundle{
		Version:     1,
		WorkspaceID: req.WorkspaceID,
		Salt:        base64.StdEncoding.EncodeToString(salt),
		Nonce:       nonce,
		Ciphertext:  ciphertext,
		ExportedAt:  envelope.ExportedAt,
		RecordCount: len(fullRecords),
		GrantCount:  len(grants),
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		WorkspaceID: req.WorkspaceID,
		Action:      "export",
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"record_count":%d,"grant_count":%d}`, bundle.RecordCount, bundle.GrantCount),
	})

	return bundle, nil
}

func DecryptExportBundle(bundle ExportBundle, password string) ([]byte, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return nil, ErrExportPasswordEmpty
	}

	salt, err := base64.StdEncoding.DecodeString(bundle.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode export salt: %w", err)
	}
	derivedKey := derivePassphraseKey(password, salt)
	return decryptBytes(derivedKey, bundle.Nonce, bundle.Ciphertext)
}

func (s *Store) currentSecretStore() SecretStore {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sessionSecretStore != nil {
		return s.sessionSecretStore
	}
	if s.primarySecretStore != nil && s.primarySecretStore.Status().Available {
		return s.primarySecretStore
	}
	return nil
}

func (s *Store) effectiveSecretStoreStatus() StoreStatus {
	current := s.currentSecretStore()
	if current != nil {
		return current.Status()
	}
	if s.primarySecretStore != nil {
		return s.primarySecretStore.Status()
	}
	return StoreStatus{
		Backend:   BackendUnavailable,
		Available: false,
		Writable:  false,
		Locked:    true,
		Message:   "no secret store configured",
	}
}

func (s *Store) ensureDataEncryptionKey(ctx context.Context, allowCreate bool) ([]byte, error) {
	s.mu.RLock()
	if len(s.cachedDEK) > 0 {
		keyCopy := append([]byte(nil), s.cachedDEK...)
		s.mu.RUnlock()
		return keyCopy, nil
	}
	s.mu.RUnlock()

	secretStore := s.currentSecretStore()
	if secretStore == nil {
		return nil, ErrVaultLocked
	}

	encodedKey, err := secretStore.Get(SecretKeyVaultDEK)
	switch {
	case err == nil:
		dek, decodeErr := decodeDataEncryptionKey(encodedKey)
		if decodeErr != nil {
			return nil, ErrVaultKeyUnavailable
		}
		s.mu.Lock()
		s.cachedDEK = append([]byte(nil), dek...)
		s.mu.Unlock()
		return dek, nil
	case errors.Is(err, ErrSecretNotFound):
		if !allowCreate {
			return nil, ErrVaultKeyUnavailable
		}
		recordCount, countErr := s.recordCount(ctx)
		if countErr != nil {
			return nil, countErr
		}
		if recordCount > 0 {
			return nil, ErrVaultKeyUnavailable
		}
		dek, genErr := generateDataEncryptionKey()
		if genErr != nil {
			return nil, fmt.Errorf("generate data encryption key: %w", genErr)
		}
		if setErr := secretStore.Set(SecretKeyVaultDEK, encodeDataEncryptionKey(dek)); setErr != nil {
			return nil, fmt.Errorf("store data encryption key: %w", setErr)
		}
		s.mu.Lock()
		s.cachedDEK = append([]byte(nil), dek...)
		s.mu.Unlock()
		return dek, nil
	case errors.Is(err, ErrSecretStoreUnavailable), errors.Is(err, ErrSecretStoreLocked):
		return nil, ErrVaultLocked
	default:
		return nil, fmt.Errorf("load data encryption key: %w", err)
	}
}

func (s *Store) recordCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_records`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count vault records: %w", err)
	}
	return count, nil
}

func (s *Store) getRecordRow(ctx context.Context, id string) (recordRow, error) {
	var row recordRow
	err := s.db.QueryRowContext(ctx, `
		SELECT id, type, workspace_id, source, retention_policy,
		       metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext,
		       created_at, updated_at
		FROM vault_records
		WHERE id = ?
	`, id).Scan(
		&row.ID,
		&row.Type,
		&row.WorkspaceID,
		&row.Source,
		&row.RetentionPolicy,
		&row.MetadataNonce,
		&row.MetadataCiphertext,
		&row.PayloadNonce,
		&row.PayloadCiphertext,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return recordRow{}, ErrRecordNotFound
	}
	if err != nil {
		return recordRow{}, fmt.Errorf("get vault record: %w", err)
	}
	return row, nil
}

func (s *Store) authorizeList(ctx context.Context, access AccessContext, filter RecordFilter) error {
	if !access.requiresGrant() {
		return nil
	}
	if !isValidAccessActor(access) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "denied",
			Details:     `{"reason":"invalid_actor"}`,
		})
		return ErrPermissionDenied
	}
	if access.WorkspaceID == "" || (filter.WorkspaceID != "" && filter.WorkspaceID != access.WorkspaceID) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "denied",
			Details:     `{"reason":"workspace_scope_missing"}`,
		})
		return ErrPermissionDenied
	}
	if !s.hasAnyGrant(ctx, access) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "denied",
			Details:     `{"reason":"persistent_grant_missing"}`,
		})
		return ErrPermissionDenied
	}
	return nil
}

func (s *Store) authorizeAccess(ctx context.Context, access AccessContext, workspaceID string, capability Capability, recordType string, action string, recordID string) error {
	if !access.requiresGrant() {
		return nil
	}
	if !isValidAccessActor(access) || access.WorkspaceID == "" || workspaceID == "" || access.WorkspaceID != workspaceID {
		s.writeAuditBestEffort(ctx, AuditEvent{
			WorkspaceID: workspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      action,
			RecordID:    recordID,
			RecordType:  recordType,
			Outcome:     "denied",
			Details:     `{"reason":"workspace_scope_missing"}`,
		})
		return ErrPermissionDenied
	}
	if !s.hasGrant(ctx, access, workspaceID, capability, recordType) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			WorkspaceID: workspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      action,
			RecordID:    recordID,
			RecordType:  recordType,
			Outcome:     "denied",
			Details:     fmt.Sprintf(`{"reason":"persistent_grant_missing","capability":%q}`, capability),
		})
		return ErrPermissionDenied
	}
	return nil
}

func (s *Store) hasAnyGrant(ctx context.Context, access AccessContext) bool {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM vault_grants
		WHERE workspace_id = ? AND actor_type = ? AND actor_id = ?
		LIMIT 1
	`, access.WorkspaceID, access.ActorType, access.ActorID).Scan(&exists)
	return err == nil && exists == 1
}

func (s *Store) hasGrant(ctx context.Context, access AccessContext, workspaceID string, capability Capability, recordType string) bool {
	recordType = normalizeRecordType(recordType)
	capability = normalizeCapability(capability)

	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM vault_grants
		WHERE workspace_id = ?
		  AND actor_type = ?
		  AND actor_id = ?
		  AND capability = ?
		  AND (record_type = '*' OR record_type = ?)
		LIMIT 1
	`, workspaceID, access.ActorType, access.ActorID, capability, recordType).Scan(&exists)
	return err == nil && exists == 1
}

func (s *Store) writeAuditBestEffort(ctx context.Context, event AuditEvent) {
	if s.db == nil {
		return
	}
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now()
	}
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.ActorType = normalizeActorType(event.ActorType)
	event.ActorID = strings.TrimSpace(event.ActorID)
	event.Action = strings.TrimSpace(event.Action)
	event.RecordID = strings.TrimSpace(event.RecordID)
	event.RecordType = normalizeRecordType(event.RecordType)
	if event.RecordType == "*" {
		event.RecordType = ""
	}
	event.Outcome = strings.TrimSpace(event.Outcome)
	event.Details = strings.TrimSpace(event.Details)

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO vault_audit_events (
			id, workspace_id, actor_type, actor_id, action, record_id, record_type, outcome, details, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.WorkspaceID, event.ActorType, event.ActorID, event.Action,
		event.RecordID, event.RecordType, event.Outcome, event.Details, event.CreatedAt); err != nil {
		logger.Warn("Failed to write vault audit event", logger.Fields{"error": err})
	}
}

func scanRecordRow(rows interface {
	Scan(dest ...interface{}) error
}) (recordRow, error) {
	var row recordRow
	if err := rows.Scan(
		&row.ID,
		&row.Type,
		&row.WorkspaceID,
		&row.Source,
		&row.RetentionPolicy,
		&row.MetadataNonce,
		&row.MetadataCiphertext,
		&row.PayloadNonce,
		&row.PayloadCiphertext,
		&row.CreatedAt,
		&row.UpdatedAt,
	); err != nil {
		return recordRow{}, fmt.Errorf("scan vault record: %w", err)
	}
	return row, nil
}

func decryptRecordRow(dek []byte, row recordRow) (*Record, error) {
	metadata, err := decryptRecordMetadata(dek, row.MetadataNonce, row.MetadataCiphertext)
	if err != nil {
		return nil, err
	}
	payload, err := decryptBytes(dek, row.PayloadNonce, row.PayloadCiphertext)
	if err != nil {
		return nil, ErrMalformedRecord
	}

	return &Record{
		ID:              row.ID,
		Type:            row.Type,
		WorkspaceID:     row.WorkspaceID,
		Label:           metadata.Label,
		Tags:            metadata.Tags,
		Source:          row.Source,
		RetentionPolicy: row.RetentionPolicy,
		Payload:         json.RawMessage(payload),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}

func encryptRecordMetadata(dek []byte, metadata encryptedRecordMetadata) (string, string, error) {
	metadata.Tags = normalizeTags(metadata.Tags)
	return encryptJSON(dek, metadata)
}

func decryptRecordMetadata(dek []byte, nonceB64 string, ciphertextB64 string) (encryptedRecordMetadata, error) {
	var metadata encryptedRecordMetadata
	if err := decryptJSON(dek, nonceB64, ciphertextB64, &metadata); err != nil {
		return encryptedRecordMetadata{}, ErrMalformedRecord
	}
	metadata.Tags = normalizeTags(metadata.Tags)
	return metadata, nil
}

func isValidAccessActor(access AccessContext) bool {
	access = access.normalized()
	if access.ActorID == "" {
		return false
	}
	return access.ActorType == ActorTypeAgent || access.ActorType == ActorTypePlugin
}
