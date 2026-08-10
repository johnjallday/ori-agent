package agenthttp

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	// AvatarDir is the directory where uploaded agent images are stored.
	//
	// The internal directory name is deliberately unchanged even though the
	// product vocabulary moved to "appearance": renaming a directory full of
	// user files to match new copy is churn with a data-loss risk and no user
	// benefit (PRD FR-65).
	AvatarDir = agent.AppearanceUploadDir
	// MaxAvatarSize is the maximum accepted upload size (5 MB), unchanged from
	// the previous contract (FR-63).
	MaxAvatarSize = 5 << 20
	// maxUploadOverhead is the slack allowed above MaxAvatarSize for the
	// multipart envelope: boundaries, part headers, and the handful of small
	// text fields the guards travel in. Generous enough never to reject a
	// legitimate 5 MB image, small enough that the body stays bounded.
	maxUploadOverhead = 1 << 20

	// appearanceUploadSuffix is the exact path tail this handler serves.
	appearanceUploadSuffix = "/appearance/upload"
	agentPathPrefix        = "/api/agents/"
)

// allowedImageTypes maps a *content-sniffed* type to the extension the server
// will use. Sniffing rather than trusting the declared type or the client's
// filename is what keeps an SVG or a video out regardless of what it claims to
// be (FR-63).
var allowedImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// AppearanceUploadHandler serves POST and DELETE on
// /api/agents/{name}/appearance/upload.
type AppearanceUploadHandler struct {
	State          store.Store
	workspaceStore workspace.Store
}

// NewAppearanceUploadHandler creates the handler. The workspace store is used
// only for the shared-definition guard, and a nil store simply means no agent
// is attached to anything.
func NewAppearanceUploadHandler(state store.Store, workspaceStore workspace.Store) *AppearanceUploadHandler {
	return &AppearanceUploadHandler{State: state, workspaceStore: workspaceStore}
}

// IsAppearanceUploadPath reports whether p is exactly an appearance-upload path
// for some agent.
//
// It matches the exact tail rather than a substring. The route this replaces
// used `strings.Contains(path, "/avatar")`, which also matched any agent whose
// name happened to contain that word — and a substring test is how a removed
// route keeps working by accident (FR-60/FR-62).
func IsAppearanceUploadPath(p string) bool {
	return appearanceUploadAgentName(p) != ""
}

// appearanceUploadAgentName extracts the agent name, or "" when p is not an
// appearance-upload path.
func appearanceUploadAgentName(p string) string {
	rest, ok := strings.CutPrefix(p, agentPathPrefix)
	if !ok {
		return ""
	}
	name, ok := strings.CutSuffix(rest, appearanceUploadSuffix)
	if !ok {
		return ""
	}
	// The remainder must be a single path segment: an agent name, not a deeper
	// path that merely ends the right way.
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

func (h *AppearanceUploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	agentName := appearanceUploadAgentName(r.URL.Path)
	if agentName == "" {
		orihttp.BadRequest(w, "Invalid appearance upload path")
		return
	}
	// Agent names may legally contain spaces, so the segment arrives escaped.
	if decoded, err := url.PathUnescape(agentName); err == nil {
		agentName = decoded
	}

	switch r.Method {
	case http.MethodPost:
		h.upload(w, r, agentName)
	case http.MethodDelete:
		h.remove(w, r, agentName)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// upload handles POST /api/agents/{name}/appearance/upload.
//
// Saving the file and activating Upload are one server operation. Storing an
// image without making it the rendered source reproduces exactly the confusion
// this feature removes: the user uploads a picture and nothing appears to
// happen (FR-36).
func (h *AppearanceUploadHandler) upload(w http.ResponseWriter, r *http.Request, agentName string) {
	ag, ok := h.State.GetAgent(agentName)
	if !ok || ag == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	// ParseMultipartForm's argument caps only what is held in memory — the rest
	// spills to disk, so on its own it bounds nothing. MaxBytesReader is what
	// actually stops a caller streaming far more than the limit; the small
	// headroom covers the multipart envelope around a 5 MB image (FR-63).
	r.Body = http.MaxBytesReader(w, r.Body, MaxAvatarSize+maxUploadOverhead)
	// #nosec G120 -- the body is bounded by the MaxBytesReader immediately
	// above, which the rule does not recognise. It fires on every
	// ParseMultipartForm in the repository; this is the only caller that
	// actually caps the request body rather than just the in-memory portion.
	if err := r.ParseMultipartForm(MaxAvatarSize); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("File too large or invalid form: %v", err))
		return
	}

	// The concurrency and shared-definition guards run before a single byte is
	// written. A multipart request carries them as form fields because it has no
	// JSON body to put them in (FR-16/FR-41/FR-42).
	if !h.checkMutationGuards(w, r, ag, agentName) {
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		orihttp.BadRequest(w, "No image file provided (expected multipart field \"image\")")
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > MaxAvatarSize {
		orihttp.BadRequest(w, "File too large (max 5MB)")
		return
	}

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to read file", err)
		return
	}
	contentType := http.DetectContentType(buffer[:n])
	ext, allowed := allowedImageTypes[contentType]
	if !allowed {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid image type: %s. Allowed: PNG, JPG, GIF, WebP", contentType))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to process file", err)
		return
	}

	// 0o750, not 0o755: nothing outside this process needs to read the
	// directory, and the static route serves its contents anyway.
	if err := os.MkdirAll(AvatarDir, 0o750); err != nil {
		logger.Error("Failed to create avatar directory", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to create avatar directory", err)
		return
	}

	// Every write below goes through an os.Root confined to the avatar
	// directory. The filenames here are already server-generated, so this is
	// belt and braces — but it makes the confinement enforced by the runtime
	// rather than asserted by the code that builds the names (FR-64).
	root, err := os.OpenRoot(AvatarDir)
	if err != nil {
		logger.Error("Failed to open avatar directory", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save image", err)
		return
	}
	defer func() { _ = root.Close() }()

	// The filename is derived entirely from the agent name and the sniffed
	// type. The client's original path is never consulted, so there is nothing
	// to traverse with (FR-64).
	filename := appearanceUploadFilename(agentName, ext)
	previous := ag.Appearance.UploadedImage()

	// Stream to a temporary file first, then rename into place. The previous
	// image is only removed after the replacement is durably written and the
	// record is saved — a failure anywhere before that leaves the agent showing
	// exactly what it showed before (FR-37).
	tmpName := filename + ".upload.tmp"
	if err := writeUploadFile(root, tmpName, file); err != nil {
		_ = root.Remove(tmpName)
		logger.Error("Failed to write uploaded image", logger.Fields{"error": err, "agent": agentName})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save image", err)
		return
	}
	if err := root.Rename(tmpName, filename); err != nil {
		_ = root.Remove(tmpName)
		logger.Error("Failed to install uploaded image", logger.Fields{"error": err, "agent": agentName})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save image", err)
		return
	}

	ag.EnsureAppearance()
	restore := ag.Appearance.Clone()
	ag.Appearance.SetUpload(filename)

	if err := h.State.SetAgent(agentName, ag); err != nil {
		// The record is the source of truth, so a failed save means the upload
		// did not happen. Roll the in-memory record back and delete only the
		// file this request created — never the one it was replacing.
		ag.Appearance = restore
		if filename != previous {
			_ = root.Remove(filename)
		}
		logger.Error("Failed to save agent appearance", logger.Fields{"error": err, "agent": agentName})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
		return
	}

	// Only now is the old file expendable. A different extension means the
	// replacement landed at a different filename, so the previous one is still
	// on disk and unreferenced.
	if previous != "" && previous != filename {
		removeAppearanceUpload(previous)
	}

	logger.Info("Appearance image uploaded", logger.Fields{"agent": agentName, "filename": filename})
	orihttp.WriteJSON(w, map[string]any{
		"success":    true,
		"appearance": appearanceForAgent(ag),
		"image_url":  "/avatars/" + filename,
		"message":    "Image uploaded",
	})
}

// remove handles DELETE /api/agents/{name}/appearance/upload.
//
// It returns to Generated only when Upload was the source actually rendering.
// Deleting an image the agent was not displaying leaves the active mode — and
// the saved character and colour — alone (FR-38/FR-39/FR-40).
func (h *AppearanceUploadHandler) remove(w http.ResponseWriter, r *http.Request, agentName string) {
	ag, ok := h.State.GetAgent(agentName)
	if !ok || ag == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}
	if !h.checkMutationGuards(w, r, ag, agentName) {
		return
	}

	ag.EnsureAppearance()
	previous := ag.Appearance.UploadedImage()
	restore := ag.Appearance.Clone()
	ag.Appearance.ClearUpload()

	if err := h.State.SetAgent(agentName, ag); err != nil {
		ag.Appearance = restore
		logger.Error("Failed to save agent appearance", logger.Fields{"error": err, "agent": agentName})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
		return
	}

	// The file goes only after the record no longer references it, so a crash
	// between the two leaves an orphaned file rather than a broken reference.
	if previous != "" {
		removeAppearanceUpload(previous)
	}

	logger.Info("Appearance image removed", logger.Fields{"agent": agentName})
	orihttp.WriteJSON(w, map[string]any{
		"success":    true,
		"appearance": appearanceForAgent(ag),
		"message":    "Image removed",
	})
}

// checkMutationGuards applies the same optimistic-concurrency and
// shared-definition rules as a JSON appearance edit.
//
// An upload is an appearance change like any other, so it must not be the one
// path that can silently clobber a concurrent edit or skip the multi-workspace
// warning. The tokens arrive as form fields because multipart has no JSON body:
// `expected_version` and `confirm_shared_edit` (FR-16/FR-42).
func (h *AppearanceUploadHandler) checkMutationGuards(w http.ResponseWriter, r *http.Request, ag *agent.Agent, agentName string) bool {
	if expected := strings.TrimSpace(r.FormValue("expected_version")); expected != "" {
		current := agentConfigVersion(ag)
		if expected != current {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":           "stale_agent_edit",
				"message":         fmt.Sprintf("%q was changed since you loaded it. Reload the latest version and reapply your edit.", agentName),
				"current_version": current,
			})
			return false
		}
	}

	if isSystemAssistantAgent(agentName) {
		return true
	}
	membership := workspace.WorkspaceMembershipFor(h.workspaceStore, agentName)
	if membership.Count <= 1 {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.FormValue("confirm_shared_edit")), "true") {
		return true
	}
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
		"error":           "shared_agent_edit_requires_confirmation",
		"message":         fmt.Sprintf("%q is attached to %d workspaces — this change affects all of them. Resend with confirm_shared_edit=true to proceed.", agentName, membership.Count),
		"workspace_count": membership.Count,
		"workspaces":      membership.Workspaces,
	})
	return false
}

// appearanceUploadFilename builds the stored filename from the agent name.
//
// Everything outside a conservative allowlist becomes an underscore, so the
// result is always a plain basename with a known extension: safe in a path, a
// URL, and a CSS selector alike (FR-64).
func appearanceUploadFilename(agentName, ext string) string {
	var b strings.Builder
	for _, r := range agentName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	safe := strings.Trim(b.String(), "_")
	if safe == "" {
		safe = "agent"
	}
	return safe + ext
}

// removeAppearanceUpload deletes one stored image.
//
// Two independent guards: the name must be a plain basename, and the delete
// goes through an os.Root confined to the avatar directory. The first rejects a
// stored value that could only have been hand-edited; the second means even a
// mistake there cannot escape the directory, because the runtime enforces it.
func removeAppearanceUpload(filename string) {
	name := strings.TrimSpace(filename)
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return
	}
	root, err := os.OpenRoot(AvatarDir)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(name); err == nil {
		logger.Debug("Removed appearance image", logger.Fields{"filename": name})
	}
}

// writeUploadFile streams the upload to `name` inside the confined root.
func writeUploadFile(root *os.Root, name string, src io.Reader) error {
	dst, err := root.Create(name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	// Sync before close so "durably written" is true before the record starts
	// pointing at the file (FR-37).
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
