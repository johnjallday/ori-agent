package workspace

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/platform"
)

const linkedSelectionTTL = 5 * time.Minute

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

func linkedDocumentPath(path string) bool {
	rel, err := ContainedRelativePath(path)
	return err == nil && rel == path && utf8.ValidString(path) && strings.EqualFold(filepath.Ext(path), ".rpp")
}

// Sign the observed facts, not just the filename. Only the expiry and MAC go
// on the wire; no registry of old listings grows with browser activity.
func (h *HTTPHandler) linkedSelection(ref DirectoryReference, rootID string, entry DirectoryEntry, expires int64) string {
	v := entry.Version
	if len(h.linkedSelectionKey) == 0 || rootID == "" || !linkedDocumentPath(entry.RelativePath) ||
		!entry.mode.IsRegular() || entry.mode.Perm()&0o111 != 0 || v.Identity == "" || v.Links != 1 || v.Placeholder {
		return ""
	}
	if !ordinaryDesktopDocument(filepath.Join(ref.Path, filepath.FromSlash(entry.RelativePath)), v) {
		return ""
	}
	facts, err := json.Marshal([]any{expires, ref.WorkspaceID, ref.ID, ref.Path, ref.CreatedAt.UnixNano(), ref.UpdatedAt.UnixNano(), rootID, entry.RelativePath, v})
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, h.linkedSelectionKey)
	_, _ = mac.Write(facts)
	return strconv.FormatInt(expires, 10) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *HTTPHandler) resolveLinkedSelection(ref DirectoryReference, rel, token string, now time.Time) (string, bool) {
	prefix, _, found := strings.Cut(token, ".")
	expires, err := strconv.ParseInt(prefix, 10, 64)
	if !found || err != nil || expires <= now.Unix() || expires > now.Add(linkedSelectionTTL).Unix() || !linkedDocumentPath(rel) {
		return "", false
	}
	root, rootID, err := openLinkedRoot(ref.Path)
	if err != nil {
		return "", false
	}
	defer func() { _ = root.Close() }()
	parent, name, release, err := containedParent(root, rel)
	if err != nil {
		return "", false
	}
	defer release()
	info, err := parent.Lstat(name)
	if err != nil {
		return "", false
	}
	want := h.linkedSelection(ref, rootID, DirectoryEntry{RelativePath: rel, Version: sourceVersion(info), mode: info.Mode()}, expires)
	if want == "" || !hmac.Equal([]byte(token), []byte(want)) {
		return "", false
	}
	return filepath.Join(ref.Path, filepath.FromSlash(rel)), true
}

// OpenLinkedFile dispatches only a previously listed regular project document
// in the owner's currently linked folder. The OS accepts a path, not a file
// descriptor: it cannot guarantee identity against a concurrent external
// rename after our final check, or report what happens inside the default app.
func (h *HTTPHandler) OpenLinkedFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !exactLocalOrigin(r) {
		orihttp.Forbidden(w, "Open this project from Files on the local Mac")
		return
	}
	if !h.linkedDesktop || h.openFile == nil {
		orihttp.ServiceUnavailable(w, "Opening a linked project requires a local Mac")
		return
	}
	var req struct {
		Path      string `json:"path"`
		Selection string `json:"selection"`
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if r.URL.RawQuery != "" || err != nil || kind != "application/json" || d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || req.Selection == "" {
		orihttp.BadRequest(w, "A listed file selection is required")
		return
	}
	ref, err := h.linkedFileSource(r.PathValue("workspaceID"), r.PathValue("dirId"))
	if err != nil {
		orihttp.NotFound(w, "Linked folder unavailable. Refresh Files and select the file again.")
		return
	}
	path, ok := h.resolveLinkedSelection(ref, req.Path, req.Selection, time.Now())
	if !ok {
		orihttp.Conflict(w, "Selection changed or expired. Refresh Files and select the file again.")
		return
	}
	if err := h.openFile(path); err != nil {
		orihttp.InternalError(w, "Could not open this file. Check its default application on this Mac.")
		return
	}
	message := "Open request accepted."
	if platform.DesktopOpenDisabled() {
		message = "Sandbox dispatch accepted. No application was opened."
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": message})
}
