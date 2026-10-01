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

// FolderSetupPlan is everything one press of Set up will do. The browser sends
// Digest back with the click and the server recomputes the plan: a mismatch
// means the card on screen is stale and the click is refused.
type FolderSetupPlan struct {
	Lines  []FolderPlanLine `json:"lines"`
	Digest string           `json:"digest"`
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
	UserID    string
	Offer     FolderOffer
	Path      string
	EntryName string
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
			strings.ContainsAny(line.Name+line.Detail, "/\\\x00\r\n") {
			return fmt.Errorf("%w: setup line", errFolderDigestInvalid)
		}
	}
	for _, name := range append([]string{run.EntryName}, run.EntryCandidates...) {
		if name != "" && validateFolderName(name) != nil {
			return fmt.Errorf("%w: setup entry", errFolderDigestInvalid)
		}
	}
	return nil
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
}
