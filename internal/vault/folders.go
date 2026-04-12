package vault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type folderRow struct {
	ID             string
	VaultID        string
	PathHash       string
	PathNonce      string
	PathCiphertext string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type folderSQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func normalizeFolderPath(path string) (string, error) {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if path == "" {
		return "", nil
	}

	rawSegments := strings.Split(path, "/")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return "", ErrFolderPathInvalid
		}
		segments = append(segments, segment)
	}

	return strings.Join(segments, "/"), nil
}

func folderPathAncestors(path string) []string {
	normalized, err := normalizeFolderPath(path)
	if err != nil || normalized == "" {
		return nil
	}

	segments := strings.Split(normalized, "/")
	ancestors := make([]string, 0, len(segments))
	for index := range segments {
		ancestors = append(ancestors, strings.Join(segments[:index+1], "/"))
	}
	return ancestors
}

func folderPathHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func decryptFolderRow(dek []byte, row folderRow) (*Folder, error) {
	plaintext, err := decryptBytes(dek, row.PathNonce, row.PathCiphertext)
	if err != nil {
		return nil, ErrMalformedRecord
	}

	path, err := normalizeFolderPath(string(plaintext))
	if err != nil {
		return nil, ErrMalformedRecord
	}

	return &Folder{
		ID:        row.ID,
		VaultID:   row.VaultID,
		Path:      path,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func encryptFolderPath(dek []byte, path string) (string, string, error) {
	return encryptBytes(dek, []byte(path))
}

func scanFolderRow(scanner interface {
	Scan(dest ...interface{}) error
}) (folderRow, error) {
	var row folderRow
	if err := scanner.Scan(
		&row.ID,
		&row.VaultID,
		&row.PathHash,
		&row.PathNonce,
		&row.PathCiphertext,
		&row.CreatedAt,
		&row.UpdatedAt,
	); err != nil {
		return folderRow{}, fmt.Errorf("scan vault folder: %w", err)
	}
	return row, nil
}

func insertFolderPathWithExecutor(ctx context.Context, executor folderSQLExecutor, dek []byte, vaultID string, path string, now time.Time) error {
	path, err := normalizeFolderPath(path)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	nonce, ciphertext, err := encryptFolderPath(dek, path)
	if err != nil {
		return fmt.Errorf("encrypt folder path: %w", err)
	}

	_, err = executor.ExecContext(ctx, `
		INSERT OR IGNORE INTO vault_folders (
			id, vault_id, path_hash, path_nonce, path_ciphertext, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, uuid.New().String(), vaultID, folderPathHash(path), nonce, ciphertext, now, now)
	if err != nil {
		return fmt.Errorf("insert vault folder: %w", err)
	}

	return nil
}

func ensureFolderPathWithExecutor(ctx context.Context, executor folderSQLExecutor, dek []byte, vaultID string, path string, now time.Time) error {
	for _, ancestor := range folderPathAncestors(path) {
		if err := insertFolderPathWithExecutor(ctx, executor, dek, vaultID, ancestor, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) getFolderByPath(ctx context.Context, vaultID string, path string, allowCreateDEK bool) (*Folder, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	vaultID = normalizeVaultID(vaultID)
	path, err := normalizeFolderPath(path)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, ErrFolderPathInvalid
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, vault_id, path_hash, path_nonce, path_ciphertext, created_at, updated_at
		FROM vault_folders
		WHERE vault_id = ? AND path_hash = ?
	`, vaultID, folderPathHash(path))

	folderRow, err := scanFolderRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecordNotFound
	}
	if err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, vaultID, allowCreateDEK)
	if err != nil {
		return nil, err
	}

	return decryptFolderRow(dek, folderRow)
}

func (s *Store) ListFolders(ctx context.Context, vaultID string) ([]Folder, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	resolvedVaultID, err := s.resolveVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	vaultID = resolvedVaultID
	if _, err := s.getVault(ctx, vaultID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vault_id, path_hash, path_nonce, path_ciphertext, created_at, updated_at
		FROM vault_folders
		WHERE vault_id = ?
		ORDER BY created_at ASC
	`, vaultID)
	if err != nil {
		return nil, fmt.Errorf("query vault folders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	rawRows := make([]folderRow, 0)
	for rows.Next() {
		row, err := scanFolderRow(rows)
		if err != nil {
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vault folders: %w", err)
	}
	if len(rawRows) == 0 {
		return []Folder{}, nil
	}

	dek, err := s.ensureDataEncryptionKey(ctx, vaultID, false)
	if err != nil {
		return nil, err
	}

	folders := make([]Folder, 0, len(rawRows))
	for _, row := range rawRows {
		folder, err := decryptFolderRow(dek, row)
		if err != nil {
			return nil, err
		}
		folders = append(folders, *folder)
	}

	sort.Slice(folders, func(i, j int) bool {
		left := strings.ToLower(folders[i].Path)
		right := strings.ToLower(folders[j].Path)
		if left == right {
			return folders[i].CreatedAt.Before(folders[j].CreatedAt)
		}
		return left < right
	})

	return folders, nil
}

func (s *Store) CreateFolder(ctx context.Context, folder *Folder) (*Folder, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}
	if folder == nil {
		return nil, fmt.Errorf("vault: folder is required")
	}

	resolvedVaultID, err := s.resolveVaultID(ctx, folder.VaultID)
	if err != nil {
		return nil, err
	}
	folder.VaultID = resolvedVaultID
	folder.Path, err = normalizeFolderPath(folder.Path)
	if err != nil {
		return nil, err
	}
	if folder.Path == "" {
		return nil, ErrFolderPathInvalid
	}
	if _, err := s.getVault(ctx, folder.VaultID); err != nil {
		return nil, err
	}

	dek, err := s.ensureDataEncryptionKey(ctx, folder.VaultID, true)
	if err != nil {
		return nil, err
	}

	now := s.now()
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		return ensureFolderPathWithExecutor(ctx, tx, dek, folder.VaultID, folder.Path, now)
	})
	if err != nil {
		return nil, err
	}

	created, err := s.getFolderByPath(ctx, folder.VaultID, folder.Path, false)
	if err != nil {
		return nil, err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID: folder.VaultID,
		Action:  "folder.create",
		Outcome: "allowed",
		Details: fmt.Sprintf(`{"path":%q}`, folder.Path),
	})

	return created, nil
}

func (s *Store) DeleteFolder(ctx context.Context, vaultID string, path string, recursive bool) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	resolvedVaultID, err := s.resolveVaultID(ctx, vaultID)
	if err != nil {
		return err
	}
	vaultID = resolvedVaultID

	path, err = normalizeFolderPath(path)
	if err != nil {
		return err
	}
	if path == "" {
		return ErrFolderPathInvalid
	}

	if _, err := s.getVault(ctx, vaultID); err != nil {
		return err
	}
	if _, err := s.getFolderByPath(ctx, vaultID, path, false); err != nil {
		return err
	}

	folders, err := s.ListFolders(ctx, vaultID)
	if err != nil {
		return err
	}
	records, err := s.ListRecords(ctx, RecordFilter{VaultID: vaultID}, AccessContext{})
	if err != nil {
		return err
	}

	folderPathsToDelete := make([]string, 0, len(folders))
	for _, folder := range folders {
		if folder.Path == path || strings.HasPrefix(folder.Path, path+"/") {
			folderPathsToDelete = append(folderPathsToDelete, folder.Path)
		}
	}

	recordIDsToDelete := make([]string, 0)
	for _, record := range records {
		if record.FolderPath == path || strings.HasPrefix(record.FolderPath, path+"/") {
			recordIDsToDelete = append(recordIDsToDelete, record.ID)
		}
	}

	nestedFolderCount := 0
	if len(folderPathsToDelete) > 0 {
		nestedFolderCount = len(folderPathsToDelete) - 1
	}
	if !recursive && (nestedFolderCount > 0 || len(recordIDsToDelete) > 0) {
		return ErrFolderNotEmpty
	}

	rowsAffected := int64(0)
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, recordID := range recordIDsToDelete {
			if _, err := tx.ExecContext(ctx, `DELETE FROM vault_record_attachments WHERE record_id = ?`, recordID); err != nil {
				return fmt.Errorf("delete vault record attachments: %w", err)
			}

			result, err := tx.ExecContext(ctx, `DELETE FROM vault_records WHERE id = ?`, recordID)
			if err != nil {
				return fmt.Errorf("delete vault record: %w", err)
			}
			recordRowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("delete vault record rows affected: %w", err)
			}
			if recordRowsAffected == 0 {
				return ErrRecordNotFound
			}
		}

		for _, folderPath := range folderPathsToDelete {
			result, err := tx.ExecContext(ctx, `
				DELETE FROM vault_folders
				WHERE vault_id = ? AND path_hash = ?
			`, vaultID, folderPathHash(folderPath))
			if err != nil {
				return fmt.Errorf("delete vault folder: %w", err)
			}
			folderRowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("delete vault folder rows affected: %w", err)
			}
			rowsAffected += folderRowsAffected
		}

		return nil
	})
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return ErrRecordNotFound
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID: vaultID,
		Action:  "folder.delete",
		Outcome: "allowed",
		Details: fmt.Sprintf(`{"path":%q,"recursive":%t,"deleted_record_count":%d,"deleted_folder_count":%d}`, path, recursive, len(recordIDsToDelete), len(folderPathsToDelete)),
	})

	return nil
}
