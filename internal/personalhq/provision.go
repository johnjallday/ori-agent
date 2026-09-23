package personalhq

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
)

// CurrentProvisioningVersion is the Personal HQ assistant provisioning version
// this build knows how to provision a new HQ to and upgrade an existing HQ to.
//
// This is deliberately DISTINCT from the template's builtin_version. The
// template builtin_version is a library concept — it drives in-place refresh of
// the shipped manifest for FUTURE workspace creations. It must never be treated
// as an existing-workspace migration (contract §5.1, task 2.2). The
// provisioning version, by contrast, records what has actually been applied to
// one specific designated HQ workspace, so upgrades are versioned, idempotent,
// and safe to re-run.
const CurrentProvisioningVersion = 1

// provisioningSharedDataKey is where per-HQ provisioning state lives on the
// workspace SharedData, alongside the provisional brief config
// (briefConfigSharedDataKey). SharedData is used rather than a new SQLite column
// to stay consistent with how Personal HQ already persists per-workspace state
// and to avoid a schema migration for a small, workspace-local record.
const provisioningSharedDataKey = "personal_hq_provisioning"

// UpgradeOutcome records how the last upgrade attempt on an HQ ended, so a
// partial/failed prior run can be surfaced as retryable (contract §5.1).
type UpgradeOutcome string

const (
	UpgradeOutcomeNone    UpgradeOutcome = ""
	UpgradeOutcomeSuccess UpgradeOutcome = "success"
	UpgradeOutcomePartial UpgradeOutcome = "partial"
	UpgradeOutcomeFailed  UpgradeOutcome = "failed"
)

// SpecialistRole is a stable, workspace-local Personal HQ role identity
// (contract §5.3). Role identity is keyed off the canonical agent Name the
// template seeds, not the free-form AgentInstance.Role label (which the
// template sets to "orchestrator"/"specialist" and a user may freely edit).
type SpecialistRole struct {
	// Slug is the stable machine identity for the role, never shown to users
	// and never changed once shipped.
	Slug string
	// AgentName is the canonical display name the template seeds for this role
	// and the key used to find the role's instance in a workspace.
	AgentName string
	// Entry marks the role that must be the workspace entry agent.
	Entry bool
}

// V1Roster is the operational Personal HQ specialist roster, in template
// declaration order (first is the entry agent). The Inbox specialist moved out
// to the dedicated Email Ops workspace (Mail spin-off), so it is no longer part
// of the HQ roster: the upgrade planner must not offer to add it back, and an
// existing HQ's own Inbox agent is preserved as a user-owned non-roster agent
// (upgrades only add, never remove). The mailbox-access gate still recognizes an
// agent literally named "Inbox" regardless of this roster. No Calendar role
// ships.
var V1Roster = []SpecialistRole{
	{Slug: "chief_of_staff", AgentName: "Personal Chief of Staff", Entry: true},
	{Slug: "journal", AgentName: "Journal"},
}

// ProvisionState is the durable, per-HQ record of applied provisioning.
type ProvisionState struct {
	// Version is the provisioning version applied to this HQ. Zero means an HQ
	// that predates assistant provisioning (e.g. an arbitrary workspace the
	// user designated, or one created before this feature shipped).
	Version int `json:"version"`
	// LastUpgradeOutcome/LastUpgradeError capture the previous attempt so the
	// UI can offer retry after a partial/failed run.
	LastUpgradeOutcome UpgradeOutcome `json:"last_upgrade_outcome,omitempty"`
	LastUpgradeError   string         `json:"last_upgrade_error,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at,omitempty"`
}

// ReadProvisionState extracts the provisioning record from a workspace's
// SharedData. A workspace with no record (nil workspace, no SharedData, or an
// unparseable value) reads as version 0 with no prior outcome — never an error,
// so read paths stay resilient (mirrors Status never failing on stale data).
func ReadProvisionState(ws *session.Workspace) ProvisionState {
	if ws == nil || ws.SharedData == nil {
		return ProvisionState{}
	}
	raw, ok := ws.SharedData[provisioningSharedDataKey]
	if !ok {
		return ProvisionState{}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return ProvisionState{}
	}
	var state ProvisionState
	if err := json.Unmarshal(data, &state); err != nil {
		return ProvisionState{}
	}
	return state
}

// writeProvisionState stores the provisioning record into a workspace's
// SharedData in place. The caller is responsible for persisting the workspace.
func writeProvisionState(ws *session.Workspace, state ProvisionState) error {
	if ws == nil {
		return nil
	}
	state.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	ws.SharedData[provisioningSharedDataKey] = raw
	return nil
}

// PersonalAssistantPresentationKey is the SharedData key the personal-assistant
// HQ creation path stamps on an HQ built around the user's hired assistant
// (sessionhttp.markPersonalAssistantPresentation). Its presence means the
// workspace's entry agent IS the hired assistant standing in for the Personal
// Chief of Staff roster role: the assistant reuses that role's prompt, and the
// contract requires zero Personal Chief of Staff instances in such an HQ.
const PersonalAssistantPresentationKey = "personal_assistant_presentation"

// AssistantEntryInstance returns the entry-point instance of an HQ built around
// the user's hired personal assistant, or nil when the workspace was not built
// that way (no presentation marker) or has no entry point.
func AssistantEntryInstance(ws *session.Workspace) *session.AgentInstance {
	if i := assistantEntryInstanceIndex(ws); i >= 0 {
		return &ws.AgentInstances[i]
	}
	return nil
}

func assistantEntryInstanceIndex(ws *session.Workspace) int {
	if ws == nil || ws.SharedData == nil {
		return -1
	}
	if raw, ok := ws.SharedData[PersonalAssistantPresentationKey]; !ok || raw == nil {
		return -1
	}
	for i := range ws.AgentInstances {
		if ws.AgentInstances[i].EntryPoint {
			return i
		}
	}
	return -1
}

// FindRoleInstance returns the agent instance fulfilling a specialist role in a
// workspace, plus whether one was found.
//
// The entry role is fulfilled by the hired personal assistant on an HQ built
// around one (see AssistantEntryInstance), whatever the assistant is named;
// every other role is matched case-insensitively by its canonical AgentName.
// Without that rule the upgrade planner read an assistant-built HQ as missing
// its Personal Chief of Staff, and applying the upgrade seeded a second
// orchestrator beside the assistant.
func FindRoleInstance(ws *session.Workspace, role SpecialistRole) (*session.AgentInstance, bool) {
	if i := findRoleInstanceIndex(ws, role); i >= 0 {
		return &ws.AgentInstances[i], true
	}
	return nil, false
}

// findRoleInstanceIndex is FindRoleInstance by index into ws.AgentInstances,
// or -1 when the role is unfulfilled.
func findRoleInstanceIndex(ws *session.Workspace, role SpecialistRole) int {
	if ws == nil {
		return -1
	}
	if role.Entry {
		if i := assistantEntryInstanceIndex(ws); i >= 0 {
			return i
		}
	}
	for i := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(ws.AgentInstances[i].Name), role.AgentName) {
			return i
		}
	}
	return -1
}
