package database

import "context"

// Backup observations and access receipts are installation-local, encrypted app
// records, never canonical workspace exports. Creating these empty tables grants
// no access. Workspace/attachment lifecycle changes invalidate receipts even if
// an obsolete service instance still holds an in-memory configuration.
func (db *DB) migration083BackupRecovery(ctx context.Context) error {
	// Partial legacy installations (and focused upgrade fixtures) may omit a
	// canonical domain or an old baseline column. Do not invent those owners or
	// install triggers with unresolved NEW fields. The backup store requires
	// every fence before construction; this incomplete schema stays unavailable.
	for _, prerequisite := range []struct {
		query   string
		columns int
	}{
		{`SELECT COUNT(*) FROM pragma_table_info('workspaces') WHERE name IN ('id','owner_user_id','kind','status','deleted_at','installed_capabilities_json')`, 6},
		{`SELECT COUNT(*) FROM pragma_table_info('continuity_attachments') WHERE name IN ('workspace_id','state','operation_id','source_generation','source_digest')`, 5},
		{`SELECT COUNT(*) FROM pragma_table_info('continuity_operations') WHERE name IN ('id','owner_user_id','status')`, 3},
		{`SELECT COUNT(*) FROM pragma_table_info('continuity_file_mutations') WHERE name IN ('workspace_id','target')`, 2},
		{`SELECT COUNT(*) FROM pragma_table_info('workspace_local_config') WHERE name IN ('workspace_id','ciphertext')`, 2},
	} {
		var count int
		if err := db.QueryRowContext(ctx, prerequisite.query).Scan(&count); err != nil {
			return err
		}
		if count != prerequisite.columns {
			return nil
		}
	}
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS backup_recovery_local (
			workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
			owner_user_id TEXT NOT NULL,
			schema_version INTEGER NOT NULL CHECK(schema_version > 0),
			generation TEXT NOT NULL CHECK(length(generation) = 36),
			revision INTEGER NOT NULL CHECK(revision > 0 AND revision <= 9007199254740991),
			sequence INTEGER NOT NULL CHECK(sequence >= 0 AND sequence <= 9007199254740991),
			authority_epoch INTEGER NOT NULL DEFAULT 0 CHECK(authority_epoch >= 0 AND authority_epoch <= 9007199254740991),
			ciphertext BLOB NOT NULL CHECK(length(ciphertext) BETWEEN 28 AND 2097180),
			updated_at DATETIME NOT NULL
		);
		-- Initial reviews have no encrypted row yet. Fence them without
		-- creating configuration, an encryption key or any access grant.
		CREATE TABLE IF NOT EXISTS backup_recovery_authority (
			workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
			epoch INTEGER NOT NULL CHECK(epoch >= 0 AND epoch <= 9007199254740991)
		);
		INSERT OR IGNORE INTO backup_recovery_authority SELECT workspace_id,authority_epoch FROM backup_recovery_local;
		CREATE TABLE IF NOT EXISTS backup_recovery_receipts (
			workspace_id TEXT PRIMARY KEY REFERENCES backup_recovery_local(workspace_id) ON DELETE CASCADE,
			owner_user_id TEXT NOT NULL,
			generation TEXT NOT NULL CHECK(length(generation) = 36),
			grant_id TEXT NOT NULL CHECK(length(grant_id) = 36),
			ciphertext BLOB NOT NULL CHECK(length(ciphertext) BETWEEN 28 AND 16412)
		);
		CREATE TRIGGER IF NOT EXISTS backup_recovery_workspace_fence
		AFTER UPDATE OF owner_user_id,status,deleted_at ON workspaces
		WHEN NEW.owner_user_id IS NOT OLD.owner_user_id OR NEW.status IS NOT 'active' OR NEW.deleted_at IS NOT NULL
		BEGIN
		 DELETE FROM backup_recovery_receipts WHERE workspace_id=NEW.id;
		 INSERT INTO backup_recovery_authority SELECT id,1 FROM workspaces WHERE id=NEW.id
		 ON CONFLICT(workspace_id) DO UPDATE SET epoch=backup_recovery_authority.epoch+1;
		 UPDATE backup_recovery_local SET authority_epoch=(SELECT epoch FROM backup_recovery_authority WHERE workspace_id=NEW.id) WHERE workspace_id=NEW.id;
		END;
		CREATE TRIGGER IF NOT EXISTS backup_recovery_install_fence
		AFTER UPDATE OF installed_capabilities_json ON workspaces
		WHEN
		 (CASE WHEN json_valid(NEW.installed_capabilities_json) THEN
		  (SELECT json_group_array(value) FROM json_each(NEW.installed_capabilities_json)
		   WHERE CASE WHEN type='object' THEN lower(trim(json_extract(value,'$.id'))) ELSE '' END='backup-recovery') ELSE '' END)
		 IS NOT
		 (CASE WHEN json_valid(OLD.installed_capabilities_json) THEN
		  (SELECT json_group_array(value) FROM json_each(OLD.installed_capabilities_json)
		   WHERE CASE WHEN type='object' THEN lower(trim(json_extract(value,'$.id'))) ELSE '' END='backup-recovery') ELSE '' END)
		BEGIN
		 DELETE FROM backup_recovery_receipts WHERE workspace_id=NEW.id;
		 INSERT INTO backup_recovery_authority SELECT id,1 FROM workspaces WHERE id=NEW.id
		 ON CONFLICT(workspace_id) DO UPDATE SET epoch=backup_recovery_authority.epoch+1;
		 UPDATE backup_recovery_local SET authority_epoch=(SELECT epoch FROM backup_recovery_authority WHERE workspace_id=NEW.id) WHERE workspace_id=NEW.id;
		END;
		CREATE TRIGGER IF NOT EXISTS backup_recovery_attachment_fence
		AFTER UPDATE OF state,operation_id,source_generation,source_digest ON continuity_attachments
		WHEN NEW.operation_id IS NOT OLD.operation_id OR NEW.source_generation IS NOT OLD.source_generation
		 OR NEW.source_digest IS NOT OLD.source_digest OR NEW.state NOT IN ('native','imported_inactive','imported_active')
		 OR (NEW.state='native') != (OLD.state='native')
		BEGIN
		 DELETE FROM backup_recovery_receipts WHERE workspace_id=NEW.workspace_id;
		 INSERT INTO backup_recovery_authority SELECT id,1 FROM workspaces WHERE id=NEW.workspace_id
		 ON CONFLICT(workspace_id) DO UPDATE SET epoch=backup_recovery_authority.epoch+1;
		 UPDATE backup_recovery_local SET authority_epoch=(SELECT epoch FROM backup_recovery_authority WHERE workspace_id=NEW.workspace_id) WHERE workspace_id=NEW.workspace_id;
		END;
		CREATE TRIGGER IF NOT EXISTS backup_recovery_attachment_delete
		AFTER DELETE ON continuity_attachments
		BEGIN
		 DELETE FROM backup_recovery_receipts WHERE workspace_id=OLD.workspace_id;
		 INSERT INTO backup_recovery_authority SELECT id,1 FROM workspaces WHERE id=OLD.workspace_id
		 ON CONFLICT(workspace_id) DO UPDATE SET epoch=backup_recovery_authority.epoch+1;
		 UPDATE backup_recovery_local SET authority_epoch=(SELECT epoch FROM backup_recovery_authority WHERE workspace_id=OLD.workspace_id) WHERE workspace_id=OLD.workspace_id;
		END;
		CREATE TRIGGER IF NOT EXISTS backup_recovery_provisioning_insert
		AFTER INSERT ON continuity_file_mutations WHEN NEW.target='native-create'
		BEGIN
		 DELETE FROM backup_recovery_receipts WHERE workspace_id=NEW.workspace_id;
		 INSERT INTO backup_recovery_authority SELECT id,1 FROM workspaces WHERE id=NEW.workspace_id
		 ON CONFLICT(workspace_id) DO UPDATE SET epoch=backup_recovery_authority.epoch+1;
		 UPDATE backup_recovery_local SET authority_epoch=(SELECT epoch FROM backup_recovery_authority WHERE workspace_id=NEW.workspace_id) WHERE workspace_id=NEW.workspace_id;
		END;
		CREATE TRIGGER IF NOT EXISTS backup_recovery_provisioning_update
		AFTER UPDATE ON continuity_file_mutations WHEN NEW.target='native-create' OR OLD.target='native-create'
		BEGIN
		 DELETE FROM backup_recovery_receipts WHERE workspace_id IN (NEW.workspace_id,OLD.workspace_id);
		 INSERT INTO backup_recovery_authority SELECT id,1 FROM workspaces WHERE id IN (NEW.workspace_id,OLD.workspace_id)
		 ON CONFLICT(workspace_id) DO UPDATE SET epoch=backup_recovery_authority.epoch+1;
		 UPDATE backup_recovery_local SET authority_epoch=(SELECT epoch FROM backup_recovery_authority a WHERE a.workspace_id=backup_recovery_local.workspace_id) WHERE workspace_id IN (NEW.workspace_id,OLD.workspace_id);
		END;
	`)
	return err
}
