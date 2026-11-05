package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The staffing-only finish (docs/architecture/project-library.md, "Shared
// assistant on confirmed setup and repair"). Activation connects a song and
// nothing else; this fills the connected song's empty required project roles
// with the Home's one shared assistant under the consent the owner already
// gave, and records it. It writes no ticket, selects no mode, seeds nothing,
// reads no project file and calls no model. Library Open staffs through it too,
// so every path takes the same per-Home turn and reports the same outcomes.

// Staffing outcomes beyond the ones Open already reported (added, joined,
// already, none, off, not_shared, consent_stale, assistant_missing).
const (
	staffingFailed            = "failed"
	staffingUnsettled         = "unsettled"
	staffingReconcile         = "reconcile"
	staffingNeedsModel        = "needs_model"
	staffingInvalid           = "invalid"
	staffingReadOnly          = "read_only"
	staffingUnavailable       = "unavailable"
	staffingUnavailableTarget = "unavailable_target"
	staffingCreate            = "create"
	staffingJoin              = "join"
	staffingSharingChanged    = "sharing_changed"
)

// sharedStaffingOutcome is what one finish did, or why it did nothing.
type sharedStaffingOutcome struct {
	State     string `json:"state"`
	AgentName string `json:"agent_name,omitempty"`
	RoleLabel string `json:"role_label,omitempty"`
	// Shared is true when the role is held by the consent's shared assistant
	// (false for a song's own agent).
	Shared  bool   `json:"shared"`
	Warning string `json:"warning,omitempty"`
}

// staffed reports whether the song's required project roles are filled.
func (o sharedStaffingOutcome) staffed() bool {
	return o.State == openStaffingAdded || o.State == openStaffingJoined || o.State == openStaffingAlready
}

var staffingMessages = map[string]string{
	openStaffingNone:          "This Home has no standing approval to add its shared assistant. Review sharing on the Home first.",
	openStaffingOff:           "Adding the shared assistant is switched off on this Home.",
	openStaffingNotShare:      "This project's role is not filled by a shared assistant. Hire or assign one on the project.",
	"consent_stale":           "The assistant changed in a plugin update. Review the updated assistant on the Home first.",
	"assistant_missing":       "The shared assistant this Home created is gone. Add it again from the Home's sharing panel, then retry.",
	staffingFailed:            "The assistant could not be added. Nothing else changed; try again.",
	staffingUnsettled:         "The assistant was added, but Ori could not record it as the shared one yet. Try again to finish.",
	staffingReconcile:         "An agent already uses the name reserved for the shared assistant, and Ori cannot confirm it is that assistant. Rename or remove it on the Agents page, then retry.",
	staffingNeedsModel:        "Set up a model so the new shared assistant has something to run on, then retry.",
	staffingInvalid:           "This Home's sharing record cannot be read. Review sharing on the Home.",
	staffingReadOnly:          "The Home's integration is unavailable, so its projects cannot be changed now.",
	staffingUnavailable:       "No installed blueprint opens this Home's projects, so there is no assistant to add.",
	staffingUnavailableTarget: "This project or its Home is not available for changes.",
	staffingSharingChanged:    "The assistant changed while you were looking. Review it again.",
}

func staffingMessage(state string) string {
	if message, ok := staffingMessages[state]; ok {
		return message
	}
	return staffingMessages[staffingFailed]
}

// lockHomeStaffing takes the Home's staffing turn for a handler that writes the
// consent itself (the sharing switch and re-add).
func (h *Handler) lockHomeStaffing(homeID string) func() {
	if h.projectStaffing == nil {
		return func() {}
	}
	return h.projectStaffing.Hold(homeID)
}

// stateForStaffingErr maps the staffing decision's refusals to outcomes.
func stateForStaffingErr(err error, consent *workspace.ProjectStaffingConsent) string {
	switch {
	case errors.Is(err, projectstaffing.ErrNoConsent):
		if consent != nil && consent.RevokedAt != nil {
			return openStaffingOff
		}
		return openStaffingNone
	case errors.Is(err, projectstaffing.ErrNotShared):
		return openStaffingNotShare
	case errors.Is(err, projectstaffing.ErrConsentStale):
		return workspace.ProjectStaffingConsentStale
	case errors.Is(err, projectstaffing.ErrAssistantMissing):
		return workspace.ProjectStaffingAssistantMissing
	case errors.Is(err, projectstaffing.ErrReservationHeld):
		return staffingReconcile
	case errors.Is(err, workspace.ErrProjectStaffingConsentInvalid):
		return staffingInvalid
	default:
		return staffingFailed
	}
}

type staffingFinishOptions struct {
	// providers also requires the Home provider to be writable and the exact
	// installed Home and project providers: the explicit finish. Open has
	// already passed its own eligibility gate.
	providers bool
	// approvedDigest, when set, is the team digest the person confirmed: the
	// consent must still be for exactly that team inside the turn.
	approvedDigest string
}

// finishSharedStaffing is the one consent-driven fill of a connected song's
// empty required project roles. It runs as one turn of the Home's staffing
// lock and re-reads everything inside it.
func (h *Handler) finishSharedStaffing(ctx context.Context, scope projectlibrary.Scope, homeID, songID string, opts staffingFinishOptions) sharedStaffingOutcome {
	if h.projectStaffing == nil {
		return sharedStaffingOutcome{State: openStaffingNone}
	}
	var outcome sharedStaffingOutcome
	_ = h.projectStaffing.WithHome(homeID, func(tx *projectstaffing.Tx) error {
		outcome = h.finishSharedStaffingLocked(ctx, scope, tx, songID, opts)
		return nil
	})
	return outcome
}

func (h *Handler) finishSharedStaffingLocked(ctx context.Context, scope projectlibrary.Scope, tx *projectstaffing.Tx, songID string, opts staffingFinishOptions) sharedStaffingOutcome {
	staffs := h.projectStaffing
	song, err := staffs.Song(songID)
	if err != nil {
		return sharedStaffingOutcome{State: openStaffingNotShare}
	}
	home := song.Home
	if home.ID != tx.HomeID() || home.OwnerUserID != scope.OwnerUserID || song.Project.OwnerUserID != scope.OwnerUserID ||
		home.Status != workspace.StatusActive || song.Project.Status != workspace.StatusActive {
		return sharedStaffingOutcome{State: staffingUnavailableTarget}
	}
	if opts.providers && !h.staffingProvidersReady(scope, home, song) {
		return sharedStaffingOutcome{State: staffingReadOnly}
	}
	consent, err := staffs.Consents().Read(home.ID)
	if err != nil {
		return sharedStaffingOutcome{State: staffingInvalid}
	}
	if opts.approvedDigest != "" && consent != nil && consent.TeamDigest != opts.approvedDigest {
		return sharedStaffingOutcome{State: staffingSharingChanged}
	}
	team, haveTeam := h.projectTeamFor(scope, home)
	label := strings.Join(teamLabels(team), ", ")
	// Stale is any disagreement between what the owner agreed to, what is
	// installed and what this song was made with.
	if consent.Active() && consent.Covers(song.Provider.PluginID, song.Provider.BlueprintID) {
		if !haveTeam {
			return sharedStaffingOutcome{State: staffingUnavailable, RoleLabel: label}
		}
		if team.Digest != consent.TeamDigest || song.Provider.ProjectTeamDigest != consent.TeamDigest {
			return sharedStaffingOutcome{State: workspace.ProjectStaffingConsentStale, RoleLabel: label}
		}
	}
	// A create bound in some song but never recorded becomes the consent's
	// agent before anything is decided (proof: a created binding of the
	// reserved name, made after the reservation).
	if err := tx.Recover(); err != nil {
		logger.Warn("Shared staffing: recovery could not settle a project", logger.Fields{"home_id": home.ID, "error": err.Error()})
	}
	if song, err = staffs.Song(songID); err != nil {
		return sharedStaffingOutcome{State: openStaffingNotShare}
	}
	missing := missingRoles(song)
	if len(missing) == 0 {
		return h.alreadyFilled(tx, songID, label)
	}
	if holder := tx.UnrecordedHolder(missing); holder != "" {
		return sharedStaffingOutcome{State: staffingReconcile, AgentName: holder, RoleLabel: label}
	}
	plan, err := staffs.Plan(songID, missing)
	if err != nil {
		return sharedStaffingOutcome{State: stateForStaffingErr(err, consent), RoleLabel: label}
	}
	for _, fill := range plan {
		if fill.Mode == projectstaffing.ModeCreate && h.staffingModelReady != nil && !h.staffingModelReady() {
			return sharedStaffingOutcome{State: staffingNeedsModel, RoleLabel: label}
		}
	}
	if h.assistantWorkspaceRoleStaffer == nil {
		return sharedStaffingOutcome{State: staffingFailed, RoleLabel: label}
	}
	fills, err := tx.Fill(songID, missing, nil)
	if err != nil {
		return sharedStaffingOutcome{State: stateForStaffingErr(err, consent), RoleLabel: label}
	}
	outcome := sharedStaffingOutcome{State: openStaffingJoined, RoleLabel: label, Shared: true}
	created := map[string]string{}
	for _, fill := range fills {
		mode := "assign"
		if fill.Mode == projectstaffing.ModeCreate {
			mode = "create"
		}
		if err := h.assistantWorkspaceRoleStaffer(ctx, songID, []RoleStaffingFill{{RoleID: fill.RoleID, Mode: mode, Name: fill.Name}}); err != nil {
			logger.Warn("Shared staffing: the role could not be filled", logger.Fields{"workspace_id": songID, "role_id": fill.RoleID, "error": err.Error()})
			return h.afterStaffingError(tx, songID, fill, label)
		}
		if fill.Mode == projectstaffing.ModeCreate {
			created[fill.RoleID] = fill.Name
			outcome.State = openStaffingAdded
		}
		outcome.AgentName = fill.Name
	}
	settleErr := tx.Settle(songID)
	recorded, readErr := staffs.Consents().Read(home.ID)
	if readErr != nil || recorded == nil {
		return sharedStaffingOutcome{State: staffingUnsettled, AgentName: outcome.AgentName, RoleLabel: label}
	}
	for roleID, name := range created {
		if entry, found := recorded.Role(roleID); !found || entry.AgentName != name {
			return sharedStaffingOutcome{State: staffingUnsettled, AgentName: name, RoleLabel: label}
		}
	}
	if settleErr != nil {
		// The agent is recorded; only tracking this song's copy failed, so a
		// later edit to the shared assistant will not reach this song.
		logger.Warn("Shared staffing: the song's copy was not tracked", logger.Fields{"workspace_id": songID, "error": settleErr.Error()})
		outcome.Warning = "copies_untracked"
	}
	return outcome
}

// alreadyFilled finishes a song whose roles are filled: it settles the song (a
// shared create bound here but never recorded is recorded now) and says who
// holds the role.
func (h *Handler) alreadyFilled(tx *projectstaffing.Tx, songID, label string) sharedStaffingOutcome {
	staffs := h.projectStaffing
	settleErr := tx.Settle(songID)
	song, err := staffs.Song(songID)
	if err != nil {
		return sharedStaffingOutcome{State: openStaffingNotShare}
	}
	consent, _ := staffs.Consents().Read(song.Home.ID)
	outcome := sharedStaffingOutcome{State: openStaffingAlready, RoleLabel: label}
	filled := song.FilledRoles()
	for _, role := range song.RequiredRoles() {
		instance, found := filled[role.ID]
		if !found {
			continue
		}
		outcome.AgentName = instance.Name
		entry, covered := consent.Role(role.ID)
		switch {
		case covered && entry.AgentName != "" && entry.AgentName == instance.Name:
			if !staffs.AgentExists(instance.Name) {
				// Bound to the shared assistant, which has since been deleted.
				return sharedStaffingOutcome{State: workspace.ProjectStaffingAssistantMissing, AgentName: instance.Name, RoleLabel: label}
			}
			outcome.Shared = true
		case covered && entry.AgentName == "" && entry.PendingName == instance.Name &&
			instance.RoleSource == workspace.RoleSourceCreated &&
			(entry.ReservedAt == nil || !instance.CreatedAt.Before(*entry.ReservedAt)):
			// Bound under the reservation but still not recorded. (An older
			// agent that only shares the name is the song's own: already.)
			return sharedStaffingOutcome{State: staffingUnsettled, AgentName: instance.Name, RoleLabel: label}
		}
	}
	if settleErr != nil && outcome.Shared {
		outcome.Warning = "copies_untracked"
	}
	return outcome
}

// afterStaffingError reads state again after a staffer call failed, so the
// report says what is true now rather than what was attempted.
func (h *Handler) afterStaffingError(tx *projectstaffing.Tx, songID string, fill projectstaffing.Fill, label string) sharedStaffingOutcome {
	staffs := h.projectStaffing
	if song, err := staffs.Song(songID); err == nil {
		if _, filled := song.FilledRoles()[fill.RoleID]; filled {
			// Someone else filled it in between (a manual Hire, another tab).
			return h.alreadyFilled(tx, songID, label)
		}
	}
	if fill.Mode == projectstaffing.ModeBind && !staffs.AgentExists(fill.Name) {
		return sharedStaffingOutcome{State: workspace.ProjectStaffingAssistantMissing, AgentName: fill.Name, RoleLabel: label}
	}
	if fill.Mode == projectstaffing.ModeCreate {
		if staffs.AgentExists(fill.Name) {
			// An agent holds the reserved name. It may be another path's create
			// that will bind and record it in a moment, or this create whose
			// rollback could not delete it: either way it is never released
			// (the next create would make a second shared assistant) and never
			// adopted. The owner decides.
			return sharedStaffingOutcome{State: staffingReconcile, AgentName: fill.Name, RoleLabel: label}
		}
		// Nothing was made: give the reservation up, so a later manual Hire of
		// the same name is never mistaken for this create. A retry reserves
		// again.
		if err := tx.ReleaseReservation(fill.RoleID, fill.Name); err != nil {
			logger.Warn("Shared staffing: the reservation was not released", logger.Fields{"workspace_id": songID, "error": err.Error()})
		}
	}
	return sharedStaffingOutcome{State: staffingFailed, RoleLabel: label}
}

// staffingProvidersReady is the explicit finish's target check: the Home's
// provider is writable and the exact installed Home and project providers
// are present.
func (h *Handler) staffingProvidersReady(scope projectlibrary.Scope, home *workspace.Workspace, song projectstaffing.Song) bool {
	if h.staffingProviderOverride != nil {
		return h.staffingProviderOverride(scope, home, song.Project)
	}
	if !h.assistantLibraryProviderEvidence(scope, home) {
		return false
	}
	state := home.GetAssistantProgramState()
	owner := state.HomeProvider
	if owner == nil && state.GroupTemplate != nil {
		owner = state.GroupTemplate.ProgramHomeOwner
	}
	installed, err := h.installedPluginLister.List()
	return err == nil && plugin.IndependentProviderEvidenceAvailable(installed, owner, song.Link.ProjectProvider)
}

func missingRoles(song projectstaffing.Song) []string {
	filled := song.FilledRoles()
	var missing []string
	for _, role := range song.RequiredRoles() {
		if _, done := filled[role.ID]; !done {
			missing = append(missing, role.ID)
		}
	}
	return missing
}

func teamLabels(team projectlibrary.ProjectTeam) []string {
	labels := make([]string, 0, len(team.Roles))
	for _, role := range team.Roles {
		labels = append(labels, role.Label)
	}
	return labels
}

// ---- Inert reads -----------------------------------------------------------

// librarySetupPlan is what confirming setup would do about the assistant for a
// song that is not connected yet. It reserves nothing and writes nothing.
type librarySetupPlan struct {
	Sharing   librarySharingView `json:"sharing"`
	Plan      string             `json:"plan"`
	AgentName string             `json:"agent_name,omitempty"`
	RoleLabel string             `json:"role_label,omitempty"`
	Message   string             `json:"message,omitempty"`
}

func (h *Handler) homeStaffingPlan(scope projectlibrary.Scope, home *workspace.Workspace) librarySetupPlan {
	view := h.librarySharing(scope, home)
	plan := librarySetupPlan{Sharing: view, RoleLabel: view.RoleLabel}
	finish := func(state string) librarySetupPlan {
		plan.Plan = state
		if state != staffingCreate && state != staffingJoin {
			plan.Message = staffingMessage(state)
		}
		return plan
	}
	if h.projectStaffing == nil {
		return finish(openStaffingNone)
	}
	if !h.assistantLibraryProviderEvidenceOrOverride(scope, home) {
		return finish(staffingReadOnly)
	}
	if _, err := h.projectStaffing.Consents().Read(home.ID); err != nil {
		return finish(staffingInvalid)
	}
	switch view.State {
	case sharingUnavailable:
		return finish(staffingUnavailable)
	case sharingNone:
		return finish(openStaffingNone)
	case sharingOff:
		return finish(openStaffingOff)
	case sharingStale:
		return finish(workspace.ProjectStaffingConsentStale)
	}
	if view.AssistantMissing {
		plan.AgentName = view.AgentName
		return finish(workspace.ProjectStaffingAssistantMissing)
	}
	if view.AgentName != "" {
		plan.AgentName = view.AgentName
		return finish(staffingJoin)
	}
	team, _ := h.projectTeamFor(scope, home)
	roleIDs := make([]string, 0, len(team.Roles))
	for _, role := range team.Roles {
		roleIDs = append(roleIDs, role.ID)
	}
	if holder := h.projectStaffing.PendingHolder(home.ID, roleIDs); holder != "" {
		plan.AgentName = holder
		return finish(staffingReconcile)
	}
	if h.staffingModelReady != nil && !h.staffingModelReady() {
		return finish(staffingNeedsModel)
	}
	if len(team.Roles) > 0 {
		plan.AgentName = h.projectStaffing.FreeName(home.ID, team.Roles[0].Label)
	}
	return finish(staffingCreate)
}

func (h *Handler) assistantLibraryProviderEvidenceOrOverride(scope projectlibrary.Scope, home *workspace.Workspace) bool {
	if h.staffingProviderOverride != nil {
		return h.staffingProviderOverride(scope, home, nil)
	}
	return h.assistantLibraryProviderEvidence(scope, home)
}

// GetAssistantLibraryStaffing handles
// GET /api/workspaces/{workspaceID}/assistant-program/library/staffing:
// what confirming setup would do about the assistant for songs not yet
// connected.
func (h *Handler) GetAssistantLibraryStaffing(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	_ = orihttp.RespondSuccess(w, h.homeStaffingPlan(scope, home))
}

// libraryRowStaffing is one catalog row's assistant, read only.
type libraryRowStaffing struct {
	EntryID     string `json:"entry_id"`
	Connected   bool   `json:"connected"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Filled      bool   `json:"filled"`
	Shared      bool   `json:"shared"`
	AgentName   string `json:"agent_name,omitempty"`
	RoleLabel   string `json:"role_label,omitempty"`
	// Plan is what the finish would do for an empty role (create, join or a
	// stop); empty when the role is filled.
	Plan    string `json:"plan,omitempty"`
	Message string `json:"message,omitempty"`
}

// connectedLibrarySong resolves one catalog row to the workspace linked to this
// Home, re-reading the row's eligibility; "" when it is not connected.
func (h *Handler) connectedLibrarySong(ctx context.Context, scope projectlibrary.Scope, entryID string) (string, error) {
	opener := h.libraryOpener()
	if opener == nil {
		return "", ErrLibraryOpenUnavailable
	}
	eligible, err := opener.Eligibility(ctx, scope, entryID)
	if err != nil {
		return "", err
	}
	if eligible.State != "connected" || strings.TrimSpace(eligible.WorkspaceID) == "" {
		return "", nil
	}
	return eligible.WorkspaceID, nil
}

func (h *Handler) rowStaffing(ctx context.Context, scope projectlibrary.Scope, home *workspace.Workspace, entryID string) (libraryRowStaffing, error) {
	row := libraryRowStaffing{EntryID: entryID}
	songID, err := h.connectedLibrarySong(ctx, scope, entryID)
	if err != nil {
		return row, err
	}
	if songID == "" {
		plan := h.homeStaffingPlan(scope, home)
		row.Plan, row.AgentName, row.RoleLabel, row.Message = plan.Plan, plan.AgentName, plan.RoleLabel, plan.Message
		return row, nil
	}
	row.Connected, row.WorkspaceID = true, songID
	if h.projectStaffing == nil {
		row.Plan, row.Message = openStaffingNone, staffingMessage(openStaffingNone)
		return row, nil
	}
	song, err := h.projectStaffing.Song(songID)
	if err != nil || song.Home.ID != home.ID {
		row.Plan, row.Message = openStaffingNotShare, staffingMessage(openStaffingNotShare)
		return row, nil
	}
	team, _ := h.projectTeamFor(scope, home)
	row.RoleLabel = strings.Join(teamLabels(team), ", ")
	consent, consentErr := h.projectStaffing.Consents().Read(home.ID)
	filled := song.FilledRoles()
	missing := missingRoles(song)
	if len(missing) == 0 {
		row.Filled = true
		for _, role := range song.RequiredRoles() {
			if instance, found := filled[role.ID]; found {
				row.AgentName = instance.Name
				if entry, covered := consent.Role(role.ID); covered && entry.AgentName == instance.Name {
					row.Shared = true
					if !h.projectStaffing.AgentExists(instance.Name) {
						row.Filled, row.Plan = false, workspace.ProjectStaffingAssistantMissing
						row.Message = staffingMessage(row.Plan)
					}
				}
			}
		}
		return row, nil
	}
	state := staffingJoin
	switch {
	case consentErr != nil:
		state = staffingInvalid
	case h.projectStaffing.PendingHolder(home.ID, missing) != "":
		state = staffingReconcile
	default:
		plan, planErr := h.projectStaffing.Plan(songID, missing)
		if planErr != nil {
			state = stateForStaffingErr(planErr, consent)
			break
		}
		for _, fill := range plan {
			row.AgentName = fill.Name
			if fill.Mode == projectstaffing.ModeCreate {
				state = staffingCreate
				if h.staffingModelReady != nil && !h.staffingModelReady() {
					state = staffingNeedsModel
				}
			}
		}
		if consent.Active() && (team.Digest != consent.TeamDigest || song.Provider.ProjectTeamDigest != consent.TeamDigest) {
			state = workspace.ProjectStaffingConsentStale
		}
	}
	row.Plan = state
	if state != staffingCreate && state != staffingJoin {
		row.Message = staffingMessage(state)
	}
	return row, nil
}

// GetAssistantLibraryProjectStaffing handles
// GET /api/workspaces/{workspaceID}/assistant-program/library/projects/{entryID}/staffing.
// It only reads: no reservation, no agent, no ticket, no mode.
func (h *Handler) GetAssistantLibraryProjectStaffing(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryRowExists(w, scope, r.PathValue("entryID")) {
		return
	}
	row, err := h.rowStaffing(r.Context(), scope, home, r.PathValue("entryID"))
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, row)
}

// ---- From the project itself ----------------------------------------------
//
// A song already on the map that never got the Home's assistant can finish it
// from its own page. The browser names only the workspace: its Home and its
// catalog row are resolved here, from the workspace's own link and the exact
// catalog link back to it, never from names or a client scan.

// projectStaffingTarget is one project workspace resolved to its Home and the
// one catalog row linked to it.
type projectStaffingTarget struct {
	scope   projectlibrary.Scope
	home    *workspace.Workspace
	entryID string
	songID  string
}

// resolveProjectStaffing answers 404 for a workspace that is not the caller's;
// ok=false with no response written means it is not a library project (the
// caller answers "not eligible").
func (h *Handler) resolveProjectStaffing(w http.ResponseWriter, r *http.Request) (projectStaffingTarget, bool, bool) {
	var target projectStaffingTarget
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return target, false, true
	}
	owner, err := h.currentUserID(r.Context())
	songID := strings.TrimSpace(r.PathValue("workspaceID"))
	song, getErr := h.workspaceTaskStore.Get(songID)
	if err != nil || strings.TrimSpace(owner) == "" || getErr != nil || song == nil || song.ID != songID || song.OwnerUserID != owner {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return target, false, true
	}
	link := song.GetAssistantProjectLink()
	if link == nil || strings.TrimSpace(link.StationWorkspaceID) == "" {
		return target, false, false
	}
	scope, err := h.portfolioLibraryScope(owner, link.StationWorkspaceID)
	if err != nil {
		return target, false, false
	}
	home, err := h.workspaceTaskStore.Get(scope.HomeID)
	opener := h.libraryOpener()
	if err != nil || home == nil || opener == nil {
		return target, false, false
	}
	entries, err := opener.LinkedEntries(scope, songID)
	if err != nil || len(entries) != 1 {
		return target, false, false // none, or ambiguous: never guessed
	}
	return projectStaffingTarget{scope: scope, home: home, entryID: entries[0], songID: songID}, true, false
}

// projectStaffingView is a project's own staffing status, read only.
type projectStaffingView struct {
	Eligible   bool   `json:"eligible"`
	HomeID     string `json:"home_id,omitempty"`
	HomeName   string `json:"home_name,omitempty"`
	TeamDigest string `json:"team_digest,omitempty"`
	libraryRowStaffing
}

// GetProjectLibraryStaffing handles GET /api/workspaces/{workspaceID}/library-staffing:
// whether this project can get its Home's shared assistant, and what that
// would do. It only reads.
func (h *Handler) GetProjectLibraryStaffing(w http.ResponseWriter, r *http.Request) {
	target, ok, answered := h.resolveProjectStaffing(w, r)
	if answered {
		return
	}
	if !ok {
		_ = orihttp.RespondSuccess(w, projectStaffingView{})
		return
	}
	row, err := h.rowStaffing(r.Context(), target.scope, target.home, target.entryID)
	if err != nil || !row.Connected || row.WorkspaceID != target.songID {
		_ = orihttp.RespondSuccess(w, projectStaffingView{})
		return
	}
	_ = orihttp.RespondSuccess(w, projectStaffingView{
		Eligible: true, HomeID: target.home.ID, HomeName: target.home.Name,
		TeamDigest: h.librarySharing(target.scope, target.home).TeamDigest, libraryRowStaffing: row,
	})
}

// FinishProjectLibraryStaffing handles POST /api/workspaces/{workspaceID}/library-staffing
// {request_id, team_digest}: the same finish as the Home's, for this project.
func (h *Handler) FinishProjectLibraryStaffing(w http.ResponseWriter, r *http.Request) {
	target, ok, answered := h.resolveProjectStaffing(w, r)
	if answered {
		return
	}
	request, decoded := decodeStaffingRequest(w, r)
	if !decoded {
		return
	}
	if !ok {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "This workspace has no Home assistant to add.", "reason": "not_eligible",
		})
		return
	}
	h.finishLibraryRow(w, r, target.scope, target.home, target.entryID, request, target.songID)
}

// libraryRowExists answers 404 for a row the Home's catalog does not have,
// through the same reviewed opener library Open reads.
func (h *Handler) libraryRowExists(w http.ResponseWriter, scope projectlibrary.Scope, entryID string) bool {
	opener := h.libraryOpener()
	if opener == nil {
		_ = orihttp.RespondConflict(w, "The reviewed project creator is unavailable")
		return false
	}
	exists, err := opener.Exists(scope, entryID)
	if err != nil {
		respondLibraryActionError(w, err)
		return false
	}
	if !exists {
		_ = orihttp.RespondNotFound(w, "Library project not found")
		return false
	}
	return true
}

// ---- The explicit finish ---------------------------------------------------

type libraryStaffingRequest struct {
	RequestID  string `json:"request_id"`
	TeamDigest string `json:"team_digest"`
}

// staffingReceipts remembers what each finish request_id was used for, so a
// key reused for another song or approval is refused. Bounded and in memory:
// after a restart a replay is still safe because the finish converges on the
// current state.
type staffingReceipts struct {
	mu    sync.Mutex
	order []string
	seen  map[string]string
}

const maxStaffingReceipts = 1024

func (r *staffingReceipts) admit(key, fingerprint string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]string{}
	}
	if previous, found := r.seen[key]; found {
		return previous == fingerprint
	}
	if len(r.order) >= maxStaffingReceipts {
		delete(r.seen, r.order[0])
		r.order = r.order[1:]
	}
	r.seen[key], r.order = fingerprint, append(r.order, key)
	return true
}

// FinishAssistantLibraryProjectStaffing handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/projects/{entryID}/staffing
// {request_id, team_digest}: add the Home's shared assistant to one connected
// song under the consent the owner already gave.
func (h *Handler) FinishAssistantLibraryProjectStaffing(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	entryID := r.PathValue("entryID")
	if !ok || !h.libraryRowExists(w, scope, entryID) {
		return
	}
	request, ok := decodeStaffingRequest(w, r)
	if !ok {
		return
	}
	h.finishLibraryRow(w, r, scope, home, entryID, request, "")
}

// decodeStaffingRequest is the finish's strict body: request_id and the exact
// team digest the person confirmed, nothing else.
func decodeStaffingRequest(w http.ResponseWriter, r *http.Request) (libraryStaffingRequest, bool) {
	var request libraryStaffingRequest
	if !decodeLibraryAction(w, r, &request) {
		return request, false
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	if request.RequestID == "" || len(request.RequestID) > 120 || strings.ContainsAny(request.RequestID, "\x00\r\n") ||
		len(request.TeamDigest) != 64 {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return request, false
	}
	return request, true
}

// finishLibraryRow runs the finish for one catalog row of the Home. expectSong,
// when set, is the workspace the caller resolved the row from: the row must
// still be linked to exactly that workspace.
func (h *Handler) finishLibraryRow(w http.ResponseWriter, r *http.Request, scope projectlibrary.Scope, home *workspace.Workspace, entryID string, request libraryStaffingRequest, expectSong string) {
	if !h.staffingReceipts.admit(scope.HomeID+"\x00"+request.RequestID, entryID+"\x00"+request.TeamDigest) {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "This request was already used for something else.", "reason": "request_reused",
		})
		return
	}
	songID, err := h.connectedLibrarySong(r.Context(), scope, entryID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	if expectSong != "" && songID != expectSong {
		songID = ""
	}
	if songID == "" {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "This project is not on the map yet.", "reason": "not_connected",
		})
		return
	}
	// The owner confirmed this exact approval. A different team now is a new
	// question, never silently the same answer.
	if view := h.librarySharing(scope, home); view.TeamDigest != request.TeamDigest {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "The assistant changed while you were looking. Review it again.", "reason": "sharing_changed", "sharing": view,
		})
		return
	}
	outcome := h.finishSharedStaffing(r.Context(), scope, home.ID, songID, staffingFinishOptions{providers: true, approvedDigest: request.TeamDigest})
	if !outcome.staffed() {
		body := map[string]any{"error": staffingMessage(outcome.State), "reason": outcome.State, "workspace_id": songID, "staffing": outcome}
		if fresh, getErr := h.workspaceTaskStore.Get(home.ID); getErr == nil && fresh != nil {
			body["sharing"] = h.librarySharing(scope, fresh)
		}
		_ = orihttp.RespondJSON(w, http.StatusConflict, body)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"entry_id": entryID, "workspace_id": songID, "staffing": outcome})
}
