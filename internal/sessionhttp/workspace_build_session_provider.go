package sessionhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// A recognized folder whose plan waits on its Home's plugin installs that
// plugin from the build itself (PRD user story 2). The host's reviewed-release
// service — the one Home's folder card uses — previews the exact release with
// its trust disclosure, and installs only that release once the user confirms
// it. The client names nothing but the build: the plugin comes from the
// folder's recognition and the release from the host.

// WorkspaceBuildHomeProvider is that reviewed-release service, keyed by the
// Home provider key the folder's recognition names.
type WorkspaceBuildHomeProvider interface {
	PreviewHomeProvider(ctx context.Context, providerKey string) (BuildHomeProvider, error)
	InstallHomeProvider(ctx context.Context, providerKey, reviewedVersion string) (BuildHomeProvider, error)
}

// BuildHomeProvider is one reviewed release of a Home's plugin, as the pane
// discloses it before anything is installed.
type BuildHomeProvider struct {
	Name     string `json:"name"`
	PluginID string `json:"plugin_id"`
	Version  string `json:"version,omitempty"`
	Source   string `json:"source,omitempty"`
	// Disclosure is the plugin manager's own trust report, unabridged.
	Disclosure any  `json:"disclosure,omitempty"`
	Ready      bool `json:"ready"`
	Installed  bool `json:"installed"`
	// Update means an older reviewed release is installed and confirming
	// updates it to Version.
	Update           bool   `json:"update,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	DevelopmentCopy  bool   `json:"development_copy,omitempty"`
}

// The reviewed-release service's refusals, as the build says them.
var (
	// ErrBuildProviderChanged: the release the user reviewed is no longer the
	// one the host would install.
	ErrBuildProviderChanged = errors.New("the reviewed release changed while it was being reviewed")
	// ErrBuildProviderHomeUpgrade: a Home is pinned to the installed release.
	ErrBuildProviderHomeUpgrade = errors.New("a Home uses the installed release")
	// ErrBuildProviderUnavailable: there is no reviewed release to install now.
	ErrBuildProviderUnavailable = errors.New("the Home's plugin cannot be installed right now")
)

const buildCopyProviderInstalled = "Installed %s %s. Set it up can run the plan now."

type buildHomeProviderRequest struct {
	Version         int64  `json:"version"`
	Confirm         bool   `json:"confirm"`
	ReviewedVersion string `json:"reviewed_version,omitempty"`
}

// buildWaitsOnProvider reports whether a plan is waiting on its Home's plugin:
// the one thing this build can install for it.
func buildWaitsOnProvider(plan *personalassistant.BuildFolderPlan) bool {
	return plan != nil && !plan.Ready &&
		(plan.Unavailable == personalassistant.FolderAvailabilityProviderNotInstalled ||
			plan.Unavailable == personalassistant.FolderAvailabilityProviderUnverified)
}

// postWorkspaceBuildHomeProvider serves POST …/{id}/home-provider: a preview
// (confirm false) discloses the reviewed release, a confirmation installs
// exactly that release and says so in the transcript.
func (h *Handler) postWorkspaceBuildHomeProvider(w http.ResponseWriter, r *http.Request, id string) {
	var req buildHomeProviderRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	if !req.Confirm && req.ReviewedVersion != "" {
		_ = orihttp.RespondBadRequest(w, "Review the plugin before confirming")
		return
	}
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	deps := h.workspaceBuild
	if deps == nil || deps.HomeProvider == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	session := doc.Session(id)
	switch {
	case session == nil:
		respondBuildCode(w, http.StatusNotFound, "not_found", buildErrorText("not_found"))
		return
	case session.Status != personalassistant.WorkspaceBuildOpen:
		respondBuildCode(w, http.StatusConflict, "closed", buildErrorText("closed"))
		return
	case session.Version != req.Version:
		respondBuildCode(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	var recognition *personalassistant.BuildFolderRecognition
	if session.Folder != nil {
		recognition = session.Folder.Recognition
	}
	if !recognition.Recognized() || recognition.HomeProviderKey == "" ||
		!buildWaitsOnProvider(h.workspaceBuildPlan(ctx, userID, nil, session)) {
		respondBuildCode(w, http.StatusConflict, "no_provider", "This folder's setup is not waiting on a plugin.")
		return
	}
	name := strings.TrimSpace(recognition.HomeProviderName)
	if name == "" {
		name = "the Home's plugin"
	}
	if !req.Confirm {
		preview, err := deps.HomeProvider.PreviewHomeProvider(ctx, recognition.HomeProviderKey)
		if err != nil {
			respondBuildProviderError(w, err, name)
			return
		}
		preview.Name = name
		h.respondBuildSession(ctx, w, http.StatusOK, session, map[string]any{"home_provider": preview})
		return
	}
	installed, err := deps.HomeProvider.InstallHomeProvider(ctx, recognition.HomeProviderKey, strings.TrimSpace(req.ReviewedVersion))
	if err != nil {
		respondBuildProviderError(w, err, name)
		return
	}
	installed.Name = name
	var result personalassistant.WorkspaceBuildSession
	if _, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		current := doc.Session(id)
		if current == nil || current.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		now := deps.Store.Now()
		current.Append(personalassistant.BuildTranscriptEntry{
			Role: personalassistant.BuildRoleForm, Text: fmt.Sprintf(buildCopyProviderInstalled, name, installed.Version), Host: true, At: now,
		})
		current.Touch(now)
		result = *current
		return nil
	}); err != nil {
		// The plugin is installed either way; the build is re-read as it stands.
		h.getWorkspaceBuildSession(w, r, id)
		return
	}
	h.respondBuildSession(ctx, w, http.StatusOK, &result, map[string]any{"home_provider": installed})
}

func respondBuildCode(w http.ResponseWriter, status int, code, message string) {
	_ = orihttp.RespondJSON(w, status, map[string]any{"error": message, "code": code})
}

func respondBuildProviderError(w http.ResponseWriter, err error, name string) {
	switch {
	case errors.Is(err, ErrBuildProviderChanged):
		respondBuildCode(w, http.StatusConflict, "provider_changed",
			"A newer release of "+name+" appeared while you were reviewing. Nothing was installed; review it again.")
	case errors.Is(err, ErrBuildProviderHomeUpgrade):
		respondBuildCode(w, http.StatusConflict, "home_upgrade_required",
			"Your Home uses the installed release of "+name+". Upgrade it from that Home first, then continue here.")
	default:
		respondBuildCode(w, http.StatusServiceUnavailable, "provider_unavailable",
			name+" can’t be installed right now. Nothing was changed.")
	}
}
