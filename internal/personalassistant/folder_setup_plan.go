package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Line kinds of a folder setup plan, in the order the card shows them.
const (
	FolderPlanIntegration = "integration"
	FolderPlanProvider    = "provider"
	FolderPlanHome        = "home"
	FolderPlanWorkspace   = "workspace"
	FolderPlanFolder      = "folder"
	FolderPlanMode        = "mode"
	FolderPlanAgents      = "agents"
	FolderPlanTask        = "task"
)

// States of one plan line while a run is in progress.
const (
	FolderLineWaiting = "waiting"
	FolderLineWorking = "working"
	FolderLineDone    = "done"
	FolderLineFailed  = "failed"
)

// Statuses of a one-card setup run.
const (
	FolderSetupRunning = "running"
	FolderSetupStopped = "stopped"
	FolderSetupDone    = "done"
)

// Stop reasons the card must be able to explain.
const (
	FolderStopPlanChanged   = "plan_changed"
	FolderStopNeedsPick     = "needs_pick"
	FolderStopNeedsChoice   = "needs_choice"
	FolderStopNeedsModel    = "needs_model"
	FolderStopInstallFailed = "install_failed"
	FolderStopInterrupted   = "interrupted"
	FolderStopFailed        = "failed"
)

// FolderPlanLine is one consequence the user consents to. Name and Detail are
// plain text and never contain a filesystem path. State is set only while a
// run is in progress and is not part of the plan's identity.
type FolderPlanLine struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	State  string `json:"state,omitempty"`
}

// Where an installable plugin stands, as the plan promised it.
const (
	FolderInstallReady   = "ready"
	FolderInstallInstall = "install"
	FolderInstallEnable  = "enable"
	FolderInstallUpdate  = "update"
)

// FolderSetupIntent is what the confirmed plan promised about the two plugins a
// setup may install, in a form the run can check. A resumed run is held to this,
// not to a recomputed plan: the plan shrinks as steps finish, the promise does not.
// It never leaves the server.
type FolderSetupIntent struct {
	Integration        string `json:"integration,omitempty"`
	IntegrationPlugin  string `json:"integration_plugin,omitempty"`
	IntegrationVersion string `json:"integration_version,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ProviderPlugin     string `json:"provider_plugin,omitempty"`
	ProviderVersion    string `json:"provider_version,omitempty"`
}

// FolderSetupPlan is everything one press of Set up will do. The browser sends
// Digest back with the click and the server recomputes the plan: a mismatch
// means the card on screen is stale and the click is refused.
type FolderSetupPlan struct {
	Lines  []FolderPlanLine `json:"lines"`
	Digest string           `json:"digest"`
	// Intent is the machine-readable promise behind the lines; server only.
	Intent FolderSetupIntent `json:"-"`
}

// FolderPlanDigest is the SHA-256 of the lines' canonical JSON with State
// excluded, so progress on a line never changes what was consented to.
func FolderPlanDigest(lines []FolderPlanLine) string {
	canonical := make([]FolderPlanLine, len(lines))
	for i, line := range lines {
		canonical[i] = FolderPlanLine{Kind: line.Kind, Name: line.Name, Detail: line.Detail}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// NewFolderSetupPlan builds a plan and stamps its digest.
func NewFolderSetupPlan(lines []FolderPlanLine) FolderSetupPlan {
	copied := append([]FolderPlanLine(nil), lines...)
	return FolderSetupPlan{Lines: copied, Digest: FolderPlanDigest(copied)}
}

// Stamped returns the plan with its digest recomputed from its lines and its
// lines copied, keeping the intent. A host's plan is never trusted to carry a
// digest it computed itself.
func (p FolderSetupPlan) Stamped() FolderSetupPlan {
	stamped := NewFolderSetupPlan(p.Lines)
	stamped.Intent = p.Intent
	return stamped
}

// FolderSetupUpdate is one snapshot of a run, written onto the offer.
type FolderSetupUpdate struct {
	Lines           []FolderPlanLine
	Status          string
	StopReason      string
	RunID           string
	EntryName       string
	EntryCandidates []string
	// keepLines makes the service keep the stored lines and change only the
	// status; the service uses it to record a stop it decided on itself.
	keepLines bool
}

// FolderSetupRequest is what the host needs to plan or run a setup. Path is the
// folder's canonical path from the offer the server holds; it is set only for
// Run and never reaches the browser.
type FolderSetupRequest struct {
	UserID string
	Offer  FolderOffer
	// AppInstalled says whether the application itself is on this computer, which
	// the plan states honestly; setup never depends on it.
	AppInstalled bool
	Path         string
	EntryName    string
	// Plan is the confirmed plan a Run is held to.
	Plan FolderSetupPlan
	// Update records the run's progress on the offer.
	Update func(ctx context.Context, update FolderSetupUpdate) error
}

// FolderSetupRunner is the host seam behind one-card setup. Plan reads the
// consequences for the offer; Run drives them to the end or to the first stop,
// reporting through req.Update, and returns only for an unexpected error.
type FolderSetupRunner interface {
	Plan(ctx context.Context, req FolderSetupRequest) (FolderSetupPlan, error)
	Run(ctx context.Context, req FolderSetupRequest) error
}

// FolderSetupInput is the click on Set up: the plan digest the card showed, the
// idempotency key, and the project file when the user chose one.
type FolderSetupInput struct {
	RequestID  string
	PlanDigest string
	EntryName  string
}

// FolderSetupView is a run as the browser sees it.
type FolderSetupView struct {
	// PlanDigest is the plan the run was confirmed on; a resume sends it back.
	PlanDigest      string           `json:"plan_digest"`
	Status          string           `json:"status"`
	StopReason      string           `json:"stop_reason,omitempty"`
	Lines           []FolderPlanLine `json:"lines"`
	EntryCandidates []string         `json:"entry_candidates,omitempty"`
}

const (
	folderSetupMaxLines   = 16
	folderSetupMaxText    = 512
	folderSetupMaxEntries = 64
)

// validateFolderSetupRun keeps a stored run bounded and path-free: lines are
// plain text, and project files are bare names.
func validateFolderSetupRun(run FolderSetupRun) error {
	switch run.Status {
	case FolderSetupRunning, FolderSetupStopped, FolderSetupDone:
	default:
		return fmt.Errorf("%w: setup status", errFolderDigestInvalid)
	}
	if len(run.Lines) > folderSetupMaxLines || len(run.EntryCandidates) > folderSetupMaxEntries ||
		len(run.PlanDigest) > 64 || len(run.RunID) > 200 {
		return fmt.Errorf("%w: setup limits", errFolderDigestInvalid)
	}
	for _, line := range run.Lines {
		if line.Kind == "" || len(line.Name) > folderSetupMaxText || len(line.Detail) > folderSetupMaxText ||
			strings.ContainsAny(line.Name+line.Detail, "\x00\r\n") || looksLikeFilesystemPath(line.Name) || looksLikeFilesystemPath(line.Detail) {
			return fmt.Errorf("%w: setup line", errFolderDigestInvalid)
		}
	}
	intent := run.Intent
	for _, text := range []string{intent.Integration, intent.IntegrationPlugin, intent.IntegrationVersion,
		intent.Provider, intent.ProviderPlugin, intent.ProviderVersion} {
		if len(text) > 80 || strings.ContainsAny(text, "/\\\x00\r\n") {
			return fmt.Errorf("%w: setup intent", errFolderDigestInvalid)
		}
	}
	for _, name := range append([]string{run.EntryName}, run.EntryCandidates...) {
		if name != "" && validateFolderName(name) != nil {
			return fmt.Errorf("%w: setup entry", errFolderDigestInvalid)
		}
	}
	return nil
}

// looksLikeFilesystemPath is true for text that names a place on disk: a leading
// slash or tilde, a backslash, or a slash-led word anywhere. A reviewed source
// label such as "owner/repo" is not one.
func looksLikeFilesystemPath(text string) bool {
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") ||
		strings.Contains(text, "\\") || strings.Contains(text, " /") || strings.Contains(text, " ~/")
}

// FolderSetupRun is the persisted state of one offer's one-card setup. It is
// stored with the offer so the card survives a reload. A Running status with
// no live run (the server restarted) is reported as Stopped/interrupted.
type FolderSetupRun struct {
	PlanDigest string           `json:"plan_digest"`
	Lines      []FolderPlanLine `json:"lines"`
	Status     string           `json:"status"`
	StopReason string           `json:"stop_reason,omitempty"`
	// EntryName is the project file the run uses; EntryCandidates are the file
	// names (never paths) offered when several exist.
	EntryName       string    `json:"entry_name,omitempty"`
	EntryCandidates []string  `json:"entry_candidates,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// Intent is what the confirmed plan promised, kept for a resume.
	Intent FolderSetupIntent `json:"intent,omitempty"`
}
