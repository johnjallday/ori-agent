package personalhq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// PersonalHQTemplateID is the built-in template Setup provisions the HQ
// workspace from. Frozen per PRD FR120 — the internal template ID never
// changes even though its display name is Personal HQ (task 2.0).
const PersonalHQTemplateID = "personal-ops"

// ErrAssistantNameConflict marks a pre-creation global profile collision.
var ErrAssistantNameConflict = errors.New("personal hq: assistant name conflict")

// briefConfigSharedDataKey is where the provisional Daily Brief
// configuration captured during setup is stored on the HQ workspace's
// SharedData, until internal/dailybrief (task 5.0) owns durable
// configuration storage. Kept as real input capture (not discarded) so task
// 5.0 has actual user-chosen settings to read rather than re-deriving
// defaults from scratch.
const briefConfigSharedDataKey = "personal_hq_brief_config"

// ErrInvalidTimezone is returned when a non-empty timezone fails
// time.LoadLocation. An empty timezone is not an error — Setup defaults it.
var ErrInvalidTimezone = errors.New("personal hq: invalid IANA timezone")

// WorkspaceCreator creates a normal workspace from a built-in template and
// returns its ID, reusing the exact same server-side creation path as the
// template library (entry-agent selection, tool binding, scaffold
// provisioning, starter-task seeding, provenance) rather than a second
// constructor (PRD FR128). Implemented by sessionhttp.Handler.
type WorkspaceCreator interface {
	CreateFromTemplate(ctx context.Context, name, templateID string) (workspaceID string, err error)
}

// AssistantCreationOptions are trusted, server-owned Personal Assistant
// substitutions applied to a private copy of the personal-ops template before
// any workspace or agent is created. They are not accepted by the generic
// workspace HTTP API.
type AssistantCreationOptions struct {
	AssistantID          string
	RequestID            string
	DisplayName          string
	Appearance           *types.AgentAppearance
	Role                 types.AgentRole
	SystemPromptFragment string
}

// AssistantWorkspaceResult identifies the actual canonical records created by
// the template path; callers must persist these IDs rather than re-resolving by
// mutable display name.
type AssistantWorkspaceResult struct {
	WorkspaceID            string
	EntryAgentInstanceID   string
	GlobalAgentProfileName string
}

// AssistantWorkspaceCreator is the PAF-only extension implemented by
// sessionhttp.Handler. The legacy WorkspaceCreator method remains unchanged.
type AssistantWorkspaceCreator interface {
	CreatePersonalAssistantHQ(ctx context.Context, workspaceName string, options AssistantCreationOptions) (*AssistantWorkspaceResult, error)
}

// WorkspaceWriter is the narrow write contract Setup needs beyond
// WorkspaceReader, to persist the provisional brief configuration onto the
// newly created HQ workspace. session.HybridStore satisfies it.
type WorkspaceWriter interface {
	WorkspaceReader
	UpdateWorkspace(ctx context.Context, ws *session.Workspace) error
}

// SetupRequest is a Build My HQ submission. Every field beyond Name is
// optional — Setup fills in sensible defaults so the flow can complete with
// no more than the primary interactions required by PRD success metric 2.
type SetupRequest struct {
	Name                    string
	Timezone                string
	ScheduleDays            []string
	ScheduleTime            string
	Scope                   string // "all" | "selected"
	SelectedWorkspaceIDs    []string
	IncludeFutureWorkspaces bool
	NotifyOnReady           bool
}

// BriefConfig is the provisional Daily Brief configuration captured by
// setup. internal/dailybrief (task 5.0) owns durable configuration; this is
// stored as workspace SharedData in the meantime.
type BriefConfig struct {
	Timezone                string   `json:"timezone"`
	ScheduleDays            []string `json:"schedule_days,omitempty"`
	ScheduleTime            string   `json:"schedule_time,omitempty"`
	Scope                   string   `json:"scope"`
	SelectedWorkspaceIDs    []string `json:"selected_workspace_ids,omitempty"`
	IncludeFutureWorkspaces bool     `json:"include_future_workspaces"`
	NotifyOnReady           bool     `json:"notify_on_ready"`
}

// SetupResult is returned after a successful Build My HQ.
type SetupResult struct {
	Status      *Status     `json:"status"`
	BriefConfig BriefConfig `json:"brief_config"`
}

// SetupPartialFailureError reports that the HQ workspace was created but a
// later step failed, naming the workspace so the caller can offer retry or
// "keep as a normal workspace" (PRD FR30) instead of a bare error with no
// path forward.
type SetupPartialFailureError struct {
	WorkspaceID string
	Step        string
	Err         error
}

func (e *SetupPartialFailureError) Error() string {
	return fmt.Sprintf("personal hq: workspace %s was created but %s failed: %v", e.WorkspaceID, e.Step, e.Err)
}

func (e *SetupPartialFailureError) Unwrap() error { return e.Err }

// SetupCoordinator builds a new Personal HQ from the guided first-launch
// flow: creates the workspace via the canonical template-creation path,
// designates it for the user (which also completes the t2-build-hq
// progression quest via Service.onDesignated), and captures the provisional
// Daily Brief configuration — all as one user-visible operation.
//
// Designation only happens after workspace creation succeeds, so a creation
// failure never leaves a stale designation. If designation itself fails, the
// workspace already exists; SetupPartialFailureError names it so the caller
// can retry designation or keep the workspace undesignated rather than
// silently losing it or deleting user data (PRD FR29/FR30).
type SetupCoordinator struct {
	service    *Service
	creator    WorkspaceCreator
	workspaces WorkspaceWriter
}

// NewSetupCoordinator constructs a Build My HQ coordinator. workspaces may
// be nil — brief-config capture then best-effort no-ops, mirroring how
// template provenance persistence degrades elsewhere in this codebase.
func NewSetupCoordinator(service *Service, creator WorkspaceCreator, workspaces WorkspaceWriter) *SetupCoordinator {
	return &SetupCoordinator{service: service, creator: creator, workspaces: workspaces}
}

// Setup runs the full Build My HQ flow for userID.
func (c *SetupCoordinator) Setup(ctx context.Context, userID string, req SetupRequest) (*SetupResult, error) {
	if c == nil || c.service == nil || c.creator == nil {
		return nil, errors.New("personal hq setup coordinator is not configured")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "My HQ"
	}
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTimezone, tz)
	}

	workspaceID, err := c.creator.CreateFromTemplate(ctx, name, PersonalHQTemplateID)
	if err != nil {
		return nil, fmt.Errorf("failed to create the personal hq workspace: %w", err)
	}

	status, err := c.service.Designate(ctx, userID, workspaceID)
	if err != nil {
		return nil, &SetupPartialFailureError{WorkspaceID: workspaceID, Step: "designation", Err: err}
	}

	brief := BriefConfig{
		Timezone:                tz,
		ScheduleDays:            normalizeScheduleDays(req.ScheduleDays),
		ScheduleTime:            normalizeScheduleTime(req.ScheduleTime),
		Scope:                   normalizeScope(req.Scope),
		SelectedWorkspaceIDs:    append([]string(nil), req.SelectedWorkspaceIDs...),
		IncludeFutureWorkspaces: req.IncludeFutureWorkspaces,
		NotifyOnReady:           req.NotifyOnReady,
	}
	if c.workspaces != nil {
		if err := c.persistBriefConfig(ctx, workspaceID, brief); err != nil {
			// Best-effort: the HQ is fully usable without this, and HQ
			// settings (task 5.0) let the user (re)configure it later.
			logger.Warn("personal hq: failed to persist brief config", logger.Fields{"workspace_id": workspaceID, "error": err})
		}
	}

	if _, err := c.service.SetOnboardingState(ctx, userID, userprofile.HQOnboardingCompleted); err != nil {
		logger.Warn("personal hq: failed to mark onboarding completed", logger.Fields{"user_id": userID, "error": err})
	}

	finalStatus, err := c.service.Status(ctx, userID)
	if err != nil {
		finalStatus = status
	}
	return &SetupResult{Status: finalStatus, BriefConfig: brief}, nil
}

func (c *SetupCoordinator) persistBriefConfig(ctx context.Context, workspaceID string, brief BriefConfig) error {
	data, err := json.Marshal(brief)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	ws, err := c.workspaces.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	ws.SharedData[briefConfigSharedDataKey] = raw
	return c.workspaces.UpdateWorkspace(ctx, ws)
}

func normalizeScope(scope string) string {
	if strings.TrimSpace(strings.ToLower(scope)) == "selected" {
		return "selected"
	}
	return "all"
}

func normalizeScheduleTime(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "08:00"
	}
	return t
}

// defaultScheduleDays is the Monday-Friday default when a setup submission
// omits schedule days entirely.
var defaultScheduleDays = []string{"mon", "tue", "wed", "thu", "fri"}

func normalizeScheduleDays(days []string) []string {
	if len(days) == 0 {
		return append([]string(nil), defaultScheduleDays...)
	}
	out := make([]string, 0, len(days))
	for _, d := range days {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), defaultScheduleDays...)
	}
	return out
}
