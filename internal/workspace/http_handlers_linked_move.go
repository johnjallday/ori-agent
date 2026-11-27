package workspace

import (
	"crypto/hmac"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// The link can predate today's picker rules. Don't treat a listing from a
// legacy broad link as permission to rearrange Ori data or a protected root.
func (h *HTTPHandler) validMutationFolder(folder string) bool {
	if h.typedFolderPaths == nil {
		return false
	}
	checked, err := h.typedFolderPaths.Check(folder)
	if err != nil || !sameWorkspaceRoot(checked.Path, folder) {
		return false
	}
	if s, ok := h.folderResolver.(interface{ BasePath() string }); ok {
		base := strings.ToLower(canonicalWorkspaceRoot(s.BasePath()))
		dir := strings.ToLower(checked.Path)
		if isPathWithin(dir, base) || isPathWithin(base, dir) {
			return false
		}
	}
	return true
}

// Moving the actual directory also moves all of its contents. Refuse to
// disconnect another workspace's linked root through an ancestor operation.
// Read reference owners only on a confirmed folder mutation, never on browse.
func (h *HTTPHandler) movableLinkedFolder(ref DirectoryReference, rel string) bool {
	target := filepath.Join(ref.Path, filepath.FromSlash(rel))
	if !h.validMutationFolder(target) {
		return false
	}
	target = strings.ToLower(canonicalWorkspaceRoot(target))
	catalog, ok := h.folderWorkspaceResolver.(interface{ List() ([]string, error) })
	if !ok {
		return false
	}
	ids, err := catalog.List()
	if err != nil {
		return false
	}
	for _, id := range ids {
		ws, err := h.folderWorkspaceResolver.GetFolderWorkspace(id)
		if err != nil || ws == nil {
			return false
		}
		if ws.Status == StatusTrashed || ws.Status == StatusMissing {
			continue
		}
		for _, other := range ws.DirectoryReferences {
			if filepath.IsAbs(other.Path) && isPathWithin(strings.ToLower(canonicalWorkspaceRoot(other.Path)), target) {
				return false
			}
		}
	}
	return true
}

// Reopen the canonical chain after the protection checks: a held directory
// remains addressable even if it was moved away from the linked root.
func linkedParentStillHere(path, rootID, rel string, held *os.Root) bool {
	root, id, err := openLinkedRoot(path)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	if id != rootID {
		return false
	}
	parent, _, release, err := containedParent(root, rel)
	if err != nil {
		return false
	}
	defer release()
	a, err := parent.Stat(".")
	if err != nil {
		return false
	}
	b, err := held.Stat(".")
	return err == nil && os.SameFile(a, b)
}

func (h *HTTPHandler) moveToLinkedDestination(w http.ResponseWriter, ref DirectoryReference, root *os.Root, rootID string, parent *os.Root, rel string, version SourceVersion, folder bool, dest, token string) {
	stale := func() { orihttp.Conflict(w, "Destination changed or expired. Refresh the folders and select again.") }
	expires, ok := linkedExpiry(token, time.Now())
	if !ok {
		stale()
		return
	}
	dir := root
	if dest != "" {
		clean, err := ContainedRelativePath(dest)
		if err != nil || clean != dest {
			stale()
			return
		}
		p, name, release, err := containedParent(root, dest)
		if err != nil {
			stale()
			return
		}
		defer release()
		dir, err = openContainedDir(p, name)
		if err != nil {
			stale()
			return
		}
		defer func() { _ = dir.Close() }()
	}
	info, err := dir.Stat(".")
	if err != nil || !info.IsDir() || sourceVersion(info).Placeholder {
		stale()
		return
	}
	id := directoryid.Witness(info)
	if id == "" || !hmac.Equal([]byte(token), []byte(h.destinationSelection(ref, rootID, dest, id, expires))) {
		stale()
		return
	}
	if dest != "" && !h.validMutationFolder(filepath.Join(ref.Path, filepath.FromSlash(dest))) {
		orihttp.Forbidden(w, "That destination is protected.")
		return
	}
	if p, err := parent.Stat("."); err != nil || os.SameFile(p, info) {
		orihttp.Conflict(w, "The item is already in that folder.")
		return
	}
	if folder && isPathWithin(strings.ToLower(filepath.Join(ref.Path, dest)), strings.ToLower(filepath.Join(ref.Path, rel))) {
		orihttp.Conflict(w, "A folder cannot move inside itself.")
		return
	}
	// The final component here stands only for the child, not something
	// opened or created. Check that both held parents remain under this root.
	if !linkedParentStillHere(ref.Path, rootID, rel, parent) || !linkedParentStillHere(ref.Path, rootID, path.Join(dest, path.Base(rel)), dir) {
		stale()
		return
	}
	err = moveLinkedItem(parent, path.Base(rel), dir, version, folder)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"message": "Moved."})
	case errors.Is(err, os.ErrExist):
		orihttp.Conflict(w, "An item with that name already exists in the destination. Nothing was replaced.")
	case errors.Is(err, ErrSourceChanged):
		stale()
	default:
		orihttp.Conflict(w, "Could not move the item. Check folder permissions and that both folders are on the same disk.")
	}
}
