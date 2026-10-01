package sessionhttp

import (
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Home's switch for its shared assistant (D7) and the two ways back when
// the consent cannot apply: a plugin update changed the team (stale, asked once
// on the Home, never per song) or the assistant it created is gone. Each change
// is the owner's, on this Home only; none is an agent tool.

// Where the Home's consent for its shared assistant stands.
const (
	sharingUnavailable = "unavailable" // no installed blueprint opens this Home's songs
	sharingNone        = "none"        // never agreed (e.g. a Home built before this)
	sharingOn          = "on"
	sharingOff         = "off"
	sharingStale       = "stale" // agreed for an older team; review the updated one
)

type librarySharingView struct {
	State     string `json:"state"`
	RoleLabel string `json:"role_label,omitempty"`
	// AgentName is the assistant the consent created; empty until the first open.
	AgentName        string `json:"agent_name,omitempty"`
	AssistantMissing bool   `json:"assistant_missing,omitempty"`
	// TeamDigest is what turning sharing on (or reviewing it) agrees to: the
	// installed blueprint's team. The browser sends it back unchanged.
	TeamDigest string `json:"team_digest,omitempty"`
}

func (h *Handler) projectTeamFor(scope projectlibrary.Scope, home *workspace.Workspace) (projectlibrary.ProjectTeam, bool) {
	if h.projectTeamOverride != nil {
		return h.projectTeamOverride(scope, home)
	}
	return h.installedProjectTeam(scope, home)
}

func (h *Handler) agentExists(name string) bool {
	return strings.TrimSpace(name) != "" && h.projectStaffing.AgentExists(name)
}

// librarySharing reads the Home's consent against the installed blueprint.
func (h *Handler) librarySharing(scope projectlibrary.Scope, home *workspace.Workspace) librarySharingView {
	team, ok := h.projectTeamFor(scope, home)
	if !ok || len(team.Roles) == 0 || home == nil {
		return librarySharingView{State: sharingUnavailable}
	}
	labels := make([]string, 0, len(team.Roles))
	for _, role := range team.Roles {
		labels = append(labels, role.Label)
	}
	view := librarySharingView{State: sharingNone, RoleLabel: strings.Join(labels, ", "), TeamDigest: team.Digest}
	consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
	if consent == nil || consent.Validate() != nil || !consent.Covers(team.PluginID, team.BlueprintID) {
		return view
	}
	switch {
	case consent.RevokedAt != nil:
		view.State = sharingOff
	case consent.TeamDigest != team.Digest:
		view.State = sharingStale
	default:
		view.State = sharingOn
	}
	for _, role := range team.Roles {
		if entry, covered := consent.Role(role.ID); covered && entry.AgentName != "" {
			view.AgentName = entry.AgentName
			view.AssistantMissing = !h.agentExists(entry.AgentName)
		}
	}
	return view
}

type librarySharingRequest struct {
	RequestID  string `json:"request_id"`
	Enabled    bool   `json:"enabled"`
	TeamDigest string `json:"team_digest,omitempty"`
}

// SetAssistantLibrarySharing handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/sharing:
// switch the Home's shared assistant off, or on (a fresh consent, also how a
// stale one is renewed after its review). Turning it on agrees to the exact team
// the switch showed: a team_digest that no longer matches is a 409 with the
// fresh state, never a silent consent to something else.
func (h *Handler) SetAssistantLibrarySharing(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request librarySharingRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 120 || len(request.TeamDigest) > 64 {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return
	}
	if h.projectStaffing == nil {
		_ = orihttp.RespondConflict(w, "Sharing the assistant is unavailable")
		return
	}
	current := h.librarySharing(scope, home)
	if current.State == sharingUnavailable {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "No installed blueprint opens this Home's projects, so there is no assistant to share.", "reason": sharingUnavailable,
		})
		return
	}
	consents := h.projectStaffing.Consents()
	if !request.Enabled {
		if current.State == sharingOn || current.State == sharingStale {
			if _, err := consents.Revoke(home.ID); err != nil {
				respondSharingError(w, err)
				return
			}
		}
	} else {
		if request.TeamDigest != current.TeamDigest {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error": "The assistant changed while you were looking. Review it again.", "reason": "sharing_changed",
				"sharing": current,
			})
			return
		}
		team, ok := h.projectTeamFor(scope, home)
		if !ok {
			respondSharingError(w, errors.New("team unavailable"))
			return
		}
		grant := workspace.ProjectStaffingConsentGrant{
			Source: workspace.ProjectStaffingConsentHomeSwitch, PluginID: team.PluginID,
			BlueprintID: team.BlueprintID, TeamDigest: team.Digest,
		}
		for _, role := range team.Roles {
			grant.RoleIDs = append(grant.RoleIDs, role.ID)
		}
		if _, err := consents.Grant(home.ID, grant); err != nil {
			respondSharingError(w, err)
			return
		}
	}
	h.respondSharing(w, scope, home.ID)
}

// ReAddAssistantLibrarySharing handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/sharing/assistant:
// the shared assistant the consent created is gone, so the next song opened
// gets a fresh one (created then, its name recorded). Songs already open keep
// what they have.
func (h *Handler) ReAddAssistantLibrarySharing(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
	}
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 120 {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return
	}
	if h.projectStaffing == nil {
		_ = orihttp.RespondConflict(w, "Sharing the assistant is unavailable")
		return
	}
	current := h.librarySharing(scope, home)
	if !current.AssistantMissing {
		// Nothing to replace (it exists, or none was made yet): an answer, not an error.
		h.respondSharing(w, scope, home.ID)
		return
	}
	team, ok := h.projectTeamFor(scope, home)
	if !ok {
		respondSharingError(w, errors.New("team unavailable"))
		return
	}
	consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
	for _, role := range team.Roles {
		entry, covered := consent.Role(role.ID)
		if !covered || entry.AgentName == "" || h.agentExists(entry.AgentName) {
			continue
		}
		if _, err := h.projectStaffing.Consents().ForgetAgent(home.ID, team.PluginID, team.BlueprintID, role.ID, entry.AgentName); err != nil {
			respondSharingError(w, err)
			return
		}
	}
	h.respondSharing(w, scope, home.ID)
}

func (h *Handler) respondSharing(w http.ResponseWriter, scope projectlibrary.Scope, homeID string) {
	home, err := h.workspaceTaskStore.Get(homeID)
	if err != nil || home == nil {
		_ = orihttp.RespondConflict(w, "The Home could not be read back")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"sharing": h.librarySharing(scope, home)})
}

func respondSharingError(w http.ResponseWriter, err error) {
	if errors.Is(err, workspace.ErrProjectStaffingConsentInvalid) {
		_ = orihttp.RespondConflict(w, "The Home's sharing changed; reload and try again")
		return
	}
	_ = orihttp.RespondConflict(w, "The Home's sharing could not be changed")
}
