package vault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// ErrBackupSecretUnavailable is deliberately non-identifying. Never return raw
// storage, decryption, authorization, or callback errors across this boundary.
var ErrBackupSecretUnavailable = errors.New("backup credential is unavailable")

// BackupSecretReference names one explicitly selected workspace secret. It is
// host-only: not a grant, a generic vault access context, or an HTTP DTO. The
// backup service must persist it only inside encrypted installation-local state.
// Revision fingerprints ciphertext and scope, not the password or a timestamp.
type BackupSecretReference struct {
	VaultID     string `json:"-"`
	RecordID    string `json:"-"`
	WorkspaceID string `json:"-"`
	Revision    string `json:"-"`
}

// BackupSecretAuthorization must check current local owner, workspace/install,
// reviewed reference and configuration generation. Server composition supplies
// this callback; model tools and client payloads must never supply an actor or
// grant. Nil is unavailable. This function does not mint or persist permission.
type BackupSecretAuthorization func(context.Context) error

const (
	maxBackupSecretPayload    = 32 << 10
	maxBackupSecretCiphertext = 64 << 10
	maxBackupPasswordBytes    = 4096
)

type backupSecretRow struct {
	id, vaultID, workspaceID, recordType, nonce, ciphertext string
}

func backupSecretID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func backupSecretAuthorized(ctx context.Context, authorize BackupSecretAuthorization) bool {
	return ctx != nil && ctx.Err() == nil && authorize != nil && authorize(ctx) == nil && ctx.Err() == nil
}

func (r backupSecretRow) revision() string {
	// Length-delimited JSON array avoids ambiguous concatenation. These are
	// ciphertext/scope fields, never decrypted metadata or a password hash.
	data, _ := json.Marshal([]string{"ori.backup-secret.v1", r.id, r.vaultID, r.workspaceID, r.recordType, r.nonce, r.ciphertext})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func readBackupSecretRow(ctx context.Context, db *sql.DB, ref BackupSecretReference) (backupSecretRow, error) {
	var row backupSecretRow
	// Scope/type and length predicates are evaluated before ciphertext is loaded
	// or a key is requested. Never use getRecordRow's all-vault discovery loop.
	err := db.QueryRowContext(ctx, `SELECT id,vault_id,workspace_id,type,payload_nonce,payload_ciphertext
		FROM vault_records WHERE id=? AND vault_id=? AND workspace_id=? AND type='secret'
		AND length(payload_nonce)<=32 AND length(payload_ciphertext)<=?`,
		ref.RecordID, ref.VaultID, ref.WorkspaceID, maxBackupSecretCiphertext).Scan(
		&row.id, &row.vaultID, &row.workspaceID, &row.recordType, &row.nonce, &row.ciphertext)
	if err != nil {
		return backupSecretRow{}, ErrBackupSecretUnavailable
	}
	return row, nil
}

// DescribeBackupSecret resolves only the chosen vault/record and returns an
// opaque revision without decrypting its payload, label, tags or attachments.
// It does not list vaults, unlock one, or use an agent/plugin grant.
func (s *Store) DescribeBackupSecret(ctx context.Context, ref BackupSecretReference, authorize BackupSecretAuthorization) (BackupSecretReference, error) {
	if s == nil || s.db == nil || !backupSecretID(ref.VaultID) || !backupSecretID(ref.RecordID) ||
		!backupSecretID(ref.WorkspaceID) || !backupSecretAuthorized(ctx, authorize) {
		return BackupSecretReference{}, ErrBackupSecretUnavailable
	}
	db, err := s.openVaultContentDB(ctx, ref.VaultID)
	if err != nil {
		return BackupSecretReference{}, ErrBackupSecretUnavailable
	}
	defer func() { _ = db.Close() }()
	row, err := readBackupSecretRow(ctx, db, ref)
	if err != nil || !backupSecretAuthorized(ctx, authorize) {
		return BackupSecretReference{}, ErrBackupSecretUnavailable
	}
	ref.Revision = row.revision()
	return ref, nil
}

// UseBackupSecret supplies a bounded single-line password to one synchronous
// host callback after exact scope/revision/authority checks. The callback should
// only transfer it to the fixed provider pipe; it must not retain or serialize
// the slice. The owned slice, plaintext payload and key copy are cleared on exit
// (not a claim of whole-process/Go-heap zeroization). No password is persisted.
// The service still owns admission, cancellation and fencing of the child/result.
func (s *Store) UseBackupSecret(ctx context.Context, ref BackupSecretReference, authorize BackupSecretAuthorization, use func([]byte) error) error {
	if s == nil || s.db == nil || use == nil || !backupSecretID(ref.VaultID) ||
		!backupSecretID(ref.RecordID) || !backupSecretID(ref.WorkspaceID) || len(ref.Revision) != 64 ||
		!backupSecretAuthorized(ctx, authorize) {
		return ErrBackupSecretUnavailable
	}
	db, err := s.openVaultContentDB(ctx, ref.VaultID)
	if err != nil {
		return ErrBackupSecretUnavailable
	}
	defer func() { _ = db.Close() }()
	row, err := readBackupSecretRow(ctx, db, ref)
	if err != nil || row.revision() != ref.Revision || !backupSecretAuthorized(ctx, authorize) {
		return ErrBackupSecretUnavailable
	}
	// Backup observation never prompts/unlocks or reads key material from other
	// vaults. Only an already unlocked, explicitly selected vault is eligible.
	s.mu.RLock()
	key := append([]byte(nil), s.cachedDEKs[ref.VaultID]...)
	s.mu.RUnlock()
	defer clear(key)
	if len(key) == 0 {
		return ErrBackupSecretUnavailable
	}
	payload, err := decryptBytes(key, row.nonce, row.ciphertext)
	if err != nil {
		return ErrBackupSecretUnavailable
	}
	defer clear(payload)
	password, err := decodeBackupPassword(payload)
	if err != nil {
		return ErrBackupSecretUnavailable
	}
	defer clear(password)
	// Recheck the current ciphertext after decryption, not only UpdatedAt: two
	// writes with the same clock timestamp must invalidate the selected revision.
	current, err := readBackupSecretRow(ctx, db, ref)
	if err != nil || current.revision() != ref.Revision || !backupSecretAuthorized(ctx, authorize) {
		return ErrBackupSecretUnavailable
	}
	// Serialize the short credential transfer with Lock, which clears cachedDEKs.
	// Once a child received bytes, its lifecycle belongs to the backup service.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cachedDEKs[ref.VaultID]) == 0 || ctx.Err() != nil {
		return ErrBackupSecretUnavailable
	}
	if err := use(password); err != nil || ctx.Err() != nil {
		return ErrBackupSecretUnavailable
	}
	return nil
}

func decodeBackupPassword(payload []byte) ([]byte, error) {
	if len(payload) > maxBackupSecretPayload || !utf8.Valid(payload) {
		return nil, ErrBackupSecretUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, ErrBackupSecretUnavailable
	}
	key, err := decoder.Token()
	if err != nil || key != "value" {
		return nil, ErrBackupSecretUnavailable
	}
	var value string
	if err := decoder.Decode(&value); err != nil || decoder.More() || len(value) == 0 ||
		len(value) > maxBackupPasswordBytes || strings.ContainsAny(value, "\x00\r\n\ufffd") {
		return nil, ErrBackupSecretUnavailable
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, ErrBackupSecretUnavailable
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrBackupSecretUnavailable
	}
	return []byte(value), nil
}
