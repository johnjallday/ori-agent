package sessionhttp

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/examples/blueprints"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type courseCreateKey struct{}
type courseCreate struct{ id, owner string }

func (h *Handler) ignoredProjects(ctx context.Context, id string) (map[string]bool, error) {
	rows, err := h.store.DB().QueryContext(ctx, `SELECT folder_ref FROM ignored_documents_projects WHERE manager_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	refs := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs[ref] = true
	}
	return refs, rows.Err()
}

// Open the already-owned folder if present. Otherwise create a managed workspace
// with an identity derived from the witnessed choice. A lost response or racing
// tab must not prompt a second hire; no source path is a creation input.
func (h *Handler) openCourse(r *http.Request, manager string, d folderagent.ProjectDiscovery, c folderagent.CourseSuggestion) (string, error) {
	ws, err := h.workspaceStore.Get(manager)
	if err != nil {
		return "", err
	}
	owner := ws.OwnerUserID
	route := func(id string, setup bool) (string, error) {
		w, e := h.workspaceStore.Get(id)
		if e != nil || w == nil || !d.Current(c) || w.ID != id || w.FolderSlug == "" {
			return "", folderagent.ErrWrongState
		}
		// Ordinary and legacy local workspace creation can leave the owner blank.
		actualOwner := w.OwnerUserID
		if actualOwner == "" {
			actualOwner = userprofile.LocalUserID
		}
		if status := w.GetStatus(); actualOwner != owner || (status != "" && status != workspace.StatusActive) {
			return "", folderagent.ErrWrongState
		}
		p := "/workspaces/" + url.PathEscape(w.FolderSlug)
		if setup {
			p += "?setup=1"
		}
		return p, nil
	}
	occupied, err := projectconnection.FindFolderOwner(h.workspaceStore, filepath.Join(d.Root, filepath.FromSlash(c.Folder)), manager)
	if err != nil {
		return "", err
	}
	if occupied != nil {
		return route(occupied.WorkspaceID, false)
	}
	id := uuid.NewHash(sha256.New(), uuid.NameSpaceOID, []byte("ori-course-v1:"+owner+":"+c.Identity), 8).String()
	ids, err := h.workspaceStore.List()
	if err != nil {
		return "", err
	}
	if slices.Contains(ids, id) {
		return route(id, true)
	}
	temp, err := os.MkdirTemp("", "ori-course-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	source, err := blueprints.StageCourse(temp)
	if err != nil {
		return "", err
	}
	if current, ok := h.projectDiscovery(r, manager); !ok || current != d || !d.Current(c) {
		return "", folderagent.ErrWrongState
	}
	ctx := context.WithValue(r.Context(), courseCreateKey{}, courseCreate{id, owner})
	created, err := h.createFromTemplate(ctx, createWorkspaceRequest{Name: c.Name(), TemplatePath: source})
	if err != nil {
		return "", err
	}
	if created != id {
		return "", errors.New("course workspace identity changed")
	}
	return route(created, true)
}
