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
// folder holds several project files, which one. The same open service runs
// in process when a folder card's one Set up has the Portfolio Manager open the
// song it just listed. Neither is ever an agent tool.

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

// Why an open stopped before making anything; the owner answers, then opens again.
const (
	LibraryOpenNeedsChoice  = "needs_choice"
	LibraryOpenConsentStale = "consent_stale"
	LibraryOpenNeedsModel   = "needs_model"
)

var (
	// ErrLibraryOpenUnavailable means the reviewed creator is not wired.
	ErrLibraryOpenUnavailable = errors.New("the reviewed project creator is unavailable")
	// ErrLibraryProjectNotFound means the Home's catalog has no such row.
	ErrLibraryProjectNotFound = errors.New("library project not found")
	// ErrLibraryFileNotOffered means the chosen file is not one the row offered.
	ErrLibraryFileNotOffered = errors.New("the chosen project file was not offered for this song")
	// ErrLibraryOpenReadBack means the opened song could not be read back.
	ErrLibraryOpenReadBack = errors.New("the opened song could not be read back")
)

// LibraryFileNotOfferedError retains the current read-only candidates for a
// canonical handoff to ask again. Public library callers keep the existing
// ErrLibraryFileNotOffered refusal; no unselected open is attempted.
type LibraryFileNotOfferedError struct {
	ProjectFiles []string
}

func (*LibraryFileNotOfferedError) Error() string { return ErrLibraryFileNotOffered.Error() }
func (*LibraryFileNotOfferedError) Unwrap() error { return ErrLibraryFileNotOffered }

// LibraryOpenStop is an open that asks the owner something first, or that the
// row's own state refuses. Nothing was made.
type LibraryOpenStop struct {
	// Reason is needs_choice, consent_stale, needs_model, or the row's own
	// eligibility state (provider missing, source disconnected, …).
	Reason  string
	Message string
	// ProjectFiles are the files to choose from for needs_choice.
	ProjectFiles []string
}

func (s *LibraryOpenStop) Error() string { return s.Message }

// LibraryOpenInput names one catalog row and how to open it. The Open row sets
// only the entry, the chosen file and its request's key.
type LibraryOpenInput struct {
	EntryID      string
	SelectedFile string
	// Key keys the activation commit, so a retry replays it.
	Key string
	// Goal, when set, is the normalized goal the song is created with.
	Goal string
	// WorkspaceName, when set, names the new workspace as the caller's review
	// named it; empty names it after the row.
	WorkspaceName string
	// SetupOfferID, when set, is the folder setup whose one Set up opens the
	// song: the Home's setup-open receipt is written with the song's link.
	SetupOfferID string
	// SeedFirstTask, when set, seeds the song's first task in place of the
	// Open row's brief-less one; it reports whether the song has it.
	SeedFirstTask func(songID, format string) bool
}

// LibraryOpenResult is what opening one song did.
type LibraryOpenResult struct {
	EntryID     string `json:"entry_id"`
	WorkspaceID string `json:"workspace_id"`
	// LinkID is the song's reciprocal link to the Home; the Open row omits it.
	LinkID string `json:"-"`
	// SelectedFile is read back from the canonical project locator, not input.
	SelectedFile string `json:"-"`
	Route        string `json:"route"`
	Created      bool   `json:"created"`
	// Staffing is how the project role was filled (or why not): added, joined,
	// already, none, off, not_shared, consent_stale, assistant_missing.
	Staffing  string `json:"staffing"`
	AgentName string `json:"agent_name,omitempty"`
	FirstTask bool   `json:"first_task"`
}

// libraryActivation is one reviewed creation of a listed song: the row, the
// chosen file, the name and goal it is created with, and the commit's key.
type libraryActivation struct {
	EntryID       string
	SelectedFile  string
	WorkspaceName string
	Goal          string
	Key           string
	SetupOfferID  string
}

// libraryOpener is the library's own read and reviewed creator for one song.
type libraryOpener interface {
	Exists(scope projectlibrary.Scope, entryID string) (bool, error)
	Eligibility(ctx context.Context, scope projectlibrary.Scope, entryID string) (projectlibrary.ActivationEligibility, error)
	// Activate reviews and commits exactly that review; its key makes a retry
	// replay.
	Activate(ctx context.Context, scope projectlibrary.Scope, activation libraryActivation) (string, error)
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

func (o reviewedLibraryOpener) Activate(ctx context.Context, scope projectlibrary.Scope, activation libraryActivation) (string, error) {
	return o.h.activateLibrarySong(ctx, o.activator, scope, activation)
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
	result, err := h.openLibraryProject(r.Context(), scope, home, LibraryOpenInput{
		EntryID: entryID, SelectedFile: request.SelectedFile, Key: "open:" + request.RequestID,
	})
	var stop *LibraryOpenStop
	switch {
	case err == nil:
		_ = orihttp.RespondSuccess(w, result)
	case errors.As(err, &stop):
		body := map[string]any{"error": stop.Message, "reason": stop.Reason}
		switch stop.Reason {
		case LibraryOpenNeedsChoice:
			body["needs_choice"], body["project_files"] = true, stop.ProjectFiles
		case LibraryOpenNeedsModel:
			body["needs_model"] = true
		}
		_ = orihttp.RespondJSON(w, http.StatusConflict, body)
	case errors.Is(err, ErrLibraryOpenUnavailable):
		_ = orihttp.RespondConflict(w, "The reviewed project creator is unavailable")
	case errors.Is(err, ErrLibraryProjectNotFound):
		_ = orihttp.RespondNotFound(w, "Library project not found")
	case errors.Is(err, ErrLibraryFileNotOffered):
		_ = orihttp.RespondBadRequest(w, "Choose one of the project files listed for this song")
	case errors.Is(err, ErrLibraryOpenReadBack):
		_ = orihttp.RespondConflict(w, "The opened song could not be read back")
	default:
		respondLibraryActionError(w, err)
	}
}

// OpenPortfolioProject opens one listed song of the owner's Home in process,
// through the very service the library's Open row uses: the folder card's one
// Set up, where the Portfolio Manager opens the song the run just listed.
func (h *Handler) OpenPortfolioProject(ctx context.Context, ownerUserID, homeID string, in LibraryOpenInput) (LibraryOpenResult, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil {
		return LibraryOpenResult{}, err
	}
	if strings.TrimSpace(in.Key) == "" {
		return LibraryOpenResult{}, ErrPortfolioSetupUnavailable
	}
	home, err := h.workspaceTaskStore.Get(scope.HomeID)
	if err != nil || home == nil {
		return LibraryOpenResult{}, ErrPortfolioSetupUnavailable
	}
	return h.openLibraryProject(ctx, scope, home, in)
}

// openLibraryProject is the one reviewed open of a listed song: eligibility,
// then (for a song not yet open) the stale-consent, file-choice and model
// checks, the library's activation review and commit under the caller's key,
// and then, for a new or already-open song alike, File-only, the shared
// assistant under the standing consent and the first task. A song that is
// already open is finished, never made twice.
func (h *Handler) openLibraryProject(ctx context.Context, scope projectlibrary.Scope, home *workspace.Workspace, in LibraryOpenInput) (LibraryOpenResult, error) {
	opener := h.libraryOpener()
	if opener == nil || h.workspaceTaskStore == nil {
		return LibraryOpenResult{}, ErrLibraryOpenUnavailable
	}
	exists, err := opener.Exists(scope, in.EntryID)
	if err != nil {
		return LibraryOpenResult{}, err
	}
	if !exists {
		return LibraryOpenResult{}, ErrLibraryProjectNotFound
	}
	eligible, err := opener.Eligibility(ctx, scope, in.EntryID)
	if err != nil {
		return LibraryOpenResult{}, err
	}
	result := LibraryOpenResult{EntryID: in.EntryID}
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
			return LibraryOpenResult{}, &LibraryOpenStop{Reason: LibraryOpenConsentStale,
				Message: "Your assistant changed in a plugin update. Review the updated assistant on this Home, then open the song."}
		}
		if eligible.State == "file_choice_required" && in.SelectedFile == "" {
			return LibraryOpenResult{}, &LibraryOpenStop{Reason: LibraryOpenNeedsChoice,
				Message: "Choose which project file to open", ProjectFiles: eligible.ProjectFiles}
		}
		if in.SelectedFile != "" && !containsName(eligible.ProjectFiles, in.SelectedFile) {
			return LibraryOpenResult{}, &LibraryFileNotOfferedError{ProjectFiles: append([]string(nil), eligible.ProjectFiles...)}
		}
		// A song whose assistant would be created now needs a model first; the
		// workspace is not made half-staffed (needs_model).
		if h.createNeedsModel(scope, home) {
			return LibraryOpenResult{}, &LibraryOpenStop{Reason: LibraryOpenNeedsModel,
				Message: "Set up a model so the assistant can work on this song, then open it again."}
		}
		workspaceID, err := opener.Activate(ctx, scope, libraryActivation{EntryID: in.EntryID, SelectedFile: in.SelectedFile,
			WorkspaceName: in.WorkspaceName, Goal: in.Goal, Key: in.Key, SetupOfferID: in.SetupOfferID})
		if err != nil {
			return LibraryOpenResult{}, err
		}
		result.WorkspaceID, result.Created = workspaceID, true
	default:
		// The row keeps its own explanation: provider missing, source
		// disconnected, folder owned elsewhere, and so on.
		return LibraryOpenResult{}, &LibraryOpenStop{Reason: eligible.State, Message: eligible.Reason}
	}
	song, err := h.workspaceTaskStore.Get(result.WorkspaceID)
	if err != nil || song == nil || song.OwnerUserID != scope.OwnerUserID || !workspace.IsCanonicalWorkspaceSlug(song.FolderSlug) {
		return LibraryOpenResult{}, ErrLibraryOpenReadBack
	}
	locator, locatorErr := workspace.GetProjectEntryLocator(song.SharedData)
	if locatorErr != nil {
		return LibraryOpenResult{}, ErrLibraryOpenReadBack
	}
	if locator != nil && personalassistant.ValidFolderEntryName(locator.RelativePath) {
		result.SelectedFile = locator.RelativePath
	}
	if in.SelectedFile != "" && result.SelectedFile != in.SelectedFile {
		return LibraryOpenResult{}, &LibraryOpenStop{Reason: personalassistant.FolderStopPlanChanged,
			Message: "The workspace is not linked to the reviewed project file."}
	}
	result.Route = "/workspaces/" + url.PathEscape(song.FolderSlug)
	if link := song.GetAssistantProjectLink(); link != nil && link.StationWorkspaceID == scope.HomeID {
		result.LinkID = link.ID
	}
	if h.selectFileOnly != nil {
		// The song works File-only (D5): record it, so opening asks no mode
		// question. A mode the user already chose is left alone.
		if err := h.selectFileOnly(ctx, result.WorkspaceID); err != nil {
			logger.Warn("Opened song: File-only was not recorded", logger.Fields{"workspace_id": result.WorkspaceID, "error": err.Error()})
		}
	}
	result.Staffing, result.AgentName = h.staffOpenedSong(ctx, home, result.WorkspaceID)
	seed := in.SeedFirstTask
	if seed == nil {
		seed = h.seedOpenedSongTask
	}
	result.FirstTask = seed(result.WorkspaceID, opener.Format(scope, in.EntryID))
	return result, nil
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
func (h *Handler) activateLibrarySong(ctx context.Context, activator *projectlibrary.ActivationService, scope projectlibrary.Scope, activation libraryActivation) (string, error) {
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(activation.WorkspaceName)
	for _, entry := range doc.Entries {
		if entry.ID != activation.EntryID || name != "" {
			continue
		}
		// The row's own name: what the user saved, else its folder's name (the
		// library's search rows use the same rule); a root that is itself the
		// song is named after the root's own folder.
		name = strings.TrimSpace(entry.Fields.DisplayName)
		for _, observed := range entry.Observations {
			switch {
			case name != "":
			case observed.RelativeFolder != "":
				name = filepath.Base(observed.RelativeFolder)
			default:
				for _, root := range doc.Roots {
					if root.ID == observed.RootID && root.RevokedAt == nil {
						name = filepath.Base(root.Path)
					}
				}
			}
		}
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", projectlibrary.ErrConflict
	}
	if runes := []rune(name); len(runes) > 128 {
		name = string(runes[:128])
	}
	review, err := activator.ReviewWithGoal(ctx, scope, activation.EntryID, doc.Revision, activation.SelectedFile, name, activation.Goal)
	if err != nil {
		return "", err
	}
	var result projectlibrary.ActivationResult
	if activation.SetupOfferID != "" {
		result, err = activator.CommitSetupOpen(ctx, scope, activation.EntryID, review.Token, activation.Key, activation.SetupOfferID)
	} else {
		result, err = activator.Commit(ctx, scope, activation.EntryID, review.Token, activation.Key)
	}
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
