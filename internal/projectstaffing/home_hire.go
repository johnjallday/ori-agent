package projectstaffing

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Hiring the Home's shared assistant before any project exists.
//
// A song's staffing proves a consent's create by the binding it leaves in the
// song (see settle). A hire made when the Home is set up has no song to bind
// into, so it needs another proof: the reservation carries a random ID, the
// agent is created with a setup marker holding that ID, and only an agent
// whose marker matches a pending reservation is ever recorded. The agent is
// one of the user's own agents and is bound into no workspace; the Home holds
// it by its consent record alone.

// ErrNoModel means a new shared agent is needed and no model can run one.
var ErrNoModel = errNoModel

// Team is the project team a Home's projects are opened with, as the installed
// blueprint declares it: what a shared hire needs when no project exists yet.
type Team struct {
	PluginID    string
	BlueprintID string
	// Digest is the team fingerprint a consent is scoped to.
	Digest string
	// Roles are the project roles the blueprint requires.
	Roles []workspace.AssistantProgramRoleSpec
}

// SharedCreate creates the user's agent for one shared role under name,
// stamped with marker and bound into no workspace. created says this call
// wrote the agent's profile, even when it then failed: such an agent keeps its
// reservation, and the sweep records it.
type SharedCreate func(ctx context.Context, role workspace.AssistantProgramRoleSpec, name string,
	marker *agent.AssistantSetupProvenance) (created bool, err error)

// offerDismissed says the owner answered "Not now" to the Home's offer to add
// its shared assistant.
func offerDismissed(home *workspace.Workspace) bool {
	state := home.GetAssistantProgramState()
	return state != nil && state.ProjectStaffingOfferDismissedAt != nil
}

// sharedRoleOperation is the setup marker's operation for a shared hire of one
// role.
func sharedRoleOperation(roleID string) string { return "shared-project-role:" + roleID }

// sharedMarker is the marker a shared hire stamps on the agent it creates.
func sharedMarker(homeID, roleID, reservationID, teamDigest string) *agent.AssistantSetupProvenance {
	return &agent.AssistantSetupProvenance{
		ID: reservationID, RunID: homeID, OperationID: sharedRoleOperation(roleID), ReviewDigest: teamDigest,
	}
}

// holdsMarker says the user's agent of that name was created by the shared
// hire that reserved reservationID for this Home and role.
func (s *Service) holdsMarker(name, homeID, roleID, reservationID string) bool {
	if reservationID == "" || !s.agentExists(name) {
		return false
	}
	found, ok := s.agents.GetAgent(name)
	if !ok || found == nil || found.AssistantSetup == nil {
		return false
	}
	marker := found.AssistantSetup
	return marker.ID == reservationID && marker.RunID == homeID && marker.OperationID == sharedRoleOperation(roleID)
}

// settleShared records, for every role of the Home's consent with a pending
// reservation, the agent that reservation's own create made: an agent of the
// reserved name whose setup marker carries the reservation's ID. It finishes a
// hire that stopped between the create and the record. Nothing else is ever
// recorded, and it is safe to repeat.
func (s *Service) settleShared(homeID string) error {
	if s == nil || s.workspaces == nil || s.agents == nil {
		return nil
	}
	consent, err := s.consents.Read(homeID)
	if err != nil || consent == nil {
		return nil
	}
	for _, role := range consent.Roles {
		if role.AgentName != "" || role.PendingName == "" || role.ReservationID == "" {
			continue
		}
		if !s.holdsMarker(role.PendingName, homeID, role.RoleID, role.ReservationID) {
			continue
		}
		if err := s.consents.RecordAgent(homeID, consent.PluginID, consent.BlueprintID, consent.TeamDigest,
			role.RoleID, role.PendingName); err != nil {
			return err
		}
	}
	return nil
}

// RecoverShared finishes, at startup, every shared hire a previous process
// left between its create and its record, one Home at a time under that Home's
// lock.
func (s *Service) RecoverShared() error {
	if s == nil || s.workspaces == nil {
		return nil
	}
	ids, err := s.workspaces.List()
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		home, getErr := s.workspaces.Get(id)
		if getErr != nil || home == nil || home.GetAssistantProjectLink() != nil {
			continue
		}
		consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
		if consent == nil {
			continue
		}
		pending := false
		for _, role := range consent.Roles {
			pending = pending || (role.AgentName == "" && role.PendingName != "" && role.ReservationID != "")
		}
		if !pending {
			continue
		}
		if err := s.WithHome(id, func(*Tx) error { return s.settleShared(id) }); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// HomeAssistant is the name of the shared assistant the Home's consent records
// for this project blueprint. It is ErrNoConsent when the Home has none
// recorded and ErrAssistantMissing when the recorded agent is gone. Whether the
// consent is switched off, or was given for an older team, does not matter
// here: those decide whether the agent joins a project, not whether the Home
// has it.
func (s *Service) HomeAssistant(homeID, pluginID, blueprintID string) (string, error) {
	if s == nil || s.workspaces == nil {
		return "", ErrNoConsent
	}
	consent, err := s.consents.Read(homeID)
	if err != nil || consent == nil || !consent.Covers(pluginID, blueprintID) {
		return "", ErrNoConsent
	}
	for _, role := range consent.Roles {
		if role.AgentName == "" {
			continue
		}
		if s.agentExists(role.AgentName) {
			return role.AgentName, nil
		}
		return "", ErrAssistantMissing
	}
	return "", ErrNoConsent
}

// Where a Home's shared assistant stands, for a plan that has not run yet.
const (
	// SharedHire: a hire would create it.
	SharedHire = "hire"
	// SharedHas: the Home has it; nothing would be made.
	SharedHas = "has"
	// SharedOff: the Home's approval is switched off; nobody would be hired.
	SharedOff = "off"
	// SharedStale: the approval is for an older team; nobody would be hired.
	SharedStale = "stale"
	// SharedHeld: an agent holds the reserved name without the reservation's
	// proof; nobody would be hired.
	SharedHeld = "held"
	// SharedNotOurs: the Home shares another blueprint's assistant.
	SharedNotOurs = "not_ours"
)

// SharedPlan is what HireShared would do for the team's one shared role, read
// without writing anything.
type SharedPlan struct {
	State string
	// Name is the agent a hire would create, the agent the Home has, or the
	// name an agent holds; Taken is the role's label when another agent holds
	// it, so a hire takes the next free name.
	Name  string
	Taken string
	// Role is the team's shared role.
	Role workspace.AssistantProgramRoleSpec
}

// PlanShared says what a hire of team's shared assistant would do on the Home
// homeID ("" when the Home does not exist yet), deciding as HireShared does
// and writing nothing. A team with more or fewer than one required role is
// ErrNotShared: the plan names one assistant.
func (s *Service) PlanShared(homeID string, team Team) (SharedPlan, error) {
	if s == nil || len(team.Roles) != 1 {
		return SharedPlan{}, ErrNotShared
	}
	role := team.Roles[0]
	plan := SharedPlan{Role: role, State: SharedHire}
	var home *workspace.Workspace
	if strings.TrimSpace(homeID) != "" && s.workspaces != nil {
		found, err := s.workspaces.Get(strings.TrimSpace(homeID))
		if err != nil || found == nil || found.GetAssistantProgramState() == nil {
			return SharedPlan{}, ErrNotShared
		}
		home = found
	}
	freeName := func() {
		plan.Name = workspace.FirstFreeAgentName(role.Label, s.nameTaken(Song{Home: home}))
		if !strings.EqualFold(plan.Name, strings.TrimSpace(role.Label)) {
			plan.Taken = strings.TrimSpace(role.Label)
		}
	}
	if home == nil {
		freeName()
		return plan, nil
	}
	consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
	if consent != nil && consent.Validate() != nil {
		consent = nil
	}
	switch {
	case consent == nil && offerDismissed(home):
		// The owner said "Not now" to the Home's offer: like a switch turned off.
		plan.State = SharedOff
		return plan, nil
	case consent == nil:
		freeName()
		return plan, nil
	case consent.RevokedAt != nil:
		plan.State = SharedOff
		return plan, nil
	case !consent.Covers(team.PluginID, team.BlueprintID):
		plan.State = SharedNotOurs
		return plan, nil
	case consent.TeamDigest != team.Digest:
		plan.State = SharedStale
		return plan, nil
	}
	entry, _ := consent.Role(role.ID)
	switch {
	case entry.AgentName != "" && s.agentExists(entry.AgentName):
		plan.State, plan.Name = SharedHas, entry.AgentName
	case entry.AgentName == "" && entry.PendingName != "" && s.agentExists(entry.PendingName):
		// The reservation's own create made it (the sweep records it), or a
		// stranger holds the name and the owner decides.
		if s.holdsMarker(entry.PendingName, home.ID, role.ID, entry.ReservationID) {
			plan.State, plan.Name = SharedHas, entry.PendingName
		} else {
			plan.State, plan.Name = SharedHeld, entry.PendingName
		}
	default:
		// None yet, or the recorded one is gone: a hire creates it.
		freeName()
	}
	return plan, nil
}

// HireShared hires the Home's shared assistant for every required role of
// team, with no project: the folder card's Set up is the approval (grant). It
// is one turn under the Home's lock.
//
// A Home with no consent gets one. A consent that is switched off, or was
// given for an older team, is left alone and nobody is hired (ErrNoConsent,
// ErrConsentStale): Set up never turns the Home's switch back on. A role whose
// agent the consent already records is returned as a bind and nothing is
// created, so running it again changes nothing. A recorded agent that is gone
// is forgotten and replaced. An agent that holds a reserved name without the
// reservation's marker stops the hire (ErrReservationHeld): it is never
// adopted and never stepped around with a second create.
func (tx *Tx) HireShared(ctx context.Context, team Team, grant Grant, modelReady bool, create SharedCreate) ([]Fill, error) {
	s := tx.s
	if s == nil || s.workspaces == nil || create == nil || len(team.Roles) == 0 ||
		strings.TrimSpace(team.PluginID) == "" || strings.TrimSpace(team.BlueprintID) == "" {
		return nil, ErrNotShared
	}
	home, err := s.workspaces.Get(tx.home)
	if err != nil || home == nil || home.GetAssistantProgramState() == nil || home.GetAssistantProjectLink() != nil {
		return nil, ErrNotShared
	}
	if err := s.settleShared(tx.home); err != nil {
		return nil, err
	}
	consent, err := s.consents.Read(tx.home)
	if err != nil {
		consent = nil // An unreadable record is no consent; the grant replaces it.
	}
	switch {
	case consent == nil && offerDismissed(home):
		// The owner said "Not now" to the Home's offer: Set up does not overrule it.
		return nil, ErrNoConsent
	case consent == nil && !modelReady:
		// A new consent always creates. With no model nothing is written, so
		// pressing Set up again after choosing one starts clean.
		return nil, ErrNoModel
	case consent == nil:
		request := workspace.ProjectStaffingConsentGrant{
			Source: grant.Source, OfferID: grant.OfferID,
			PluginID: team.PluginID, BlueprintID: team.BlueprintID, TeamDigest: team.Digest,
		}
		for _, role := range team.Roles {
			request.RoleIDs = append(request.RoleIDs, role.ID)
		}
		if consent, err = s.consents.Grant(tx.home, request); err != nil {
			return nil, err
		}
	case consent.RevokedAt != nil:
		return nil, ErrNoConsent
	case !consent.Covers(team.PluginID, team.BlueprintID):
		return nil, ErrNotShared
	case consent.TeamDigest != team.Digest:
		return nil, ErrConsentStale
	}

	roleIDs := make([]string, 0, len(team.Roles))
	for _, role := range team.Roles {
		roleIDs = append(roleIDs, role.ID)
	}
	// A recorded agent that is gone is forgotten first, so the decision below
	// creates its replacement instead of stopping.
	for _, role := range team.Roles {
		entry, found := consent.Role(role.ID)
		if !found || entry.AgentName == "" || s.agentExists(entry.AgentName) {
			continue
		}
		if consent, err = s.consents.ForgetAgent(tx.home, team.PluginID, team.BlueprintID, role.ID, entry.AgentName); err != nil {
			return nil, err
		}
	}
	if holder := s.unrecordedHolder(tx.home, roleIDs); holder != "" {
		return nil, ErrReservationHeld
	}
	creates := false
	for _, role := range team.Roles {
		if entry, found := consent.Role(role.ID); !found || entry.AgentName == "" {
			creates = true
		}
	}
	if creates && !modelReady {
		return nil, ErrNoModel
	}

	taken := s.nameTaken(Song{Home: home})
	fills := make([]Fill, 0, len(team.Roles))
	for _, role := range team.Roles {
		decided, err := s.decideTeam(tx.home, team, consent, []string{role.ID}, taken, true)
		if err != nil {
			return fills, err
		}
		if len(decided) != 1 {
			return fills, ErrNotShared
		}
		fill := decided[0]
		if fill.Mode == ModeBind {
			fills = append(fills, fill)
			continue
		}
		// The reservation decideTeam just wrote carries the ID the create stamps.
		reserved, err := s.consents.Read(tx.home)
		if err != nil || reserved == nil {
			return fills, ErrNoConsent
		}
		entry, found := reserved.Role(role.ID)
		if !found || entry.PendingName != fill.Name || entry.ReservationID == "" {
			return fills, ErrNoConsent
		}
		created, createErr := create(ctx, role, fill.Name, sharedMarker(tx.home, role.ID, entry.ReservationID, team.Digest))
		switch {
		case createErr == nil:
		case created:
			// The profile exists and something after it failed. It keeps its
			// reservation; the sweep records it by its marker.
			return fills, createErr
		case s.holdsMarker(fill.Name, tx.home, role.ID, entry.ReservationID):
			// An earlier attempt under this same reservation made the agent.
		default:
			// Nothing was made, or someone else's agent has the name: the next
			// hire decides afresh.
			if releaseErr := s.consents.ReleaseReservation(tx.home, role.ID, fill.Name); releaseErr != nil {
				return fills, errors.Join(createErr, releaseErr)
			}
			return fills, createErr
		}
		if err := s.consents.RecordAgent(tx.home, team.PluginID, team.BlueprintID, team.Digest, role.ID, fill.Name); err != nil {
			return fills, err
		}
		fills = append(fills, fill)
		if consent, err = s.consents.Read(tx.home); err != nil || consent == nil {
			return fills, ErrNoConsent
		}
	}
	return fills, nil
}
