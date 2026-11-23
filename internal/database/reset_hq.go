package database

import (
	"context"
	"database/sql"
	"errors"
)

// ResetHQRecord is bounded registration evidence, not a filesystem location
// or archive authority. Only the unique local designation can select it.
type ResetHQRecord struct {
	ID, Slug   string
	OtherPaths []string
}

var ErrResetHQIneligible = errors.New("no unique, active, native Personal HQ is registered")

// InspectResetHQ uses one read-only snapshot of the designation, canonical
// workspace registration and native attachment. No presentation name or
// folder discovery participates in authority.
func InspectResetHQ(ctx context.Context, db *sql.DB) (ResetHQRecord, error) {
	var result ResetHQRecord
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx, `SELECT w.id, substr(w.folder_slug,1,65)
		FROM users u JOIN workspaces w ON w.id=u.personal_workspace_id
		JOIN continuity_attachments a ON a.workspace_id=w.id
		WHERE u.id='local' AND w.owner_user_id='local' AND w.kind='workspace' AND w.status='active'
			AND w.deleted_at IS NULL AND COALESCE(w.parent_id,'')=''
			AND COALESCE(json_extract(w.shared_data,'$.anchor_directory_reference_id'),'')=''
			AND json_extract(w.shared_data,'$.folder_import') IS NULL
			AND a.state='native' AND a.disposition='ordinary'
			AND NOT EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=w.id)
			AND NOT EXISTS(SELECT 1 FROM workspaces child WHERE child.parent_id=w.id)
			AND NOT EXISTS(SELECT 1 FROM users other WHERE other.id<>'local' AND trim(other.personal_workspace_id)<>'')`).Scan(&result.ID, &result.Slug)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrResetHQIneligible
	}
	if err != nil {
		return result, err
	}
	// FileStore corroborates live folder registrations. At pre-start there
	// is no FileStore: also check other registrations' declared anchors or
	// import folders so a linked workspace cannot be carried along.
	rows, err := tx.QueryContext(ctx, `SELECT substr(COALESCE(json_extract(shared_data,'$.folder_import.path'),''),1,4097),
		substr(COALESCE((SELECT json_extract(d.value,'$.path') FROM json_each(NULLIF(directory_references_json,'')) d
			WHERE json_extract(d.value,'$.id')=json_extract(workspaces.shared_data,'$.anchor_directory_reference_id') LIMIT 1),''),1,4097)
		FROM workspaces WHERE id<>? LIMIT 10001`, result.ID)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	for count := 1; rows.Next(); count++ {
		if count > 10000 {
			return ResetHQRecord{}, ErrResetHQIneligible
		}
		var imported, anchor string
		if err := rows.Scan(&imported, &anchor); err != nil {
			return ResetHQRecord{}, err
		}
		for _, path := range []string{imported, anchor} {
			if path != "" {
				result.OtherPaths = append(result.OtherPaths, path)
			}
		}
	}
	return result, rows.Err()
}

// InspectResetHQFile repeats the same observation before any app stores are
// opened or cleared. The raw probe never migrates or checkpoints the database.
func InspectResetHQFile(ctx context.Context, path string) (ResetHQRecord, error) {
	probe, err := sql.Open("sqlite", resetSQLiteURI(path, "ro", true))
	if err != nil {
		return ResetHQRecord{}, err
	}
	probe.SetMaxOpenConns(1)
	record, err := InspectResetHQ(ctx, probe)
	return record, errors.Join(err, probe.Close())
}
