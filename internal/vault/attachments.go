package vault

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxRecordAttachmentBytes = 10 << 20 // 10 MB per attachment

type encryptedRecordAttachmentMetadata struct {
	Name      string `json:"name"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	Kind      string `json:"kind,omitempty"`
}

type recordAttachmentRow struct {
	ID                 string
	RecordID           string
	VaultID            string
	MetadataNonce      string
	MetadataCiphertext string
	DataNonce          string
	DataCiphertext     string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type inlineRecordAttachment struct {
	Attachment RecordAttachment
	Content    []byte
}

type attachmentSQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func normalizeRecordAttachmentKind(kind string, mimeType string) string {
	normalizedKind := strings.TrimSpace(strings.ToLower(kind))
	if normalizedKind == "image" || normalizedKind == "file" {
		return normalizedKind
	}

	if strings.HasPrefix(strings.TrimSpace(strings.ToLower(mimeType)), "image/") {
		return "image"
	}

	return "file"
}

func extractInlineRecordAttachments(payload json.RawMessage) (json.RawMessage, []inlineRecordAttachment, bool, error) {
	if len(payload) == 0 {
		return json.RawMessage(`{}`), nil, false, nil
	}

	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return payload, nil, false, nil
	}

	rawAttachments, found := object["attachments"]
	if !found {
		normalizedPayload, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			return payload, nil, false, nil
		}
		if len(normalizedPayload) == 0 || string(normalizedPayload) == "null" {
			normalizedPayload = []byte(`{}`)
		}
		return json.RawMessage(normalizedPayload), nil, false, nil
	}

	delete(object, "attachments")

	attachments, _ := rawAttachments.([]any)
	inlineAttachments := make([]inlineRecordAttachment, 0, len(attachments))
	for _, rawItem := range attachments {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}

		name := strings.TrimSpace(stringValue(item["name"]))
		if name == "" {
			continue
		}

		contentBase64 := strings.TrimSpace(stringValue(item["content_base64"]))
		if contentBase64 == "" {
			contentBase64 = strings.TrimSpace(stringValue(item["base64_data"]))
		}
		if contentBase64 == "" {
			continue
		}

		content, err := base64.StdEncoding.DecodeString(contentBase64)
		if err != nil {
			return payload, nil, true, fmt.Errorf("decode attachment %q: %w", name, err)
		}

		mimeType := strings.TrimSpace(stringValue(item["mime_type"]))
		if mimeType == "" {
			mimeType = strings.TrimSpace(stringValue(item["mimeType"]))
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		sizeBytes := int64(numberValue(item["size_bytes"]))
		if sizeBytes <= 0 {
			sizeBytes = int64(len(content))
		}

		inlineAttachments = append(inlineAttachments, inlineRecordAttachment{
			Attachment: RecordAttachment{
				Name:      name,
				MimeType:  mimeType,
				SizeBytes: sizeBytes,
				Kind:      normalizeRecordAttachmentKind(stringValue(item["kind"]), mimeType),
			},
			Content: content,
		})
	}

	normalizedPayload, err := json.Marshal(object)
	if err != nil {
		return payload, nil, true, fmt.Errorf("marshal record payload: %w", err)
	}
	if len(normalizedPayload) == 0 || string(normalizedPayload) == "null" {
		normalizedPayload = []byte(`{}`)
	}

	return json.RawMessage(normalizedPayload), inlineAttachments, true, nil
}

func mergeRecordPayloadAttachments(payload json.RawMessage, attachments []RecordAttachment, includeContent bool) json.RawMessage {
	if len(attachments) == 0 {
		return payload
	}

	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		object = make(map[string]any)
	}

	serializedAttachments := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		item := map[string]any{
			"id":         attachment.ID,
			"name":       attachment.Name,
			"mime_type":  attachment.MimeType,
			"size_bytes": attachment.SizeBytes,
			"kind":       attachment.Kind,
		}
		if includeContent {
			item["content_base64"] = attachment.ContentBase64
		} else if strings.TrimSpace(attachment.DownloadURL) != "" {
			item["download_url"] = attachment.DownloadURL
		}
		serializedAttachments = append(serializedAttachments, item)
	}

	object["attachments"] = serializedAttachments
	normalizedPayload, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return json.RawMessage(normalizedPayload)
}

func scanRecordAttachmentRow(rows interface {
	Scan(dest ...interface{}) error
}) (recordAttachmentRow, error) {
	var row recordAttachmentRow
	if err := rows.Scan(
		&row.ID,
		&row.RecordID,
		&row.VaultID,
		&row.MetadataNonce,
		&row.MetadataCiphertext,
		&row.DataNonce,
		&row.DataCiphertext,
		&row.CreatedAt,
		&row.UpdatedAt,
	); err != nil {
		return recordAttachmentRow{}, fmt.Errorf("scan vault record attachment: %w", err)
	}
	return row, nil
}

func encryptRecordAttachmentMetadata(dek []byte, metadata encryptedRecordAttachmentMetadata) (string, string, error) {
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.MimeType = strings.TrimSpace(metadata.MimeType)
	if metadata.MimeType == "" {
		metadata.MimeType = "application/octet-stream"
	}
	metadata.Kind = normalizeRecordAttachmentKind(metadata.Kind, metadata.MimeType)
	if metadata.SizeBytes < 0 {
		metadata.SizeBytes = 0
	}
	return encryptJSON(dek, metadata)
}

func decryptRecordAttachmentMetadata(dek []byte, nonceB64 string, ciphertextB64 string) (encryptedRecordAttachmentMetadata, error) {
	var metadata encryptedRecordAttachmentMetadata
	if err := decryptJSON(dek, nonceB64, ciphertextB64, &metadata); err != nil {
		return encryptedRecordAttachmentMetadata{}, ErrMalformedRecord
	}
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.MimeType = strings.TrimSpace(metadata.MimeType)
	if metadata.MimeType == "" {
		metadata.MimeType = "application/octet-stream"
	}
	metadata.Kind = normalizeRecordAttachmentKind(metadata.Kind, metadata.MimeType)
	if metadata.SizeBytes < 0 {
		metadata.SizeBytes = 0
	}
	return metadata, nil
}

func (s *Store) getRecordAttachmentRow(ctx context.Context, recordID string, attachmentID string) (recordAttachmentRow, error) {
	row, err := s.getRecordRow(ctx, recordID)
	if err != nil {
		return recordAttachmentRow{}, err
	}
	_, vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return recordAttachmentRow{}, err
	}
	defer func() { _ = vaultDB.Close() }()
	return getRecordAttachmentRowWithExecutor(ctx, vaultDB, recordID, attachmentID)
}

func getRecordAttachmentRowWithExecutor(ctx context.Context, executor attachmentSQLExecutor, recordID string, attachmentID string) (recordAttachmentRow, error) {
	var row recordAttachmentRow
	err := executor.QueryRowContext(ctx, `
		SELECT id, record_id, vault_id, metadata_nonce, metadata_ciphertext, data_nonce, data_ciphertext, created_at, updated_at
		FROM vault_record_attachments
		WHERE id = ? AND record_id = ?
	`, strings.TrimSpace(attachmentID), strings.TrimSpace(recordID)).Scan(
		&row.ID,
		&row.RecordID,
		&row.VaultID,
		&row.MetadataNonce,
		&row.MetadataCiphertext,
		&row.DataNonce,
		&row.DataCiphertext,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err == nil {
		return row, nil
	}
	if err == sql.ErrNoRows {
		return recordAttachmentRow{}, ErrRecordAttachmentNotFound
	}
	return recordAttachmentRow{}, fmt.Errorf("get vault record attachment: %w", err)
}

func (s *Store) listRecordAttachments(ctx context.Context, recordID string, includeContent bool) ([]RecordAttachment, error) {
	row, err := s.getRecordRow(ctx, recordID)
	if err != nil {
		return nil, err
	}
	_, vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = vaultDB.Close() }()

	dek, err := s.ensureDataEncryptionKey(ctx, row.VaultID, false)
	if err != nil {
		return nil, err
	}

	return listRecordAttachmentsWithExecutor(ctx, vaultDB, dek, recordID, row.VaultID, includeContent)
}

func listRecordAttachmentsWithExecutor(ctx context.Context, executor attachmentSQLExecutor, dek []byte, recordID string, vaultID string, includeContent bool) ([]RecordAttachment, error) {
	rows, err := executor.QueryContext(ctx, `
		SELECT id, record_id, vault_id, metadata_nonce, metadata_ciphertext, data_nonce, data_ciphertext, created_at, updated_at
		FROM vault_record_attachments
		WHERE record_id = ? AND vault_id = ?
		ORDER BY created_at ASC, id ASC
	`, strings.TrimSpace(recordID), normalizeVaultID(vaultID))
	if err != nil {
		return nil, fmt.Errorf("query vault record attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	attachments := make([]RecordAttachment, 0)
	for rows.Next() {
		row, err := scanRecordAttachmentRow(rows)
		if err != nil {
			return nil, err
		}
		attachment, err := decryptRecordAttachmentRow(dek, row, includeContent)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vault record attachments: %w", err)
	}

	return attachments, nil
}

func decryptRecordAttachmentRow(dek []byte, row recordAttachmentRow, includeContent bool) (RecordAttachment, error) {
	metadata, err := decryptRecordAttachmentMetadata(dek, row.MetadataNonce, row.MetadataCiphertext)
	if err != nil {
		return RecordAttachment{}, err
	}

	attachment := RecordAttachment{
		ID:        row.ID,
		RecordID:  row.RecordID,
		VaultID:   row.VaultID,
		Name:      metadata.Name,
		MimeType:  metadata.MimeType,
		SizeBytes: metadata.SizeBytes,
		Kind:      metadata.Kind,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}

	if includeContent {
		content, err := decryptBytes(dek, row.DataNonce, row.DataCiphertext)
		if err != nil {
			return RecordAttachment{}, ErrMalformedRecord
		}
		attachment.ContentBase64 = base64.StdEncoding.EncodeToString(content)
	}

	return attachment, nil
}

func insertRecordAttachmentWithExecutor(ctx context.Context, executor attachmentSQLExecutor, dek []byte, attachment RecordAttachment, content []byte) (RecordAttachment, error) {
	attachment.RecordID = strings.TrimSpace(attachment.RecordID)
	attachment.VaultID = normalizeVaultID(attachment.VaultID)
	attachment.Name = strings.TrimSpace(attachment.Name)
	attachment.MimeType = strings.TrimSpace(attachment.MimeType)
	attachment.Kind = normalizeRecordAttachmentKind(attachment.Kind, attachment.MimeType)
	if attachment.MimeType == "" {
		attachment.MimeType = "application/octet-stream"
	}
	if attachment.ID == "" {
		attachment.ID = uuid.New().String()
	}
	if attachment.Name == "" {
		attachment.Name = "attachment"
	}
	if attachment.SizeBytes <= 0 {
		attachment.SizeBytes = int64(len(content))
	}
	now := time.Now().UTC()
	if attachment.CreatedAt.IsZero() {
		attachment.CreatedAt = now
	}
	attachment.UpdatedAt = now

	metadataNonce, metadataCiphertext, err := encryptRecordAttachmentMetadata(dek, encryptedRecordAttachmentMetadata{
		Name:      attachment.Name,
		MimeType:  attachment.MimeType,
		SizeBytes: attachment.SizeBytes,
		Kind:      attachment.Kind,
	})
	if err != nil {
		return RecordAttachment{}, err
	}
	dataNonce, dataCiphertext, err := encryptBytes(dek, content)
	if err != nil {
		return RecordAttachment{}, fmt.Errorf("encrypt record attachment: %w", err)
	}

	if _, err := executor.ExecContext(ctx, `
		INSERT INTO vault_record_attachments (
			id, record_id, vault_id, metadata_nonce, metadata_ciphertext, data_nonce, data_ciphertext, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, attachment.ID, attachment.RecordID, attachment.VaultID, metadataNonce, metadataCiphertext, dataNonce, dataCiphertext, attachment.CreatedAt, attachment.UpdatedAt); err != nil {
		return RecordAttachment{}, fmt.Errorf("insert vault record attachment: %w", err)
	}

	return attachment, nil
}

func replaceRecordAttachmentsWithExecutor(ctx context.Context, executor attachmentSQLExecutor, dek []byte, record *Record, attachments []inlineRecordAttachment) error {
	if record == nil {
		return ErrRecordNotFound
	}

	if _, err := executor.ExecContext(ctx, `DELETE FROM vault_record_attachments WHERE record_id = ?`, record.ID); err != nil {
		return fmt.Errorf("delete vault record attachments: %w", err)
	}

	for _, attachment := range attachments {
		item := attachment.Attachment
		item.RecordID = record.ID
		item.VaultID = record.VaultID
		if _, err := insertRecordAttachmentWithExecutor(ctx, executor, dek, item, attachment.Content); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) AddRecordAttachment(ctx context.Context, recordID string, attachment RecordAttachment, content []byte, access AccessContext) (*RecordAttachment, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return nil, ErrRecordNotFound
	}
	if len(content) == 0 {
		return nil, ErrRecordAttachmentRequired
	}
	if int64(len(content)) > MaxRecordAttachmentBytes {
		return nil, ErrRecordAttachmentTooLarge
	}

	row, err := s.getRecordRow(ctx, recordID)
	if err != nil {
		return nil, err
	}
	_, vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = vaultDB.Close() }()

	access = access.normalized()
	_, writeCapability := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.VaultID, row.WorkspaceID, writeCapability, row.Type, "attachment.create", recordID); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, row.VaultID, false)
	if err != nil {
		return nil, err
	}

	attachment.RecordID = row.ID
	attachment.VaultID = row.VaultID
	created, err := insertRecordAttachmentWithExecutor(ctx, vaultDB, dek, attachment, content)
	if err != nil {
		return nil, err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     row.VaultID,
		WorkspaceID: row.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "attachment.create",
		RecordID:    row.ID,
		RecordType:  row.Type,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"attachment_id":%q,"name":%q,"size_bytes":%d}`, created.ID, created.Name, created.SizeBytes),
	})

	return &created, nil
}

func (s *Store) DeleteRecordAttachment(ctx context.Context, recordID string, attachmentID string, access AccessContext) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	row, err := s.getRecordRow(ctx, recordID)
	if err != nil {
		return err
	}
	_, vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return err
	}
	defer func() { _ = vaultDB.Close() }()

	attachmentRow, err := getRecordAttachmentRowWithExecutor(ctx, vaultDB, row.ID, attachmentID)
	if err != nil {
		return err
	}

	access = access.normalized()
	_, writeCapability := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.VaultID, row.WorkspaceID, writeCapability, row.Type, "attachment.delete", recordID); err != nil {
		return err
	}

	result, err := vaultDB.ExecContext(ctx, `DELETE FROM vault_record_attachments WHERE id = ? AND record_id = ?`, attachmentRow.ID, row.ID)
	if err != nil {
		return fmt.Errorf("delete vault record attachment: %w", err)
	}
	if err := checkRowsAffectedWithError(result, ErrRecordAttachmentNotFound); err != nil {
		return err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     row.VaultID,
		WorkspaceID: row.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "attachment.delete",
		RecordID:    row.ID,
		RecordType:  row.Type,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"attachment_id":%q}`, attachmentRow.ID),
	})

	return nil
}

func (s *Store) GetRecordAttachmentContent(ctx context.Context, recordID string, attachmentID string, access AccessContext) (*RecordAttachment, []byte, error) {
	if s.db == nil {
		return nil, nil, ErrSecretStoreUnavailable
	}

	row, err := s.getRecordRow(ctx, recordID)
	if err != nil {
		return nil, nil, err
	}
	_, vaultDB, err := s.openVaultContentDB(ctx, row.VaultID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = vaultDB.Close() }()

	attachmentRow, err := getRecordAttachmentRowWithExecutor(ctx, vaultDB, row.ID, attachmentID)
	if err != nil {
		return nil, nil, err
	}

	access = access.normalized()
	readCapability, _ := capabilitiesForRecordType(row.Type)
	if err := s.authorizeAccess(ctx, access, row.VaultID, row.WorkspaceID, readCapability, row.Type, "attachment.read", recordID); err != nil {
		return nil, nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, row.VaultID, false)
	if err != nil {
		return nil, nil, err
	}

	attachment, err := decryptRecordAttachmentRow(dek, attachmentRow, false)
	if err != nil {
		return nil, nil, err
	}
	content, err := decryptBytes(dek, attachmentRow.DataNonce, attachmentRow.DataCiphertext)
	if err != nil {
		return nil, nil, ErrMalformedRecord
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     row.VaultID,
		WorkspaceID: row.WorkspaceID,
		ActorType:   access.ActorType,
		ActorID:     access.ActorID,
		Action:      "attachment.read",
		RecordID:    row.ID,
		RecordType:  row.Type,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"attachment_id":%q}`, attachment.ID),
	})

	return &attachment, content, nil
}

func checkRowsAffectedWithError(result sql.Result, notFoundErr error) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return notFoundErr
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case int16:
		return float64(typed)
	case int8:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint64:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint8:
		return float64(typed)
	default:
		return 0
	}
}
