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
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	CreatedAt string `yaml:"created_at"`
	UpdatedAt string `yaml:"updated_at"`
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
				continue
			}
			note.ID = importedNoteStableID(workspaceID, entry.Name())
			if existing, err := h.store.GetNote(ctx, note.ID); err == nil {
				if existing.WorkspaceID == workspaceID {
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
