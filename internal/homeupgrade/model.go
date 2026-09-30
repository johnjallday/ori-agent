// Package homeupgrade moves every Home pinned to an installed Home provider
// package, and the projects linked to those Homes, onto one exact newer
// release of that package. It implements the owner-reviewed, guidance-only
// upgrade in docs/architecture/independent-program-homes.md §6.1: review
// (read-only), commit (claim → replace → rebind → agents → succeeded), and
// recovery of an operation interrupted by a crash.
package homeupgrade

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	reviewTTL = 10 * time.Minute
	// Bounds keep the persisted plan small; a real owner has a handful of Homes.
	maxHomes           = 32
	maxProjectsPerHome = 256
)

// Operation statuses. claimed, replaced and reconcile_required hold the
// package's single active slot.
const (
	StatusClaimed           = "claimed"
	StatusReplaced          = "replaced"
	StatusReconcileRequired = "reconcile_required"
	StatusSucceeded         = "succeeded"
	StatusCancelled         = "cancelled"
)

// Refusals. Code maps each to a stable API code.
var (
	ErrUnavailable     = errors.New("the Home package upgrade is unavailable")
	ErrNoHomes         = errors.New("no Home uses this package")
	ErrCurrent         = errors.New("no newer release of this package is available")
	ErrNotGuidanceOnly = errors.New("the newer release changes more than Home guidance")
	ErrOtherOwner      = errors.New("a Home that uses this package belongs to another owner")
	ErrPinMismatch     = errors.New("a Home or linked project is not pinned to the installed release")
	ErrMirrorsDisagree = errors.New("the saved copies of a Home or linked project disagree")
	ErrRepairActive    = errors.New("a project role repair for a linked project is in progress")
	ErrUpgradeActive   = errors.New("another upgrade of this package is in progress or needs reconciliation")
	ErrReviewStale     = errors.New("the review expired or no longer matches; review the upgrade again")
	ErrTooLarge        = errors.New("too many Homes or linked projects for one upgrade")
	// ErrReconcileRequired means the operation stopped with records it cannot
	// account for; the affected Homes stay read-only until the owner reconciles.
	ErrReconcileRequired = errors.New("the upgrade stopped and needs reconciliation")
)

// Code is the stable API code for a refusal.
func Code(err error) string {
	for _, known := range []struct {
		err  error
		code string
	}{
		{ErrNoHomes, "home_upgrade_no_homes"}, {ErrCurrent, "home_upgrade_current"},
		{ErrNotGuidanceOnly, "home_upgrade_not_guidance_only"}, {ErrOtherOwner, "home_upgrade_other_owner"},
		{ErrPinMismatch, "home_upgrade_pin_mismatch"}, {ErrMirrorsDisagree, "home_upgrade_mirrors_disagree"},
		{ErrRepairActive, "home_upgrade_repair_active"}, {ErrUpgradeActive, "home_upgrade_active"},
		{ErrReviewStale, "home_upgrade_review_stale"}, {ErrTooLarge, "home_upgrade_too_large"},
		{ErrReconcileRequired, "home_upgrade_reconcile_required"},
	} {
		if errors.Is(err, known.err) {
			return known.code
		}
	}
	return "home_upgrade_unavailable"
}

// Target is the release the Plugins page would install for a plugin, inspected
// without installing: a reviewed release from its pinned source, or a local
// install's recorded source (Source empty).
type Target struct {
	Source     string
	Format     plugin.SourceFormat
	Inspection plugin.ReplacementTarget
}

// Plugins is the host's plugin manager seen by the upgrade.
type Plugins interface {
	List() ([]plugin.InstalledPlugin, error)
	// Target resolves and inspects the replacement the Plugins page would
	// offer. It returns ErrCurrent when there is nothing newer to install.
	Target(ctx context.Context, installed plugin.InstalledPlugin) (Target, error)
	// Replace installs exactly target through the ordinary replacement path,
	// which consults the replacement guard.
	Replace(ctx context.Context, name string, target Target) error
}

// Profiles is the global agent library.
type Profiles interface {
	GetAgent(name string) (*agent.Agent, bool)
	UpdateAgent(name string, updateFn func(*agent.Agent) error) error
}

// Plan is everything one upgrade will do, derived only from installed and
// inspected package evidence and the Homes' own records. Its digest binds a
// review to a commit.
type Plan struct {
	OwnerID         string        `json:"owner_id"`
	PluginID        string        `json:"plugin_id"`
	FromVersion     string        `json:"from_version"`
	FromFingerprint string        `json:"from_fingerprint"`
	FromGeneration  uint64        `json:"from_generation"`
	ToVersion       string        `json:"to_version"`
	ToFingerprint   string        `json:"to_fingerprint"`
	Programs        []PlanProgram `json:"programs"`
	Homes           []PlanHome    `json:"homes"`
	// TrashedHomes keep their old pin and stay read-only if restored.
	TrashedHomes int `json:"trashed_homes"`
}

// PlanProgram is one Home declaration the package contributes.
type PlanProgram struct {
	ProgramID           string                                  `json:"program_id"`
	From                workspace.AssistantProgramHomeOwner     `json:"from"`
	ToDeclarationDigest string                                  `json:"to_declaration_digest"`
	RolePrompts         []projecttemplates.HomeRolePromptChange `json:"role_prompts"`
}

// PlanHome is one affected Home.
type PlanHome struct {
	HomeID        string        `json:"home_id"`
	Name          string        `json:"name"`
	ProgramID     string        `json:"program_id"`
	StateRevision int64         `json:"state_revision"`
	Projects      []PlanProject `json:"projects"`
	Agents        []PlanAgent   `json:"agents"`
}

// PlanProject is one project linked to an affected Home.
type PlanProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Agent prompt actions. An agent's prompt is replaced only while it still
// equals the old role prompt exactly; an edited prompt is kept.
const (
	AgentReplace = "replace"
	AgentKeep    = "keep"
	AgentMissing = "missing"
)

// PlanAgent is one agent staffed in a Home role whose prompt changes. Its
// profile and its Home copy are decided separately.
type PlanAgent struct {
	RoleID    string `json:"role_id"`
	RoleLabel string `json:"role_label"`
	AgentName string `json:"agent_name"`
	Profile   string `json:"profile"`
	HomeCopy  string `json:"home_copy"`
}

// Review is a plan the owner may commit until it expires.
type Review struct {
	Token     string             `json:"token"`
	ExpiresAt time.Time          `json:"expires_at"`
	Plan      Plan               `json:"plan"`
	Trust     plugin.TrustReport `json:"trust"`
}

// Operation is the durable record of one committed upgrade.
type Operation struct {
	ID               string    `json:"id"`
	PluginID         string    `json:"plugin_id"`
	Status           string    `json:"status"`
	Plan             Plan      `json:"plan"`
	TargetGeneration uint64    `json:"target_generation,omitempty"`
	Outcome          Outcome   `json:"outcome"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Outcome is what the operation actually did, or why it stopped.
type Outcome struct {
	Reason string         `json:"reason,omitempty"`
	Agents []AgentOutcome `json:"agents,omitempty"`
}

// AgentOutcome is what happened to one staffed agent's prompt.
type AgentOutcome struct {
	HomeID    string `json:"home_id"`
	RoleID    string `json:"role_id"`
	AgentName string `json:"agent_name"`
	Profile   string `json:"profile"`
	HomeCopy  string `json:"home_copy"`
}

// Active reports whether the operation still holds its package's slot.
func (o Operation) Active() bool {
	return o.Status == StatusClaimed || o.Status == StatusReplaced || o.Status == StatusReconcileRequired
}
