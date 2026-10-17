//go:build darwin || linux

package backuprecovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

const localFixtureWorkspace = "invented-backup-workspace"
const localFixtureInstall = `[{"id":"backup-recovery","version":1,"installed_at":"2026-01-01T00:00:00Z"}]`

type localFixtureSecrets struct {
	*vault.MemorySecretStore
	gets atomic.Int32
}

func (s *localFixtureSecrets) Get(key vault.SecretKey) (string, error) {
	s.gets.Add(1)
	return s.MemorySecretStore.Get(key)
}

func newLocalStoreFixture(t *testing.T) (*localStore, *localFixtureSecrets) {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	addLocalWorkspace(t, db, localFixtureWorkspace)
	secrets := &localFixtureSecrets{MemorySecretStore: vault.NewMemorySecretStore()}
	store, err := newLocalStore(db, secrets, root, &resetstate.WorkGate{})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return localFixtureTime }
	return store, secrets
}

func addLocalWorkspace(t *testing.T, db *database.DB, id string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at,installed_capabilities_json) VALUES (?,'Invented',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?);
	 INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES (?,'native','ordinary',CURRENT_TIMESTAMP)`, id, localFixtureInstall, id)
	if err != nil {
		t.Fatal(err)
	}
}

func seedLocalConfiguration(t *testing.T, s *localStore) localRecord {
	t.Helper()
	empty, err := s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.update(t.Context(), "local", localFixtureWorkspace, empty.expected(), changeUser, func(r *localRecord) error {
		r.state.Configuration = inventedLocalConfiguration()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// These are invented persistence facts, NOT provider/product acceptance.
func appendLocalAttempt(r *localRecord, outcome string, at time.Time) {
	attempt := localAttempt{r.state.Generation, r.state.Sequence, at.Add(-time.Second), at, outcome}
	r.state.Attempts = append([]localAttempt{attempt}, r.state.Attempts...)
	if len(r.state.Attempts) > maxAttemptHistory {
		r.state.Attempts = r.state.Attempts[:maxAttemptHistory]
	}
	if outcome == "read_ok" {
		r.state.Observation = &localObservation{Generation: r.state.Generation, Sequence: r.state.Sequence, ObservedAt: at, Snapshots: []localSnapshot{}}
	}
}

func activateLocalFixture(t *testing.T, s *localStore, record localRecord) localRecord {
	t.Helper()
	result, err := s.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeActivation, func(r *localRecord) error {
		appendLocalAttempt(r, "read_ok", localFixtureTime)
		r.activate(localFixtureTime)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func localCiphertexts(t *testing.T, s *localStore) ([]byte, []byte) {
	t.Helper()
	var state, receipt []byte
	if err := s.db.QueryRowContext(t.Context(), `SELECT ciphertext,(SELECT ciphertext FROM backup_recovery_receipts WHERE workspace_id=?) FROM backup_recovery_local WHERE workspace_id=?`, localFixtureWorkspace, localFixtureWorkspace).Scan(&state, &receipt); err != nil {
		t.Fatal(err)
	}
	return state, receipt
}

func TestLocalBackupStoreCreationIsInertAndEncrypted(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	empty, err := s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || empty.state != nil || empty.receipt != nil || secrets.gets.Load() != 0 {
		t.Fatal("empty read minted state or touched a key", err)
	}
	record := seedLocalConfiguration(t, s)
	if record.receipt != nil || record.state.Revision != 1 || record.state.Sequence != 0 || record.state.Observation != nil {
		t.Fatal("configuration invented authority/evidence")
	}
	state, receipt := localCiphertexts(t, s)
	for _, private := range []string{"/Invented/Project", "invented-vault", "invented-record", strings.Repeat("a", 64), localUUID(4)} {
		if bytes.Contains(state, []byte(private)) {
			t.Fatal("plaintext private state")
		}
	}
	if len(receipt) != 0 {
		t.Fatal("configuration created a receipt")
	}
	loaded, err := s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || localDigest(loaded.state) != localDigest(record.state) {
		t.Fatal("state did not round trip", err)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("owning record escaped as a public DTO")
	}
}

func TestLocalBackupStoreActivationRestartAndObservationCounters(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	active := activateLocalFixture(t, s, seedLocalConfiguration(t, s))
	if active.receipt == nil || active.receipt.MonitoringDigest != "" {
		t.Fatal("activation missing or auto-monitored")
	}
	restarted, err := newLocalStore(s.db, secrets, s.root.path, s.work)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := restarted.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || reloaded.receipt == nil || reloaded.receipt.GrantID != active.receipt.GrantID {
		t.Fatal("same installation lost local consent", err)
	}
	failed, err := s.update(t.Context(), "local", localFixtureWorkspace, active.expected(), changeObservation, func(r *localRecord) error {
		appendLocalAttempt(r, "destination_absent", localFixtureTime.Add(time.Hour))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.state.Revision != active.state.Revision || failed.state.Sequence != active.state.Sequence+1 || localDigest(failed.state.Observation) != localDigest(active.state.Observation) {
		t.Fatal("failed check changed user revision or refreshed successful evidence")
	}
	before, _ := localCiphertexts(t, s)
	_, err = s.update(t.Context(), "local", localFixtureWorkspace, failed.expected(), changeObservation, func(r *localRecord) error {
		appendLocalAttempt(r, "read_unavailable", localFixtureTime.Add(2*time.Hour))
		r.state.Observation.ObservedAt = localFixtureTime.Add(2 * time.Hour)
		return nil
	})
	after, _ := localCiphertexts(t, s)
	if err == nil || !bytes.Equal(before, after) {
		t.Fatal("failed read overwrote successful observation")
	}
}

func TestLocalBackupStoreRevisionConflictsAreAtomicAcrossInstances(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	record := seedLocalConfiguration(t, s)
	other, err := newLocalStore(s.db, secrets, s.root.path, s.work)
	if err != nil {
		t.Fatal(err)
	}
	var winners, conflicts atomic.Int32
	var group sync.WaitGroup
	for i := range 8 {
		group.Go(func() {
			store := s
			if i%2 == 1 {
				store = other
			}
			_, err := store.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeUser, func(r *localRecord) error { r.state.Monitoring.Policy.ExpectedAgeSeconds += 3600; return nil })
			switch err {
			case nil:
				winners.Add(1)
			case errLocalConflict:
				conflicts.Add(1)
			default:
				t.Error(err)
			}
		})
	}
	group.Wait()
	if winners.Load() != 1 || conflicts.Load() != 7 {
		t.Fatal("stale writers were not fenced", winners.Load(), conflicts.Load())
	}
}

func TestLocalBackupStoreScopeAndInstallChecksPrecedeDecryption(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	seedLocalConfiguration(t, s)
	for _, owner := range []string{"Local", "other", "../local"} {
		secrets.gets.Store(0)
		if _, err := s.load(t.Context(), owner, localFixtureWorkspace); err != errLocalScope || secrets.gets.Load() != 0 {
			t.Fatal("foreign owner reached decryption")
		}
	}
	for _, installs := range []string{`[]`, `[{"id":"backup-recovery","version":2,"installed_at":"2026-01-01T00:00:00Z"}]`, `[{"id":"backup-recovery","version":1}]`, `[{"id":"backup-recovery","version":1,"installed_at":"2026-01-01T00:00:00Z","Owner":{}}]`} {
		if _, err := s.db.ExecContext(t.Context(), `UPDATE workspaces SET installed_capabilities_json=?`, installs); err != nil {
			t.Fatal(err)
		}
		secrets.gets.Store(0)
		if _, err := s.load(t.Context(), "local", localFixtureWorkspace); err != errLocalScope || secrets.gets.Load() != 0 {
			t.Fatal("unsupported/ambiguous install reached decryption", err)
		}
	}
}

func TestLocalBackupStoreForeignAndCorruptStateIsPreserved(t *testing.T) {
	for name, statement := range map[string]string{
		"future schema":      `UPDATE backup_recovery_local SET schema_version=2`,
		"foreign owner":      `UPDATE backup_recovery_local SET owner_user_id='other'`,
		"revision tampered":  `UPDATE backup_recovery_local SET revision=revision+1`,
		"sequence tampered":  `UPDATE backup_recovery_local SET sequence=sequence+1`,
		"ciphertext corrupt": `UPDATE backup_recovery_local SET ciphertext=zeroblob(28)`,
		"receipt corrupt":    `UPDATE backup_recovery_receipts SET ciphertext=zeroblob(28)`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newLocalStoreFixture(t)
			record := activateLocalFixture(t, s, seedLocalConfiguration(t, s))
			if _, err := s.db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			before, beforeReceipt := localCiphertexts(t, s)
			if _, err := s.load(t.Context(), "local", localFixtureWorkspace); err != errLocalState {
				t.Fatal("unsafe state accepted", err)
			}
			if _, err := s.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeUser, func(*localRecord) error { return nil }); err == nil {
				t.Fatal("unsafe state repaired")
			}
			after, afterReceipt := localCiphertexts(t, s)
			if !bytes.Equal(before, after) || !bytes.Equal(beforeReceipt, afterReceipt) {
				t.Fatal("unreadable state was destroyed")
			}
		})
	}
}

func TestLocalBackupStoreCopiedRootCannotReuseReceipt(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	activateLocalFixture(t, s, seedLocalConfiguration(t, s))
	before, beforeReceipt := localCiphertexts(t, s)
	copied, err := newLocalStore(s.db, secrets, t.TempDir(), &resetstate.WorkGate{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copied.load(t.Context(), "local", localFixtureWorkspace); err != errLocalState {
		t.Fatal("copied root reused authority", err)
	}
	after, afterReceipt := localCiphertexts(t, s)
	if !bytes.Equal(before, after) || !bytes.Equal(beforeReceipt, afterReceipt) {
		t.Fatal("foreign receipt was silently repaired")
	}
	// A same-named replacement of the original data directory is also inert.
	if err := os.Rename(s.root.path, s.root.path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.root.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.load(t.Context(), "local", localFixtureWorkspace); err != errLocalState {
		t.Fatal("replaced root accepted")
	}
}

func TestLocalBackupStoreWorkspaceCopyAndMissingKeyStayInert(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	seedLocalConfiguration(t, s)
	addLocalWorkspace(t, s.db, "copied-workspace")
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO backup_recovery_local SELECT 'copied-workspace',owner_user_id,schema_version,generation,revision,sequence,authority_epoch,ciphertext,updated_at FROM backup_recovery_local WHERE workspace_id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.load(t.Context(), "local", "copied-workspace"); err != errLocalState {
		t.Fatal("workspace copy decrypted foreign state")
	}
	if err := secrets.Delete(vault.SecretKeyWorkspaceConfigDEK); err != nil {
		t.Fatal(err)
	}
	if _, err := s.load(t.Context(), "local", localFixtureWorkspace); err != errLocalState {
		t.Fatal("missing key accepted")
	}
	addLocalWorkspace(t, s.db, "new-workspace")
	empty, err := s.load(t.Context(), "local", "new-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.update(t.Context(), "local", "new-workspace", empty.expected(), changeUser, func(r *localRecord) error { r.state.Configuration = inventedLocalConfiguration(); return nil }); err != errLocalState {
		t.Fatal("missing shared key repaired", err)
	}
	if _, err := secrets.MemorySecretStore.Get(vault.SecretKeyWorkspaceConfigDEK); err != vault.ErrSecretNotFound {
		t.Fatal("missing key recreated over encrypted owners")
	}
}

func TestLocalBackupStoreLifecycleEpochFencesEvenInitialReadABA(t *testing.T) {
	cases := map[string]string{
		"archive return":      `UPDATE workspaces SET status='archived'; UPDATE workspaces SET status='active'`,
		"uninstall reinstall": `UPDATE workspaces SET installed_capabilities_json='[]'; UPDATE workspaces SET installed_capabilities_json='` + localFixtureInstall + `'`,
		"detach return":       `UPDATE continuity_attachments SET state='detached'; UPDATE continuity_attachments SET state='native'`,
		"ownership return":    `INSERT INTO users(id,created_at,updated_at) VALUES ('other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP); UPDATE workspaces SET owner_user_id='other'; UPDATE workspaces SET owner_user_id='local'`,
	}
	for name, statement := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := newLocalStoreFixture(t)
			pending := seedLocalConfiguration(t, s) // no durable receipt exists yet
			if _, err := s.db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			called := false
			_, err := s.update(t.Context(), "local", localFixtureWorkspace, pending.expected(), changeActivation, func(r *localRecord) error {
				called = true
				appendLocalAttempt(r, "read_ok", localFixtureTime)
				r.activate(localFixtureTime)
				return nil
			})
			if err != errLocalConflict || called {
				t.Fatal("obsolete initial-read permit survived ABA", err)
			}
			current, err := s.load(t.Context(), "local", localFixtureWorkspace)
			if err != nil || current.receipt != nil || current.state.Observation != nil || current.authority.Epoch == pending.authority.Epoch {
				t.Fatal("fence lost state or restored authority", err)
			}
		})
	}
}

func TestLocalBackupStoreReplacementFencesLateResultsAndRetainsHistory(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	active := activateLocalFixture(t, s, seedLocalConfiguration(t, s))
	replaced, err := s.update(t.Context(), "local", localFixtureWorkspace, active.expected(), changeUser, func(r *localRecord) error {
		r.state.Generation = uuid.NewString()
		r.state.Configuration.Sources[0].Path = "/Invented/OtherProject"
		return nil
	})
	if err != nil || replaced.receipt != nil || localDigest(replaced.state.Observation) != localDigest(active.state.Observation) {
		t.Fatal("replacement inherited authority or erased history", err)
	}
	if _, err := s.update(t.Context(), "local", localFixtureWorkspace, active.expected(), changeObservation, func(r *localRecord) error {
		appendLocalAttempt(r, "read_ok", localFixtureTime.Add(time.Hour))
		return nil
	}); err != errLocalConflict {
		t.Fatal("late observation survived replacement")
	}
}

func TestLocalBackupStoreUserAndJournalCannotInventEvidenceOrConsent(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	record := seedLocalConfiguration(t, s)
	for _, kind := range []localChange{changeUser, changeJournal} {
		if _, err := s.update(t.Context(), "local", localFixtureWorkspace, record.expected(), kind, func(r *localRecord) error { r.activate(localFixtureTime); return nil }); err == nil {
			t.Fatal("non-validation write created authority")
		}
		if _, err := s.update(t.Context(), "local", localFixtureWorkspace, record.expected(), kind, func(r *localRecord) error {
			r.state.Observation = &localObservation{Generation: r.state.Generation, Sequence: 1, ObservedAt: localFixtureTime, Snapshots: []localSnapshot{}}
			return nil
		}); err == nil {
			t.Fatal("non-provider write invented evidence")
		}
	}
	active := activateLocalFixture(t, s, record)
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM backup_recovery_receipts`); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || loaded.receipt != nil || loaded.state.Generation != active.state.Generation {
		t.Fatal("missing receipt was repaired")
	}
}

func TestLocalBackupStoreResetAndCancellationFenceWrites(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	record := seedLocalConfiguration(t, s)
	before, _ := localCiphertexts(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.update(ctx, "local", localFixtureWorkspace, record.expected(), changeUser, func(*localRecord) error { return nil }); err == nil {
		t.Fatal("cancelled mutation saved")
	}
	if err := s.work.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeUser, func(*localRecord) error { return nil }); err == nil {
		t.Fatal("reset fence admitted a writer")
	}
	if _, err := newLocalStore(s.db, s.secrets, s.root.path, nil); err != errLocalState {
		t.Fatal("untracked store constructed")
	}
	after, _ := localCiphertexts(t, s)
	if !bytes.Equal(before, after) {
		t.Fatal("fenced/cancelled update changed state")
	}
	if snapshot := s.work.Snapshot(); snapshot.Active != 0 || !snapshot.Fenced {
		t.Fatal("work permit leaked")
	}
}
