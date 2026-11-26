package workspace

import (
	"net/http"
	"path/filepath"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// linkedFileSources reads portable, owner-scoped references, not the label of
// a group or the workspace's primary project locator. A missing external root
// is still a source: its disclosure should offer retry, not imply emptiness.
func (h *HTTPHandler) linkedFileSources(owner string) ([]DirectoryReference, error) {
	if h.folderResolver == nil || h.folderWorkspaceResolver == nil {
		return nil, ErrSourceUnreadable
	}
	ws, err := h.folderWorkspaceResolver.GetFolderWorkspace(owner)
	if err != nil || ws == nil || ws.ID != owner || ws.Status == StatusTrashed || ws.Status == StatusMissing {
		return nil, ErrSourceMissing
	}
	own, err := h.folderResolver.GetFolderPath(owner)
	if err != nil || !filepath.IsAbs(own) {
		return nil, ErrSourceUnreadable
	}
	var sources []DirectoryReference
	for _, ref := range ws.DirectoryReferences {
		if ref.ID == "" || ref.WorkspaceID != owner || ref.Purpose != "" || !filepath.IsAbs(ref.Path) || isPathWithin(ref.Path, own) {
			continue
		}
		sources = append(sources, ref)
	}
	return sources, nil
}

func (h *HTTPHandler) addLinkedFileSources(owner string, response map[string]any) {
	sources, err := h.linkedFileSources(owner)
	if err != nil {
		response["linked_sources_unavailable"] = true
		return
	}
	type source struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]source, 0, len(sources))
	for _, ref := range sources {
		out = append(out, source{ID: ref.ID, Name: ref.Name})
	}
	response["linked_sources"] = out
}

func (h *HTTPHandler) linkedFileSource(owner, id string) (DirectoryReference, error) {
	sources, err := h.linkedFileSources(owner)
	if err != nil {
		return DirectoryReference{}, err
	}
	for _, source := range sources {
		if source.ID == id {
			return source, nil
		}
	}
	return DirectoryReference{}, ErrSourceMissing
}

// ListLinkedFilePage lists only the disclosed directory. Neither this action
// nor the source summary registers attachments, reads documents or opens apps.
func (h *HTTPHandler) ListLinkedFilePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	source, err := h.linkedFileSource(r.PathValue("workspaceID"), r.PathValue("dirId"))
	if err != nil {
		orihttp.NotFound(w, "Linked folder is no longer available")
		return
	}
	listing, err := listContainedPage(source.Path, r.URL.Query().Get("path"))
	if err != nil {
		orihttp.NotFound(w, "Folder unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": listing.Entries, "partial": listing.Truncated})
}
