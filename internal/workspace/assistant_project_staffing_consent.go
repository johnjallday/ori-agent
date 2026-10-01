package workspace

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Where a standing staffing consent came from.
const (
	// ProjectStaffingConsentFolderOffer is the folder card's Set up.
	ProjectStaffingConsentFolderOffer = "folder_offer"
	// ProjectStaffingConsentHomeSwitch is the switch on the Home's library panel.
	ProjectStaffingConsentHomeSwitch = "home_switch"
)

// ErrProjectStaffingConsentInvalid means a consent record (stored or requested)
// failed validation. A stored one that fails is treated as no consent.
var ErrProjectStaffingConsentInvalid = errors.New("project staffing consent is invalid")

// ProjectStaffingConsent is the user's one-time agreement that a project role of
// one blueprint is filled, on every project they open in this Home, by the one
// agent the consent created (hire once, assign many). It lives on the Home's
// AssistantProgramState so it travels in the Home envelope and is written under
// the same write fence as the library.
type ProjectStaffingConsent struct {
	GrantedAt time.Time `json:"granted_at"`
	Source    string    `json:"source"`
	OfferID   string    `json:"offer_id,omitempty"`
	// PluginID and BlueprintID name the project blueprint the consent covers;
	// TeamDigest is the fingerprint of its declared project roles
	// (projecttemplates.AssistantProjectDigest, the value pinned on every linked
	// project as AssistantProjectProviderOwner.ProjectTeamDigest).
	PluginID    string                       `json:"plugin_id"`
	BlueprintID string                       `json:"blueprint_id"`
	TeamDigest  string                       `json:"team_digest"`
	Roles       []ProjectStaffingConsentRole `json:"roles"`
	RevokedAt   *time.Time                   `json:"revoked_at,omitempty"`
}

// ProjectStaffingConsentRole is one project role the consent covers. AgentName
// is the agent the consent itself created; it is empty until the first create.
// PendingName is the name a create is about to use: it is reserved before the
// create and becomes AgentName only once that create is seen in a project, so
// a crash between the two can neither lose the agent nor adopt a stranger.
type ProjectStaffingConsentRole struct {
	RoleID      string `json:"role_id"`
	AgentName   string `json:"agent_name,omitempty"`
	PendingName string `json:"pending_name,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
}

// Clone returns a deep copy.
func (c *ProjectStaffingConsent) Clone() *ProjectStaffingConsent {
	if c == nil {
		return nil
	}
	clone := *c
	clone.Roles = append([]ProjectStaffingConsentRole(nil), c.Roles...)
	if c.RevokedAt != nil {
		value := *c.RevokedAt
		clone.RevokedAt = &value
	}
	return &clone
}

// Validate keeps a consent bounded and well formed.
func (c *ProjectStaffingConsent) Validate() error {
	invalid := func(what string) error { return fmt.Errorf("%w: %s", ErrProjectStaffingConsentInvalid, what) }
	if c == nil {
		return invalid("missing")
	}
	if c.GrantedAt.IsZero() {
		return invalid("granted_at")
	}
	if c.Source != ProjectStaffingConsentFolderOffer && c.Source != ProjectStaffingConsentHomeSwitch {
		return invalid("source")
	}
	if !consentText(c.OfferID, 160, true) || !consentText(c.PluginID, 160, false) || !consentText(c.BlueprintID, 160, false) {
		return invalid("identity")
	}
	if !lowerHex(c.TeamDigest, 64) {
		return invalid("team_digest")
	}
	if len(c.Roles) == 0 || len(c.Roles) > AssistantProgramMaxRoles {
		return invalid("roles")
	}
	seen := make(map[string]struct{}, len(c.Roles))
	for _, role := range c.Roles {
		if !consentText(role.RoleID, 80, false) || role.RoleID != strings.ToLower(role.RoleID) ||
			!consentText(role.AgentName, 80, true) || !consentText(role.PendingName, 80, true) ||
			!consentText(role.Provider, 120, true) || !consentText(role.Model, 240, true) {
			return invalid("role")
		}
		if _, duplicate := seen[role.RoleID]; duplicate {
			return invalid("duplicate role")
		}
		seen[role.RoleID] = struct{}{}
	}
	if c.RevokedAt != nil && c.RevokedAt.Before(c.GrantedAt) {
		return invalid("revoked_at")
	}
	return nil
}

func consentText(value string, limit int, optional bool) bool {
	if value == "" {
		return optional
	}
	return len(value) <= limit && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

// Active is true for a valid consent that has not been switched off.
func (c *ProjectStaffingConsent) Active() bool {
	return c != nil && c.RevokedAt == nil && c.Validate() == nil
}

// Covers reports whether the consent is for this exact project blueprint.
func (c *ProjectStaffingConsent) Covers(pluginID, blueprintID string) bool {
	return c != nil && c.PluginID == pluginID && c.BlueprintID == blueprintID
}

// Role returns the consent's entry for one project role.
func (c *ProjectStaffingConsent) Role(roleID string) (ProjectStaffingConsentRole, bool) {
	if c == nil {
		return ProjectStaffingConsentRole{}, false
	}
	for _, role := range c.Roles {
		if role.RoleID == roleID {
			return role, true
		}
	}
	return ProjectStaffingConsentRole{}, false
}

// How a project role is filled under the standing consent.
const (
	ProjectStaffingSkip   = "skip"
	ProjectStaffingCreate = "create"
	ProjectStaffingBind   = "bind"
	ProjectStaffingStop   = "stop"
)

// Why a role is skipped or stopped.
const (
	ProjectStaffingNoConsent        = "no_consent"
	ProjectStaffingConsentRevoked   = "consent_revoked"
	ProjectStaffingNotCovered       = "not_covered"
	ProjectStaffingConsentStale     = "consent_stale"
	ProjectStaffingAssistantMissing = "assistant_missing"
)

// ProjectStaffingFacts are what the decision reads besides the consent itself.
type ProjectStaffingFacts struct {
	PluginID    string
	BlueprintID string
	// TeamDigest is the installed blueprint's project-team digest, as pinned on
	// the project being staffed.
	TeamDigest string
	RoleID     string
	// RoleLabel is the role's declared label: the shared agent's name ("Studio
	// Assistant"), never "<label> · <project>".
	RoleLabel string
	// AgentExists says whether an agent of that name is in the user's agents.
	AgentExists func(name string) bool
	// NameTaken says whether a create may not use the name: an existing agent,
	// or an agent already in the Home or the project.
	NameTaken func(name string) bool
}

// ProjectStaffingDecision is how one project role is filled.
type ProjectStaffingDecision struct {
	Action    string
	Reason    string
	AgentName string
}

// DecideProjectStaffing is the one rule for filling a project role under the
// Home's standing consent:
//
//	no consent, or switched off   → skip (the manual path stays available)
//	another blueprint or role     → skip
//	team digest differs           → stop: consent_stale
//	no agent recorded yet         → create, with the first free name
//	recorded agent exists         → bind that agent
//	recorded agent is gone        → stop: assistant_missing
//
// It never binds an agent the consent did not record: a name taken by anything
// else makes the create use the next free name instead (D3).
func DecideProjectStaffing(consent *ProjectStaffingConsent, facts ProjectStaffingFacts) ProjectStaffingDecision {
	switch {
	case consent == nil || consent.Validate() != nil:
		return ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNoConsent}
	case consent.RevokedAt != nil:
		return ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingConsentRevoked}
	case !consent.Covers(facts.PluginID, facts.BlueprintID):
		return ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNotCovered}
	case consent.TeamDigest != facts.TeamDigest:
		return ProjectStaffingDecision{Action: ProjectStaffingStop, Reason: ProjectStaffingConsentStale}
	}
	role, covered := consent.Role(facts.RoleID)
	if !covered {
		return ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNotCovered}
	}
	if role.AgentName == "" {
		name := FirstFreeAgentName(facts.RoleLabel, facts.NameTaken)
		if name == "" {
			return ProjectStaffingDecision{Action: ProjectStaffingStop, Reason: ProjectStaffingAssistantMissing}
		}
		return ProjectStaffingDecision{Action: ProjectStaffingCreate, AgentName: name}
	}
	if facts.AgentExists != nil && facts.AgentExists(role.AgentName) {
		return ProjectStaffingDecision{Action: ProjectStaffingBind, AgentName: role.AgentName}
	}
	return ProjectStaffingDecision{Action: ProjectStaffingStop, Reason: ProjectStaffingAssistantMissing, AgentName: role.AgentName}
}

// maxAgentNameSuffix bounds the search for a free name.
const maxAgentNameSuffix = 99

// FirstFreeAgentName is label, or "label 2", "label 3", … : the first name taken
// does not claim. It returns "" when the label is empty or no name is free.
func FirstFreeAgentName(label string, taken func(name string) bool) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	for n := 1; n <= maxAgentNameSuffix; n++ {
		name := label
		if n > 1 {
			suffix := " " + strconv.Itoa(n)
			if runes := []rune(label); len(runes)+len(suffix) > 80 {
				name = string(runes[:80-len(suffix)])
			}
			name += suffix
		} else if runes := []rune(label); len(runes) > 80 {
			name = string(runes[:80])
		}
		if taken == nil || !taken(name) {
			return name
		}
	}
	return ""
}

// GetProjectStaffingConsent returns the Home's consent, or nil.
func (state *AssistantProgramState) GetProjectStaffingConsent() *ProjectStaffingConsent {
	if state == nil {
		return nil
	}
	return state.ProjectStaffingConsent.Clone()
}

// ProjectStaffingConsentGrant is a fresh consent to record.
type ProjectStaffingConsentGrant struct {
	Source      string
	OfferID     string
	PluginID    string
	BlueprintID string
	TeamDigest  string
	RoleIDs     []string
}

// ProjectStaffingConsents reads and writes a Home's standing staffing consent.
// Every write is one Update of the Home, so it goes through the workspace write
// fence like every other Home write.
type ProjectStaffingConsents struct {
	store Store
	now   func() time.Time
}

// NewProjectStaffingConsents binds the consent service to a workspace store.
func NewProjectStaffingConsents(store Store) *ProjectStaffingConsents {
	return &ProjectStaffingConsents{store: store, now: time.Now}
}

// Read returns the Home's consent (nil when there is none). A stored record that
// fails validation is reported as ErrProjectStaffingConsentInvalid.
func (c *ProjectStaffingConsents) Read(homeID string) (*ProjectStaffingConsent, error) {
	home, err := c.home(homeID)
	if err != nil {
		return nil, err
	}
	consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
	if consent != nil && consent.Validate() != nil {
		return nil, ErrProjectStaffingConsentInvalid
	}
	return consent, nil
}

func (c *ProjectStaffingConsents) home(homeID string) (*Workspace, error) {
	if c == nil || c.store == nil || strings.TrimSpace(homeID) == "" {
		return nil, ErrAssistantStationNotFound
	}
	home, err := c.store.Get(strings.TrimSpace(homeID))
	if err != nil || home == nil || home.GetAssistantProgramState() == nil || home.GetAssistantProjectLink() != nil {
		return nil, ErrAssistantStationNotFound
	}
	return home, nil
}

// update applies fn to the Home's current consent in one fenced Home write.
// fn returns the consent to store and whether anything changed.
func (c *ProjectStaffingConsents) update(homeID string, fn func(current *ProjectStaffingConsent) (*ProjectStaffingConsent, bool, error)) (*ProjectStaffingConsent, error) {
	if _, err := c.home(homeID); err != nil {
		return nil, err
	}
	var result *ProjectStaffingConsent
	err := c.store.Update(strings.TrimSpace(homeID), func(home *Workspace) error {
		state := home.GetAssistantProgramState()
		if state == nil || home.GetAssistantProjectLink() != nil {
			return ErrAssistantStationNotFound
		}
		current := state.GetProjectStaffingConsent()
		if current != nil && current.Validate() != nil {
			current = nil // An unreadable record is replaced, never extended.
		}
		next, changed, err := fn(current)
		if err != nil {
			return err
		}
		result = next.Clone()
		if !changed {
			return errConsentUnchanged
		}
		if err := next.Validate(); err != nil {
			return err
		}
		state.ProjectStaffingConsent = next.Clone()
		home.SetAssistantProgramState(state)
		return nil
	})
	if errors.Is(err, errConsentUnchanged) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

var errConsentUnchanged = errors.New("project staffing consent unchanged")

// Grant records a fresh consent for one project blueprint. An active consent for
// the same blueprint and team digest is kept as it is (granting twice is one
// grant). A consent replaced for the same blueprint keeps the agents it already
// created for roles still declared, so the Home never gets a second shared
// assistant for a role it already has one for.
func (c *ProjectStaffingConsents) Grant(homeID string, grant ProjectStaffingConsentGrant) (*ProjectStaffingConsent, error) {
	return c.update(homeID, func(current *ProjectStaffingConsent) (*ProjectStaffingConsent, bool, error) {
		if current.Active() && current.Covers(grant.PluginID, grant.BlueprintID) && current.TeamDigest == grant.TeamDigest {
			return current, false, nil
		}
		next := &ProjectStaffingConsent{
			GrantedAt: c.now().UTC(), Source: grant.Source, OfferID: grant.OfferID,
			PluginID: grant.PluginID, BlueprintID: grant.BlueprintID, TeamDigest: grant.TeamDigest,
		}
		for _, roleID := range grant.RoleIDs {
			role := ProjectStaffingConsentRole{RoleID: roleID}
			if current.Covers(grant.PluginID, grant.BlueprintID) {
				if earlier, found := current.Role(roleID); found {
					role = earlier
				}
			}
			next.Roles = append(next.Roles, role)
		}
		if err := next.Validate(); err != nil {
			return nil, false, err
		}
		return next, true, nil
	})
}

// ReserveAgent records the name a create is about to use for a role, before the
// create runs. It writes only while the role has no agent yet; a later
// reservation replaces an earlier one that never became an agent.
func (c *ProjectStaffingConsents) ReserveAgent(homeID, pluginID, blueprintID, teamDigest, roleID, agentName string) error {
	if !consentText(agentName, 80, false) {
		return ErrProjectStaffingConsentInvalid
	}
	_, err := c.update(homeID, func(current *ProjectStaffingConsent) (*ProjectStaffingConsent, bool, error) {
		if current == nil || !current.Covers(pluginID, blueprintID) || current.TeamDigest != teamDigest {
			return current, false, ErrProjectStaffingConsentInvalid
		}
		next := current.Clone()
		for i := range next.Roles {
			if next.Roles[i].RoleID != roleID {
				continue
			}
			if next.Roles[i].AgentName != "" {
				return current, false, ErrProjectStaffingConsentInvalid
			}
			if next.Roles[i].PendingName == agentName {
				return current, false, nil
			}
			next.Roles[i].PendingName = agentName
			return next, true, nil
		}
		return current, false, ErrProjectStaffingConsentInvalid
	})
	return err
}

// RecordAgent saves the agent the consent's create made for a role, so every
// later project binds that same agent. Only the reserved name can be recorded,
// and only while the role has no agent: a recorded name is never replaced. The
// caller has seen that exact name created in a project for this role.
func (c *ProjectStaffingConsents) RecordAgent(homeID, pluginID, blueprintID, teamDigest, roleID, agentName string) error {
	_, err := c.update(homeID, func(current *ProjectStaffingConsent) (*ProjectStaffingConsent, bool, error) {
		if current == nil || !current.Covers(pluginID, blueprintID) || current.TeamDigest != teamDigest {
			return current, false, nil
		}
		next := current.Clone()
		for i := range next.Roles {
			role := &next.Roles[i]
			if role.RoleID == roleID && role.AgentName == "" && role.PendingName != "" && role.PendingName == agentName {
				role.AgentName, role.PendingName = agentName, ""
				return next, true, nil
			}
		}
		return current, false, nil
	})
	return err
}

// Revoke switches the consent off (D7). Songs opened afterwards get no agent; the
// record and its agent stay, so switching it back on reuses the same agent.
func (c *ProjectStaffingConsents) Revoke(homeID string) (*ProjectStaffingConsent, error) {
	return c.update(homeID, func(current *ProjectStaffingConsent) (*ProjectStaffingConsent, bool, error) {
		if current == nil || current.RevokedAt != nil {
			return current, false, nil
		}
		next := current.Clone()
		now := c.now().UTC()
		if now.Before(next.GrantedAt) {
			now = next.GrantedAt
		}
		next.RevokedAt = &now
		return next, true, nil
	})
}
