package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityNote is a workspace note's authored content and identity. The
// notes/*.md mirror travels as an ordinary folder file; this record restores
// the canonical SQL row with its original ID and dates. A private vault source
// reference is installation-local and is not carried.
type ContinuityNote struct {
	Version     int       `json:"version"`
	WorkspaceID string    `json:"workspace_id"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Content     string    `json:"content"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func SnapshotContinuityNote(value ContinuityNote, owner string) (workspacecontinuity.Record, error) {
	if value.Version != 1 || value.WorkspaceID != owner || !workspacecontinuity.ValidID(owner) || !workspacecontinuity.ValidID(value.ID) ||
		value.Name == "" || !utf8.ValidString(value.Name) || !utf8.ValidString(value.Content) || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() ||
		value.Tags == nil || len(value.Tags) > workspacecontinuity.MaxFiles {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	seen := map[string]bool{}
	for _, tag := range value.Tags {
		if tag == "" || len(tag) > 1024 || !utf8.ValidString(tag) || seen[tag] {
			return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
		}
		seen[tag] = true
	}
	if !sort.StringsAreSorted(value.Tags) {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	return workspacecontinuity.EncodeRecord(value.ID, value)
}

func DecodeContinuityNote(record workspacecontinuity.Record, owner string) (ContinuityNote, error) {
	var value ContinuityNote
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	encoded, err := SnapshotContinuityNote(value, owner)
	if err != nil {
		return value, err
	}
	if record.ID != encoded.ID || !bytes.Equal(record.Data, encoded.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

// CollectContinuityNotes keyset-pages every note owned by the workspace in the
// caller's shared SQL view. Headings, links and search are derived indexes and
// are rebuilt on restore, not exported.
func CollectContinuityNotes(ctx context.Context, query workspacecontinuity.Queryer, owner string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(owner) {
		return workspacecontinuity.ErrInvalid
	}
	if err := requireContinuitySourceOwner(ctx, query, owner); err != nil {
		return err
	}
	const pageSize = 128
	after, total := "", 0
	for {
		rows, err := query.QueryContext(ctx, `SELECT id,name,COALESCE(content,''),created_at,updated_at FROM workspace_notes
			WHERE workspace_id=? AND id>? ORDER BY id LIMIT ?`, owner, after, pageSize)
		if err != nil {
			return err
		}
		page := make([]ContinuityNote, 0, pageSize)
		for rows.Next() {
			var item ContinuityNote
			var createdRaw, updatedRaw any
			if err := rows.Scan(&item.ID, &item.Name, &item.Content, &createdRaw, &updatedRaw); err != nil {
				_ = rows.Close()
				return err
			}
			if item.CreatedAt, item.UpdatedAt, err = parseNoteTimes(createdRaw, updatedRaw); err != nil {
				_ = rows.Close()
				return workspacecontinuity.ErrInvalid
			}
			item.Version, item.WorkspaceID = 1, owner
			page = append(page, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, item := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if total >= workspacecontinuity.MaxReferences*workspacecontinuity.MaxRecords {
				return workspacecontinuity.ErrLimit
			}
			tags, err := collectContinuityNoteTags(ctx, query, item.ID)
			if err != nil {
				return err
			}
			item.Tags = tags
			record, err := SnapshotContinuityNote(item, owner)
			if err != nil {
				return err
			}
			if err := spool.AddRecord(ctx, "notes", "notes", record); err != nil {
				return err
			}
			after = item.ID
			total++
		}
		if len(page) < pageSize {
			break
		}
	}
	if total == 0 {
		return spool.SetAvailability(ctx, "notes", workspacecontinuity.Empty, "")
	}
	return nil
}

func collectContinuityNoteTags(ctx context.Context, query workspacecontinuity.Queryer, noteID string) ([]string, error) {
	rows, err := query.QueryContext(ctx, `SELECT tag FROM note_tags WHERE note_id=? ORDER BY tag LIMIT ?`, noteID, workspacecontinuity.MaxFiles+1)
	if err != nil {
		return nil, err
	}
	tags := []string{}
	for rows.Next() {
		if len(tags) >= workspacecontinuity.MaxFiles {
			_ = rows.Close()
			return nil, workspacecontinuity.ErrLimit
		}
		var tag string
		if err := rows.Scan(&tag); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tags = append(tags, tag)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	sort.Strings(tags)
	return tags, nil
}

// RestoreContinuityNote inserts one reviewed note under the import receipt.
// An exact prior claim is a no-op that preserves later local edits/deletions;
// an unrelated local note with the same ID is a conflict, never overwritten.
// Derived headings/links are rebuilt by RebuildContinuityNoteIndexes after
// the caller commits.
func (s *SQLiteStore) RestoreContinuityNote(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	if s == nil || tx == nil || scope.UserID != "local" {
		return false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuityNote(record, scope.WorkspaceID)
	if err != nil {
		return false, err
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "notes", "notes", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_notes WHERE id=?`, value.ID).Scan(&exists); err != nil {
		return false, err
	}
	if exists != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_notes(id,workspace_id,name,content,vault_reference_json,created_at,updated_at) VALUES(?,?,?,?,'',?,?)`,
		value.ID, scope.WorkspaceID, value.Name, value.Content, value.CreatedAt, value.UpdatedAt); err != nil {
		return false, workspacecontinuity.ErrConflict
	}
	for _, tag := range value.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO note_tags(note_id,tag,created_at) VALUES(?,?,?)`, value.ID, tag, value.UpdatedAt); err != nil {
			return false, workspacecontinuity.ErrConflict
		}
	}
	return true, nil
}

// RebuildContinuityNoteIndexes recomputes the derived heading and link indexes
// for restored notes. It creates no note, event or notification.
func (s *SQLiteStore) RebuildContinuityNoteIndexes(ctx context.Context, workspaceID string, noteIDs []string) error {
	for _, id := range noteIDs {
		note, err := s.GetNote(ctx, id)
		if err != nil {
			return err
		}
		if note.WorkspaceID != workspaceID {
			return workspacecontinuity.ErrConflict
		}
		if err := s.indexNoteHeadings(ctx, note.ID, note.Content); err != nil {
			return err
		}
		if err := s.indexNoteLinks(ctx, note.ID, note.Content, note.WorkspaceID); err != nil {
			return err
		}
	}
	return nil
}
