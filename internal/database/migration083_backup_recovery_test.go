package database

import (
	"path/filepath"
	"testing"
)

const backupFixtureGeneration = "11111111-1111-4111-8111-111111111111"

func newBackupMigrationFixture(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "backup.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at,installed_capabilities_json)
	 VALUES ('backup-owner','Invented',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'[{"id":"backup-recovery","version":1,"installed_at":"2026-01-01T00:00:00Z"}]');
	 INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES ('backup-owner','native','ordinary',CURRENT_TIMESTAMP);
	 INSERT INTO backup_recovery_local VALUES ('backup-owner','local',1,?,1,0,0,zeroblob(28),CURRENT_TIMESTAMP)`, backupFixtureGeneration)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedBackupReceipt(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO backup_recovery_receipts VALUES ('backup-owner','local',?,?,zeroblob(28))`, backupFixtureGeneration, backupFixtureGeneration); err != nil {
		t.Fatal(err)
	}
}

func backupRowCounts(t *testing.T, db *DB, stateWant, receiptWant int) {
	t.Helper()
	var state, receipt int
	if err := db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM backup_recovery_local),(SELECT COUNT(*) FROM backup_recovery_receipts)`).Scan(&state, &receipt); err != nil {
		t.Fatal(err)
	}
	if state != stateWant || receipt != receiptWant {
		t.Fatalf("state/receipt counts %d/%d, want %d/%d", state, receipt, stateWant, receiptWant)
	}
}

func TestMigration083BackupStateIsLocalBoundedAndResetInventoried(t *testing.T) {
	db := newBackupMigrationFixture(t)
	seedBackupReceipt(t, db)
	for range 2 {
		if err := db.migration083BackupRecovery(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	backupRowCounts(t, db, 1, 1)
	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("unclassified backup state: %v %v", err, inspection.Problems)
	}
	for _, name := range []string{"backup_recovery_local", "backup_recovery_receipts", "backup_recovery_authority"} {
		if inspection.Counts[name] == nil || *inspection.Counts[name] != 1 {
			t.Fatalf("missing reset inventory: %s", name)
		}
	}
	for _, statement := range []string{
		`UPDATE backup_recovery_local SET revision=0`,
		`UPDATE backup_recovery_local SET revision=9007199254740992`,
		`UPDATE backup_recovery_local SET sequence=-1`,
		`UPDATE backup_recovery_local SET ciphertext=zeroblob(2097181)`,
		`UPDATE backup_recovery_receipts SET ciphertext=zeroblob(16413)`,
		`UPDATE backup_recovery_receipts SET generation='malformed'`,
		`UPDATE backup_recovery_local SET workspace_id='missing'`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err == nil {
			t.Fatal("invalid state accepted")
		}
	}
	// Future state is retained, not destructively rewritten by an older schema.
	if _, err := db.ExecContext(t.Context(), `UPDATE backup_recovery_local SET schema_version=2`); err != nil {
		t.Fatal(err)
	}
	if err := db.migration083BackupRecovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	backupRowCounts(t, db, 1, 1)
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id='backup-owner'`); err != nil {
		t.Fatal(err)
	}
	backupRowCounts(t, db, 0, 0)
}

func TestMigration083LifecycleFencesReceiptWithoutErasingHistory(t *testing.T) {
	cases := map[string]string{
		"archive":            `UPDATE workspaces SET status='archived'`,
		"unknown status":     `UPDATE workspaces SET status=NULL`,
		"trash":              `UPDATE workspaces SET deleted_at=CURRENT_TIMESTAMP`,
		"uninstall":          `UPDATE workspaces SET installed_capabilities_json='[]'`,
		"install replaced":   `UPDATE workspaces SET installed_capabilities_json='[{"id":"backup-recovery","version":1,"installed_at":"2026-01-02T00:00:00Z"}]'`,
		"malformed install":  `UPDATE workspaces SET installed_capabilities_json='["invalid"]'`,
		"foreign import":     `UPDATE continuity_attachments SET operation_id='invented-other-operation'`,
		"detached":           `UPDATE continuity_attachments SET state='detached'`,
		"attachment deleted": `DELETE FROM continuity_attachments`,
		"provisioning":       `INSERT INTO continuity_file_mutations VALUES ('backup-owner','native-create','invented','pending',CURRENT_TIMESTAMP)`,
	}
	for name, statement := range cases {
		t.Run(name, func(t *testing.T) {
			db := newBackupMigrationFixture(t)
			seedBackupReceipt(t, db)
			if _, err := db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			backupRowCounts(t, db, 1, 0)
		})
	}
}

func TestMigration083OtherCapabilityAndRenameDoNotRevokeBackup(t *testing.T) {
	db := newBackupMigrationFixture(t)
	seedBackupReceipt(t, db)
	if _, err := db.ExecContext(t.Context(), `UPDATE workspaces SET name='Renamed',installed_capabilities_json='[{"id":"backup-recovery","version":1,"installed_at":"2026-01-01T00:00:00Z"},{"id":"unrelated","version":1}]'`); err != nil {
		t.Fatal(err)
	}
	backupRowCounts(t, db, 1, 1)
}
