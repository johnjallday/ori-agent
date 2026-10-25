package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/homeupgrade"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HomePackageUpgrades is the reviewed Home package upgrade
// (independent-program-homes.md §6.1) as the Home page sees it.
type HomePackageUpgrades interface {
	Status(ctx context.Context, ownerID, pluginID string) (*homeupgrade.Operation, error)
	Review(ctx context.Context, ownerID, pluginID string) (homeupgrade.Review, error)
	Commit(ctx context.Context, ownerID, pluginID, reviewToken string) (homeupgrade.Operation, error)
	Attempt(ctx context.Context, ownerID, pluginID, reviewToken string) (homeupgrade.Attempt, error)
}

// SetHomePackageUpgrades wires the upgrade and the Plugins page's cached update
// availability. Reading availability never resolves a source.
func (h *Handler) SetHomePackageUpgrades(upgrades HomePackageUpgrades, availability func() plugin.UpdateSnapshot) {
	h.homePackageUpgrades = upgrades
	h.pluginUpdateSnapshot = availability
}

type providerUpgradeView struct {
	PluginID         string `json:"plugin_id"`
	InstalledVersion string `json:"installed_version"`
	AvailableVersion string `json:"available_version,omitempty"`
	Available        bool   `json:"available"`
	// Operation is the package's operation while it still holds the package;
	// LastOperation is the newest one once it has finished. Neither is the
	// outcome of any particular review's commit.
	Operation     *providerUpgradeOperationView `json:"operation,omitempty"`
	LastOperation *providerUpgradeOperationView `json:"last_operation,omitempty"`
	// StatusUnavailable says the operation could not be read, so its absence
	// means nothing.
	StatusUnavailable bool `json:"status_unavailable,omitempty"`
	// Homes are the owner's Homes the package backs, listed only when the
	// upgrade is addressed by plugin rather than from one of them.
	Homes []providerUpgradeHomeLink `json:"homes,omitempty"`
}

// providerUpgradeHomeLink names one of the owner's Homes, with its page when
// it has one.
type providerUpgradeHomeLink struct {
	Name string `json:"name"`
	Link string `json:"link,omitempty"`
}

type providerUpgradeOperationView struct {
	ID          string                     `json:"id"`
	Status      string                     `json:"status"`
	FromVersion string                     `json:"from_version"`
	ToVersion   string                     `json:"to_version"`
	Reason      string                     `json:"reason,omitempty"`
	Agents      []homeupgrade.AgentOutcome `json:"agents,omitempty"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

type providerUpgradeRoleView struct {
	RoleID string `json:"role_id"`
	Label  string `json:"label"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

type providerUpgradeHomeView struct {
	Name     string                  `json:"name"`
	Link     string                  `json:"link,omitempty"`
	Projects []string                `json:"projects"`
	Agents   []homeupgrade.PlanAgent `json:"agents"`
}

type providerUpgradeReviewView struct {
	Token        string                    `json:"token"`
	ExpiresAt    time.Time                 `json:"expires_at"`
	PluginID     string                    `json:"plugin_id"`
	FromVersion  string                    `json:"from_version"`
	ToVersion    string                    `json:"to_version"`
	Roles        []providerUpgradeRoleView `json:"roles"`
	Homes        []providerUpgradeHomeView `json:"homes"`
	TrashedHomes int                       `json:"trashed_homes,omitempty"`
	Skills       []string                  `json:"skills"`
	Warnings     []string                  `json:"warnings,omitempty"`
	// Additions are what the newer release adds to each Home besides guidance,
	// one plain sentence each.
	Additions []string `json:"additions,omitempty"`
}

// providerUpgradeProfileAddition is the review's line for a release that adds
// a profile card: the card's own title, and that the upgrade itself looks for
// and reads nothing.
func providerUpgradeProfileAddition(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Adds a profile card to this Home. Nothing is detected or read until you open it."
	}
	article := "a"
	if strings.ContainsRune("AEIOUaeiou", rune(title[0])) {
		article = "an"
	}
	return "Adds " + article + " " + title + " card to this Home. Nothing is detected or read until you open it."
}

// providerUpgradeResourceAddition is the review's line for a release that lets
// the Home have a resource workspace: the Home package's own label for it,
// that the upgrade creates and reads nothing, and what will be shared once
// the owner sets it up.
func providerUpgradeResourceAddition(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "a resource workspace"
	}
	return "Adds " + label + " to this Home. Nothing is created or read until you set it up. " +
		"Once set up, assistants in this Home's projects can look up entries from sources you approve."
}

type providerUpgradeCommitRequest struct {
	Token string `json:"token"`
}

// providerUpgradeAttemptView is what became of one review's commit: its
// operation, or, when it started none, the review's state (pending, expired
// or unknown).
type providerUpgradeAttemptView struct {
	Operation *providerUpgradeOperationView `json:"operation,omitempty"`
	Review    string                        `json:"review,omitempty"`
}

// providerUpgradeScope resolves the owner's Home and the package its pin names.
func (h *Handler) providerUpgradeScope(w http.ResponseWriter, r *http.Request) (string, *workspace.Workspace, bool) {
	unavailable := func() (string, *workspace.Workspace, bool) {
		_ = orihttp.RespondNotFound(w, "This Home has no package upgrade")
		return "", nil, false
	}
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil || h.homePackageUpgrades == nil {
		return unavailable()
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		return unavailable()
	}
	id := strings.TrimSpace(r.PathValue("workspaceID"))
	station, err := h.workspaceTaskStore.Get(id)
	if err != nil || station == nil || station.ID != id || station.OwnerUserID != owner {
		return unavailable()
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || state.Key.Normalize().OwnerUserID != owner {
		return unavailable()
	}
	return owner, station, true
}

// providerUpgradeSummary reports whether a newer release of the Home's package
// is available, and the package's newest operation.
func (h *Handler) providerUpgradeSummary(ctx context.Context, owner string, station *workspace.Workspace) *providerUpgradeView {
	state := station.GetAssistantProgramState()
	if h == nil || h.homePackageUpgrades == nil || state == nil || state.HomeProvider == nil {
		return nil
	}
	return h.providerUpgradeStatus(ctx, owner, state.HomeProvider.PluginID, state.HomeProvider.PluginVersion)
}

// providerUpgradeStatus reads the cached availability of a newer release, which
// never resolves a source, and the package's newest operation.
func (h *Handler) providerUpgradeStatus(ctx context.Context, owner, pluginID, installedVersion string) *providerUpgradeView {
	view := &providerUpgradeView{PluginID: pluginID, InstalledVersion: installedVersion}
	if h.pluginUpdateSnapshot != nil {
		for _, update := range h.pluginUpdateSnapshot().Updates {
			if update.Name == pluginID && update.Available && update.AvailableVersion != installedVersion {
				view.Available, view.AvailableVersion = true, update.AvailableVersion
			}
		}
	}
	operation, err := h.homePackageUpgrades.Status(ctx, owner, pluginID)
	switch {
	case err != nil:
		logger.Warn("Home package upgrade status unavailable", logger.Fields{"plugin": pluginID, "error": err.Error()})
		view.StatusUnavailable = true
	case operation == nil:
	case operation.Active():
		view.Operation = providerUpgradeOperation(*operation)
	default:
		view.LastOperation = providerUpgradeOperation(*operation)
	}
	return view
}

// providerUpgradeHomeLink is the page of one of the owner's live Homes, built
// from the Home's own record. It is empty for a Home without a folder slug or
// one that is not the owner's; it never carries the workspace ID.
func (h *Handler) providerUpgradeHomeLink(owner, homeID string) string {
	if h == nil || h.workspaceTaskStore == nil {
		return ""
	}
	home, err := h.workspaceTaskStore.Get(homeID)
	if err != nil || home == nil {
		return ""
	}
	return ownerHomeLink(owner, home)
}

func ownerHomeLink(owner string, home *workspace.Workspace) string {
	slug := strings.TrimSpace(home.FolderSlug)
	if slug == "" || home.OwnerUserID != owner || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return ""
	}
	return "/workspaces/" + url.PathEscape(slug) + "/assistant"
}

func providerUpgradeOperation(operation homeupgrade.Operation) *providerUpgradeOperationView {
	return &providerUpgradeOperationView{
		ID: operation.ID, Status: operation.Status, FromVersion: operation.Plan.FromVersion, ToVersion: operation.Plan.ToVersion,
		Reason: operation.Outcome.Reason, Agents: operation.Outcome.Agents, UpdatedAt: operation.UpdatedAt,
	}
}

// GetAssistantProviderUpgrade is GET .../assistant-program/provider-upgrade.
func (h *Handler) GetAssistantProviderUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, station, ok := h.providerUpgradeScope(w, r)
	if !ok {
		return
	}
	_ = orihttp.RespondSuccess(w, h.providerUpgradeSummary(r.Context(), owner, station))
}

// ReviewAssistantProviderUpgrade is POST .../assistant-program/provider-upgrade/review.
// It inspects the release the Plugins page would install and records the
// review; it installs and rebinds nothing.
func (h *Handler) ReviewAssistantProviderUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, station, ok := h.providerUpgradeScope(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	h.respondProviderUpgradeReview(w, r, owner, station.GetAssistantProgramState().HomeProvider.PluginID)
}

// respondProviderUpgradeReview reviews the upgrade of every Home the package
// backs, whichever page asked.
func (h *Handler) respondProviderUpgradeReview(w http.ResponseWriter, r *http.Request, owner, pluginID string) {
	review, err := h.homePackageUpgrades.Review(r.Context(), owner, pluginID)
	if err != nil {
		respondProviderUpgradeError(w, err, nil)
		return
	}
	plan := review.Plan
	view := providerUpgradeReviewView{
		Token: review.Token, ExpiresAt: review.ExpiresAt, PluginID: plan.PluginID,
		FromVersion: plan.FromVersion, ToVersion: plan.ToVersion, TrashedHomes: plan.TrashedHomes,
		Roles: []providerUpgradeRoleView{}, Homes: make([]providerUpgradeHomeView, 0, len(plan.Homes)),
		Skills: append([]string{}, review.Trust.Skills...), Warnings: review.Trust.Warnings,
	}
	for _, program := range plan.Programs {
		for _, change := range program.RolePrompts {
			view.Roles = append(view.Roles, providerUpgradeRoleView{RoleID: change.RoleID, Label: change.Label, Old: change.Old, New: change.New})
		}
		if program.AddsHomeProfile {
			view.Additions = append(view.Additions, providerUpgradeProfileAddition(program.HomeProfileTitle))
		}
		for _, attachment := range program.AddsResourceAttachments {
			view.Additions = append(view.Additions, providerUpgradeResourceAddition(attachment.Label))
		}
	}
	for _, home := range plan.Homes {
		projects := make([]string, 0, len(home.Projects))
		for _, project := range home.Projects {
			projects = append(projects, project.Name)
		}
		agents := append([]homeupgrade.PlanAgent{}, home.Agents...)
		view.Homes = append(view.Homes, providerUpgradeHomeView{
			Name: home.Name, Link: h.providerUpgradeHomeLink(owner, home.HomeID), Projects: projects, Agents: agents,
		})
	}
	_ = orihttp.RespondSuccess(w, view)
}

// CommitAssistantProviderUpgrade is POST .../assistant-program/provider-upgrade/commit.
func (h *Handler) CommitAssistantProviderUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, station, ok := h.providerUpgradeScope(w, r)
	if !ok {
		return
	}
	h.respondProviderUpgradeCommit(w, r, owner, station.GetAssistantProgramState().HomeProvider.PluginID)
}

// respondProviderUpgradeCommit commits exactly the reviewed plan the token
// names; the service re-derives it and refuses one that changed.
func (h *Handler) respondProviderUpgradeCommit(w http.ResponseWriter, r *http.Request, owner, pluginID string) {
	var request providerUpgradeCommitRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Token) == "" {
		_ = orihttp.RespondBadRequest(w, "Review the upgrade first")
		return
	}
	operation, err := h.homePackageUpgrades.Commit(r.Context(), owner, pluginID, request.Token)
	if err != nil {
		var view *providerUpgradeOperationView
		if operation.ID != "" {
			view = providerUpgradeOperation(operation)
		}
		respondProviderUpgradeError(w, err, view)
		return
	}
	_ = orihttp.RespondSuccess(w, providerUpgradeOperation(operation))
}

// OutcomeAssistantProviderUpgrade is POST .../assistant-program/provider-upgrade/outcome.
func (h *Handler) OutcomeAssistantProviderUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, station, ok := h.providerUpgradeScope(w, r)
	if !ok {
		return
	}
	h.respondProviderUpgradeAttempt(w, r, owner, station.GetAssistantProgramState().HomeProvider.PluginID)
}

// respondProviderUpgradeAttempt answers a client that lost a commit's
// response. The token travels in the body, never in a URL, and the answer is
// only that review's own operation: reading it settles an interrupted
// operation as every upgrade read does, and never commits.
func (h *Handler) respondProviderUpgradeAttempt(w http.ResponseWriter, r *http.Request, owner, pluginID string) {
	var request providerUpgradeCommitRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Token) == "" {
		_ = orihttp.RespondBadRequest(w, "Name the reviewed update")
		return
	}
	attempt, err := h.homePackageUpgrades.Attempt(r.Context(), owner, pluginID, request.Token)
	if err != nil {
		respondProviderUpgradeError(w, err, nil)
		return
	}
	view := providerUpgradeAttemptView{Review: attempt.Review}
	if attempt.Operation != nil {
		view.Operation, view.Review = providerUpgradeOperation(*attempt.Operation), ""
	}
	_ = orihttp.RespondSuccess(w, view)
}

var providerUpgradeMessages = map[string]string{
	"home_upgrade_no_homes":           "No Home uses this plugin, so it can be updated from the Plugins page.",
	"home_upgrade_current":            "No newer release of this plugin is available.",
	"home_upgrade_not_guidance_only":  "The newer release changes more than the Home's guidance, so it cannot be applied to existing Homes.",
	"home_upgrade_other_owner":        "Another person's Home uses this plugin, so it cannot be upgraded here.",
	"home_upgrade_pin_mismatch":       "A Home or linked project does not match the installed release. Nothing was changed.",
	"home_upgrade_mirrors_disagree":   "A Home or linked project's saved copies disagree. Nothing was changed.",
	"home_upgrade_repair_active":      "A project role repair is in progress. Finish it, then review the upgrade again.",
	"home_upgrade_active":             "Another upgrade of this plugin is in progress or needs attention.",
	"home_upgrade_review_stale":       "The review expired or something changed since. Review the upgrade again.",
	"home_upgrade_too_large":          "Too many Homes or linked projects to upgrade at once.",
	"home_upgrade_reconcile_required": "The upgrade stopped partway and needs attention. Nothing will be chosen automatically.",
	"home_upgrade_unavailable":        "The upgrade could not be completed. Nothing was changed; try again.",
}

func respondProviderUpgradeError(w http.ResponseWriter, err error, operation *providerUpgradeOperationView) {
	code := homeupgrade.Code(err)
	status := http.StatusConflict
	if code == "home_upgrade_unavailable" && operation == nil {
		status = http.StatusServiceUnavailable
	}
	if code == "home_upgrade_unavailable" && operation != nil && operation.Status == homeupgrade.StatusCancelled {
		code = "home_upgrade_cancelled"
	}
	message := providerUpgradeMessages[code]
	if message == "" {
		message = "The new release could not be installed. Nothing was changed; review the upgrade again."
	}
	apiErr := orihttp.NewAPIError(code, message)
	if operation != nil {
		apiErr = apiErr.WithDetails(operation)
	}
	if writeErr := orihttp.RespondAPIError(w, status, apiErr); writeErr != nil && !errors.Is(writeErr, http.ErrHandlerTimeout) {
		logger.Error("Failed to write response", logger.Fields{"error": writeErr})
	}
}
