package workspace

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/platform"
)

const linkedSelectionTTL = 5 * time.Minute

// Distinguish a refusal opening the Trash directory from a refusal to move
// an item: granting access to Downloads does not grant access to Trash.
var errTrashDirectoryDenied = errors.New("access to Trash directory denied")

func writeLinkedTrashError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTrashDirectoryDenied):
		writeJSON(w, http.StatusForbidden, orihttp.APIError{Code: "trash_directory_denied", Message: "macOS denied access to Trash. This item was not moved."})
	case errors.Is(err, syscall.EXDEV):
		writeJSON(w, http.StatusUnprocessableEntity, orihttp.APIError{Code: "trash_different_disk", Message: "This item is on a different disk from your home Trash. Use Finder’s Move to Trash on the original disk instead."})
	case errors.Is(err, os.ErrPermission):
		writeJSON(w, http.StatusForbidden, orihttp.APIError{Code: "trash_access_denied", Message: "macOS denied access during the move to Trash."})
	default:
		orihttp.InternalError(w, "Could not confirm the move to Trash. Refresh the folder and check the item in Finder.")
	}
}

func linkedOpenLocal(r *http.Request) bool {
	return projectOpenRequestIsLoopback(r) && connectionshttp.NewOriginGuard().Check(r) == nil
}

func exactLocalOrigin(r *http.Request) bool {
	scheme := "http://"
	if r.TLS != nil {
		scheme = "https://"
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return linkedOpenLocal(r) && (site == "" || site == "same-origin") && r.Header.Get("Origin") == scheme+r.Host
}

// Each capability is tied to a listed version and a single operation. In
// particular, a read or desktop-launch selection cannot authorize removal.
type linkedActions struct {
	Selection            string `json:"selection,omitempty"`
	OpenLabel            string `json:"open_label,omitempty"`
	useDefaultApp        bool   // only the project-marker policy can enable a default launch
	PreviewSelection     string `json:"preview_selection,omitempty"`
	TrashSelection       string `json:"trash_selection,omitempty"`
	MoveSelection        string `json:"move_selection,omitempty"`
	DestinationSelection string `json:"destination_selection,omitempty"`
	ColorKey             string `json:"color_key,omitempty"` // presentation only; never accepted by action routes
}

func linkedTextFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".md", ".markdown", ".log", ".csv", ".tsv", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".ini", ".xml", ".html", ".htm", ".css", ".js", ".mjs", ".ts", ".jsx", ".tsx", ".go", ".py", ".rb", ".rs", ".java", ".c", ".h", ".cpp", ".sh", ".bash", ".zsh", ".sql", ".lua":
		return true
	}
	return false
}

func linkedRaster(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// Sign the facts from one no-follow stat. Only the expiry and MAC go on the
// wire. The server chooses the reader; a script is data, not an application.
func (h *HTTPHandler) linkedActions(ref DirectoryReference, rootID string, entry DirectoryEntry, expires int64) linkedActions {
	var actions linkedActions
	rel, err := ContainedRelativePath(entry.RelativePath)
	v := entry.Version
	if err != nil || rel != entry.RelativePath || !utf8.ValidString(rel) || len(h.linkedSelectionKey) == 0 || rootID == "" ||
		v.Identity == "" || v.Placeholder {
		return actions
	}
	sign := func(action string) string { return h.signLinked(ref, rootID, rel, action, v, expires) }
	if entry.mode.IsDir() {
		if entry.folderVerified {
			// No filename, path, raw inode or expiring authority in browser preferences.
			key, _ := json.Marshal([]string{ref.WorkspaceID, ref.ID, rootID, v.Identity})
			sum := sha256.Sum256(key)
			actions.ColorKey = base64.RawURLEncoding.EncodeToString(sum[:])
		}
		if entry.folderVerified && h.typedFolderPaths != nil {
			actions.TrashSelection = sign("trash-folder")
			actions.MoveSelection = sign("move-folder")
			actions.DestinationSelection = h.destinationSelection(ref, rootID, rel, v.Identity, expires)
		}
		return actions
	}
	if !entry.mode.IsRegular() || v.Links != 1 || !ordinaryDesktopDocument(filepath.Join(ref.Path, filepath.FromSlash(rel)), v) {
		return actions
	}
	if h.typedFolderPaths != nil {
		actions.MoveSelection = sign("move-file")
	}
	actions.TrashSelection = sign("trash")
	text := linkedTextFile(rel)
	if text || linkedRaster(rel) != "" {
		actions.PreviewSelection = sign("preview")
	}
	marker, _ := folderdigest.MatchMarker(filepath.Base(rel), false)
	switch {
	case text:
		actions.OpenLabel = "Open in editor"
	case entry.mode.Perm()&0o111 != 0:
		return actions
	case marker.LinkedDesktopDocument:
		actions.OpenLabel = "Open in default app"
		actions.useDefaultApp = true
	case platform.ReviewDocumentViewer(rel) != "" || linkedRaster(rel) != "":
		actions.OpenLabel = "Open in viewer"
	default:
		return actions
	}
	actions.Selection = sign("open")
	return actions
}

// Destination tokens bind directory identity, not its modification time: the
// first confirmed sibling move changes that time without replacing the folder.
func (h *HTTPHandler) destinationSelection(ref DirectoryReference, rootID, rel, identity string, expires int64) string {
	return h.signLinked(ref, rootID, rel, "move-into", identity, expires)
}

func (h *HTTPHandler) signLinked(ref DirectoryReference, rootID, rel, action string, version any, expires int64) string {
	if len(h.linkedSelectionKey) == 0 {
		return ""
	}
	facts, err := json.Marshal([]any{action, expires, ref.WorkspaceID, ref.ID, ref.Path, ref.CreatedAt.UnixNano(), ref.UpdatedAt.UnixNano(), rootID, rel, version})
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, h.linkedSelectionKey)
	_, _ = mac.Write(facts)
	return strconv.FormatInt(expires, 10) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func linkedExpiry(token string, now time.Time) (int64, bool) {
	prefix, _, found := strings.Cut(token, ".")
	expires, err := strconv.ParseInt(prefix, 10, 64)
	return expires, found && err == nil && expires > now.Unix() && expires <= now.Add(linkedSelectionTTL).Unix()
}

func (h *HTTPHandler) OpenLinkedFile(w http.ResponseWriter, r *http.Request) {
	h.linkedAction(w, r, "open")
}
func (h *HTTPHandler) PreviewLinkedFile(w http.ResponseWriter, r *http.Request) {
	h.linkedAction(w, r, "preview")
}
func (h *HTTPHandler) TrashLinkedFile(w http.ResponseWriter, r *http.Request) {
	h.linkedAction(w, r, "trash")
}

func (h *HTTPHandler) TrashLinkedFolder(w http.ResponseWriter, r *http.Request) {
	h.linkedAction(w, r, "trash-folder")
}
func (h *HTTPHandler) MoveLinkedItem(w http.ResponseWriter, r *http.Request) {
	h.linkedAction(w, r, "move-item")
}

func (h *HTTPHandler) linkedAction(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "no-store")
	if !exactLocalOrigin(r) {
		orihttp.Forbidden(w, "Use Files on the local Mac for this action")
		return
	}
	if !h.linkedDesktop {
		orihttp.ServiceUnavailable(w, "This action requires a local Mac")
		return
	}
	var req struct {
		Path                 string  `json:"path"`
		Selection            string  `json:"selection"`
		Destination          *string `json:"destination"`
		DestinationSelection string  `json:"destination_selection"`
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if r.URL.RawQuery != "" || err != nil || kind != "application/json" || d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || req.Selection == "" {
		orihttp.BadRequest(w, "A listed file selection is required")
		return
	}
	if (action == "move-item") != (req.Destination != nil) || (action == "move-item") != (req.DestinationSelection != "") {
		orihttp.BadRequest(w, "Choose a listed destination for a move")
		return
	}
	ref, err := h.linkedFileSource(r.PathValue("workspaceID"), r.PathValue("dirId"))
	if err != nil {
		orihttp.NotFound(w, "Linked folder unavailable. Refresh Files and select the file again.")
		return
	}
	stale := func() { orihttp.Conflict(w, "Selection changed or expired. Refresh Files and select the file again.") }
	rel, err := ContainedRelativePath(req.Path)
	expires, valid := linkedExpiry(req.Selection, time.Now())
	if !valid || err != nil || rel != req.Path {
		stale()
		return
	}
	root, rootID, err := openLinkedRoot(ref.Path)
	if err != nil {
		stale()
		return
	}
	defer func() { _ = root.Close() }()
	parent, name, release, err := containedParent(root, rel)
	if err != nil {
		stale()
		return
	}
	defer release()
	info, err := parent.Lstat(name)
	if err != nil {
		stale()
		return
	}
	version := sourceVersion(info)
	entry := DirectoryEntry{RelativePath: rel, Version: version, mode: info.Mode()}
	if info.IsDir() {
		if action != "trash-folder" && action != "move-item" {
			stale()
			return
		}
		sub, err := openContainedDir(parent, name)
		if err != nil {
			stale()
			return
		}
		defer func() { _ = sub.Close() }()
		seen, err := sub.Stat(".")
		entry.folderVerified = err == nil && sourceVersion(seen) == version
	} else if action == "trash-folder" {
		stale()
		return
	}
	actions := h.linkedActions(ref, rootID, entry, expires)
	token := map[string]string{"open": actions.Selection, "preview": actions.PreviewSelection, "trash": actions.TrashSelection, "trash-folder": actions.TrashSelection, "move-item": actions.MoveSelection}[action]
	if token == "" || !hmac.Equal([]byte(token), []byte(req.Selection)) {
		stale()
		return
	}
	if action == "move-item" || action == "trash-folder" {
		if !h.validMutationFolder(ref.Path) || (info.IsDir() && !h.movableLinkedFolder(ref, rel)) {
			orihttp.Forbidden(w, "This folder is protected or in use by a workspace. Nothing was moved.")
			return
		}
		fresh, err := h.linkedFileSource(ref.WorkspaceID, ref.ID)
		if err != nil || fresh != ref || time.Now().Unix() >= expires || r.Context().Err() != nil {
			stale()
			return
		}
	}
	switch action {
	case "move-item":
		h.moveToLinkedDestination(w, ref, root, rootID, parent, rel, version, info.IsDir(), *req.Destination, req.DestinationSelection)
	case "preview":
		writeLinkedPreview(w, ref, rel, rootID, version)
	case "trash", "trash-folder":
		if !linkedParentStillHere(ref.Path, rootID, rel, parent) {
			stale()
			return
		}
		if err := trashLinkedItem(parent, name, version, info.IsDir()); err != nil {
			if errors.Is(err, ErrSourceChanged) {
				stale()
				return
			}
			writeLinkedTrashError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Moved to Trash."})
	case "open":
		path := filepath.Join(ref.Path, filepath.FromSlash(rel))
		message := "Open request accepted."
		if platform.DesktopOpenDisabled() {
			message = "Sandbox dispatch accepted. No application was opened."
		} else {
			opener := h.openLinkedReader
			if actions.useDefaultApp {
				opener = h.openFile
			}
			err = callDesktopOpener(opener, path)
			if err != nil {
				orihttp.InternalError(w, "Could not open this file on this Mac.")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": message})
	}
}

func openLinkedReader(path string) error {
	if linkedTextFile(path) {
		return platform.OpenTextDocument(path)
	}
	if linkedRaster(path) != "" {
		return platform.OpenImageDocument(path)
	}
	return platform.OpenReviewDocument(path)
}

func writeLinkedPreview(w http.ResponseWriter, ref DirectoryReference, rel, rootID string, version SourceVersion) {
	limit := int64(512 << 10)
	imageType := linkedRaster(rel)
	if imageType != "" {
		limit = 8 << 20
	}
	file, err := ReadVersionedFile(ref.Path, rel, limit, rootID, version)
	if errors.Is(err, ErrSourceTooLarge) {
		writeJSON(w, http.StatusOK, map[string]bool{"tooLarge": true})
		return
	}
	if err != nil {
		orihttp.Conflict(w, "File unavailable or changed. Refresh Files and select it again.")
		return
	}
	if imageType != "" && http.DetectContentType(file.Data) == imageType {
		writeJSON(w, http.StatusOK, map[string]string{"kind": "image", "url": "data:" + imageType + ";base64," + base64.StdEncoding.EncodeToString(file.Data)})
		return
	}
	if linkedTextFile(rel) && utf8.Valid(file.Data) && !bytes.ContainsRune(file.Data, 0) {
		writeJSON(w, http.StatusOK, map[string]string{"kind": "text", "text": string(file.Data)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kind": "none"})
}
