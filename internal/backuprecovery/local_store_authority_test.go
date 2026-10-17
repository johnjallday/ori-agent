//go:build darwin || linux

package backuprecovery

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestLocalBackupStoreNeverReturnsWrappedPrivateDiagnostics(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	pending := seedLocalConfiguration(t, s)
	for _, category := range []error{errLocalScope, errLocalConflict, errLocalState} {
		_, err := s.update(t.Context(), "local", localFixtureWorkspace, pending.expected(), changeUser, func(*localRecord) error { return fmt.Errorf("invented private diagnostic: %w", category) })
		if err != category {
			t.Fatal("wrapped diagnostic escaped", err)
		}
	}
}

func TestLocalBackupStoreImportedIntentNeedsNewLocalAuthority(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	active := activateLocalFixture(t, s, seedLocalConfiguration(t, s))
	monitored, err := s.update(t.Context(), "local", localFixtureWorkspace, active.expected(), changeUser, func(r *localRecord) error {
		r.state.Monitoring.Intent = "active"
		r.state.Monitoring.Revision = 1
		r.receipt.MonitoringDigest = monitoringDigest(r.state.Monitoring)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO continuity_operations(id,tree_digest,destination_digest,owner_user_id,action,status,created_at,updated_at)
	 VALUES ('invented-import','invented-tree','invented-destination','local','continue','complete',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
	 UPDATE continuity_attachments SET state='imported_inactive',operation_id='invented-import',source_generation=?,source_digest=?`, localUUID(200), strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || imported.receipt != nil || imported.automatic || imported.state.Monitoring.Intent != "active" {
		t.Fatal("imported intent became authority or was silently reset", err)
	}
	if _, err := s.update(t.Context(), "local", localFixtureWorkspace, monitored.expected(), changeObservation, func(r *localRecord) error { appendLocalAttempt(r, "read_ok", localFixtureTime); return nil }); err != errLocalConflict {
		t.Fatal("old native result crossed import")
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state='imported_active'`); err != nil {
		t.Fatal(err)
	}
	imported, err = s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || imported.receipt != nil || !imported.automatic {
		t.Fatal("general workspace activation silently restored backup consent", err)
	}
}

func TestLocalBackupStoreRequiresAllSchemaFences(t *testing.T) {
	s, secrets := newLocalStoreFixture(t)
	if _, err := s.db.ExecContext(t.Context(), `DROP TRIGGER backup_recovery_install_fence`); err != nil {
		t.Fatal(err)
	}
	secrets.gets.Store(0)
	if _, err := newLocalStore(s.db, secrets, s.root.path, s.work); err != errLocalState || secrets.gets.Load() != 0 {
		t.Fatal("incomplete schema constructed an authority owner")
	}
}

func TestLocalBackupStoreReceiptFailureRollsBackStateAndRedactsError(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	pending := seedLocalConfiguration(t, s)
	before, _ := localCiphertexts(t, s)
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_fixture_receipt BEFORE INSERT ON backup_recovery_receipts BEGIN SELECT RAISE(ABORT,'invented private backend diagnostic'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.update(t.Context(), "local", localFixtureWorkspace, pending.expected(), changeActivation, func(r *localRecord) error {
		appendLocalAttempt(r, "read_ok", localFixtureTime)
		r.activate(localFixtureTime)
		return nil
	})
	if err != errLocalState {
		t.Fatal("raw backend error escaped", err)
	}
	after, receipt := localCiphertexts(t, s)
	if !bytes.Equal(before, after) || len(receipt) != 0 {
		t.Fatal("partial state/authority transaction committed")
	}
	current, err := s.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || current.state.Sequence != 0 || current.state.Observation != nil {
		t.Fatal("failed activation invented observation", err)
	}
}

func TestLocalBackupStoreActivationCannotChangeReviewedIdentityOrEnableMonitoring(t *testing.T) {
	cases := map[string]func(*localRecord){
		"different repository": func(r *localRecord) { r.state.Configuration.RepositoryID = strings.Repeat("d", 64) },
		"different storage":    func(r *localRecord) { r.state.Configuration.Destination.VolumeID = localUUID(20) },
		"different root":       func(r *localRecord) { r.state.Configuration.Sources[0].DirectoryFileID++ },
		"different source":     func(r *localRecord) { r.state.Configuration.Sources[0].Path = "/Invented/Other" },
		"unreviewed monitoring": func(r *localRecord) {
			r.state.Monitoring.Intent = "active"
			r.state.Monitoring.Revision = 1
			r.receipt.MonitoringDigest = monitoringDigest(r.state.Monitoring)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := newLocalStoreFixture(t)
			pending := seedLocalConfiguration(t, s)
			before, _ := localCiphertexts(t, s)
			_, err := s.update(t.Context(), "local", localFixtureWorkspace, pending.expected(), changeActivation, func(r *localRecord) error {
				appendLocalAttempt(r, "read_ok", localFixtureTime)
				r.activate(localFixtureTime)
				mutate(r)
				r.receipt.ConfigurationDigest = localDigest(r.state.Configuration)
				return nil
			})
			after, receipt := localCiphertexts(t, s)
			if err == nil || !bytes.Equal(before, after) || len(receipt) != 0 {
				t.Fatal("validation changed reviewed authority")
			}
		})
	}
}

func TestLocalBackupStoreMonitoringReceiptDoesNotFollowFlagsOrBackgroundWrites(t *testing.T) {
	s, _ := newLocalStoreFixture(t)
	active := activateLocalFixture(t, s, seedLocalConfiguration(t, s))
	monitored, err := s.update(t.Context(), "local", localFixtureWorkspace, active.expected(), changeUser, func(r *localRecord) error {
		r.state.Monitoring.Intent = "active"
		r.state.Monitoring.Revision = 1
		r.receipt.MonitoringDigest = monitoringDigest(r.state.Monitoring)
		return nil
	})
	if err != nil || monitored.receipt.MonitoringDigest == "" {
		t.Fatal(err)
	}
	before, beforeReceipt := localCiphertexts(t, s)
	_, err = s.update(t.Context(), "local", localFixtureWorkspace, monitored.expected(), changeUser, func(r *localRecord) error { r.state.Monitoring.Policy.CadenceSeconds = 900; return nil })
	if err == nil {
		t.Fatal("changed policy inherited old consent")
	}
	_, err = s.update(t.Context(), "local", localFixtureWorkspace, monitored.expected(), changeJournal, func(r *localRecord) error {
		r.state.Monitoring.Revision++
		r.receipt.MonitoringDigest = monitoringDigest(r.state.Monitoring)
		return nil
	})
	if err == nil {
		t.Fatal("background journal changed monitoring consent")
	}
	after, afterReceipt := localCiphertexts(t, s)
	if !bytes.Equal(before, after) || !bytes.Equal(beforeReceipt, afterReceipt) {
		t.Fatal("failed policy mutation committed")
	}
}
