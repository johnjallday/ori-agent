package sessionhttp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"gopkg.in/yaml.v3"
)

type importedNoteFrontmatter struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name"`
	Tags      []string `yaml:"tags"`
	CreatedAt string   `yaml:"created_at"`
	UpdatedAt string   `yaml:"updated_at"`
}

func (h *Handler) importWorkspaceNoteFilesForWorkspace(ctx context.Context, workspaceID string) (int, error) {
	if h == nil || h.workspaceStore == nil {
		return 0, nil
	}

	folderPath, err := h.workspaceStore.GetFolderPath(workspaceID)
	if err != nil {
		return 0, nil
	}

	return h.importWorkspaceNoteFiles(ctx, workspaceID, folderPath)
}

func (h *Handler) importWorkspaceNoteFiles(ctx context.Context, workspaceID string, folderPath string) (int, error) {
	if h == nil || h.store == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(folderPath) == "" {
		return 0, nil
	}

	notesDir := filepath.Join(folderPath, agentworkspace.NotesDir)
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read notes directory: %w", err)
	}

	imported := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}

		path := filepath.Join(notesDir, entry.Name())
		note, err := parseImportedWorkspaceNoteFile(workspaceID, path, entry.Name())
		if err != nil {
			logger.Warn("Failed to parse imported workspace note", logger.Fields{
				"workspace_id": workspaceID,
				"path":         path,
				"error":        err,
			})
			continue
		}

		if note.ID == "" {
			note.ID = importedNoteIDFromFilename(entry.Name())
		}
		if note.ID == "" {
			note.ID = importedNoteStableID(workspaceID, entry.Name())
		}

		if existing, err := h.store.GetNote(ctx, note.ID); err == nil {
			if existing.WorkspaceID == workspaceID {
				if synced, err := h.syncImportedNoteIfChanged(ctx, existing, note, path); err != nil {
					return imported, err
				} else if synced {
					imported++
				}
				continue
			}
			note.ID = importedNoteStableID(workspaceID, entry.Name())
			if existing, err := h.store.GetNote(ctx, note.ID); err == nil {
				if existing.WorkspaceID == workspaceID {
					if synced, err := h.syncImportedNoteIfChanged(ctx, existing, note, path); err != nil {
						return imported, err
					} else if synced {
						imported++
					}
					continue
				}
				return imported, fmt.Errorf("fallback note id %s already belongs to workspace %s", note.ID, existing.WorkspaceID)
			} else if err != session.ErrNoteNotFound {
				return imported, fmt.Errorf("check fallback note %s: %w", note.ID, err)
			}
		} else if err != session.ErrNoteNotFound {
			return imported, fmt.Errorf("check note %s: %w", note.ID, err)
		}

		if err := h.store.CreateNote(ctx, note); err != nil {
			if err == session.ErrDuplicateID {
				note.ID = uuid.New().String()
				if retryErr := h.store.CreateNote(ctx, note); retryErr != nil {
					return imported, fmt.Errorf("create imported note %s: %w", entry.Name(), retryErr)
				}
			} else {
				return imported, fmt.Errorf("create imported note %s: %w", entry.Name(), err)
			}
		}
		imported++
	}

	return imported, nil
}

// syncImportedNoteIfChanged refreshes a DB note when its on-disk markdown file
// has been edited outside the app. The DB is the source of truth, but external
// edits to the note file (e.g. in an editor or Obsidian) are pulled back in so
// the app stops serving stale cached content. Returns true if a write occurred.
//
// Change detection is content/name based, not timestamp based: an external
// editor changes the body but leaves the frontmatter timestamp untouched, so a
// timestamp comparison would miss the edit. VaultRef and CreatedAt on the
// existing note are preserved; UpdatedAt is bumped to the file's mod time.
func (h *Handler) syncImportedNoteIfChanged(ctx context.Context, existing, parsed *session.WorkspaceNote, path string) (bool, error) {
	if !importedNoteDiffers(existing, parsed) {
		return false, nil
	}

	existing.Name = parsed.Name
	existing.Content = parsed.Content
	existing.Tags = parsed.Tags
	existing.UpdatedAt = noteFileModTime(path, time.Now())

	if err := h.store.UpdateNote(ctx, existing); err != nil {
		return false, fmt.Errorf("sync edited note %s: %w", existing.ID, err)
	}

	logger.Info("Synced externally edited note from file", logger.Fields{
		"note_id":      existing.ID,
		"workspace_id": existing.WorkspaceID,
		"path":         path,
	})
	return true, nil
}

// importedNoteDiffers reports whether the parsed file diverges from the stored
// note. Trailing newline differences are ignored because SyncNoteFile appends a
// trailing newline when writing notes to disk; without this the first sync of
// every note would be a spurious no-op write.
func importedNoteDiffers(existing, parsed *session.WorkspaceNote) bool {
	if existing.Name != parsed.Name {
		return true
	}
	if !importedNoteTagsEqual(existing.Tags, parsed.Tags) {
		return true
	}
	return normalizeImportedNoteContent(existing.Content) != normalizeImportedNoteContent(parsed.Content)
}

// importedNoteTagsEqual compares tag lists order-insensitively: the DB returns
// tags in insertion order while frontmatter preserves authoring order, and a
// reordering is not an edit worth syncing.
func importedNoteTagsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, tag := range a {
		seen[tag]++
	}
	for _, tag := range b {
		if seen[tag] == 0 {
			return false
		}
		seen[tag]--
	}
	return true
}

func normalizeImportedNoteContent(content string) string {
	return strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

func noteFileModTime(path string, fallback time.Time) time.Time {
	if info, err := os.Stat(path); err == nil && !info.ModTime().IsZero() {
		return info.ModTime()
	}
	return fallback
}

func parseImportedWorkspaceNoteFile(workspaceID, path, filename string) (*session.WorkspaceNote, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	name := importedNoteNameFromFilename(filename)
	if name == "" {
		name = "Imported Note"
	}

	content := string(data)
	frontmatter, body, hasFrontmatter, err := splitImportedNoteFrontmatter(content)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if info, statErr := os.Stat(path); statErr == nil && !info.ModTime().IsZero() {
		now = info.ModTime()
	}

	note := &session.WorkspaceNote{
		WorkspaceID: workspaceID,
		Name:        name,
		Content:     body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if hasFrontmatter {
		var fm importedNoteFrontmatter
		if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
			return nil, fmt.Errorf("parse note frontmatter: %w", err)
		}
		note.ID = strings.TrimSpace(fm.ID)
		if fmName := strings.TrimSpace(fm.Name); fmName != "" {
			note.Name = fmName
		}
		note.Tags = agentworkspace.NormalizeWorkspaceTags(fm.Tags)
		note.CreatedAt = parseImportedNoteTime(fm.CreatedAt, now)
		note.UpdatedAt = parseImportedNoteTime(fm.UpdatedAt, note.CreatedAt)
	}

	return note, nil
}

func splitImportedNoteFrontmatter(content string) (frontmatter, body string, hasFrontmatter bool, err error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", content, false, nil
	}

	rest := normalized[len("---\n"):]
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		return "", "", true, fmt.Errorf("note frontmatter is missing closing delimiter")
	}

	frontmatter = strings.TrimSpace(rest[:endIdx])
	body = rest[endIdx+len("\n---\n"):]
	body = strings.TrimPrefix(body, "\n")
	return frontmatter, body, true, nil
}

func parseImportedNoteTime(value string, fallback time.Time) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed
	}
	return fallback
}

func importedNoteNameFromFilename(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if idx := strings.LastIndex(base, "--"); idx > 0 {
		base = base[:idx]
	}
	return titleFromSlug(base)
}

func importedNoteIDFromFilename(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	idx := strings.LastIndex(base, "--")
	if idx < 0 || idx+2 >= len(base) {
		return ""
	}
	return strings.TrimSpace(base[idx+2:])
}

func importedNoteStableID(workspaceID, filename string) string {
	key := strings.TrimSpace(workspaceID) + "\x00" + filepath.Base(filename)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func titleFromSlug(slug string) string {
	parts := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_' || unicode.IsSpace(r)
	})
	if len(parts) == 0 {
		return ""
	}

	for i, part := range parts {
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
