package backuprecovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// This store has no public mutation API or provider dependency. The eventual
// domain service owns reviewed-connect permits, cancellation and lifecycle drain.
// A receipt is created only by an explicit activation transaction, never on load.
type localStore struct {
	db      *database.DB
	secrets vault.SecretStore
	root    localRoot
	work    *resetstate.WorkGate
	clock   func() time.Time
}

type localAuthority struct {
	Owner      string `json:"owner"`
	Workspace  string `json:"workspace"`
	Install    string `json:"install"`
	Attachment string `json:"attachment"`
	Root       string `json:"root"`
	Epoch      uint64 `json:"epoch"`
}

type localReceipt struct {
	Version             int            `json:"version"`
	Authority           localAuthority `json:"authority"`
	Generation          string         `json:"generation"`
	GrantID             string         `json:"grant_id"`
	ConfigurationDigest string         `json:"configuration_digest"`
	MonitoringDigest    string         `json:"monitoring_digest,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
}

type localRecord struct {
	state     *localState
	receipt   *localReceipt
	authority localAuthority
	automatic bool // current continuity admission, never serialized authority
}

type localRevision struct {
	generation string
	revision   uint64
	authority  localAuthority
}

func (r localRecord) expected() localRevision {
	result := localRevision{authority: r.authority}
	if r.state != nil {
		result.generation = r.state.Generation
		result.revision = r.state.Revision
	}
	return result
}

type localChange uint8

const (
	changeUser        localChange = iota // settings/reports/revoke/reconfiguration
	changeObservation                    // sequence, not user revision
	changeActivation                     // first successful read under a service-owned one-read permit
	changeJournal                        // durable pending/idempotency outcome; no implicit read
	changeAdmission                      // reserve a monotonic read sequence, without claiming an observation
)

func newLocalStore(db *database.DB, secrets vault.SecretStore, root string, work *resetstate.WorkGate) (*localStore, error) {
	if db == nil || db.DB == nil || work == nil || work.Snapshot().Fenced {
		return nil, errLocalState
	}
	identity, err := newLocalRoot(root)
	if err != nil {
		return nil, errLocalState
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var fences int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN (
	 'backup_recovery_workspace_fence','backup_recovery_install_fence','backup_recovery_attachment_fence',
	 'backup_recovery_attachment_delete','backup_recovery_provisioning_insert','backup_recovery_provisioning_update') AND EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='backup_recovery_authority')`).Scan(&fences); err != nil || fences != 6 {
		return nil, errLocalState
	}
	return &localStore{db: db, secrets: secrets, root: identity, work: work, clock: time.Now}, nil
}

func (s *localStore) load(ctx context.Context, owner, workspace string) (localRecord, error) {
	var record localRecord
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		record, err = s.read(ctx, tx, owner, workspace)
		return err
	})
	if err != nil {
		return localRecord{}, err
	}
	return record, nil
}

// update serializes read/compare/change/encrypt/receipt publication in one SQL
// transaction. change must be a short, pure in-process mutation: no provider,
// filesystem, secret lookup or nested DB calls. It receives a detached value.
// All external work and the single-use permit remain the service's responsibility.
func (s *localStore) update(ctx context.Context, owner, workspace string, expected localRevision, kind localChange, change func(*localRecord) error) (localRecord, error) {
	var record localRecord
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		record, err = s.read(ctx, tx, owner, workspace)
		if err != nil {
			return err
		}
		if expected.authority != record.authority {
			return errLocalConflict
		}
		existed := record.state != nil
		if existed {
			if record.state.Generation != expected.generation || record.state.Revision != expected.revision {
				return errLocalConflict
			}
		} else {
			if kind != changeUser || expected.generation != "" || expected.revision != 0 {
				return errLocalConflict
			}
			record.state = &localState{Version: localSchemaVersion, Generation: uuid.NewString(), Monitoring: localMonitoring{Intent: "manual_only", Policy: proposedMonitoringPolicy()}}
		}
		before, err := json.Marshal(record.state)
		if err != nil || change == nil {
			return errLocalState
		}
		defer clear(before)
		old, err := decodeLocalJSON[localState](before, maxPrivateStateBytes)
		if err != nil {
			return err
		}
		oldConfiguration := localDigest(old.Configuration)
		oldIntent := configurationIntentDigest(old.Configuration)
		oldUserFields := userFieldsDigest(old)
		oldReceiptDigest := localDigest(record.receipt)
		oldGrant := ""
		if record.receipt != nil {
			oldGrant = record.receipt.GrantID
		}
		authority, automatic := record.authority, record.automatic
		switch kind {
		case changeUser:
			record.state.Revision++
		case changeObservation, changeActivation, changeAdmission:
			record.state.Sequence++
		case changeJournal:
		default:
			return errLocalState
		}
		if err := change(&record); err != nil {
			return err
		}
		if record.state == nil || record.authority != authority || record.automatic != automatic || !record.state.valid() {
			return errLocalState
		}
		current := record.state
		wantRevision, wantSequence := old.Revision, old.Sequence
		if kind == changeUser {
			wantRevision++
		}
		if kind == changeObservation || kind == changeActivation || kind == changeAdmission {
			wantSequence++
		}
		if current.Revision != wantRevision || current.Sequence != wantSequence {
			return errLocalState
		}
		if kind != changeUser && current.Generation != old.Generation {
			return errLocalState
		}
		if kind == changeUser && existed && oldConfiguration != localDigest(current.Configuration) && current.Generation == old.Generation {
			return errLocalState
		}
		if kind != changeUser && kind != changeActivation && oldUserFields != userFieldsDigest(*current) {
			return errLocalState
		}
		if kind == changeActivation {
			if oldGrant != "" || oldIntent != configurationIntentDigest(current.Configuration) || !compatibleValidationEvidence(old.Configuration, current.Configuration) ||
				monitoringDigest(old.Monitoring) != monitoringDigest(current.Monitoring) || localDigest(old.Reports) != localDigest(current.Reports) || current.Configuration == nil || !current.Configuration.valid(true) ||
				current.Observation == nil || current.Observation.Generation != current.Generation || current.Observation.Sequence != current.Sequence {
				return errLocalState
			}
		}
		if (kind == changeUser || kind == changeJournal || kind == changeAdmission) && (localDigest(old.Observation) != localDigest(current.Observation) || localDigest(old.Attempts) != localDigest(current.Attempts)) {
			return errLocalState
		}
		if (kind == changeObservation || kind == changeActivation) && !validObservationTransition(old, *current) {
			return errLocalState
		}
		if current.Generation != old.Generation {
			record.receipt = nil
		} // replacement/revoke cannot inherit access
		if record.receipt != nil {
			if (kind == changeObservation || kind == changeJournal || kind == changeAdmission) && oldReceiptDigest != localDigest(record.receipt) {
				return errLocalState
			}
			if kind == changeActivation && record.receipt.MonitoringDigest != "" {
				return errLocalState
			}
			if oldGrant != record.receipt.GrantID && kind != changeActivation {
				return errLocalState
			}
			if !validLocalReceipt(record) {
				return errLocalState
			}
		}
		return s.write(ctx, tx, record, existed)
	})
	if err != nil {
		return localRecord{}, err
	}
	return record, nil
}

func (s *localStore) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	if s == nil || s.work == nil || !s.root.check() {
		return errLocalState
	}
	release, err := s.work.Enter()
	if err != nil {
		return errLocalState
	}
	defer release()
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if !s.root.check() {
			return errLocalState
		}
		return ctx.Err()
	})
	// Even a wrapped sentinel can carry a private path in its outer message.
	// Return canonical categories, never callback/SQL/key/backend diagnostics.
	if errors.Is(err, errLocalScope) {
		return errLocalScope
	}
	if errors.Is(err, errLocalConflict) {
		return errLocalConflict
	}
	if err != nil {
		return errLocalState
	}
	return nil
}

func (s *localStore) read(ctx context.Context, tx *sql.Tx, owner, workspace string) (localRecord, error) {
	authority, automatic, err := readLocalAuthority(ctx, tx, owner, workspace, s.root.fingerprint)
	if err != nil {
		return localRecord{}, err
	}
	result := localRecord{authority: authority, automatic: automatic}
	var rowOwner, generation string
	var version int
	var revision, sequence uint64
	var ciphertext []byte
	err = tx.QueryRowContext(ctx, `SELECT owner_user_id,schema_version,generation,revision,sequence,
	 CASE WHEN length(ciphertext)<=2097180 THEN ciphertext END FROM backup_recovery_local WHERE workspace_id=?`, workspace).Scan(&rowOwner, &version, &generation, &revision, &sequence, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil || rowOwner != owner || version != localSchemaVersion || !validStorageUUID(generation) || revision == 0 || revision > maxSafeRevision || sequence > maxSafeRevision || len(ciphertext) < 28 {
		return localRecord{}, errLocalState
	}
	aead, err := vault.WorkspaceConfigCipher(s.secrets, false)
	if err != nil {
		return localRecord{}, errLocalState
	}
	plain, err := aead.Open(nil, nil, ciphertext, localAAD("state", owner, workspace, generation, version, ""))
	if err != nil {
		return localRecord{}, errLocalState
	}
	defer clear(plain)
	state, err := decodeLocalJSON[localState](plain, maxPrivateStateBytes)
	if err != nil || !state.valid() || state.Generation != generation || state.Version != version || state.Revision != revision || state.Sequence != sequence {
		return localRecord{}, errLocalState
	}
	result.state = &state
	var grant, receiptOwner, receiptGeneration string
	err = tx.QueryRowContext(ctx, `SELECT owner_user_id,generation,grant_id,
	 CASE WHEN length(ciphertext)<=16412 THEN ciphertext END FROM backup_recovery_receipts WHERE workspace_id=?`, workspace).Scan(&receiptOwner, &receiptGeneration, &grant, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil || receiptOwner != owner || receiptGeneration != generation || !validStorageUUID(grant) || len(ciphertext) < 28 {
		return localRecord{}, errLocalState
	}
	plainReceipt, err := aead.Open(nil, nil, ciphertext, localAAD("receipt", owner, workspace, generation, version, grant))
	if err != nil {
		return localRecord{}, errLocalState
	}
	defer clear(plainReceipt)
	receipt, err := decodeLocalJSON[localReceipt](plainReceipt, maxReceiptBytes)
	if err != nil || receipt.GrantID != grant {
		return localRecord{}, errLocalState
	}
	result.receipt = &receipt
	if !validLocalReceipt(result) {
		return localRecord{}, errLocalState
	}
	return result, nil
}

func (s *localStore) write(ctx context.Context, tx *sql.Tx, record localRecord, existed bool) error {
	plain, err := json.Marshal(record.state)
	if err != nil || len(plain) > maxPrivateStateBytes {
		return errLocalState
	}
	defer clear(plain)
	if _, err := decodeMetadata(plain, nil, 0, maxPrivateStateBytes); err != nil {
		return errLocalState
	}
	// Missing keys must not be repaired while ANY existing owner has encrypted
	// configuration under this shared key, even when this workspace is new.
	var encryptedRecords bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_local_config) OR EXISTS(SELECT 1 FROM backup_recovery_local) OR EXISTS(SELECT 1 FROM backup_recovery_receipts)`).Scan(&encryptedRecords); err != nil {
		return errLocalState
	}
	aead, err := vault.WorkspaceConfigCipher(s.secrets, !encryptedRecords && !existed)
	if err != nil {
		return errLocalState
	}
	state := record.state
	a := record.authority
	// #nosec G407 -- WorkspaceConfigCipher uses NewGCMWithRandomNonce: nil is required; Seal generates and prepends a fresh random nonce.
	ciphertext := aead.Seal(nil, nil, plain, localAAD("state", a.Owner, a.Workspace, state.Generation, state.Version, ""))
	_, err = tx.ExecContext(ctx, `INSERT INTO backup_recovery_local(workspace_id,owner_user_id,schema_version,generation,revision,sequence,authority_epoch,ciphertext,updated_at)
	 VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET owner_user_id=excluded.owner_user_id,schema_version=excluded.schema_version,
	 generation=excluded.generation,revision=excluded.revision,sequence=excluded.sequence,authority_epoch=excluded.authority_epoch,ciphertext=excluded.ciphertext,updated_at=excluded.updated_at`,
		a.Workspace, a.Owner, state.Version, state.Generation, state.Revision, state.Sequence, a.Epoch, ciphertext, s.clock().UTC())
	if err != nil {
		return errLocalState
	}
	if record.receipt == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM backup_recovery_receipts WHERE workspace_id=?`, a.Workspace)
		return err
	}
	encoded, err := json.Marshal(record.receipt)
	if err != nil || len(encoded) > maxReceiptBytes {
		return errLocalState
	}
	defer clear(encoded)
	// #nosec G407 -- NewGCMWithRandomNonce generates a distinct random nonce for this receipt; its nonce argument must be nil.
	sealed := aead.Seal(nil, nil, encoded, localAAD("receipt", a.Owner, a.Workspace, state.Generation, state.Version, record.receipt.GrantID))
	_, err = tx.ExecContext(ctx, `INSERT INTO backup_recovery_receipts VALUES (?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET owner_user_id=excluded.owner_user_id,
	 generation=excluded.generation,grant_id=excluded.grant_id,ciphertext=excluded.ciphertext`, a.Workspace, a.Owner, state.Generation, record.receipt.GrantID, sealed)
	return err
}

func validLocalReceipt(record localRecord) bool {
	r, s := record.receipt, record.state
	return r != nil && s != nil && r.Version == localSchemaVersion && r.Authority == record.authority && r.Generation == s.Generation && validStorageUUID(r.GrantID) &&
		localTime(r.CreatedAt) && s.Configuration != nil && s.Configuration.valid(true) && r.ConfigurationDigest == localDigest(s.Configuration) &&
		(r.MonitoringDigest == "" || r.MonitoringDigest == monitoringDigest(s.Monitoring))
}

// Called only after the domain service has consumed its reviewed-connect permit
// and validated the real observation. It is not itself an admission mechanism.
func (r *localRecord) activate(at time.Time) {
	r.receipt = &localReceipt{Version: localSchemaVersion, Authority: r.authority, Generation: r.state.Generation, GrantID: uuid.NewString(), ConfigurationDigest: localDigest(r.state.Configuration), CreatedAt: at.UTC()}
}

func localAAD(kind, owner, workspace, generation string, version int, grant string) []byte {
	encoded, _ := json.Marshal([]any{"ori.backup-recovery.local.v1", kind, owner, workspace, generation, version, grant})
	return encoded
}
func localDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func monitoringDigest(m localMonitoring) string {
	return localDigest([]any{m.Intent, m.Revision, m.Policy})
}
func userFieldsDigest(s localState) string {
	return localDigest([]any{s.Configuration, monitoringDigest(s.Monitoring), s.Reports})
}

func validObservationTransition(before, after localState) bool {
	retained := len(before.Attempts)
	if retained >= maxAttemptHistory {
		retained = maxAttemptHistory - 1
	}
	if len(after.Attempts) != retained+1 || after.Attempts[0].Sequence != after.Sequence || after.Attempts[0].Generation != after.Generation {
		return false
	}
	for i := range retained {
		if localDigest(before.Attempts[i]) != localDigest(after.Attempts[i+1]) {
			return false
		}
	}
	if after.Attempts[0].Outcome != "read_ok" {
		return localDigest(before.Observation) == localDigest(after.Observation)
	}
	return after.Observation != nil && after.Observation.Generation == after.Generation && after.Observation.Sequence == after.Sequence && after.Observation.ObservedAt.Equal(after.Attempts[0].FinishedAt)
}

func compatibleValidationEvidence(before, after *localConfiguration) bool {
	if before == nil || after == nil || len(before.Sources) != len(after.Sources) {
		return false
	}
	if before.RepositoryID != "" && before.RepositoryID != after.RepositoryID || before.RepositoryFileID != 0 && before.RepositoryFileID != after.RepositoryFileID || before.Destination != nil && localDigest(before.Destination) != localDigest(after.Destination) {
		return false
	}
	for i, source := range before.Sources {
		actual := after.Sources[i]
		if source.DirectoryFileID != 0 && source.DirectoryFileID != actual.DirectoryFileID || source.Storage != nil && localDigest(source.Storage) != localDigest(actual.Storage) {
			return false
		}
	}
	return true
}

func configurationIntentDigest(c *localConfiguration) string {
	if c == nil {
		return localDigest(nil)
	}
	paths := make([]string, len(c.Sources))
	for i, source := range c.Sources {
		paths[i] = source.Path
	}
	return localDigest([]any{c.Provider, paths, c.VolumePath, c.RepositoryPath, c.Credential})
}

// Resolve authority from the current canonical SQL view, never a display name,
// imported enabled flag or caller-supplied install/attachment fingerprint.
func readLocalAuthority(ctx context.Context, tx *sql.Tx, owner, workspace, root string) (localAuthority, bool, error) {
	if !localID(owner) || !localID(workspace) {
		return localAuthority{}, false, errLocalScope
	}
	var installs, state, operation, sourceGeneration, sourceDigest string
	var epoch uint64
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(w.installed_capabilities_json AS BLOB))<=131072 THEN w.installed_capabilities_json ELSE '' END,
	 COALESCE(a.state,'legacy_native'),COALESCE(a.operation_id,''),COALESCE(a.source_generation,''),COALESCE(a.source_digest,''),
	 COALESCE((SELECT epoch FROM backup_recovery_authority b WHERE b.workspace_id=w.id),0)
	 FROM workspaces w LEFT JOIN continuity_attachments a ON a.workspace_id=w.id
	 WHERE w.id=? AND w.owner_user_id=? AND w.owner_user_id='local' AND w.kind='workspace' AND w.status='active' AND w.deleted_at IS NULL
	 AND (a.workspace_id IS NULL OR a.state IN ('native','imported_inactive','imported_active'))
	 AND NOT EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=w.id AND m.target='native-create')`, workspace, owner).Scan(&installs, &state, &operation, &sourceGeneration, &sourceDigest, &epoch)
	if err != nil || epoch > maxSafeRevision {
		return localAuthority{}, false, errLocalScope
	}
	if state == "native" || state == "legacy_native" {
		if operation != "" || sourceGeneration != "" || sourceDigest != "" {
			return localAuthority{}, false, errLocalScope
		}
	} else {
		if !localID(operation) || !localID(sourceGeneration) || !metadataID(sourceDigest) {
			return localAuthority{}, false, errLocalScope
		}
		var complete bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM continuity_operations WHERE id=? AND owner_user_id=? AND status='complete')`, operation, owner).Scan(&complete); err != nil || !complete {
			return localAuthority{}, false, errLocalScope
		}
	}
	value, err := decodeMetadata([]byte(installs), nil, 0, maxSettingsBytes)
	if err != nil {
		return localAuthority{}, false, errLocalScope
	}
	rows, ok := value.([]any)
	if !ok || len(rows) > 128 {
		return localAuthority{}, false, errLocalScope
	}
	install := ""
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return localAuthority{}, false, errLocalScope
		}
		for key := range row {
			for _, exact := range []string{"id", "version", "installed_at", "owner", "removed_at"} {
				if strings.EqualFold(key, exact) && key != exact {
					return localAuthority{}, false, errLocalScope
				}
			}
		}
		id, ok := row["id"].(string)
		if !ok {
			return localAuthority{}, false, errLocalScope
		}
		if strings.ToLower(strings.TrimSpace(id)) != "backup-recovery" {
			continue
		}
		version, err := metadataCount(row["version"])
		at, timeErr := metadataTime(row["installed_at"])
		if install != "" || err != nil || version == nil || *version != 1 || timeErr != nil || at == nil || row["owner"] != nil || row["removed_at"] != nil {
			return localAuthority{}, false, errLocalScope
		}
		install = localDigest([]any{"backup-recovery", *version, at})
	}
	if install == "" {
		return localAuthority{}, false, errLocalScope
	}
	// Ordinary non-private creation predates continuity attachments (the same
	// policy as continuity.CheckExecution). Its absent-row origin is distinct
	// from an explicit native attachment; neither is permission to read. The
	// existing owner/install/root/epoch and explicit activation still apply.
	origin := "imported"
	if state == "legacy_native" {
		origin = "legacy_native"
	}
	if state == "native" {
		origin = "native"
	}
	return localAuthority{owner, workspace, install, localDigest([]string{origin, operation, sourceGeneration, sourceDigest}), root, epoch}, state != "imported_inactive", nil
}
