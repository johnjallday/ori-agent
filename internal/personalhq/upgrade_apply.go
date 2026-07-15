package personalhq

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

var (
	// ErrNotDesignatedHQ is returned when ApplyUpgrade targets a workspace that
	// is not the user's current, valid Personal HQ. Upgrades only apply to the
	// designated HQ (contract §5.1 revalidation).
	ErrNotDesignatedHQ = errors.New("personal hq: workspace is not the user's designated personal hq")
	// ErrUpgradeBlocked is returned when a plan has blockers that prevent apply.
	ErrUpgradeBlocked = errors.New("personal hq: upgrade is blocked")
)

// SpecialistProvisioner attaches the named Personal HQ specialist roles to an
// existing workspace, idempotently, and persists the roster. It reuses the same
// agent-create + attach primitives as template-based workspace creation (task
// 2.9), so Build My HQ and Upgrade converge on one provisioning path rather than
// a second constructor. Implemented by sessionhttp.Handler.
//
// It MUST attach missing specialists without disturbing the workspace's existing
// entry agent, user-edited agents, tasks, or settings (task 2.6), and MUST be a
// no-op for a role whose agent instance already exists. It returns the roles it
// actually added.
type SpecialistProvisioner interface {
	EnsureSpecialists(ctx context.Context, workspaceID string, roles []SpecialistRole) (added []string, err error)
}

// UpgradeResult reports the outcome of an apply for the HTTP layer and UI.
type UpgradeResult struct {
	Plan       UpgradePlan    `json:"plan"`
	AddedRoles []string       `json:"added_roles,omitempty"`
	Version    int            `json:"version"`
	Outcome    UpgradeOutcome `json:"outcome"`
}

// UpgradeCoordinator applies a planned upgrade to a designated Personal HQ. It
// mirrors SetupCoordinator: a small orchestrator that composes the pure domain
// (PlanUpgrade), the specialist provisioner, and workspace persistence into one
// idempotent, retryable operation.
type UpgradeCoordinator struct {
	service     *Service
	workspaces  WorkspaceWriter
	specialists SpecialistProvisioner
}

// NewUpgradeCoordinator constructs an upgrade coordinator. All dependencies are
// required for a functional apply; PlanFor still works with only service+workspaces.
func NewUpgradeCoordinator(service *Service, workspaces WorkspaceWriter, specialists SpecialistProvisioner) *UpgradeCoordinator {
	return &UpgradeCoordinator{service: service, workspaces: workspaces, specialists: specialists}
}

// PlanFor computes the upgrade plan for the user's designated HQ (preview,
// task 2.10). It revalidates that workspaceID is the user's current valid HQ so
// the preview and apply agree on the target.
func (c *UpgradeCoordinator) PlanFor(ctx context.Context, userID, workspaceID string) (UpgradePlan, error) {
	if c == nil || c.service == nil || c.workspaces == nil {
		return UpgradePlan{}, errors.New("personal hq upgrade coordinator is not configured")
	}
	userID = normalizeUserID(userID)
	if err := c.assertDesignated(ctx, userID, workspaceID); err != nil {
		return UpgradePlan{}, err
	}
	ws, err := c.workspaces.GetWorkspace(ctx, strings.TrimSpace(workspaceID))
	if err != nil {
		return UpgradePlan{}, err
	}
	return PlanUpgrade(ws, userID), nil
}

// ApplyUpgrade upgrades the user's designated HQ to CurrentProvisioningVersion.
// It is idempotent (re-running after success is a no-op that re-stamps the
// version) and retryable after a partial failure (a prior partial/failed run is
// simply re-planned and re-applied — EnsureSpecialists is itself idempotent per
// role).
//
// Ordering guarantees the version is only stamped as success after the roster
// changes persist, so a crash mid-apply leaves the HQ recoverable: the version
// stays behind and the next apply retries.
func (c *UpgradeCoordinator) ApplyUpgrade(ctx context.Context, userID, workspaceID string) (*UpgradeResult, error) {
	if c == nil || c.service == nil || c.workspaces == nil || c.specialists == nil {
		return nil, errors.New("personal hq upgrade coordinator is not configured")
	}
	userID = normalizeUserID(userID)
	workspaceID = strings.TrimSpace(workspaceID)
	if err := c.assertDesignated(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	ws, err := c.workspaces.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	plan := PlanUpgrade(ws, userID)
	if plan.Blocked() {
		return nil, fmt.Errorf("%w: %s", ErrUpgradeBlocked, strings.Join(plan.Blockers, "; "))
	}

	// Up to date and nothing to add: re-stamp the version (idempotent) and return.
	if plan.UpToDate && !plan.HasChanges() {
		if err := c.stampVersion(ctx, workspaceID, UpgradeOutcomeSuccess, ""); err != nil {
			return nil, err
		}
		return &UpgradeResult{Plan: plan, Version: CurrentProvisioningVersion, Outcome: UpgradeOutcomeSuccess}, nil
	}

	added, provErr := c.specialists.EnsureSpecialists(ctx, workspaceID, rolesByAgentName(plan.MissingRoles))
	if provErr != nil {
		// Partial/failed: record the outcome so the UI can offer retry, then
		// surface the error. The version is NOT advanced.
		outcome := UpgradeOutcomeFailed
		if len(added) > 0 {
			outcome = UpgradeOutcomePartial
		}
		if stampErr := c.stampVersionKeepingVersion(ctx, workspaceID, outcome, provErr.Error()); stampErr != nil {
			logger.Warn("personal hq: failed to record upgrade failure outcome", logger.Fields{"workspace_id": workspaceID, "error": stampErr})
		}
		return &UpgradeResult{Plan: plan, AddedRoles: added, Outcome: outcome}, provErr
	}

	if err := c.stampVersion(ctx, workspaceID, UpgradeOutcomeSuccess, ""); err != nil {
		return nil, err
	}
	logger.Info("personal hq: upgrade applied", logger.Fields{"user_id": userID, "workspace_id": workspaceID, "added": strings.Join(added, ","), "version": CurrentProvisioningVersion})
	return &UpgradeResult{Plan: plan, AddedRoles: added, Version: CurrentProvisioningVersion, Outcome: UpgradeOutcomeSuccess}, nil
}

// assertDesignated verifies workspaceID is the user's current, valid HQ.
func (c *UpgradeCoordinator) assertDesignated(ctx context.Context, userID, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return ErrWorkspaceIDRequired
	}
	status, err := c.service.Status(ctx, userID)
	if err != nil {
		return err
	}
	if !status.Valid || !strings.EqualFold(status.WorkspaceID, workspaceID) {
		return ErrNotDesignatedHQ
	}
	return nil
}

// stampVersion records a successful provisioning at CurrentProvisioningVersion.
func (c *UpgradeCoordinator) stampVersion(ctx context.Context, workspaceID string, outcome UpgradeOutcome, errMsg string) error {
	return c.persistProvisionState(ctx, workspaceID, CurrentProvisioningVersion, outcome, errMsg)
}

// stampVersionKeepingVersion records a failure outcome WITHOUT advancing the
// version, so a later retry re-plans from the same version.
func (c *UpgradeCoordinator) stampVersionKeepingVersion(ctx context.Context, workspaceID string, outcome UpgradeOutcome, errMsg string) error {
	ws, err := c.workspaces.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	current := ReadProvisionState(ws).Version
	return c.persistProvisionState(ctx, workspaceID, current, outcome, errMsg)
}

func (c *UpgradeCoordinator) persistProvisionState(ctx context.Context, workspaceID string, version int, outcome UpgradeOutcome, errMsg string) error {
	ws, err := c.workspaces.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if err := writeProvisionState(ws, ProvisionState{Version: version, LastUpgradeOutcome: outcome, LastUpgradeError: errMsg}); err != nil {
		return err
	}
	return c.workspaces.UpdateWorkspace(ctx, ws)
}

// rolesByAgentName maps canonical agent names (as reported in a plan's
// MissingRoles) back to their SpecialistRole definitions, preserving roster order.
func rolesByAgentName(names []string) []SpecialistRole {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[strings.ToLower(strings.TrimSpace(n))] = struct{}{}
	}
	var out []SpecialistRole
	for _, role := range V1Roster {
		if _, ok := want[strings.ToLower(role.AgentName)]; ok {
			out = append(out, role)
		}
	}
	return out
}
