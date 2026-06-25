package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

func (s *Store) ListRecords(ctx context.Context, filter RecordFilter, access AccessContext) ([]RecordListItem, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	access = access.normalized()
	resolvedVaultID, err := s.resolveVaultID(ctx, filter.VaultID)
	if err != nil {
		return nil, err
	}
	filter.VaultID = resolvedVaultID
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	filter.Type = normalizeRecordType(filter.Type)
	if filter.Type == "*" {
		filter.Type = ""
	}
	if _, err := s.getVault(ctx, filter.VaultID); err != nil {
		return nil, err
	}
	vaultDB, err := s.openVaultContentDB(ctx, filter.VaultID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = vaultDB.Close() }()

	if err := s.authorizeList(ctx, access, filter); err != nil {
		return nil, err
	}

	items := make([]RecordListItem, 0)
	hasRecords, err := s.hasMatchingRecords(ctx, filter)
	if err != nil {
		return nil, err
	}
	if !hasRecords {
		s.writeAuditBestEffort(ctx, AuditEvent{
			VaultID:     filter.VaultID,
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "allowed",
			Details:     `{"count":0}`,
		})
		return items, nil
	}

	dek, err := s.ensureDataEncryptionKey(ctx, filter.VaultID, false)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, vault_id, type, workspace_id, source, retention_policy,
		       metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext,
		       created_at, updated_at
		FROM vault_records
		WHERE vault_id = ?
		  AND (? = '' OR workspace_id = ?)
		  AND (? = '' OR type = ?)
		ORDER BY updated_at DESC
	`
	rows, err := vaultDB.QueryContext(ctx, query, filter.VaultID, filter.WorkspaceID, filter.WorkspaceID, filter.Type, filter.Type)
	if err != nil {
		return nil, fmt.Errorf("query vault records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		row, err := scanRecordRow(rows)
		if err != nil {
			return nil, err
		}
		readCapability, _ := capabilitiesForRecordType(row.Type)
		if access.requiresGrant() && !hasGrantWithExecutor(ctx, vaultDB, row.VaultID, access, row.WorkspaceID, readCapability, row.Type) {
			continue
		}

		metadata, err := decryptRecordMetadata(dek, row.MetadataNonce, row.MetadataCiphertext)
		if err != nil {
			return nil, err
		}

		items = append(items, RecordListItem{
			ID:              row.ID,
			VaultID:         row.VaultID,
			Type:            row.Type,
			WorkspaceID:     row.WorkspaceID,
			FolderPath:      metadata.FolderPath,
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
		VaultID:     filter.VaultID,
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
	return s.getRecord(ctx, id, access, false)
}

func (s *Store) getRecord(ctx context.Context, id string, access AccessContext, includeAttachmentContent bool) (*Record, error) {
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
	if err := s.authorizeAccess(ctx, access, row.VaultID, row.WorkspaceID, readCapability, row.Type, "read", id); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, row.VaultID, false)
	if err != nil {
		return nil, err
	}
	vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = vaultDB.Close() }()

	record, err := decryptRecordRow(dek, row)
	if err != nil {
		return nil, err
	}

	attachments, err := listRecordAttachmentsWithExecutor(ctx, vaultDB, dek, row.ID, row.VaultID, includeAttachmentContent)
	if err != nil {
		return nil, err
	}
	record.Payload = mergeRecordPayloadAttachments(record.Payload, attachments, includeAttachmentContent)

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     row.VaultID,
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
	resolvedVaultID, err := s.resolveVaultID(ctx, record.VaultID)
	if err != nil {
		return err
	}
	record.VaultID = resolvedVaultID
	record.Type = normalizeRecordType(record.Type)
	record.WorkspaceID = strings.TrimSpace(record.WorkspaceID)
	record.FolderPath, err = normalizeFolderPath(record.FolderPath)
	if err != nil {
		return err
	}
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
	cleanPayload, inlineAttachments, _, err := extractInlineRecordAttachments(record.Payload)
	if err != nil {
		return err
	}
	record.Payload = cleanPayload
	if _, err := s.getVault(ctx, record.VaultID); err != nil {
		return err
	}
	vaultDB, err := s.openVaultContentDB(ctx, record.VaultID)
	if err != nil {
		return err
	}
	defer func() { _ = vaultDB.Close() }()

	_, writeCapability := capabilitiesForRecordType(record.Type)
	if err := s.authorizeAccess(ctx, access, record.VaultID, record.WorkspaceID, writeCapability, record.Type, "create", record.ID); err != nil {
		return err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, record.VaultID, true)
	if err != nil {
		return err
	}

	now := s.now()
	record.CreatedAt = now
	record.UpdatedAt = now

	metadataNonce, metadataCiphertext, err := encryptRecordMetadata(dek, encryptedRecordMetadata{
		Label:      record.Label,
		FolderPath: record.FolderPath,
		Tags:       record.Tags,
	})
	if err != nil {
		return err
	}
	payloadNonce, payloadCiphertext, err := encryptBytes(dek, record.Payload)
	if err != nil {
		return fmt.Errorf("encrypt record payload: %w", err)
	}

	tx, err := vaultDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vault record transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vault_records (
			id, vault_id, type, workspace_id, source, retention_policy,
			metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.VaultID, record.Type, record.WorkspaceID, record.Source, record.RetentionPolicy,
		metadataNonce, metadataCiphertext, payloadNonce, payloadCiphertext, record.CreatedAt, record.UpdatedAt); err != nil {
		return fmt.Errorf("insert vault record: %w", err)
	}

	if err := ensureFolderPathWithExecutor(ctx, tx, dek, record.VaultID, record.FolderPath, record.CreatedAt); err != nil {
		return err
	}

	if len(inlineAttachments) > 0 {
		for index := range inlineAttachments {
			inlineAttachments[index].Attachment.CreatedAt = record.CreatedAt
		}
		if err := replaceRecordAttachmentsWithExecutor(ctx, tx, dek, record, inlineAttachments); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vault record transaction: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     record.VaultID,
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
	vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = vaultDB.Close() }()

	access = access.normalized()
	_, writeCapability := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.VaultID, row.WorkspaceID, writeCapability, row.Type, "update", id); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, row.VaultID, false)
	if err != nil {
		return nil, err
	}

	record, err := decryptRecordRow(dek, row)
	if err != nil {
		return nil, err
	}

	if update.Type != nil {
		record.Type = normalizeRecordType(*update.Type)
	}
	if update.WorkspaceID != nil {
		record.WorkspaceID = strings.TrimSpace(*update.WorkspaceID)
	}
	if update.FolderPath != nil {
		record.FolderPath, err = normalizeFolderPath(*update.FolderPath)
		if err != nil {
			return nil, err
		}
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
	cleanPayload, inlineAttachments, attachmentsFieldPresent, err := extractInlineRecordAttachments(record.Payload)
	if err != nil {
		return nil, err
	}
	record.Payload = cleanPayload

	_, targetWriteCapability := capabilitiesForRecordType(record.Type)
	if record.Type != row.Type || record.WorkspaceID != row.WorkspaceID {
		if err := s.authorizeAccess(ctx, access, row.VaultID, record.WorkspaceID, targetWriteCapability, record.Type, "update", id); err != nil {
			return nil, err
		}
	}
	record.UpdatedAt = s.now()

	metadataNonce, metadataCiphertext, err := encryptRecordMetadata(dek, encryptedRecordMetadata{
		Label:      record.Label,
		FolderPath: record.FolderPath,
		Tags:       record.Tags,
	})
	if err != nil {
		return nil, err
	}
	payloadNonce, payloadCiphertext, err := encryptBytes(dek, record.Payload)
	if err != nil {
		return nil, fmt.Errorf("encrypt record payload: %w", err)
	}

	tx, err := vaultDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin vault record transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE vault_records
		SET type = ?, workspace_id = ?, source = ?, retention_policy = ?, metadata_nonce = ?, metadata_ciphertext = ?,
		    payload_nonce = ?, payload_ciphertext = ?, updated_at = ?
		WHERE id = ?
	`, record.Type, record.WorkspaceID, record.Source, record.RetentionPolicy, metadataNonce, metadataCiphertext,
		payloadNonce, payloadCiphertext, record.UpdatedAt, id)
	if err != nil {
		return nil, fmt.Errorf("update vault record: %w", err)
	}
	if err := database.CheckRowsAffectedWithError(result, "vault_record", ErrRecordNotFound); err != nil {
		return nil, err
	}

	if err := ensureFolderPathWithExecutor(ctx, tx, dek, record.VaultID, record.FolderPath, record.UpdatedAt); err != nil {
		return nil, err
	}

	if attachmentsFieldPresent {
		for index := range inlineAttachments {
			inlineAttachments[index].Attachment.CreatedAt = record.UpdatedAt
		}
		if err := replaceRecordAttachmentsWithExecutor(ctx, tx, dek, record, inlineAttachments); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit vault record transaction: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     record.VaultID,
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
	vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return err
	}
	defer func() { _ = vaultDB.Close() }()

	access = access.normalized()
	_, writeCapability := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.VaultID, row.WorkspaceID, writeCapability, row.Type, "delete", id); err != nil {
		return err
	}

	tx, err := vaultDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vault record delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM vault_record_attachments WHERE record_id = ?`, id); err != nil {
		return fmt.Errorf("delete vault record attachments: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM vault_records WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vault record: %w", err)
	}
	if err := database.CheckRowsAffectedWithError(result, "vault_record", ErrRecordNotFound); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vault record delete transaction: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     row.VaultID,
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

func (s *Store) recordCount(ctx context.Context, vaultID string) (int, error) {
	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		return 0, ErrVaultRequired
	}
	vaultDB, err := s.openVaultContentDB(ctx, vaultID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = vaultDB.Close() }()

	var count int
	if err := vaultDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_records WHERE vault_id = ?`, vaultID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count vault records: %w", err)
	}
	return count, nil
}

func (s *Store) hasMatchingRecords(ctx context.Context, filter RecordFilter) (bool, error) {
	filter.VaultID = normalizeVaultID(filter.VaultID)
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	filter.Type = normalizeRecordType(filter.Type)
	if filter.Type == "*" {
		filter.Type = ""
	}
	vaultDB, err := s.openVaultContentDB(ctx, filter.VaultID)
	if err != nil {
		return false, err
	}
	defer func() { _ = vaultDB.Close() }()

	var exists int
	err = vaultDB.QueryRowContext(ctx, `
		SELECT 1
		FROM vault_records
		WHERE vault_id = ?
		  AND (? = '' OR workspace_id = ?)
		  AND (? = '' OR type = ?)
		LIMIT 1
	`, filter.VaultID, filter.WorkspaceID, filter.WorkspaceID, filter.Type, filter.Type).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("query matching vault records: %w", err)
	default:
		return exists == 1, nil
	}
}

func (s *Store) getRecordRow(ctx context.Context, id string) (recordRow, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return recordRow{}, ErrRecordNotFound
	}

	vaults, err := s.listVaultCatalog(ctx)
	if err != nil {
		return recordRow{}, err
	}

	for _, item := range vaults {
		vaultDB, err := openExistingVaultFile(ctx, s.resolveVaultFileAbsolutePath(item.FilePath))
		if err != nil {
			if errors.Is(err, ErrVaultFileMissing) || errors.Is(err, ErrVaultFileCorrupt) {
				continue
			}
			return recordRow{}, fmt.Errorf("open vault content database: %w", err)
		}

		row, err := getRecordRowWithExecutor(ctx, vaultDB, id)
		_ = vaultDB.Close()
		if err == nil {
			return row, nil
		}
		if errors.Is(err, ErrRecordNotFound) {
			continue
		}
		return recordRow{}, err
	}

	return recordRow{}, ErrRecordNotFound
}

func getRecordRowWithExecutor(ctx context.Context, executor attachmentSQLExecutor, id string) (recordRow, error) {
	var row recordRow
	err := executor.QueryRowContext(ctx, `
		SELECT id, vault_id, type, workspace_id, source, retention_policy,
		       metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext,
		       created_at, updated_at
		FROM vault_records
		WHERE id = ?
	`, id).Scan(
		&row.ID,
		&row.VaultID,
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

func scanRecordRow(rows interface {
	Scan(dest ...any) error
}) (recordRow, error) {
	var row recordRow
	if err := rows.Scan(
		&row.ID,
		&row.VaultID,
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
		VaultID:         row.VaultID,
		Type:            row.Type,
		WorkspaceID:     row.WorkspaceID,
		FolderPath:      metadata.FolderPath,
		Label:           metadata.Label,
		Tags:            metadata.Tags,
		Source:          row.Source,
		RetentionPolicy: row.RetentionPolicy,
		Payload:         json.RawMessage(payload),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}
