package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Opening a listed song is one click for the owner: the song's workspace is
// made through the library's own activation review and commit (File-only, the
// exact reviewed creator), its project role is filled by the Home's shared
// assistant under the standing consent, and its first task is seeded to start
// on the first open. The browser names only the catalog row and, when the song
// folder holds several project files, which one. This is an HTTP route for the
// owner only and never an agent tool.

type assistantLibraryOpenRequest struct {
	RequestID    string `json:"request_id"`
	SelectedFile string `json:"selected_file,omitempty"`
}

// How an opened song's project role was filled.
const (
	openStaffingAdded    = "added"   // the shared assistant was created for it
	openStaffingJoined   = "joined"  // the shared assistant joined it
	openStaffingAlready  = "already" // its roles were already filled
	openStaffingNone     = "none"    // no standing consent
	openStaffingOff      = "off"     // the consent is switched off on the Home
	openStaffingNotShare = "not_shared"
)

type assistantLibraryOpenResult struct {
	EntryID     string `json:"entry_id"`
	WorkspaceID string `json:"workspace_id"`
	Route       string `json:"route"`
	Created     bool   `json:"created"`
	// Staffing is how the project role was filled (or why not): added, joined,
	// already, none, off, not_shared, consent_stale, assistant_missing.
	Staffing  string `json:"staffing"`
	AgentName string `json:"agent_name,omitempty"`
	FirstTask bool   `json:"first_task"`
}

// libraryOpener is the library's own read and reviewed creator for one song.
type libraryOpener interface {
	Exists(scope projectlibrary.Scope, entryID string) (bool, error)
	Eligibility(ctx context.Context, scope projectlibrary.Scope, entryID string) (projectlibrary.ActivationEligibility, error)
	// Activate reviews and commits exactly that review; key makes a retry replay.
	Activate(ctx context.Context, scope projectlibrary.Scope, entryID, selectedFile, key string) (string, error)
	// Format is the project format the catalog observed for the row.
	Format(scope projectlibrary.Scope, entryID string) string
}

type reviewedLibraryOpener struct {
	h         *Handler
	activator *projectlibrary.ActivationService
	inspector *projectlibrary.ActivationInspector
}

func (h *Handler) libraryOpener() libraryOpener {
	if h.libraryOpenerOverride != nil {
		return h.libraryOpenerOverride
	}
	activator, inspector := h.assistantLibraryActivatorWithInspector()
	if activator == nil || inspector == nil {
		return nil
	}
	return reviewedLibraryOpener{h: h, activator: activator, inspector: inspector}
}

func (o reviewedLibraryOpener) Exists(scope projectlibrary.Scope, entryID string) (bool, error) {
	doc, err := o.h.assistantLibraryStore().Read(scope)
	if err != nil {
		return false, err
	}
	for _, entry := range doc.Entries {
		if entry.ID == entryID {
			return true, nil
		}
	}
	return false, nil
}

func (o reviewedLibraryOpener) Eligibility(ctx context.Context, scope projectlibrary.Scope, entryID string) (projectlibrary.ActivationEligibility, error) {
	return o.inspector.Eligibility(ctx, scope, entryID)
}

func (o reviewedLibraryOpener) Activate(ctx context.Context, scope projectlibrary.Scope, entryID, selectedFile, key string) (string, error) {
	return o.h.activateLibrarySong(ctx, o.activator, scope, entryID, selectedFile, key)
}

func (o reviewedLibraryOpener) Format(scope projectlibrary.Scope, entryID string) string {
	return o.h.libraryEntryFormat(scope, entryID)
}

// OpenAssistantLibraryProject handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/projects/{entryID}/open.
func (h *Handler) OpenAssistantLibraryProject(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	entryID := r.PathValue("entryID")
	var request assistantLibraryOpenRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.SelectedFile = strings.TrimSpace(request.SelectedFile)
	if request.RequestID == "" || len(request.RequestID) > 120 || strings.ContainsAny(request.RequestID, "\x00\r\n") ||
		len(request.SelectedFile) > 255 || strings.ContainsAny(request.SelectedFile, "/\\\x00\r\n") {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return
	}
	opener := h.libraryOpener()
	if opener == nil || h.workspaceTaskStore == nil {
		_ = orihttp.RespondConflict(w, "The reviewed project creator is unavailable")
		return
	}
	exists, err := opener.Exists(scope, entryID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	if !exists {
		_ = orihttp.RespondNotFound(w, "Library project not found")
		return
	}
	ctx := r.Context()
	eligible, err := opener.Eligibility(ctx, scope, entryID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	result := assistantLibraryOpenResult{EntryID: entryID}
	switch eligible.State {
	case "connected":
		// Already open (or a retry after the workspace was made): finish what is
		// missing and go there. No second workspace is ever made.
		result.WorkspaceID = eligible.WorkspaceID
	case "review_available", "file_choice_required":
		// D6: a plugin update changed the assistant the user agreed to. They are
		// asked once, on the Home (before any file question); until then no song
		// is opened half-staffed.
		if h.projectStaffing != nil && h.librarySharing(scope, home).State == sharingStale {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":  "Your assistant changed in a plugin update. Review the updated assistant on this Home, then open the song.",
				"reason": "consent_stale",
			})
			return
		}
		if eligible.State == "file_choice_required" && request.SelectedFile == "" {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error": "Choose which project file to open", "reason": "needs_choice",
				"needs_choice": true, "project_files": eligible.ProjectFiles,
			})
			return
		}
		if request.SelectedFile != "" && !containsName(eligible.ProjectFiles, request.SelectedFile) {
			_ = orihttp.RespondBadRequest(w, "Choose one of the project files listed for this song")
			return
		}
		// A song whose assistant would be created now needs a model first; the
		// workspace is not made half-staffed (needs_model).
		if h.createNeedsModel(scope, home) {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":  "Set up a model so the assistant can work on this song, then open it again.",
				"reason": "needs_model", "needs_model": true,
			})
			return
		}
		workspaceID, err := opener.Activate(ctx, scope, entryID, request.SelectedFile, "open:"+request.RequestID)
		if err != nil {
			respondLibraryActionError(w, err)
			return
		}
		result.WorkspaceID, result.Created = workspaceID, true
	default:
		// The row keeps its own explanation: provider missing, source
		// disconnected, folder owned elsewhere, and so on.
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": eligible.Reason, "reason": eligible.State,
		})
		return
	}
	song, err := h.workspaceTaskStore.Get(result.WorkspaceID)
	if err != nil || song == nil || song.OwnerUserID != scope.OwnerUserID || !workspace.IsCanonicalWorkspaceSlug(song.FolderSlug) {
		_ = orihttp.RespondConflict(w, "The opened song could not be read back")
		return
	}
	result.Route = "/workspaces/" + url.PathEscape(song.FolderSlug)
	if h.selectFileOnly != nil {
		// The song works File-only (D5): record it, so opening asks no mode
		// question. A mode the user already chose is left alone.
		if err := h.selectFileOnly(ctx, result.WorkspaceID); err != nil {
			logger.Warn("Opened song: File-only was not recorded", logger.Fields{"workspace_id": result.WorkspaceID, "error": err.Error()})
		}
	}
	result.Staffing, result.AgentName = h.staffOpenedSong(ctx, home, result.WorkspaceID)
	result.FirstTask = h.seedOpenedSongTask(result.WorkspaceID, opener.Format(scope, entryID))
	_ = orihttp.RespondSuccess(w, result)
}

// libraryEntryFormat is the project format the catalog observed for the row.
func (h *Handler) libraryEntryFormat(scope projectlibrary.Scope, entryID string) string {
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return ""
	}
	for _, entry := range doc.Entries {
		if entry.ID != entryID {
			continue
		}
		for _, observed := range entry.Observations {
			if observed.Format != "" {
				return observed.Format
			}
		}
	}
	return ""
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// activateLibrarySong is the library's own reviewed creator: review, then the
// commit of exactly that review, keyed by the request so a retry replays.
func (h *Handler) activateLibrarySong(ctx context.Context, activator *projectlibrary.ActivationService, scope projectlibrary.Scope, entryID, selectedFile, key string) (string, error) {
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return "", err
	}
	name := ""
	for _, entry := range doc.Entries {
		if entry.ID != entryID {
			continue
		}
		// The row's own name: what the user saved, else its folder's name (the
		// library's search rows use the same rule).
		name = strings.TrimSpace(entry.Fields.DisplayName)
		for _, observed := range entry.Observations {
			if name == "" && observed.RelativeFolder != "" {
				name = filepath.Base(observed.RelativeFolder)
			}
		}
	}
	if name == "" {
		return "", projectlibrary.ErrConflict
	}
	if runes := []rune(name); len(runes) > 128 {
		name = string(runes[:128])
	}
	review, err := activator.Review(ctx, scope, entryID, doc.Revision, selectedFile, name)
	if err != nil {
		return "", err
	}
	result, err := activator.Commit(ctx, scope, entryID, review.Token, key)
	if err != nil {
		return "", err
	}
	return result.WorkspaceID, nil
}

// createNeedsModel is true when opening a song now would create the Home's
// shared assistant and no model is set up for a new agent.
func (h *Handler) createNeedsModel(scope projectlibrary.Scope, home *workspace.Workspace) bool {
	if h.projectStaffing == nil || h.staffingModelReady == nil || h.staffingModelReady() {
		return false
	}
	consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
	if !consent.Active() {
		return false
	}
	team, ok := h.projectTeamFor(scope, home)
	if !ok || !consent.Covers(team.PluginID, team.BlueprintID) || consent.TeamDigest != team.Digest {
		return false // nothing would be created: the open adds no agent
	}
	for _, role := range team.Roles {
		if entry, covered := consent.Role(role.ID); covered && entry.AgentName == "" {
			return true
		}
	}
	return false
}

// installedProjectTeam is the blueprint the Home opens its songs with.
func (h *Handler) installedProjectTeam(scope projectlibrary.Scope, home *workspace.Workspace) (projectlibrary.ProjectTeam, bool) {
	if h.installedPluginLister == nil || home == nil {
		return projectlibrary.ProjectTeam{}, false
	}
	state := home.GetAssistantProgramState()
	owner := state.HomeProvider
	if owner == nil && state.GroupTemplate != nil {
		owner = state.GroupTemplate.ProgramHomeOwner
	}
	installed, err := h.installedPluginLister.List()
	if err != nil || owner == nil {
		return projectlibrary.ProjectTeam{}, false
	}
	return projectlibrary.CompatibleProjectTeam(installed, owner, scope)
}

// staffOpenedSong fills the song's empty required project roles with the
// Home's shared assistant, through the one staffing seam the workspace role
// route uses. A song whose roles are already filled (its own per-song agent, or
// an earlier open) is left as it is (D8).
func (h *Handler) staffOpenedSong(ctx context.Context, home *workspace.Workspace, songID string) (string, string) {
	if h.projectStaffing == nil {
		return openStaffingNone, ""
	}
	song, err := h.projectStaffing.Song(songID)
	if err != nil {
		return openStaffingNotShare, ""
	}
	filled := song.FilledRoles()
	var missing []string
	for _, role := range song.RequiredRoles() {
		if _, done := filled[role.ID]; !done {
			missing = append(missing, role.ID)
		}
	}
	if len(missing) == 0 {
		for _, instance := range filled {
			return openStaffingAlready, instance.Name
		}
		return openStaffingAlready, ""
	}
	fills, err := h.projectStaffing.Fill(songID, missing, nil)
	switch {
	case errors.Is(err, projectstaffing.ErrNoConsent):
		if consent := home.GetAssistantProgramState().GetProjectStaffingConsent(); consent != nil && consent.RevokedAt != nil {
			return openStaffingOff, ""
		}
		return openStaffingNone, ""
	case errors.Is(err, projectstaffing.ErrNotShared):
		return openStaffingNotShare, ""
	case errors.Is(err, projectstaffing.ErrConsentStale):
		return workspace.ProjectStaffingConsentStale, ""
	case errors.Is(err, projectstaffing.ErrAssistantMissing):
		return workspace.ProjectStaffingAssistantMissing, ""
	case err != nil:
		logger.Warn("Opened song: the shared assistant could not be decided", logger.Fields{"workspace_id": songID, "error": err.Error()})
		return openStaffingNone, ""
	}
	if h.assistantWorkspaceRoleStaffer == nil {
		return openStaffingNone, ""
	}
	outcome, name := openStaffingJoined, ""
	for _, fill := range fills {
		mode := "create"
		if fill.Mode == projectstaffing.ModeBind {
			mode = "assign"
		} else {
			outcome = openStaffingAdded
		}
		if err := h.assistantWorkspaceRoleStaffer(ctx, songID, []RoleStaffingFill{{RoleID: fill.RoleID, Mode: mode, Name: fill.Name}}); err != nil {
			logger.Warn("Opened song: the shared assistant could not be added", logger.Fields{"workspace_id": songID, "role_id": fill.RoleID, "error": err.Error()})
			return openStaffingNone, ""
		}
		name = fill.Name
	}
	if err := h.projectStaffing.Settle(songID); err != nil {
		logger.Warn("Opened song: the shared assistant was not recorded", logger.Fields{"workspace_id": songID, "error": err.Error()})
	}
	return outcome, name
}

// seedOpenedSongTask gives the song the same first read-only task a folder's
// project gets; the user starts it with Start first look. Seeding skips a
// workspace that already has it, so a retry adds nothing.
func (h *Handler) seedOpenedSongTask(songID, format string) bool {
	shape := folderdigest.Shape("")
	for _, marker := range folderdigest.Markers {
		if marker.ProjectFormat != "" && marker.ProjectFormat == format {
			shape = marker.Shape
		}
	}
	description, details := personalassistant.FolderFirstTask(shape)
	seeded, err := h.SeedStarterTasks(songID, projecttemplates.Template{
		ID:           "folder-digest",
		StarterTasks: []projecttemplates.StarterTask{{Description: description, Details: details}},
	})
	if err != nil {
		logger.Warn("Opened song: its first task could not be seeded", logger.Fields{"workspace_id": songID, "error": err.Error()})
		return false
	}
	if seeded > 0 {
		return true
	}
	// Already there from an earlier open.
	if ws, getErr := h.workspaceTaskStore.Get(songID); getErr == nil && ws != nil {
		for i := range ws.Tasks {
			if isFolderFirstTask(&ws.Tasks[i]) {
				return true
			}
		}
	}
	return false
}
