// Package workspaceroles projects a workspace's declared blueprint roles and
// its current attachments into the one roster both surfaces render: the Create
// Workspace Team step and the workspace's own Roles roster.
//
// A role is a slot, not an agent. It is empty until someone fills it, either by
// creating a new agent from the role's spec or by assigning one they already
// have. Nothing here creates, mutates, or deletes an agent — it reads state and
// describes it, so the same description cannot disagree between the two places
// it is shown.
package workspaceroles

import (
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// Scope separates a role that belongs to the group workspace from one that
// belongs to this project. It mirrors workspace.AssistantRoleScope as plain
// data so this package stays free of the workspace store.
type Scope string

const (
	ScopeHome    Scope = "home"
	ScopeProject Scope = "project"
)

// A role is in exactly one of these states (FR2).
const (
	StateEmpty  = "empty"
	StateFilled = "filled"
)

// How a filled role was filled. Mirrors workspace.RoleSource*.
const (
	SourceCreated  = "created"
	SourceAssigned = "assigned"
)

// readOnlyGroupRoleReason explains why Create/Assign/Clear are absent on a
// group-scoped role seen from a project (PRD D2).
const readOnlyGroupRoleReason = "This role belongs to the group workspace. Fill or clear it there."

// Role is one slot a blueprint declares.
type Role struct {
	ID          string
	Label       string
	Description string
	Scope       Scope
	Required    bool
	Primary     bool
	// Proposed is the setup the blueprint suggests for whoever fills this role.
	// Present only where the blueprint's spec is already disclosed to the
	// client — see ProposedSetup.
	Proposed *ProposedSetup
}

// ProposedSetup is what a blueprint proposes for a role's agent, so the Create
// form can show the instructions the agent would actually get instead of an
// empty box that hides them.
//
// It is deliberately absent for assistant-program roles: that declaration's
// prompts are never disclosed in a staffing projection (the staffing adapter
// has a test asserting they do not leak), and the server applies them itself at
// commit time. An ordinary blueprint's prompt is already public — the
// template-agent-plan endpoint returns it in full — so carrying it here
// discloses nothing new.
type ProposedSetup struct {
	Type         string `json:"type,omitempty"`
	Model        string `json:"model,omitempty"`
	Provider     string `json:"provider,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// Attachment is one agent currently in the workspace. RoleID is empty for an
// agent that is in the workspace without holding a declared role — those are
// listed separately rather than dropped (FR63).
type Attachment struct {
	Name       string
	RoleID     string
	RoleSource string
	EntryPoint bool
}

// AgentIdentity is the identity payload the shared avatar renderer needs. It
// deliberately carries no prompt, memory, credential, or tool grant: this
// projection is read by two UI surfaces and nothing else.
type AgentIdentity struct {
	Name       string                 `json:"name"`
	Role       string                 `json:"role,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Appearance *types.AgentAppearance `json:"appearance,omitempty"`
}

// RoleProjection is one roster row.
type RoleProjection struct {
	RoleID      string `json:"role_id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Scope       Scope  `json:"scope"`
	Required    bool   `json:"required"`
	Primary     bool   `json:"primary"`
	State       string `json:"state"`
	// Source and Agent are present only on a filled role.
	Source string         `json:"source,omitempty"`
	Agent  *AgentIdentity `json:"agent,omitempty"`
	// NeedsClear means storage still names a holder whose saved definition no
	// longer exists. It exposes no stale identity; the only valid next action is
	// an explicit role clear before another Create or Assign.
	NeedsClear bool `json:"needs_clear,omitempty"`
	// ReadOnly marks a role this workspace may display but not change — a
	// group-scoped role seen from a project (D2).
	ReadOnly       bool   `json:"read_only,omitempty"`
	ReadOnlyReason string `json:"read_only_reason,omitempty"`
	// Proposed seeds the Create form for an empty role. Omitted once the role
	// is filled — there is nothing left to propose.
	Proposed *ProposedSetup `json:"proposed,omitempty"`
}

// Roster is the whole projection for one workspace.
type Roster struct {
	WorkspaceID string `json:"workspace_id"`
	// GroupWorkspaceID names where group-scoped roles are managed, when this
	// workspace has any it cannot change itself.
	GroupWorkspaceID string           `json:"group_workspace_id,omitempty"`
	Roles            []RoleProjection `json:"roles"`
	// Unassigned lists agents attached to the workspace that hold no declared
	// role — "Also in this workspace".
	Unassigned  []AgentIdentity `json:"unassigned"`
	FilledCount int             `json:"filled_count"`
	TotalCount  int             `json:"total_count"`
	// EntryAgentName is the agent chat resolves to: the primary role's holder,
	// falling back to the first filled role in declaration order (D3). Empty
	// when no role is filled, which is what the unstaffed guidance keys on.
	EntryAgentName string `json:"entry_agent_name,omitempty"`
}

// Input is everything Build needs. The caller resolves where roles come from
// (an assistant-program declaration or a template's agent roster) and hands
// them over already normalized, so this package never reads a store.
type Input struct {
	WorkspaceID string
	// ViewScope is the scope of the workspace being looked at. Roles of a
	// different scope are projected read-only (D2).
	ViewScope        Scope
	GroupWorkspaceID string
	Roles            []Role
	Attachments      []Attachment
	// Bindings maps role id to agent name for workspaces whose attachments
	// predate role ids — an assistant program's binding sets. Attachments win
	// where both are present.
	Bindings map[string]string
	// Lookup reports the identity of a saved agent definition, and whether it
	// still exists. A role whose holder was deleted on /agents projects as
	// empty again (FR42), which is why this is a lookup and not a cache.
	Lookup func(name string) (AgentIdentity, bool)
}

// Build projects declared roles plus current attachments into a roster.
//
// Roles come back in declaration order. Display order (primary, then missing
// required, then filled, then empty optional) is applied by the shared roster
// component so both surfaces sort identically; keeping the projection in
// declaration order means "the third role" means the same thing everywhere.
func Build(in Input) Roster {
	roster := Roster{
		WorkspaceID:      in.WorkspaceID,
		GroupWorkspaceID: strings.TrimSpace(in.GroupWorkspaceID),
		Roles:            make([]RoleProjection, 0, len(in.Roles)),
		Unassigned:       []AgentIdentity{},
	}

	byRole := make(map[string]Attachment, len(in.Attachments))
	for _, attachment := range in.Attachments {
		roleID := normalizeRoleID(attachment.RoleID)
		if roleID == "" {
			continue
		}
		// A slot holds one agent; if storage somehow carries two, the first
		// wins so the roster is deterministic rather than order-of-scan.
		if _, taken := byRole[roleID]; !taken {
			byRole[roleID] = attachment
		}
	}

	claimed := make(map[string]struct{}, len(in.Roles))
	for _, role := range in.Roles {
		roleID := normalizeRoleID(role.ID)
		item := RoleProjection{
			RoleID:      roleID,
			Label:       strings.TrimSpace(role.Label),
			Description: strings.TrimSpace(role.Description),
			Scope:       role.Scope,
			Required:    role.Required,
			Primary:     role.Primary,
			State:       StateEmpty,
		}
		if item.Scope == "" {
			item.Scope = ScopeProject
		}
		if in.ViewScope != "" && item.Scope != in.ViewScope {
			item.ReadOnly = true
			item.ReadOnlyReason = readOnlyGroupRoleReason
		}

		if role.Proposed != nil {
			proposed := *role.Proposed
			item.Proposed = &proposed
		}

		holder, source := holderFor(roleID, byRole, in.Bindings)
		if holder != "" {
			if identity, exists := lookup(in.Lookup, holder); exists {
				item.State = StateFilled
				item.Source = source
				item.Agent = &identity
				// A filled role has nothing left to propose, and the holder's
				// real setup lives on its own definition.
				item.Proposed = nil
				claimed[strings.ToLower(holder)] = struct{}{}
				roster.FilledCount++
			} else {
				item.NeedsClear = true
			}
			// A holder whose definition is gone leaves the role empty (FR42).
			// The stale attachment is not reported as "also in this workspace"
			// either — there is no agent there to report.
		}
		roster.Roles = append(roster.Roles, item)
	}
	roster.TotalCount = len(roster.Roles)
	roster.EntryAgentName = entryAgentName(roster.Roles)

	for _, attachment := range in.Attachments {
		name := strings.TrimSpace(attachment.Name)
		if name == "" {
			continue
		}
		if _, filled := claimed[strings.ToLower(name)]; filled {
			continue
		}
		identity, exists := lookup(in.Lookup, name)
		if !exists {
			continue
		}
		roster.Unassigned = append(roster.Unassigned, identity)
	}
	sort.SliceStable(roster.Unassigned, func(i, j int) bool {
		return strings.ToLower(roster.Unassigned[i].Name) < strings.ToLower(roster.Unassigned[j].Name)
	})
	return roster
}

// holderFor resolves who fills a role, preferring the attachment's own role id
// over a legacy binding record. It returns the agent name and how the role was
// filled; an unrecognized source reads as "created", which is what every
// pre-vacancy binding was.
func holderFor(roleID string, byRole map[string]Attachment, bindings map[string]string) (string, string) {
	if attachment, found := byRole[roleID]; found {
		name := strings.TrimSpace(attachment.Name)
		if name != "" {
			return name, normalizeSource(attachment.RoleSource)
		}
	}
	for boundRole, name := range bindings {
		if normalizeRoleID(boundRole) != roleID {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			return name, SourceCreated
		}
	}
	return "", ""
}

// entryAgentName applies D3: the primary role's agent, else the first filled
// role in declaration order, so a workspace with agents in it is never dead.
func entryAgentName(roles []RoleProjection) string {
	fallback := ""
	for _, role := range roles {
		if role.State != StateFilled || role.Agent == nil {
			continue
		}
		if role.Primary {
			return role.Agent.Name
		}
		if fallback == "" {
			fallback = role.Agent.Name
		}
	}
	return fallback
}

func lookup(fn func(string) (AgentIdentity, bool), name string) (AgentIdentity, bool) {
	if fn == nil {
		return AgentIdentity{Name: name}, true
	}
	identity, exists := fn(name)
	if !exists {
		return AgentIdentity{}, false
	}
	if strings.TrimSpace(identity.Name) == "" {
		identity.Name = name
	}
	return identity, true
}

func normalizeRoleID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeSource(value string) string {
	if strings.ToLower(strings.TrimSpace(value)) == SourceAssigned {
		return SourceAssigned
	}
	return SourceCreated
}
