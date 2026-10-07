package sessionhttp

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/homeprofile"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Home profile card: what a Home knows about where its owner works, under
// the rows the Home's package declares. Every route is the owner's, on that
// exact Home; a linked project, another person's Home or any other workspace
// is a 404. Nothing here is an agent tool.

// HomeProfiles returns the profile service bound to this handler's stores and
// installed packages. Each dependency is resolved when it is used, so a plugin
// installed or disabled after startup is seen.
func (h *Handler) HomeProfiles() *homeprofile.Service {
	if h == nil {
		return nil
	}
	return homeprofile.New(homeprofile.Dependencies{
		Workspaces:     h.workspaceTaskStore,
		Declared:       h.homeProfileDeclared,
		Writable:       h.assistantHomeProviderAvailable,
		InstalledApps:  h.homeProfileInstalledApps,
		LibraryFormats: h.homeProfileLibraryFormats,
		TimeSignatures: h.homeProfileTimeSignatures,
	})
}

// SetHomeProfileAppDetector replaces how installed applications are looked
// for. Tests use it; production keeps the computer's Applications folders.
func (h *Handler) SetHomeProfileAppDetector(detect func() []folderdigest.InstalledApp) {
	if h != nil {
		h.homeProfileApps = detect
	}
}

func (h *Handler) homeProfileInstalledApps() []folderdigest.InstalledApp {
	if h.homeProfileApps != nil {
		return h.homeProfileApps()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return folderdigest.DetectInstalledApps(home)
}

// homeProfilePinned returns the installed Home declaration the Home's provider
// pin names, with the package that carries it.
func (h *Handler) homeProfilePinned(home *workspace.Workspace) (plugin.InstalledPlugin, projecttemplates.AssistantProgramHome, bool) {
	if h == nil || h.installedPluginLister == nil || home == nil {
		return plugin.InstalledPlugin{}, projecttemplates.AssistantProgramHome{}, false
	}
	state := home.GetAssistantProgramState()
	if state == nil {
		return plugin.InstalledPlugin{}, projecttemplates.AssistantProgramHome{}, false
	}
	owner := state.HomeProvider
	if owner == nil && state.GroupTemplate != nil {
		owner = state.GroupTemplate.ProgramHomeOwner
	}
	installed, err := h.installedPluginLister.List()
	if err != nil {
		return plugin.InstalledPlugin{}, projecttemplates.AssistantProgramHome{}, false
	}
	return plugin.PinnedHomeDeclaration(installed, owner)
}

func (h *Handler) homeProfileDeclared(home *workspace.Workspace) (homeprofile.Declared, bool) {
	installed, declaration, ok := h.homeProfilePinned(home)
	if !ok || declaration.HomeProfile == nil {
		return homeprofile.Declared{}, false
	}
	return homeprofile.Declared{PluginID: installed.Name, Version: installed.Version, Profile: *declaration.HomeProfile.Clone()}, true
}

// homeProfileLibraryFormats counts the Home's library entries per project
// format. A Home without a readable library counts nothing.
func (h *Handler) homeProfileLibraryFormats(home *workspace.Workspace) map[string]int {
	state := home.GetAssistantProgramState()
	if state == nil || len(state.ProjectLibrary) == 0 {
		return nil
	}
	key := state.Key.Normalize()
	doc, err := h.assistantLibraryStore().Read(projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID,
		ProviderID: key.PluginID, ProgramID: key.ProgramID})
	if err != nil {
		return nil
	}
	return doc.FormatCounts()
}

// homeProfileTimeSignatureInput is the blueprint input a new project's time
// signature comes from.
const homeProfileTimeSignatureInput = "time_signature"

// homeProfileTimeSignatures returns the time signatures a new project in this
// Home may start with: the options of that input on the first installed
// blueprint the Home's declaration allows projects from.
func (h *Handler) homeProfileTimeSignatures(home *workspace.Workspace) []homeprofile.Option {
	_, declaration, ok := h.homeProfilePinned(home)
	if !ok {
		return nil
	}
	installed, err := h.installedPluginLister.List()
	if err != nil {
		return nil
	}
	for _, allowed := range declaration.AllowedProjectAttachments {
		for _, candidate := range installed {
			if !candidate.Enabled || !strings.EqualFold(candidate.Name, allowed.ProviderPluginID) {
				continue
			}
			for _, blueprint := range candidate.ResolvedBlueprints {
				if blueprint.ID != allowed.BlueprintID || blueprint.Template.Inputs == nil {
					continue
				}
				for _, field := range blueprint.Template.Inputs.Fields {
					if field.ID != homeProfileTimeSignatureInput || field.Type != projecttemplates.InputFieldSelect {
						continue
					}
					options := make([]homeprofile.Option, 0, len(field.Options))
					for _, option := range field.Options {
						options = append(options, homeprofile.Option{Value: option.Value, Label: option.Label})
					}
					return options
				}
			}
		}
	}
	return nil
}

// homeProfileOwner resolves the signed-in owner and the Home id of a profile
// route. The service checks that the workspace is that owner's Home.
func (h *Handler) homeProfileOwner(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil {
		respondHomeProfileNotFound(w)
		return "", "", false
	}
	owner, err := h.currentUserID(r.Context())
	homeID := strings.TrimSpace(r.PathValue("workspaceID"))
	if err != nil || strings.TrimSpace(owner) == "" || homeID == "" {
		respondHomeProfileNotFound(w)
		return "", "", false
	}
	return owner, homeID, true
}

func respondHomeProfileNotFound(w http.ResponseWriter) {
	_ = orihttp.RespondNotFound(w, "This Home has no profile")
}

func respondHomeProfileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, homeprofile.ErrNotFound), errors.Is(err, homeprofile.ErrNotDeclared):
		respondHomeProfileNotFound(w)
	case errors.Is(err, homeprofile.ErrChanged):
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("home_profile_changed",
			"This profile changed somewhere else. Reload and try again."))
	case errors.Is(err, homeprofile.ErrReadOnly):
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("home_read_only",
			"Saved values are readable; the Home provider is unavailable for changes."))
	case errors.Is(err, homeprofile.ErrInvalid):
		_ = orihttp.RespondBadRequest(w, "Invalid profile request")
	default:
		logger.Error("Home profile request failed", logger.Fields{"error": err.Error()})
		_ = orihttp.RespondInternalError(w, "The profile could not be saved. Try again.")
	}
}

// GetAssistantProfile handles
// GET /api/workspaces/{workspaceID}/assistant-program/profile. It returns the
// declared rows and the stored values. It detects nothing and reads no folder.
func (h *Handler) GetAssistantProfile(w http.ResponseWriter, r *http.Request) {
	owner, homeID, ok := h.homeProfileOwner(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid profile request")
		return
	}
	view, err := h.HomeProfiles().Read(owner, homeID)
	if err != nil {
		respondHomeProfileError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

// DetectAssistantProfile handles
// POST /api/workspaces/{workspaceID}/assistant-program/profile/detect
// {request_id}: the owner's Detect. It looks for installed applications and
// records them as hints.
func (h *Handler) DetectAssistantProfile(w http.ResponseWriter, r *http.Request) {
	owner, homeID, ok := h.homeProfileOwner(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
	}
	if !decodeStrictAction(w, r, &request, "Invalid profile request") {
		return
	}
	view, err := h.HomeProfiles().Detect(r.Context(), owner, homeID, request.RequestID)
	if err != nil {
		respondHomeProfileError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

// SetAssistantProfileFields handles
// POST /api/workspaces/{workspaceID}/assistant-program/profile/fields
// {request_id, if_revision, main_app?, defaults?, confirm_apps?, hide_apps?,
// show_apps?}: the owner's confirmations and edits. A stale if_revision is 409
// home_profile_changed; an application that was not detected or a value out
// of bounds is 400.
func (h *Handler) SetAssistantProfileFields(w http.ResponseWriter, r *http.Request) {
	owner, homeID, ok := h.homeProfileOwner(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID   string                     `json:"request_id"`
		IfRevision  *int64                     `json:"if_revision"`
		MainApp     *string                    `json:"main_app"`
		Defaults    *homeprofile.DefaultsInput `json:"defaults"`
		ConfirmApps []string                   `json:"confirm_apps"`
		HideApps    []string                   `json:"hide_apps"`
		ShowApps    []string                   `json:"show_apps"`
	}
	if !decodeStrictAction(w, r, &request, "Invalid profile request") {
		return
	}
	if request.IfRevision == nil || *request.IfRevision < 0 {
		_ = orihttp.RespondBadRequest(w, "Invalid profile request")
		return
	}
	view, err := h.HomeProfiles().SetFields(owner, homeID, homeprofile.FieldsInput{
		RequestID: request.RequestID, IfRevision: *request.IfRevision,
		MainApp: request.MainApp, Defaults: request.Defaults,
		ConfirmApps: request.ConfirmApps, HideApps: request.HideApps, ShowApps: request.ShowApps,
	})
	if err != nil {
		respondHomeProfileError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
