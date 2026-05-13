package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/vaultref"
)

// ============================================================================
// Workspace Note Operations
// ============================================================================

func parseNoteTimes(createdAtRaw, updatedAtRaw any) (time.Time, time.Time, error) {
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("updated_at: %w", err)
	}
	return createdAt, updatedAt, nil
}

// CreateNote creates a new workspace note in the database.
func (s *SQLiteStore) CreateNote(ctx context.Context, note *WorkspaceNote) error {
	if note.ID == "" {
		return ErrInvalidID
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_notes (id, workspace_id, name, content, vault_reference_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, note.ID, note.WorkspaceID, note.Name, note.Content, encodeNoteVaultReference(note.VaultRef), note.CreatedAt, note.UpdatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint") {
			return ErrWorkspaceNotFound
		}
		return fmt.Errorf("failed to create note: %w", err)
	}

	if err := s.indexNoteHeadings(ctx, note.ID, note.Content); err != nil {
		return fmt.Errorf("failed to index note headings: %w", err)
	}
	if err := s.indexNoteLinks(ctx, note.ID, note.Content, note.WorkspaceID); err != nil {
		return fmt.Errorf("failed to index note links: %w", err)
	}
	if err := s.retroResolveBrokenLinks(ctx, note.ID, note.WorkspaceID, note.Name); err != nil {
		return fmt.Errorf("failed to retro-resolve note links: %w", err)
	}

	return nil
}

// GetNote retrieves a note by ID.
func (s *SQLiteStore) GetNote(ctx context.Context, id string) (*WorkspaceNote, error) {
	note := &WorkspaceNote{}
	var vaultReferenceJSON sql.NullString
	var createdAtRaw any
	var updatedAtRaw any

	err := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, content, COALESCE(vault_reference_json, ''), created_at, updated_at
		FROM workspace_notes WHERE id = ?
	`, id).Scan(&note.ID, &note.WorkspaceID, &note.Name, &note.Content,
		&vaultReferenceJSON, &createdAtRaw, &updatedAtRaw)

	if err == sql.ErrNoRows {
		return nil, ErrNoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get note: %w", err)
	}
	note.CreatedAt, note.UpdatedAt, err = parseNoteTimes(createdAtRaw, updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse note timestamps: %w", err)
	}
	if note.VaultRef, err = decodeNoteVaultReference(vaultReferenceJSON.String); err != nil {
		return nil, fmt.Errorf("failed to decode note vault reference: %w", err)
	}

	return note, nil
}

// UpdateNote updates note metadata and content.
func (s *SQLiteStore) UpdateNote(ctx context.Context, note *WorkspaceNote) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_notes
		SET name = ?, content = ?, workspace_id = ?, vault_reference_json = ?, updated_at = ?
		WHERE id = ?
	`, note.Name, note.Content, note.WorkspaceID, encodeNoteVaultReference(note.VaultRef), note.UpdatedAt, note.ID)

	if err != nil {
		return fmt.Errorf("failed to update note: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "note", ErrNoteNotFound); err != nil {
		return err
	}

	if err := s.indexNoteHeadings(ctx, note.ID, note.Content); err != nil {
		return fmt.Errorf("failed to index note headings: %w", err)
	}
	if err := s.indexNoteLinks(ctx, note.ID, note.Content, note.WorkspaceID); err != nil {
		return fmt.Errorf("failed to index note links: %w", err)
	}
	// Retro-resolve previously-broken links elsewhere in the workspace whose
	// target_text now matches this note's name. This handles both create and
	// rename — both go through here.
	if err := s.retroResolveBrokenLinks(ctx, note.ID, note.WorkspaceID, note.Name); err != nil {
		return fmt.Errorf("failed to retro-resolve note links: %w", err)
	}

	return nil
}

// DeleteNote removes a note.
func (s *SQLiteStore) DeleteNote(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM workspace_notes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete note: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "note", ErrNoteNotFound); err != nil {
		return err
	}

	return nil
}

// ListNotesByWorkspace returns all notes in a workspace.
func (s *SQLiteStore) ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]WorkspaceNoteListItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_id, name,
		       CASE WHEN LENGTH(content) > 100 THEN SUBSTR(content, 1, 100) || '...' ELSE content END as preview,
		       COALESCE(vault_reference_json, ''), created_at, updated_at
		FROM workspace_notes
		WHERE workspace_id = ?
		ORDER BY updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list notes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	notes := make([]WorkspaceNoteListItem, 0)
	for rows.Next() {
		var note WorkspaceNoteListItem
		var vaultReferenceRaw string
		var createdAtRaw any
		var updatedAtRaw any
		if err := rows.Scan(&note.ID, &note.WorkspaceID, &note.Name, &note.Preview,
			&vaultReferenceRaw, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, fmt.Errorf("failed to scan note: %w", err)
		}
		note.CreatedAt, note.UpdatedAt, err = parseNoteTimes(createdAtRaw, updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse note timestamps: %w", err)
		}
		note.VaultRef, err = decodeNoteVaultReference(vaultReferenceRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to decode note vault reference: %w", err)
		}
		notes = append(notes, note)
	}

	return notes, nil
}

func encodeNoteVaultReference(ref *vaultref.Reference) string {
	clean := vaultref.Normalize(ref)
	if clean == nil {
		return ""
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeNoteVaultReference(raw string) (*vaultref.Reference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var ref vaultref.Reference
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		return nil, err
	}
	return vaultref.Normalize(&ref), nil
}

// indexNoteHeadings re-indexes the note_headings + note_headings_fts rows for one note.
// Old rows are deleted and fresh ones inserted in a single transaction so the FTS mirror
// stays consistent with note_headings.
func (s *SQLiteStore) indexNoteHeadings(ctx context.Context, noteID, content string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM note_headings WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("delete existing headings: %w", err)
	}

	for _, h := range ParseHeadings(content) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_headings (note_id, level, text, position) VALUES (?, ?, ?, ?)
		`, noteID, h.Level, h.Text, h.Position); err != nil {
			return fmt.Errorf("insert heading: %w", err)
		}
	}

	return tx.Commit()
}

// SearchHeadings performs full-text search over note headings across all workspaces.
// Results are joined back to the parent note and workspace for display context.
func (s *SQLiteStore) SearchHeadings(ctx context.Context, query string, limit int) ([]HeadingSearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT h.note_id, n.name, n.workspace_id, w.name,
		       h.level, h.text, h.position,
		       snippet(note_headings_fts, 0, '<mark>', '</mark>', '...', 16) as snippet
		FROM note_headings h
		INNER JOIN note_headings_fts fts ON h.id = fts.rowid
		INNER JOIN workspace_notes n ON h.note_id = n.id
		LEFT JOIN workspaces w ON n.workspace_id = w.id
		WHERE note_headings_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		if strings.Contains(err.Error(), "fts5") {
			return []HeadingSearchResult{}, nil
		}
		return nil, fmt.Errorf("failed to search headings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]HeadingSearchResult, 0)
	for rows.Next() {
		var r HeadingSearchResult
		var workspaceName sql.NullString
		if err := rows.Scan(&r.NoteID, &r.NoteName, &r.WorkspaceID, &workspaceName,
			&r.Level, &r.Text, &r.Position, &r.Snippet); err != nil {
			return nil, fmt.Errorf("failed to scan heading result: %w", err)
		}
		r.WorkspaceName = workspaceName.String
		results = append(results, r)
	}
	return results, nil
}

// BackfillHeadingIndex indexes headings for any note that has no rows in note_headings.
// Called once at startup after migration; idempotent for ongoing use.
func (s *SQLiteStore) BackfillHeadingIndex(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.content
		FROM workspace_notes n
		LEFT JOIN note_headings h ON n.id = h.note_id
		WHERE h.id IS NULL
		GROUP BY n.id
	`)
	if err != nil {
		return 0, fmt.Errorf("query unindexed notes: %w", err)
	}

	type pair struct {
		id, content string
	}
	var pending []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.content); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan unindexed note: %w", err)
		}
		pending = append(pending, p)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	for _, p := range pending {
		if err := s.indexNoteHeadings(ctx, p.id, p.content); err != nil {
			return 0, fmt.Errorf("index note %s: %w", p.id, err)
		}
	}
	return len(pending), nil
}

// indexNoteLinks rebuilds the note_links rows for one note. Each `[[…]]`
// reference becomes one row, with target_note_id resolved against the
// workspace's notes (exact title match, case-insensitive fallback). Broken
// links keep target_note_id NULL so they're easy to find later.
func (s *SQLiteStore) indexNoteLinks(ctx context.Context, noteID, content, workspaceID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM note_links WHERE source_note_id = ?`, noteID); err != nil {
		return fmt.Errorf("delete existing note_links: %w", err)
	}

	for _, link := range ParseWikilinks(content) {
		var targetID *string
		if workspaceID != "" {
			id := s.resolveWikilinkTargetTx(ctx, tx, link.Target, workspaceID)
			if id != "" {
				targetID = &id
			}
		}
		var display *string
		if link.Display != "" {
			d := link.Display
			display = &d
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_links (source_note_id, target_note_id, target_text, display_text, position)
			VALUES (?, ?, ?, ?, ?)
		`, noteID, targetID, link.Target, display, link.Position); err != nil {
			return fmt.Errorf("insert note_link: %w", err)
		}
	}
	return tx.Commit()
}

// resolveWikilinkTargetTx is the transactional twin of resolveWikilinkTarget
// in note_links.go — it lets indexNoteLinks see uncommitted writes inside the
// same transaction (relevant when batches span multiple notes).
func (s *SQLiteStore) resolveWikilinkTargetTx(ctx context.Context, tx interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, target, workspaceID string) string {
	if target == "" || workspaceID == "" {
		return ""
	}
	var id string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM workspace_notes WHERE workspace_id = ? AND name = ? LIMIT 1`,
		workspaceID, target).Scan(&id); err == nil {
		return id
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM workspace_notes WHERE workspace_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`,
		workspaceID, target).Scan(&id); err == nil {
		return id
	}
	return ""
}

// retroResolveBrokenLinks looks for note_links rows in `workspaceID` whose
// target_note_id IS NULL and whose target_text matches `noteName` (case-
// insensitive), and points them at `noteID`. Called from CreateNote and
// UpdateNote so that creating or renaming a note retroactively fixes broken
// references in the workspace.
func (s *SQLiteStore) retroResolveBrokenLinks(ctx context.Context, noteID, workspaceID, noteName string) error {
	if noteID == "" || workspaceID == "" || noteName == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE note_links
		SET target_note_id = ?
		WHERE target_note_id IS NULL
		  AND LOWER(target_text) = LOWER(?)
		  AND source_note_id IN (SELECT id FROM workspace_notes WHERE workspace_id = ?)
	`, noteID, noteName, workspaceID)
	if err != nil {
		return fmt.Errorf("retro-resolve broken links: %w", err)
	}
	return nil
}

// SearchBacklinks returns notes that link TO the given note via wikilinks.
// Each result includes a context snippet pulled from the source note's body
// around the link position so users have something to read in the panel.
func (s *SQLiteStore) SearchBacklinks(ctx context.Context, noteID string, limit int) ([]BacklinkResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.source_note_id, n.name, n.workspace_id, w.name,
		       l.target_text, COALESCE(l.display_text, ''), l.position,
		       n.content
		FROM note_links l
		INNER JOIN workspace_notes n ON l.source_note_id = n.id
		LEFT JOIN workspaces w ON n.workspace_id = w.id
		WHERE l.target_note_id = ?
		ORDER BY n.updated_at DESC
		LIMIT ?
	`, noteID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query backlinks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]BacklinkResult, 0)
	for rows.Next() {
		var r BacklinkResult
		var workspaceName sql.NullString
		var content string
		if err := rows.Scan(&r.SourceNoteID, &r.SourceNoteName, &r.WorkspaceID,
			&workspaceName, &r.TargetText, &r.DisplayText, &r.Position, &content); err != nil {
			return nil, fmt.Errorf("scan backlink row: %w", err)
		}
		r.WorkspaceName = workspaceName.String
		r.ContextSnippet = backlinkContextSnippet(content, r.Position, 120)
		results = append(results, r)
	}
	return results, nil
}

// backlinkContextSnippet returns up to `width` characters of context around
// `position` in `content`. Trims to whitespace boundaries so the snippet
// doesn't start or end mid-word.
func backlinkContextSnippet(content string, position, width int) string {
	if content == "" || position < 0 || position >= len(content) {
		return ""
	}
	half := width / 2
	start := max(position-half, 0)
	end := min(position+half, len(content))
	// Snap to whitespace boundaries when possible.
	for start > 0 && content[start] != ' ' && content[start] != '\n' {
		start--
	}
	for end < len(content) && content[end] != ' ' && content[end] != '\n' {
		end++
	}
	snippet := strings.TrimSpace(content[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(content) {
		snippet = snippet + "…"
	}
	return snippet
}

// BackfillNoteLinks indexes wikilinks for any note whose content contains
// `[[…]]` syntax but has no rows in note_links yet. Called once at startup;
// idempotent. The `content LIKE '%[[%'` filter avoids re-indexing notes that
// legitimately have zero links.
func (s *SQLiteStore) BackfillNoteLinks(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.content, n.workspace_id
		FROM workspace_notes n
		LEFT JOIN note_links l ON n.id = l.source_note_id
		WHERE l.id IS NULL AND n.content LIKE '%[[%'
		GROUP BY n.id
	`)
	if err != nil {
		return 0, fmt.Errorf("query unindexed notes: %w", err)
	}

	type pair struct{ id, content, workspace string }
	var pending []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.content, &p.workspace); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan unindexed note: %w", err)
		}
		pending = append(pending, p)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	for _, p := range pending {
		if err := s.indexNoteLinks(ctx, p.id, p.content, p.workspace); err != nil {
			return 0, fmt.Errorf("index note %s: %w", p.id, err)
		}
	}
	return len(pending), nil
}

// SearchNotes performs full-text search across note names and content.
func (s *SQLiteStore) SearchNotes(ctx context.Context, query string, limit int) ([]NoteSearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.workspace_id, n.name,
		       CASE WHEN LENGTH(n.content) > 100 THEN SUBSTR(n.content, 1, 100) || '...' ELSE n.content END as preview,
		       n.created_at, n.updated_at,
		       w.name as workspace_name,
		       snippet(workspace_notes_fts, 2, '<mark>', '</mark>', '...', 32) as snippet
		FROM workspace_notes n
		INNER JOIN workspace_notes_fts fts ON n.id = fts.note_id
		LEFT JOIN workspaces w ON n.workspace_id = w.id
		WHERE workspace_notes_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		// If FTS match fails (e.g., empty query), return empty results
		if strings.Contains(err.Error(), "fts5") {
			return []NoteSearchResult{}, nil
		}
		return nil, fmt.Errorf("failed to search notes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]NoteSearchResult, 0)
	for rows.Next() {
		var result NoteSearchResult
		var workspaceName sql.NullString
		var snippet string
		var createdAtRaw any
		var updatedAtRaw any

		if err := rows.Scan(&result.ID, &result.WorkspaceID, &result.Name, &result.Preview,
			&createdAtRaw, &updatedAtRaw, &workspaceName, &snippet); err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		result.CreatedAt, result.UpdatedAt, err = parseNoteTimes(createdAtRaw, updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse search result timestamps: %w", err)
		}

		result.WorkspaceName = workspaceName.String
		if snippet != "" {
			result.Snippets = []string{snippet}
		}

		results = append(results, result)
	}

	return results, nil
}
