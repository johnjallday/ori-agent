package sessionhttp

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Global tag management: usage preview, rename (with merge), and delete
// across every entity type that stores tags — workspaces, sessions, notes,
// and tasks. Project template manifests are read-only by design: they are
// source files (built-ins ship embedded in the binary), so renames and
// deletes never touch them; the usage endpoint reports which templates
// declare a tag so the UI can mark it as reintroducible.

// tagMutationCounts reports how many entities a rename/delete touched.
type tagMutationCounts struct {
	Workspaces int `json:"workspaces"`
	Sessions   int `json:"sessions"`
	Notes      int `json:"notes"`
	Tasks      int `json:"tasks"`
}

// HandleTagUsage handles GET /api/tags/usage?tag=… — per-source usage counts
// plus the templates that declare the tag, for confirmation dialogs.
func (h *Handler) HandleTagUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	tag := normalizeAdminTag(r.URL.Query().Get("tag"))
	if tag == "" {
		_ = orihttp.RespondBadRequest(w, "tag is required")
		return
	}

	pool, err := h.collectUnifiedTags(r.Context())
	if err != nil {
		logger.Error("Failed to collect tag usage", logger.Fields{"tag": tag, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to collect tag usage")
		return
	}

	entry := UnifiedTag{Name: tag}
	for _, candidate := range pool {
		if candidate.Name == tag {
			entry = candidate
			break
		}
	}

	orihttp.WriteJSON(w, map[string]any{
		"tag":       entry.Name,
		"counts":    entry.Counts,
		"total":     entry.Total,
		"templates": h.templatesDeclaringTag(tag),
	})
}

// HandleTagRename handles POST /api/tags/rename {"from","to"} — renames a tag
// across workspaces, sessions, notes, and tasks. Renaming onto an existing
// tag merges the two (per-entity dedupe).
func (h *Handler) HandleTagRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	from := normalizeAdminTag(req.From)
	to := normalizeAdminTag(req.To)
	if from == "" || to == "" {
		_ = orihttp.RespondBadRequest(w, "from and to are required")
		return
	}
	if utf8.RuneCountInString(to) > agentworkspace.MaxWorkspaceTagLength {
		_ = orihttp.RespondBadRequest(w, "new tag exceeds the length limit")
		return
	}
	if from == to {
		_ = orihttp.RespondBadRequest(w, "from and to are the same tag")
		return
	}

	counts, err := h.applyTagMutation(r.Context(), from, to)
	if err != nil {
		logger.Error("Failed to rename tag", logger.Fields{"from": from, "to": to, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to rename tag")
		return
	}

	logger.Info("Tag renamed", logger.Fields{
		"from": from, "to": to,
		"workspaces": counts.Workspaces, "sessions": counts.Sessions,
		"notes": counts.Notes, "tasks": counts.Tasks,
	})
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"from":    from,
		"to":      to,
		"renamed": counts,
	})
}

// HandleTagDelete handles POST /api/tags/delete {"tag"} — removes a tag from
// all workspaces, sessions, notes, and tasks.
func (h *Handler) HandleTagDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	var req struct {
		Tag string `json:"tag"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	tag := normalizeAdminTag(req.Tag)
	if tag == "" {
		_ = orihttp.RespondBadRequest(w, "tag is required")
		return
	}

	counts, err := h.applyTagMutation(r.Context(), tag, "")
	if err != nil {
		logger.Error("Failed to delete tag", logger.Fields{"tag": tag, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete tag")
		return
	}

	logger.Info("Tag deleted", logger.Fields{
		"tag":        tag,
		"workspaces": counts.Workspaces, "sessions": counts.Sessions,
		"notes": counts.Notes, "tasks": counts.Tasks,
	})
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"tag":     tag,
		"removed": counts,
	})
}

// applyTagMutation renames `from` to `to` everywhere, or removes `from`
// everywhere when `to` is empty. Sources are mutated independently and
// best-effort per entity: a failure in one source aborts with an error, but
// already-applied sources stay applied (each is individually idempotent, so
// retrying the same mutation converges).
func (h *Handler) applyTagMutation(ctx context.Context, from, to string) (tagMutationCounts, error) {
	var counts tagMutationCounts

	workspaces, err := h.mutateWorkspaceTags(ctx, from, to)
	if err != nil {
		return counts, err
	}
	counts.Workspaces = workspaces

	sessions, err := h.mutateSessionTags(ctx, from, to)
	if err != nil {
		return counts, err
	}
	counts.Sessions = sessions

	notes, err := h.mutateNoteTags(ctx, from, to)
	if err != nil {
		return counts, err
	}
	counts.Notes = notes

	counts.Tasks = h.mutateTaskTags(from, to)
	return counts, nil
}

func (h *Handler) mutateSessionTags(ctx context.Context, from, to string) (int, error) {
	if to == "" {
		return h.store.RemoveSessionTag(ctx, from)
	}
	return h.store.RenameSessionTag(ctx, from, to)
}

// mutateNoteTags renames/removes the tag in SQLite, then re-syncs the
// affected notes' markdown files so the frontmatter on disk stays truthful.
func (h *Handler) mutateNoteTags(ctx context.Context, from, to string) (int, error) {
	var (
		noteIDs []string
		err     error
	)
	if to == "" {
		noteIDs, err = h.store.RemoveNoteTag(ctx, from)
	} else {
		noteIDs, err = h.store.RenameNoteTag(ctx, from, to)
	}
	if err != nil {
		return 0, err
	}
	for _, id := range noteIDs {
		note, err := h.store.GetNote(ctx, id)
		if err != nil {
			logger.Warn("Tag mutation: failed to reload note for file sync", logger.Fields{"note_id": id, "error": err})
			continue
		}
		h.syncNoteToFile(note)
	}
	return len(noteIDs), nil
}

// mutateWorkspaceTags rewrites tags on every workspace carrying the tag.
// workspace.json is the read-canonical store whenever the SQLite row has no
// tags, so the folder store is written first; the session row mirror is
// best-effort.
func (h *Handler) mutateWorkspaceTags(ctx context.Context, from, to string) (int, error) {
	workspaces, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return 0, err
	}
	workspaces = pruneTrashedWorkspaces(workspaces)

	affected := 0
	for i := range workspaces {
		ws := &workspaces[i]
		h.hydrateWorkspaceMetadataInto(ws)
		next, changed := replaceTagInList(ws.Tags, from, to)
		if !changed {
			continue
		}
		ws.Tags = next
		ws.UpdatedAt = time.Now()
		if err := h.syncWorkspaceTagsToFileStore(ws); err != nil {
			logger.Warn("Tag mutation: failed to sync workspace tags to file store", logger.Fields{"id": ws.ID, "error": err})
		}
		if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
			logger.Warn("Tag mutation: failed to update workspace row", logger.Fields{"id": ws.ID, "error": err})
		}
		affected++
	}
	return affected, nil
}

// mutateTaskTags rewrites tags on every task carrying the tag. Tasks live
// in the folder store; Save also refreshes the task markdown files.
func (h *Handler) mutateTaskTags(from, to string) int {
	if h.workspaceStore == nil {
		return 0
	}
	ids, err := h.workspaceStore.List()
	if err != nil {
		logger.Warn("Tag mutation: failed to list folder workspaces", logger.Fields{"error": err})
		return 0
	}

	affected := 0
	for _, id := range ids {
		ws, err := h.workspaceStore.Get(id)
		if err != nil || ws == nil {
			continue
		}
		changed := false
		for i := range ws.Tasks {
			next, taskChanged := replaceTagInList(ws.Tasks[i].Tags, from, to)
			if !taskChanged {
				continue
			}
			ws.Tasks[i].Tags = next
			affected++
			changed = true
		}
		if !changed {
			continue
		}
		ws.UpdatedAt = time.Now()
		if err := h.workspaceStore.Save(ws); err != nil {
			logger.Warn("Tag mutation: failed to save workspace tasks", logger.Fields{"id": id, "error": err})
		}
	}
	return affected
}

// replaceTagInList maps `from` to `to` (or drops it when `to` is empty) and
// re-normalizes, which also merges duplicates introduced by the rename.
// Returns the new list and whether the input carried `from`.
func replaceTagInList(tags []string, from, to string) ([]string, bool) {
	normalized := agentworkspace.NormalizeWorkspaceTags(tags)
	carried := false
	next := make([]string, 0, len(normalized))
	for _, tag := range normalized {
		if tag == from {
			carried = true
			if to != "" {
				next = append(next, to)
			}
			continue
		}
		next = append(next, tag)
	}
	if !carried {
		return normalized, false
	}
	return agentworkspace.NormalizeWorkspaceTags(next), true
}

// templatesDeclaringTag returns the display names of library templates whose
// manifest declares the tag.
func (h *Handler) templatesDeclaringTag(tag string) []string {
	names := []string{}
	if h.templatesRootResolver == nil {
		return names
	}
	templates, err := projecttemplates.ListLibrary(h.templatesRootResolver())
	if err != nil {
		logger.Warn("Failed to list templates for tag usage", logger.Fields{"error": err})
		return names
	}
	for _, tpl := range templates {
		for _, declared := range tpl.Tags {
			if declared == tag {
				names = append(names, tpl.Name)
				break
			}
		}
	}
	return names
}

func normalizeAdminTag(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
