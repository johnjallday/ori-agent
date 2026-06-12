package sessionhttp

import (
	"context"
	"sort"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// UnifiedTagCounts breaks a tag's usage down by source.
type UnifiedTagCounts struct {
	Workspaces int `json:"workspaces"`
	Sessions   int `json:"sessions"`
	Notes      int `json:"notes"`
	Tasks      int `json:"tasks"`
	Templates  int `json:"templates"`
}

// UnifiedTag is one entry of the app-wide tag pool exposed by
// GET /api/tags?scope=all.
type UnifiedTag struct {
	Name   string           `json:"name"`
	Counts UnifiedTagCounts `json:"counts"`
	Total  int              `json:"total"`
}

// collectUnifiedTags aggregates the app-wide tag pool: workspace tags (SQLite
// rows hydrated from workspace.json so disk-only tags count too), session
// tags, note tags, task tags (folder store), and project template tags.
// Counts are per-entity — a workspace carrying a tag counts once — keyed by
// the normalized tag value. Optional sources (folder store, templates
// library) degrade to zero counts instead of failing the whole pool.
func (h *Handler) collectUnifiedTags(ctx context.Context) ([]UnifiedTag, error) {
	pool := map[string]*UnifiedTag{}
	entry := func(name string) *UnifiedTag {
		if e, ok := pool[name]; ok {
			return e
		}
		e := &UnifiedTag{Name: name}
		pool[name] = e
		return e
	}

	// Session and note tags are stored normalized with usage counts.
	sessionTags, err := h.store.GetAllTags(ctx)
	if err != nil {
		return nil, err
	}
	for _, tag := range sessionTags {
		entry(tag.Name).Counts.Sessions += tag.UsageCount
	}

	noteTags, err := h.store.GetAllNoteTags(ctx)
	if err != nil {
		return nil, err
	}
	for _, tag := range noteTags {
		entry(tag.Name).Counts.Notes += tag.UsageCount
	}

	// Workspace tags use the same source as the workspace listing, so the
	// pool matches what the UI shows (trashed workspaces excluded).
	workspaces, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	workspaces = pruneTrashedWorkspaces(workspaces)
	workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)
	var countWorkspace func(ws *session.Workspace)
	countWorkspace = func(ws *session.Workspace) {
		for _, tag := range agentworkspace.NormalizeWorkspaceTags(ws.Tags) {
			entry(tag).Counts.Workspaces++
		}
		for i := range ws.Children {
			countWorkspace(&ws.Children[i])
		}
	}
	for i := range workspaces {
		countWorkspace(&workspaces[i])
	}

	// Task tags live only in the folder store.
	if h.workspaceStore != nil {
		ids, err := h.workspaceStore.List()
		if err != nil {
			logger.Warn("Unified tags: failed to list folder workspaces for task tags", logger.Fields{"error": err})
		} else {
			for _, id := range ids {
				ws, err := h.workspaceStore.Get(id)
				if err != nil || ws == nil {
					continue
				}
				for _, task := range ws.Tasks {
					for _, tag := range agentworkspace.NormalizeWorkspaceTags(task.Tags) {
						entry(tag).Counts.Tasks++
					}
				}
			}
		}
	}

	// Template tags are normalized when manifests load.
	if h.templatesRootResolver != nil {
		templates, err := projecttemplates.ListLibrary(h.templatesRootResolver())
		if err != nil {
			logger.Warn("Unified tags: failed to list templates library", logger.Fields{"error": err})
		} else {
			for _, tpl := range templates {
				for _, tag := range tpl.Tags {
					entry(tag).Counts.Templates++
				}
			}
		}
	}

	result := make([]UnifiedTag, 0, len(pool))
	for _, e := range pool {
		e.Total = e.Counts.Workspaces + e.Counts.Sessions + e.Counts.Notes + e.Counts.Tasks + e.Counts.Templates
		result = append(result, *e)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Total != result[j].Total {
			return result[i].Total > result[j].Total
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
