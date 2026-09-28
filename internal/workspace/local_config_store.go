package workspace

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

const (
	maxLocalConfigBytes = 1 << 20
	// Live canonical persistence is independent of the 8 MiB checkpoint-file
	// limit. A large native workspace stays editable while preparation reports
	// that limit; private configuration itself remains bounded to 1 MiB.
	maxNativeWorkspaceBytes = 256 << 20
)

var (
	ErrLocalConfigUnavailable = errors.New("workspace local configuration is unavailable")
	ErrLocalConfigInvalid     = errors.New("workspace local configuration is invalid")
	// The installation lease excludes other processes; this lock also prevents
	// two in-process owners from racing to create different encryption keys.
	localConfigKeyMu     sync.Mutex
	localConfigFileLocks LockTable
)

// LocalConfigStore owns encrypted, installation-only configuration. It is not
// an import receipt, authority source, or portable domain. A caller must hold
// its workspace/file mutation lock across Stage -> publish reference -> Prune.
// A portable reference without its exact local row grants nothing.
type LocalConfigStore struct {
	db      *database.DB
	secrets vault.SecretStore
	locks   *LockTable
	work    *resetstate.WorkGate
}

// NewLocalConfigStore is the untracked adapter constructor for isolated owners.
// Production composition must use NewLocalConfigStoreWithWorkGate.
func NewLocalConfigStore(db *database.DB, secrets vault.SecretStore) *LocalConfigStore {
	return &LocalConfigStore{db: db, secrets: secrets, locks: &localConfigFileLocks}
}

func NewLocalConfigStoreWithWorkGate(db *database.DB, secrets vault.SecretStore, gate *resetstate.WorkGate) (*LocalConfigStore, error) {
	if gate == nil {
		return nil, resetstate.ErrWorkUntracked
	}
	if db == nil {
		return nil, ErrLocalConfigUnavailable
	}
	result := NewLocalConfigStore(db, secrets)
	result.work = gate
	return result, nil
}

func (s *LocalConfigStore) enterWork() (func(), error) {
	if s == nil {
		return nil, ErrLocalConfigUnavailable
	}
	return s.work.Enter()
}

// authority binds local configuration to the current attachment, not a copied
// workspace ID. Reimport after deletion/reset gets a new operation and cannot
// reuse old local grants even if a portable file still names an old slot.
func (s *LocalConfigStore) authority(ctx context.Context, workspaceID string) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrLocalConfigUnavailable
	}
	a, err := workspacecontinuity.NewLocalStore(s.db).Attachment(ctx, workspaceID)
	// Explicit native provisioning may stage encrypted configuration before
	// runtime admission. This exception is persistence authority only; all
	// execution consumers must still use Attachment.AllowsManual/Automatic.
	canPersist := a.AllowsManual() || a.State == workspacecontinuity.Native && a.Provisioning
	if err != nil || !canPersist {
		return "", ErrLocalConfigUnavailable
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, workspaceID).Scan(&count); err != nil || count != 1 {
		return "", ErrLocalConfigUnavailable
	}
	if a.State == workspacecontinuity.Native {
		return "native", nil
	}
	if !workspacecontinuity.ValidID(a.OperationID) {
		return "", ErrLocalConfigInvalid
	}
	return "import:" + a.OperationID, nil
}

func validLocalAuthority(authority string) bool {
	if authority == "native" {
		return true
	}
	operation, imported := strings.CutPrefix(authority, "import:")
	return imported && workspacecontinuity.ValidID(operation)
}

func localConfigIdentity(workspaceID, kind, itemID, slotID, authority string) ([]byte, error) {
	if !workspacecontinuity.ValidID(workspaceID) || !workspacecontinuity.ValidID(itemID) ||
		(kind != "agent" && kind != "bindings") || !validLocalAuthority(authority) {
		return nil, ErrLocalConfigInvalid
	}
	parsed, err := uuid.Parse(slotID)
	if err != nil || parsed.String() != slotID {
		return nil, ErrLocalConfigInvalid
	}
	// Bind ciphertext to all identity fields. A copied SQL row under another
	// workspace/item/slot must not decrypt, even with the same installation key.
	data, err := json.Marshal([]string{"ori.workspace-local.v1", workspaceID, kind, itemID, slotID, authority})
	if err != nil {
		return nil, ErrLocalConfigInvalid
	}
	return data, nil
}

func (s *LocalConfigStore) aead(create bool) (cipher.AEAD, error) {
	if s == nil || s.db == nil || s.secrets == nil {
		return nil, ErrLocalConfigUnavailable
	}
	localConfigKeyMu.Lock()
	defer localConfigKeyMu.Unlock()
	encoded, err := s.secrets.Get(vault.SecretKeyWorkspaceConfigDEK)
	if errors.Is(err, vault.ErrSecretNotFound) && create {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, ErrLocalConfigUnavailable
		}
		encoded = base64.StdEncoding.EncodeToString(key)
		err = s.secrets.Set(vault.SecretKeyWorkspaceConfigDEK, encoded)
	}
	if err != nil {
		// Backend errors can contain command output or paths. Do not propagate
		// them into HTTP errors, preparation reports or telemetry.
		return nil, ErrLocalConfigUnavailable
	}
	if len(encoded) != base64.StdEncoding.EncodedLen(32) {
		return nil, ErrLocalConfigInvalid
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, ErrLocalConfigInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrLocalConfigInvalid
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrLocalConfigInvalid
	}
	return aead, nil
}

// Stage durably encrypts a new immutable local slot before the portable file is
// changed. An unavailable secure backend leaves the existing file/key untouched;
// there is deliberately no plaintext fallback or key generated from folder IDs.
func (s *LocalConfigStore) Stage(ctx context.Context, workspaceID, kind, itemID string, data []byte) (string, error) {
	release, err := s.enterWork()
	if err != nil {
		return "", err
	}
	defer release()
	if len(data) > maxLocalConfigBytes || !json.Valid(data) {
		return "", ErrLocalConfigInvalid
	}
	authority, err := s.authority(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	slot := uuid.NewString()
	aad, err := localConfigIdentity(workspaceID, kind, itemID, slot, authority)
	if err != nil {
		return "", err
	}
	aead, err := s.aead(true)
	if err != nil {
		return "", err
	}
	// #nosec G407 -- NewGCMWithRandomNonce generates/prepends a fresh nonce and requires a nil nonce argument.
	ciphertext := aead.Seal(nil, nil, data, aad)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Recheck admission in the same statement as insertion. Interrupted writes
	// are bounded to 32 slots per item until a successful publication prunes them.
	result, err := s.db.ExecContext(ctx, `INSERT INTO workspace_local_config(workspace_id,kind,item_id,authority_id,slot_id,ciphertext,created_at)
		SELECT ?,?,?,?,?,?,? FROM continuity_attachments a JOIN workspaces w ON w.id=a.workspace_id
		WHERE w.id=? AND w.owner_user_id='local' AND w.deleted_at IS NULL
		AND ((a.state='native' AND ?='native') OR (a.state IN ('imported_inactive','imported_active') AND 'import:' || a.operation_id=?))
		AND (SELECT COUNT(*) FROM workspace_local_config WHERE workspace_id=? AND kind=? AND item_id=?)<32`,
		workspaceID, kind, itemID, authority, slot, ciphertext, time.Now().UTC(), workspaceID, authority, authority, workspaceID, kind, itemID)
	if err != nil {
		return "", ErrLocalConfigUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return "", ErrLocalConfigUnavailable
	}
	return slot, nil
}

// Load is observational. Missing local state (for example after copying only a
// folder or resetting app records) is absent, not a reason to import a grant.
func (s *LocalConfigStore) Load(ctx context.Context, workspaceID, kind, itemID, slot string) ([]byte, bool, error) {
	release, err := s.enterWork()
	if err != nil {
		return nil, false, err
	}
	defer release()
	if s == nil || s.db == nil {
		return nil, false, ErrLocalConfigUnavailable
	}
	authority, err := s.authority(ctx, workspaceID)
	if err != nil {
		return nil, false, err
	}
	aad, err := localConfigIdentity(workspaceID, kind, itemID, slot, authority)
	if err != nil {
		return nil, false, err
	}
	var ciphertext []byte
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN length(ciphertext)<=? THEN ciphertext ELSE NULL END
		FROM workspace_local_config WHERE workspace_id=? AND kind=? AND item_id=? AND slot_id=? AND authority_id=?`,
		maxLocalConfigBytes+32, workspaceID, kind, itemID, slot, authority).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrLocalConfigUnavailable
	}
	if len(ciphertext) < 28 {
		return nil, false, ErrLocalConfigInvalid
	}
	aead, err := s.aead(false)
	if err != nil {
		return nil, false, err
	}
	data, err := aead.Open(nil, nil, ciphertext, aad)
	if err != nil || len(data) > maxLocalConfigBytes || !json.Valid(data) {
		return nil, false, ErrLocalConfigInvalid
	}
	if current, err := s.authority(ctx, workspaceID); err != nil || current != authority {
		return nil, false, ErrLocalConfigUnavailable
	}
	return data, true, nil
}

// pruneUnpublished is called only while holding the canonical file lock, after
// reading its current reference. It bounds retries even if 32 prior writes failed
// before publishing. It never removes the currently referenced slot; an empty
// reference means the legacy file still owns the complete inline configuration.
func (s *LocalConfigStore) pruneUnpublished(ctx context.Context, workspaceID, kind, itemID, current string) error {
	if current != "" {
		if _, err := localConfigIdentity(workspaceID, kind, itemID, current, "native"); err != nil {
			return err
		}
	}
	if _, err := s.authority(ctx, workspaceID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM workspace_local_config WHERE workspace_id=? AND kind=? AND item_id=? AND slot_id!=?`, workspaceID, kind, itemID, current)
	if err != nil {
		return ErrLocalConfigUnavailable
	}
	return nil
}

// Prune forgets superseded/unpublished local slots only after the caller has
// durably published the kept reference. It never cleans any workspace files.
func (s *LocalConfigStore) Prune(ctx context.Context, workspaceID, kind, itemID, keep string) error {
	release, err := s.enterWork()
	if err != nil {
		return err
	}
	defer release()
	if s == nil || s.db == nil {
		return ErrLocalConfigUnavailable
	}
	authority, err := s.authority(ctx, workspaceID)
	if err != nil {
		return err
	}
	if _, err := localConfigIdentity(workspaceID, kind, itemID, keep, authority); err != nil {
		return err
	}
	var present int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_local_config WHERE workspace_id=? AND kind=? AND item_id=? AND slot_id=? AND authority_id=?`, workspaceID, kind, itemID, keep, authority).Scan(&present); err != nil || present != 1 {
		return ErrLocalConfigInvalid
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM workspace_local_config WHERE workspace_id=? AND kind=? AND item_id=? AND slot_id!=?`, workspaceID, kind, itemID, keep)
	if err != nil {
		return ErrLocalConfigUnavailable
	}
	return nil
}
