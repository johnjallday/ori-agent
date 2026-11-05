package agenthttp

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	if ag, ok := h.State.GetAgent(agentName); !ok || ag == nil {
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

	// The ownership and shared-definition guards run before a single byte is
	// written. A multipart request carries them as form fields because it has no
	// JSON body to put them in (FR-16/FR-41/FR-42). The version is checked
	// again where it counts: inside the store's protected mutation below.
	if !h.checkMutationGuards(w, r, agentName) {
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

	// One of the user's agents keeps its image in its own folder, so the image
	// travels with it; others use the shared data-dir folder.
	avatarDir := avatarDirFor(h.State, agentName)
	// 0o750, not 0o755: nothing outside this process needs to read the
	// directory, and the static route serves its contents anyway.
	// #nosec G703 -- avatarDir is resolved by the agent store for an agent it
	// already holds (GetAgent succeeded above), or is the data-dir avatar
	// folder; the request never names a path, and every write below is
	// confined to this folder by os.Root.
	if err := os.MkdirAll(avatarDir, 0o750); err != nil {
		logger.Error("Failed to create avatar directory", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to create avatar directory", err)
		return
	}

	// Every write below goes through an os.Root confined to the avatar
	// directory. The filenames here are already server-generated, so this is
	// belt and braces — but it makes the confinement enforced by the runtime
	// rather than asserted by the code that builds the names (FR-64).
	root, err := os.OpenRoot(avatarDir)
	if err != nil {
		logger.Error("Failed to open avatar directory", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save image", err)
		return
	}
	defer func() { _ = root.Close() }()

	// The filename is derived from the agent name, the sniffed type, and a
	// fresh random part. The client's original path is never consulted, so
	// there is nothing to traverse with (FR-64). Because every upload gets a
	// name of its own, staging it never overwrites the image on display, and
	// replacing an image with another of the same format still changes the
	// definition (and so its version).
	filename, err := uploadFilenameFor(h.State, agentName, ext)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save image", err)
		return
	}
	if _, err := root.Stat(filename); err == nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save image", fmt.Errorf("image name %q is taken", filename))
		return
	}

	// Stream to a temporary file first, then rename it to its own new name.
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

	// Adopting the staged file is a definition mutation like any other: the
	// version check and the switch to the new image happen in one protected
	// operation, against the record as it is now (FR-16/FR-37).
	expected := strings.TrimSpace(r.FormValue("expected_version"))
	var previous string
	committed, err := store.MutateDefinition(h.State, agentName, func(draft *agent.Agent) error {
		if expected != "" {
			if current := agentConfigVersion(draft); current != expected {
				return &staleAgentEditError{current: current}
			}
		}
		draft.EnsureAppearance()
		previous = draft.Appearance.UploadedImage()
		draft.Appearance.SetUpload(filename)
		return nil
	})
	if err != nil {
		// Nothing references the staged file: it is this request's own, and the
		// image the agent was showing is untouched.
		_ = root.Remove(filename)
		writeAgentMutationError(w, agentName, err)
		return
	}

	// Only now is the replaced file expendable — and only if no agent still
	// names it. A newer request's image always has its own name, so it can
	// never be the one removed here.
	removeUnreferencedUpload(h.State, agentName, previous, filename)

	logger.Info("Appearance image uploaded", logger.Fields{"agent": agentName, "filename": filename})
	orihttp.WriteJSON(w, map[string]any{
		"success":    true,
		"appearance": appearanceForAgent(committed),
		"image_url":  "/avatars/" + filename,
		"message":    "Image uploaded",
		"version":    agentConfigVersion(committed),
		"agent":      definitionReceipt(agentName, committed),
	})
}

// removeUnreferencedUpload deletes a replaced image once no agent's
// appearance names it any more.
func removeUnreferencedUpload(st store.Store, agentName, previous, current string) {
	if previous == "" || previous == current || avatarOwner(st, previous) != "" {
		return
	}
	removeAppearanceUpload(st, agentName, previous)
}

// remove handles DELETE /api/agents/{name}/appearance/upload.
//
// It returns to Generated only when Upload was the source actually rendering.
// Deleting an image the agent was not displaying leaves the active mode — and
// the saved character and colour — alone (FR-38/FR-39/FR-40).
func (h *AppearanceUploadHandler) remove(w http.ResponseWriter, r *http.Request, agentName string) {
	if ag, ok := h.State.GetAgent(agentName); !ok || ag == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}
	if !h.checkMutationGuards(w, r, agentName) {
		return
	}

	// DELETE has no body; the version travels as a query parameter.
	expected := strings.TrimSpace(r.FormValue("expected_version"))
	var previous string
	committed, err := store.MutateDefinition(h.State, agentName, func(draft *agent.Agent) error {
		if expected != "" {
			if current := agentConfigVersion(draft); current != expected {
				return &staleAgentEditError{current: current}
			}
		}
		draft.EnsureAppearance()
		previous = draft.Appearance.UploadedImage()
		draft.Appearance.ClearUpload()
		return nil
	})
	if err != nil {
		writeAgentMutationError(w, agentName, err)
		return
	}

	// The file goes only after the record no longer references it, so a crash
	// between the two leaves an orphaned file rather than a broken reference.
	removeUnreferencedUpload(h.State, agentName, previous, "")

	logger.Info("Appearance image removed", logger.Fields{"agent": agentName})
	orihttp.WriteJSON(w, map[string]any{
		"success":    true,
		"appearance": appearanceForAgent(committed),
		"message":    "Image removed",
		"version":    agentConfigVersion(committed),
		"agent":      definitionReceipt(agentName, committed),
	})
}

// checkMutationGuards applies the ownership and shared-definition rules of a
// JSON appearance edit before any byte is written. The version itself is
// checked inside the store's protected mutation, against the record as it is
// at write time.
//
// An upload is an appearance change like any other, so it must not be the one
// path that can silently clobber a concurrent edit or skip the multi-workspace
// warning. The tokens arrive as form fields because multipart has no JSON body:
// `expected_version` and `confirm_shared_edit` (FR-16/FR-42).
func (h *AppearanceUploadHandler) checkMutationGuards(w http.ResponseWriter, r *http.Request, agentName string) bool {
	if owned := workspaceOwnedAgent(h.State, agentName); owned != nil {
		WriteAgentStoreError(w, "Failed to update agent", owned)
		return false
	}
	if expected := strings.TrimSpace(r.FormValue("expected_version")); expected != "" {
		if err := checkExpectedVersion(h.State, agentName, expected); err != nil {
			writeAgentMutationError(w, agentName, err)
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

// removeAppearanceUpload deletes one agent's stored image, from its own folder
// or, for an image an older build wrote, the shared folder.
//
// Two independent guards. The name must be a plain basename with an image
// extension, which rejects a stored value that could only have been
// hand-edited — an agent folder also holds agent_settings.json. And the delete
// goes through an os.Root confined to the folder, so even a mistake in the
// first cannot escape it, because the runtime enforces it.
func removeAppearanceUpload(st store.Store, agentName, filename string) {
	name := strings.TrimSpace(filename)
	if !agent.IsAppearanceUploadFilename(name) {
		return
	}
	for _, dir := range avatarDirsForAgent(st, agentName) {
		root, err := os.OpenRoot(dir)
		if err != nil {
			continue
		}
		err = root.Remove(name)
		_ = root.Close()
		if err == nil {
			logger.Debug("Removed appearance image", logger.Fields{"filename": name})
			return
		}
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
