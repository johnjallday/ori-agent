package workspace

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// GroupDescendants hydrates the candidates of this owner and group only. A
// summary is not authoritative for the parent, owner, status or linked paths.
func GroupDescendants(store Store, owner, group string) (map[string]*Workspace, error) {
	listed, err := ListActiveSummaries(store)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, row := range listed {
		if row.OwnerUserID == owner {
			children[row.ParentID] = append(children[row.ParentID], row.ID)
		}
	}
	members := map[string]*Workspace{}
	parents := []string{group}
	for len(parents) > 0 {
		id := parents[0]
		parents = parents[1:]
		for _, child := range children[id] {
			if _, seen := members[child]; seen || child == group {
				continue
			}
			ws, err := store.Get(child)
			if err != nil || ws == nil || ws.ID != child || ws.OwnerUserID != owner || ws.ParentID != id || ws.Status == StatusTrashed || ws.Status == StatusMissing {
				return nil, ErrSourceUnreadable
			}
			members[child] = ws
			if ws.Kind == "group" {
				parents = append(parents, child)
			}
		}
	}
	return members, nil
}

type filesLink struct {
	WorkspaceID string          `json:"workspace_id"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Inside      *filesPlacement `json:"inside,omitempty"`
	path        string
}
type filesPlacement struct {
	WorkspaceID string `json:"workspace_id"`
	ID          string `json:"id"`
	Path        string `json:"path"`
}

// Shortest paths first: the nearest already placed enclosing reference owns
// the presentation. Only relative paths that Files can list are placements.
func placeFilesLinks(links []filesLink) {
	sort.Slice(links, func(i, j int) bool {
		a, b := links[i], links[j]
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		if a.WorkspaceID != b.WorkspaceID {
			return a.WorkspaceID < b.WorkspaceID
		}
		return a.ID < b.ID
	})
	byPath := map[string]filesLink{}
	for i := range links {
		link := &links[i]
		for p := link.path; ; p = filepath.Dir(p) {
			if parent, ok := byPath[p]; ok {
				rel, err := filepath.Rel(p, link.path)
				rel = filepath.ToSlash(rel)
				if clean, valid := ContainedRelativePath(rel); err == nil && (rel == "." || valid == nil && clean == rel) {
					link.Inside = &filesPlacement{parent.WorkspaceID, parent.ID, rel}
				}
				break
			}
			if p == filepath.Dir(p) {
				break
			}
		}
		if _, ok := byPath[link.path]; !ok {
			byPath[link.path] = *link
		}
	}
}

// GetFilesLayout only arranges references that already feed the scoped Files
// reader. It neither walks the disk nor grants access to a directory page.
func (h *HTTPHandler) GetFilesLayout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("workspaceID")
	root, err := h.store.Get(id)
	if err != nil || root == nil || root.ID != id || root.Status == StatusTrashed || root.Status == StatusMissing {
		orihttp.NotFound(w, "Workspace unavailable")
		return
	}
	owners := map[string]*Workspace{}
	if root.Kind == "group" {
		owners, err = GroupDescendants(h.store, root.OwnerUserID, id)
		if err != nil {
			orihttp.NotFound(w, "Group membership unavailable")
			return
		}
	}
	owners[id] = root
	links := []filesLink{}
	for owner := range owners {
		refs, err := h.linkedFileSources(owner)
		if err != nil {
			orihttp.NotFound(w, "Linked folders unavailable")
			return
		}
		for _, ref := range refs {
			name := strings.TrimSpace(ref.Name)
			if name == "" || filepath.IsAbs(name) {
				name = filepath.Base(ref.Path)
			}
			links = append(links, filesLink{WorkspaceID: owner, ID: ref.ID, Name: name, path: filepath.Clean(ref.Path)})
		}
	}
	placeFilesLinks(links)
	writeJSON(w, http.StatusOK, map[string]any{"folders": links, "local_mac": h.linkedDesktop && linkedOpenLocal(r)})
}
