package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
)

// RecoveryProfile is the bounded subset of a global agent profile that may be
// used to prove ownership of an orphaned personal-assistant relationship. It
// deliberately excludes prompts, model configuration, tools, and credentials.
type RecoveryProfile struct {
	Name          string
	AssistantID   string
	HireRequestID string
	Role          types.AgentRole
	Appearance    *types.AgentAppearance
	CreatedAt     time.Time
}

// RecoveryProfileLister finds profiles carrying Personal Assistant Foundation
// provenance. Implementations must not infer candidates from names or roles.
type RecoveryProfileLister interface {
	PersonalAssistantRecoveryProfiles() []RecoveryProfile
}

// RecoveryWorkspace is the bounded subset of a workspace that can establish
// whether Personal HQ provenance exists independently of the current
// designation. It deliberately excludes tasks, messages, memory, paths, tools,
// and arbitrary shared data.
type RecoveryWorkspace struct {
	ID                string
	OwnerUserID       string
	AssistantID       string
	HQRequestID       string
	PresentationValid bool
	EntryAgents       []RecoveryEntryAgent
}

// RecoveryEntryAgent is the stable identity-bearing subset of an entry agent.
type RecoveryEntryAgent struct {
	ID   string
	Name string
}

// RecoveryWorkspaceLister enumerates every workspace carrying the Personal HQ
// presentation key, including malformed markers. Malformed evidence must remain
// visible so recovery blocks rather than misclassifying the install as fresh.
type RecoveryWorkspaceLister interface {
	PersonalAssistantRecoveryWorkspaces(ctx context.Context) ([]RecoveryWorkspace, error)
}

// RecoveryCandidate is a fully validated, server-owned relationship identity.
// A client never supplies any of these IDs to the repair mutation.
type RecoveryCandidate struct {
	AssistantID            string
	DisplayName            string
	Appearance             *types.AgentAppearance
	GlobalAgentProfileName string
	HireRequestID          string
	HQRequestID            string
	HQWorkspaceID          string
	HQEntryAgentInstanceID string
	Status                 RelationshipStatus
	HiredAt                time.Time
}

func (c *RecoveryCandidate) clone() *RecoveryCandidate {
	if c == nil {
		return nil
	}
	out := *c
	out.Appearance = c.Appearance.Clone()
	return &out
}

// RelationshipRecoveryInspector is the read-only seam used when the canonical
// relationship row is absent. ErrNotFound means there is no recovery evidence;
// ErrRepairNeeded means evidence exists but is ambiguous or inconsistent.
type RelationshipRecoveryInspector interface {
	Inspect(ctx context.Context, userID string) (*RecoveryCandidate, error)
}

// RelationshipRecoveryService performs the one explicit repair mutation.
type RelationshipRecoveryService interface {
	RelationshipRecoveryInspector
	Repair(ctx context.Context, userID string, ifVersion int64) (*State, error)
}

// RecoveryCoordinator reconnects an orphaned relationship only when independent
// durable stores agree on every stable identity. Reads are observational; only
// Repair writes the missing relationship row.
type RecoveryCoordinator struct {
	store      Store
	profiles   RecoveryProfileLister
	workspaces RecoveryWorkspaceLister
	hq         PersonalHQReader
	briefs     BriefConfigReader
	now        func() time.Time
	mu         sync.Mutex
}

// NewRecoveryCoordinator constructs the deterministic relationship repair path.
func NewRecoveryCoordinator(
	store Store,
	profiles RecoveryProfileLister,
	workspaces RecoveryWorkspaceLister,
	hq PersonalHQReader,
	briefs BriefConfigReader,
) *RecoveryCoordinator {
	return &RecoveryCoordinator{
		store: store, profiles: profiles, workspaces: workspaces, hq: hq, briefs: briefs, now: time.Now,
	}
}

var _ RelationshipRecoveryService = (*RecoveryCoordinator)(nil)

type recoveryHQPresentation struct {
	AssistantID string `json:"assistant_id"`
	RequestID   string `json:"request_id"`
}

// Inspect validates recovery evidence without mutating any store.
func (c *RecoveryCoordinator) Inspect(ctx context.Context, userID string) (*RecoveryCandidate, error) {
	if c == nil || c.profiles == nil || c.workspaces == nil || c.hq == nil {
		return nil, errors.New("personal assistant: recovery service is unavailable")
	}
	userID, err := validateOpaqueID("user id", userID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	profiles := c.profiles.PersonalAssistantRecoveryProfiles()
	workspaces, err := c.workspaces.PersonalAssistantRecoveryWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 && len(workspaces) == 0 {
		return nil, ErrNotFound
	}
	if len(profiles) != 1 {
		return nil, ErrRepairNeeded
	}
	profile := profiles[0]
	profile.Name = strings.TrimSpace(profile.Name)
	profile.AssistantID = strings.TrimSpace(profile.AssistantID)
	profile.HireRequestID = strings.TrimSpace(profile.HireRequestID)
	if profile.Name == "" || profile.AssistantID == "" || profile.HireRequestID == "" ||
		profile.Role != types.RoleOrchestrator {
		return nil, ErrRepairNeeded
	}

	status, err := c.hq.Status(ctx, userID)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, errors.New("personal assistant: recovery hq status is unavailable")
	}

	hiredAt := profile.CreatedAt.UTC()
	if profile.CreatedAt.IsZero() {
		hiredAt = c.now().UTC()
	}
	candidate := &RecoveryCandidate{
		AssistantID: profile.AssistantID, DisplayName: profile.Name,
		Appearance: profile.Appearance.Clone(), GlobalAgentProfileName: profile.Name,
		HireRequestID: profile.HireRequestID, Status: StatusAwaitingHQ, HiredAt: hiredAt,
	}

	// A profile-only hire is safe only when an independent workspace scan finds
	// no Personal HQ marker and the designation agrees that no HQ exists.
	if len(workspaces) == 0 {
		if status.HasDesignation() || status.Valid || status.Workspace != nil ||
			strings.TrimSpace(status.WorkspaceID) != "" || strings.TrimSpace(status.EntryAgentInstanceID) != "" {
			return nil, ErrRepairNeeded
		}
		return candidate, nil
	}
	// Even two otherwise-valid HQ folders are ambiguous. A designation does not
	// authorize silently abandoning or overwriting the other durable artifact.
	if len(workspaces) != 1 {
		return nil, ErrRepairNeeded
	}

	evidence := workspaces[0]
	evidence.ID = strings.TrimSpace(evidence.ID)
	evidence.OwnerUserID = strings.TrimSpace(evidence.OwnerUserID)
	evidence.AssistantID = strings.TrimSpace(evidence.AssistantID)
	evidence.HQRequestID = strings.TrimSpace(evidence.HQRequestID)
	if !evidence.PresentationValid || evidence.ID == "" || evidence.OwnerUserID != userID ||
		evidence.AssistantID != profile.AssistantID || evidence.HQRequestID == "" {
		return nil, ErrRepairNeeded
	}
	if !status.Valid || !status.HasDesignation() || status.Workspace == nil ||
		strings.TrimSpace(status.UserID) != userID ||
		strings.TrimSpace(status.WorkspaceID) != evidence.ID {
		return nil, ErrRepairNeeded
	}
	workspace := status.Workspace
	if strings.TrimSpace(workspace.ID) != evidence.ID || strings.TrimSpace(workspace.OwnerUserID) != userID {
		return nil, ErrRepairNeeded
	}
	presentation, err := parseRecoveryHQPresentation(workspace)
	if err != nil || presentation.AssistantID != profile.AssistantID ||
		presentation.RequestID != evidence.HQRequestID {
		return nil, ErrRepairNeeded
	}

	entryID, ok := recoveryEntryAgent(evidence.EntryAgents, profile.Name)
	if !ok {
		return nil, ErrRepairNeeded
	}
	currentEntries := make([]RecoveryEntryAgent, 0, 1)
	for _, instance := range workspace.AgentInstances {
		if instance.EntryPoint {
			currentEntries = append(currentEntries, RecoveryEntryAgent{ID: instance.ID, Name: instance.Name})
		}
	}
	currentEntryID, ok := recoveryEntryAgent(currentEntries, profile.Name)
	if !ok || currentEntryID != entryID || strings.TrimSpace(status.EntryAgentInstanceID) != entryID ||
		!strings.EqualFold(strings.TrimSpace(status.EntryAgentName), profile.Name) {
		return nil, ErrRepairNeeded
	}
	if c.briefs == nil {
		return nil, errors.New("personal assistant: recovery daily brief service is unavailable")
	}
	brief, err := c.briefs.GetConfig(ctx, evidence.ID)
	if err != nil {
		if errors.Is(err, dailybrief.ErrConfigNotFound) {
			return nil, ErrRepairNeeded
		}
		return nil, err
	}
	if brief == nil || strings.TrimSpace(brief.UserID) != userID ||
		strings.TrimSpace(brief.WorkspaceID) != evidence.ID {
		return nil, ErrRepairNeeded
	}

	candidate.Status = StatusPaused
	candidate.HQRequestID = evidence.HQRequestID
	candidate.HQWorkspaceID = evidence.ID
	candidate.HQEntryAgentInstanceID = entryID
	return candidate, nil
}

func recoveryEntryAgent(entries []RecoveryEntryAgent, profileName string) (string, bool) {
	if len(entries) != 1 {
		return "", false
	}
	entryID := strings.TrimSpace(entries[0].ID)
	if entryID == "" || !strings.EqualFold(strings.TrimSpace(entries[0].Name), strings.TrimSpace(profileName)) {
		return "", false
	}
	return entryID, true
}

// Repair recreates only the missing relationship row. A recovered complete HQ
// starts paused so losing the old row can never silently re-enable proactive
// work; the user may explicitly resume it through the existing continuity API.
func (c *RecoveryCoordinator) Repair(ctx context.Context, userID string, ifVersion int64) (*State, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("personal assistant: recovery service is unavailable")
	}
	if ifVersion < 0 {
		return nil, fmt.Errorf("%w: recovery version cannot be negative", ErrValidation)
	}
	if ifVersion != 0 {
		return nil, fmt.Errorf("%w: recovery requires a missing relationship version", ErrConflict)
	}
	userID, err := validateOpaqueID("user id", userID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, getErr := c.store.GetState(ctx, userID); getErr == nil {
		return nil, fmt.Errorf("%w: a relationship already exists", ErrConflict)
	} else if !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	}
	candidate, err := c.Inspect(ctx, userID)
	if err != nil {
		return nil, err
	}

	state := NewState(userID)
	state.AssistantID = candidate.AssistantID
	state.Status = candidate.Status
	state.DisplayName = candidate.DisplayName
	state.Appearance = candidate.Appearance.Clone()
	state.HQWorkspaceID = candidate.HQWorkspaceID
	state.HQEntryAgentInstanceID = candidate.HQEntryAgentInstanceID
	state.GlobalAgentProfileName = candidate.GlobalAgentProfileName
	state.FirstAssignmentStatus = FirstAssignmentNotStarted
	state.LastHireRequestID = candidate.HireRequestID
	state.LastHQRequestID = candidate.HQRequestID
	state.HiredAt = &candidate.HiredAt
	if err := state.ValidateStateInvariants(); err != nil {
		return nil, fmt.Errorf("%w: recovery candidate is invalid", ErrRepairNeeded)
	}
	created, err := c.store.CreateState(ctx, state)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func parseRecoveryHQPresentation(workspace *session.Workspace) (recoveryHQPresentation, error) {
	var presentation recoveryHQPresentation
	if workspace == nil || workspace.SharedData == nil {
		return presentation, errors.New("personal assistant: recovery hq provenance is missing")
	}
	raw, ok := workspace.SharedData["personal_assistant_presentation"]
	if !ok || raw == nil {
		return presentation, errors.New("personal assistant: recovery hq provenance is missing")
	}
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > 4096 {
		return presentation, errors.New("personal assistant: recovery hq provenance is invalid")
	}
	if err := json.Unmarshal(encoded, &presentation); err != nil {
		return presentation, errors.New("personal assistant: recovery hq provenance is invalid")
	}
	presentation.AssistantID = strings.TrimSpace(presentation.AssistantID)
	presentation.RequestID = strings.TrimSpace(presentation.RequestID)
	if presentation.AssistantID == "" || presentation.RequestID == "" {
		return presentation, errors.New("personal assistant: recovery hq provenance is incomplete")
	}
	return presentation, nil
}
