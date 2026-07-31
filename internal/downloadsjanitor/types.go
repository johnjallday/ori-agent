// Package downloadsjanitor implements the Downloads Janitor domain: the
// settings and readiness of one workspace's configured inbox folder, the
// candidates and batches a scan produces, and the deterministic file-action
// service that carries out user-approved moves and Trash actions.
//
// Two rules shape everything here:
//
//   - Nothing mutates a file without a prior, explicit user approval recorded
//     against that exact candidate. The agent proposes; only the user approves.
//   - The server derives every path. Callers (browser, agent, scheduler,
//     watcher, API) submit candidate and category IDs, never filesystem paths.
package downloadsjanitor

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// SettingsSchemaVersion is the on-disk revision of a workspace's Janitor
// settings. Persisted state that predates a field loads with that field's zero
// value and is treated as unconfigured rather than as an error.
const SettingsSchemaVersion = 1

// DefaultFilingRootName is the single directory, directly inside the configured
// root, that every approved move files into: <root>/Filed/<category>.
const DefaultFilingRootName = "Filed"

// DefaultDailyScanLocalTime is the daily catch-up time shown during setup, in
// the user's own timezone (FR-20).
const DefaultDailyScanLocalTime = "09:00"

// ContentMode records whether — and where — file contents may be read to help
// classification. It defaults to metadata-only: names, types, sizes, and dates
// only, with no file ever opened (FR-21, FR-46, FR-115).
type ContentMode string

const (
	// ContentModeMetadataOnly is the default. No file content is ever read.
	ContentModeMetadataOnly ContentMode = "metadata_only"
	// ContentModeLocalModel permits bounded, classifier-only content reads whose
	// content stays on the device.
	ContentModeLocalModel ContentMode = "local_model"
	// ContentModeCloudModel permits bounded, classifier-only content reads whose
	// content is sent to the named cloud provider. Requires a provider-specific
	// first-use confirmation before any content leaves the device (FR-55).
	ContentModeCloudModel ContentMode = "cloud_model"
)

// ValidContentModes is the accepted set of content-inspection modes.
var ValidContentModes = []ContentMode{ContentModeMetadataOnly, ContentModeLocalModel, ContentModeCloudModel}

// ReadsFileContent reports whether the mode permits opening files at all. It is
// the single predicate the scanner, classifier, and privacy disclosure share,
// so "metadata-only" can never mean two different things in two places.
func (m ContentMode) ReadsFileContent() bool {
	return m == ContentModeLocalModel || m == ContentModeCloudModel
}

// LeavesDevice reports whether the mode can send file content off the device.
func (m ContentMode) LeavesDevice() bool {
	return m == ContentModeCloudModel
}

// NormalizeContentMode lower-cases and validates a mode, falling back to
// metadata-only when empty or unrecognized. Failing closed matters here: a
// corrupted or hand-edited value must never silently upgrade into reading file
// contents.
func NormalizeContentMode(m ContentMode) ContentMode {
	normalized := ContentMode(strings.ToLower(strings.TrimSpace(string(m))))
	if slices.Contains(ValidContentModes, normalized) {
		return normalized
	}
	return ContentModeMetadataOnly
}

// ErrInvalidSettings reports settings that cannot be persisted: a missing
// workspace, an unusable filing-root name, a malformed daily time, or an
// unknown timezone.
var ErrInvalidSettings = errors.New("invalid downloads janitor settings")

// JanitorSettings is one workspace's Downloads Janitor configuration. It is
// created empty (setup required) and filled in only by confirmed setup: no
// field here is ever inferred from a template suggestion, a previous workspace,
// or the presence of a folder on disk.
//
// The on-disk copy is authoritative. In-memory callers must treat a returned
// value as a snapshot, not a handle.
type JanitorSettings struct {
	// SchemaVersion is the revision this record was written with.
	SchemaVersion int `json:"schema_version"`
	// WorkspaceID scopes every candidate, batch, action, and notification. It is
	// also the isolation boundary: a read for one workspace must never return
	// another's state (FR-118).
	WorkspaceID string `json:"workspace_id"`
	// DirectoryReferenceID is the workspace directory reference created for the
	// approved root. Empty until setup is confirmed.
	DirectoryReferenceID string `json:"directory_reference_id,omitempty"`
	// RootConflictWorkspaceID names the workspace that already manages an
	// overlapping folder, when reconciliation found a pre-existing conflict.
	//
	// Set only by ReconcileOverlappingRoots, for overlaps that predate the
	// cross-workspace check. It pauses unattended work and surfaces a repairable
	// Needs-attention state; it never changes the folder, because choosing a
	// different one is the user's decision (task 3.14).
	RootConflictWorkspaceID string `json:"root_conflict_workspace_id,omitempty"`
	// RootID identifies this generation of the managed folder. It is issued when
	// a folder is first confirmed and RE-ISSUED whenever the folder changes, so
	// journal entries can say which folder they belong to without storing an
	// absolute path (FR-57, FR-143). Empty on records written before it existed.
	RootID string `json:"root_id,omitempty"`
	// RootPath is the normalized absolute path of the confirmed inbox folder.
	// Empty until setup is confirmed; "~" is expanded server-side at that point
	// and never before (FR-13).
	RootPath string `json:"root_path,omitempty"`
	// FilingRootName is the destination directory directly inside RootPath.
	// Always DefaultFilingRootName in version 1; stored so the value the user
	// was shown at setup stays recorded rather than being re-derived.
	FilingRootName string `json:"filing_root_name,omitempty"`
	// DailyScanLocalTime is the daily catch-up time as "HH:MM" wall clock.
	DailyScanLocalTime string `json:"daily_scan_local_time,omitempty"`
	// Timezone is the IANA zone the daily time is interpreted in. Empty means
	// the server's local zone, resolved when the schedule is installed.
	Timezone string `json:"timezone,omitempty"`
	// ContentMode is the content-inspection setting; metadata-only by default.
	ContentMode ContentMode `json:"content_mode,omitempty"`
	// ContentProvider names the model provider content may be sent to, for
	// disclosure. Only meaningful when ContentMode reads content.
	ContentProvider string `json:"content_provider,omitempty"`
	// ContentConsentProvider records the provider the user confirmed content
	// transfer to. Consent is provider-specific and workspace-specific: changing
	// the provider clears it, so consent is never inherited (FR-55).
	ContentConsentProvider string `json:"content_consent_provider,omitempty"`
	// ContentConsentAt is when that confirmation was given.
	ContentConsentAt time.Time `json:"content_consent_at,omitempty"`
	// Paused stops watcher and scheduled scans without discarding configuration
	// or history.
	Paused bool `json:"paused,omitempty"`
	// AutomationApprovedAt is when the user approved unattended work — the
	// folder watcher and the daily catch-up — after being shown what they do.
	// Zero means they never have, which is not the same as having paused them:
	// pausing is an operational choice about something already approved, and it
	// must never read as unfinished setup.
	AutomationApprovedAt time.Time `json:"automation_approved_at,omitempty"`
	// SetupCompletedAt is when the user confirmed a folder. Zero means setup has
	// not completed, which is what makes SetupRequired the default state.
	SetupCompletedAt time.Time `json:"setup_completed_at,omitempty"`
	// UpdatedAt is the last time these settings changed.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// NewSettings returns the initial, unconfigured settings for a workspace:
// metadata-only, unpaused, no folder selected. This is what a workspace with no
// persisted state loads as, so "never set up" and "state file missing" behave
// identically (FR-5).
func NewSettings(workspaceID string) JanitorSettings {
	return JanitorSettings{
		SchemaVersion:      SettingsSchemaVersion,
		WorkspaceID:        strings.TrimSpace(workspaceID),
		FilingRootName:     DefaultFilingRootName,
		DailyScanLocalTime: DefaultDailyScanLocalTime,
		ContentMode:        ContentModeMetadataOnly,
	}
}

// IsSetUp reports whether setup has been confirmed with a usable root. Both the
// path and the directory reference are required: a root without a reference
// means the binding step did not finish, which is not a set-up workspace.
func (s JanitorSettings) IsSetUp() bool {
	return strings.TrimSpace(s.RootPath) != "" &&
		strings.TrimSpace(s.DirectoryReferenceID) != "" &&
		!s.SetupCompletedAt.IsZero()
}

// FilingRootPath returns <root>/Filed. It returns "" before setup, so callers
// cannot accidentally derive a destination from an unconfigured workspace.
func (s JanitorSettings) FilingRootPath() string {
	root := strings.TrimSpace(s.RootPath)
	if root == "" {
		return ""
	}
	name := strings.TrimSpace(s.FilingRootName)
	if name == "" {
		name = DefaultFilingRootName
	}
	return filepath.Join(root, name)
}

// RequiresContentConsent reports whether a content read would need a
// confirmation the user has not yet given for the currently configured cloud
// provider. Local-model and metadata-only modes never require it.
func (s JanitorSettings) RequiresContentConsent() bool {
	if !s.ContentMode.LeavesDevice() {
		return false
	}
	provider := strings.TrimSpace(s.ContentProvider)
	if provider == "" {
		return true
	}
	return !strings.EqualFold(provider, strings.TrimSpace(s.ContentConsentProvider))
}

// Normalize applies defaults and canonical forms without rejecting anything. It
// is what the load path uses: a missing, older, or hand-edited record must
// degrade to a safe configuration rather than fail to load. Unsafe values fail
// closed (unknown content mode becomes metadata-only; an unusable filing-root
// name becomes "Filed").
func (s JanitorSettings) Normalize() JanitorSettings {
	out := s
	out.SchemaVersion = SettingsSchemaVersion
	out.WorkspaceID = strings.TrimSpace(out.WorkspaceID)
	out.DirectoryReferenceID = strings.TrimSpace(out.DirectoryReferenceID)
	out.RootPath = strings.TrimSpace(out.RootPath)
	if out.RootPath != "" {
		out.RootPath = filepath.Clean(out.RootPath)
	}
	if !isSafeDirectoryName(out.FilingRootName) {
		out.FilingRootName = DefaultFilingRootName
	}
	if normalized, err := workspace.NormalizeLocalTimeOfDay(out.DailyScanLocalTime); err == nil {
		out.DailyScanLocalTime = normalized
	} else {
		out.DailyScanLocalTime = DefaultDailyScanLocalTime
	}
	out.Timezone = strings.TrimSpace(out.Timezone)
	if out.Timezone != "" {
		if _, err := time.LoadLocation(out.Timezone); err != nil {
			out.Timezone = ""
		}
	}
	out.ContentMode = NormalizeContentMode(out.ContentMode)
	out.ContentProvider = strings.TrimSpace(out.ContentProvider)
	out.ContentConsentProvider = strings.TrimSpace(out.ContentConsentProvider)
	if !out.ContentMode.ReadsFileContent() {
		out.ContentProvider = ""
	}
	// A confirmed root with no completion timestamp (or the reverse) is a
	// half-written record: treat it as not set up rather than as configured.
	if out.RootPath == "" || out.DirectoryReferenceID == "" {
		out.SetupCompletedAt = time.Time{}
	}
	return out
}

// Validate enforces what a *save* may contain. Unlike Normalize (lenient, used
// on load), this rejects: the authoring path should report a bad daily time or
// unknown timezone rather than silently substituting a default the user did not
// choose.
func (s JanitorSettings) Validate() error {
	if strings.TrimSpace(s.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidSettings)
	}
	if !isSafeDirectoryName(s.FilingRootName) {
		return fmt.Errorf("%w: filing root %q must be a single directory name", ErrInvalidSettings, s.FilingRootName)
	}
	if _, err := workspace.NormalizeLocalTimeOfDay(s.DailyScanLocalTime); err != nil {
		return fmt.Errorf("%w: daily scan time %v", ErrInvalidSettings, err)
	}
	if tz := strings.TrimSpace(s.Timezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("%w: unknown timezone %q", ErrInvalidSettings, s.Timezone)
		}
	}
	if !slices.Contains(ValidContentModes, NormalizeContentMode(s.ContentMode)) || string(s.ContentMode) != string(NormalizeContentMode(s.ContentMode)) {
		return fmt.Errorf("%w: unknown content mode %q", ErrInvalidSettings, s.ContentMode)
	}
	if s.RootPath != "" && !filepath.IsAbs(s.RootPath) {
		return fmt.Errorf("%w: root path %q must be absolute", ErrInvalidSettings, s.RootPath)
	}
	return nil
}

// isSafeDirectoryName reports whether name is a single, non-traversing
// directory name usable directly inside the configured root.
func isSafeDirectoryName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return false
	}
	return name == filepath.Base(name)
}

// ReadinessState is the workspace-level status shown in the header and review
// surface (FR-109). Scanning and Review-ready are activity states layered on
// top of these by the service; they are not configuration readiness.
type ReadinessState string

const (
	// ReadinessSetupRequired means no folder has been confirmed yet.
	ReadinessSetupRequired ReadinessState = "setup_required"
	// ReadinessReady means every required component check passed.
	ReadinessReady ReadinessState = "ready"
	// ReadinessNeedsAttention means setup completed but a required component is
	// failing — permission loss, a missing root, a watcher or scheduler that did
	// not register, or unwritable state.
	ReadinessNeedsAttention ReadinessState = "needs_attention"
)

// ReadinessComponent names one thing that must work before the Janitor can run
// unattended. Watcher and scheduler registration are declared here from the
// start, and report ComponentPending until the automation groups implement
// them, so "Ready" can never be shown by a build that simply has not checked.
type ReadinessComponent string

const (
	ComponentDirectoryAccess ReadinessComponent = "directory_access"
	ComponentDestination     ReadinessComponent = "destination"
	ComponentMCPBinding      ReadinessComponent = "mcp_binding"
	ComponentPersistence     ReadinessComponent = "persistence"
	ComponentWatcher         ReadinessComponent = "watcher"
	ComponentScheduler       ReadinessComponent = "scheduler"
)

// RequiredComponents lists every component that must pass before a workspace
// may report Ready, in display order.
var RequiredComponents = []ReadinessComponent{
	ComponentDirectoryAccess,
	ComponentDestination,
	ComponentMCPBinding,
	ComponentPersistence,
	ComponentWatcher,
	ComponentScheduler,
}

// ComponentStatus is the outcome of one component check.
type ComponentStatus string

const (
	// ComponentOK means the component is working.
	ComponentOK ComponentStatus = "ok"
	// ComponentFailed means the component is required and not working. Any
	// failed component forces NeedsAttention.
	ComponentFailed ComponentStatus = "failed"
	// ComponentPending means the check has not run yet (setup incomplete, or a
	// checker not wired in this build). Pending is never Ready.
	ComponentPending ComponentStatus = "pending"
)

// ComponentCheck is one component's readiness result. Message is user-facing
// and must stay free of raw internal errors and absolute paths (FR-110); Code
// is a stable machine-readable reason the UI maps to a repair action.
type ComponentCheck struct {
	Component ReadinessComponent `json:"component"`
	Status    ComponentStatus    `json:"status"`
	// Code is a stable identifier for the failure reason (e.g.
	// "permission_denied", "root_missing"). Empty when the check passed.
	Code string `json:"code,omitempty"`
	// Message is the plain-language explanation shown to the user.
	Message string `json:"message,omitempty"`
	// Repair, when set, names the recovery action the UI should offer (e.g.
	// "relink_folder", "grant_permission").
	Repair string `json:"repair,omitempty"`
}

// Readiness is the full readiness picture for one workspace.
type Readiness struct {
	WorkspaceID string           `json:"workspace_id"`
	State       ReadinessState   `json:"state"`
	Checks      []ComponentCheck `json:"checks,omitempty"`
	// Paused mirrors the setting so the UI can distinguish "ready but paused"
	// from "not ready".
	Paused bool `json:"paused,omitempty"`
	// CheckedAt is when these checks ran.
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

// Failing returns the checks that are failing, in RequiredComponents order.
func (r Readiness) Failing() []ComponentCheck {
	var out []ComponentCheck
	for _, check := range r.Checks {
		if check.Status == ComponentFailed {
			out = append(out, check)
		}
	}
	return out
}

// SetupRequiredReadiness is the readiness of a workspace that has not completed
// setup: every component pending, nothing claimed as working.
func SetupRequiredReadiness(workspaceID string, now time.Time) Readiness {
	checks := make([]ComponentCheck, 0, len(RequiredComponents))
	for _, component := range RequiredComponents {
		checks = append(checks, ComponentCheck{
			Component: component,
			Status:    ComponentPending,
			Message:   "Waiting for you to choose a folder.",
		})
	}
	return Readiness{
		WorkspaceID: strings.TrimSpace(workspaceID),
		State:       ReadinessSetupRequired,
		Checks:      checks,
		CheckedAt:   now,
	}
}

// DeriveReadinessState resolves the workspace-level state from component
// checks. It fails closed in three ways: a workspace that is not set up is
// always SetupRequired, any failed component forces NeedsAttention, and a
// component that is missing or still pending also forces NeedsAttention rather
// than Ready.
//
// Pausing is the exception, and it is not a failure. A paused workspace has no
// registered watcher and no scheduled catch-up by design — that is what the
// user asked for. Counting those against readiness reported "Needs attention"
// for a workspace behaving exactly as instructed, which is crying wolf: a user
// who sees that badge for their own deliberate choice learns to ignore it, and
// then misses a real permission loss. Everything the user did not switch off
// must still be OK.
func DeriveReadinessState(setUp bool, checks []ComponentCheck) ReadinessState {
	return deriveReadinessState(setUp, false, checks)
}

// DeriveReadinessStateWhenPaused is DeriveReadinessState for a workspace the
// user has paused.
func DeriveReadinessStateWhenPaused(setUp, paused bool, checks []ComponentCheck) ReadinessState {
	return deriveReadinessState(setUp, paused, checks)
}

// PausableComponents are the components a pause deliberately switches off.
var PausableComponents = map[ReadinessComponent]bool{
	ComponentWatcher:   true,
	ComponentScheduler: true,
}

func deriveReadinessState(setUp, paused bool, checks []ComponentCheck) ReadinessState {
	if !setUp {
		return ReadinessSetupRequired
	}
	byComponent := make(map[ReadinessComponent]ComponentStatus, len(checks))
	for _, check := range checks {
		byComponent[check.Component] = check.Status
	}
	for _, component := range RequiredComponents {
		if paused && PausableComponents[component] {
			// Off because it was switched off. Still surfaced per-component,
			// where the message says "Paused by you".
			continue
		}
		if byComponent[component] != ComponentOK {
			return ReadinessNeedsAttention
		}
	}
	return ReadinessReady
}
