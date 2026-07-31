package downloadsjanitor

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// CapabilityRuntime adapts this service to the Workspace Capability registry,
// so an installed `file-janitor` record can report health derived from the real
// Janitor service rather than from anything persisted on the workspace (FR-6).
//
// It is deliberately read-only. Installing a capability record must not request
// folder access or start automation (FR-20), so this type implements only
// Runtime — not Installer — and the registry's install hook is a no-op for File
// Janitor by omission rather than by an empty method that could later grow one.
//
// This adapter lives in the Janitor package (not the registry package) so the
// dependency points the right way: the capability registry knows nothing about
// the Janitor, and the Janitor names the registry's small interface.
type CapabilityRuntime struct {
	service *Service
}

// NewCapabilityRuntime wraps a Janitor service as a capability runtime. A nil
// service yields a runtime that reports the capability as unavailable rather
// than panicking, so a wiring failure degrades one capability instead of the
// workspace (FR-145).
func NewCapabilityRuntime(service *Service) *CapabilityRuntime {
	return &CapabilityRuntime{service: service}
}

// HasConfiguredJanitorState implements workspacecapability.LegacyStateProbe: it
// reports whether this workspace completed Downloads Janitor setup before
// capabilities existed, which is one of the three authoritative migration
// signals (FR-126).
//
// It is deliberately strict. IsSetUp requires an approved root AND a directory
// reference AND a completed-setup timestamp, so a state file that exists but
// records no grant — a workspace that opened the panel and stopped — is not
// treated as evidence the capability was in use.
func (r *CapabilityRuntime) HasConfiguredJanitorState(workspaceID string) bool {
	if r == nil || r.service == nil || r.service.store == nil {
		return false
	}
	settings, err := r.service.store.LoadSettings(workspaceID)
	if err != nil {
		// Unreadable state is not evidence either way. Reporting true here would
		// migrate a workspace on the strength of an I/O error.
		return false
	}
	return settings.IsSetUp()
}

// CapabilityStatus derives the File Janitor station/card status for a workspace.
//
// The state is chosen by the required display priority (PRD design 8.4) through
// the registry's shared helper, so this and the Map station can never disagree
// about which of several simultaneous conditions wins.
func (r *CapabilityRuntime) CapabilityStatus(workspaceID string) (workspacecapability.Status, error) {
	if r == nil || r.service == nil {
		return workspacecapability.Status{
			State:  workspacecapability.StatusUnavailable,
			Detail: "File Janitor is not available.",
		}, nil
	}

	settings, err := r.service.store.LoadSettings(workspaceID)
	if err != nil {
		return workspacecapability.Status{}, err
	}

	if !settings.IsSetUp() {
		return workspacecapability.Status{
			State:      workspacecapability.StatusSetupNeeded,
			Detail:     "Choose a folder to manage.",
			Configured: false,
		}, nil
	}

	readiness := r.service.evaluateReadiness(settings)
	reviewCount := r.pendingReviewCount(workspaceID)

	// Collect every condition that currently applies; the helper picks the one
	// the user most needs to act on.
	applicable := []workspacecapability.StatusState{workspacecapability.StatusWatching}
	if readiness.State == ReadinessNeedsAttention {
		applicable = append(applicable, workspacecapability.StatusNeedsAttention)
	}
	if reviewCount > 0 {
		applicable = append(applicable, workspacecapability.StatusReviewReady)
	}
	if settings.Paused {
		applicable = append(applicable, workspacecapability.StatusPaused)
	}

	state := workspacecapability.HighestPriorityStatus(applicable...)
	return workspacecapability.Status{
		State:             state,
		Detail:            capabilityStatusDetail(state, reviewCount),
		ReviewCount:       reviewCount,
		FolderDisplayName: folderDisplayName(settings.RootPath),
		Configured:        true,
	}, nil
}

// pendingReviewCount counts candidates still awaiting a decision.
//
// A scan-state read failure is not escalated: the folder access and automation
// checks in readiness are what report a broken install, and refusing to render
// a station because a count could not be loaded would be a worse outcome than
// rendering it without one.
func (r *CapabilityRuntime) pendingReviewCount(workspaceID string) int {
	state, err := r.service.store.LoadScanState(workspaceID)
	if err != nil {
		logger.Warn("File Janitor could not read scan state for its status", logger.Fields{
			"workspace_id": workspaceID,
			"error":        err.Error(),
		})
		return 0
	}
	count := 0
	for _, candidate := range state.Candidates {
		if candidate.State == CandidatePending {
			count++
		}
	}
	return count
}

// capabilityStatusDetail is the short text shown beside the station and on the
// compact card. Status must always be readable as words, not only as a colour
// or badge (FR-96).
func capabilityStatusDetail(state workspacecapability.StatusState, reviewCount int) string {
	switch state {
	case workspacecapability.StatusNeedsAttention:
		return "Needs attention"
	case workspacecapability.StatusSetupNeeded:
		return "Choose a folder to manage."
	case workspacecapability.StatusReviewReady:
		return strconv.Itoa(reviewCount) + " ready for review"
	case workspacecapability.StatusPaused:
		return "Paused"
	case workspacecapability.StatusWatching:
		return "Watching"
	default:
		return ""
	}
}

// folderDisplayName reduces the managed root to a safe short name for the
// station and console header. The full authorized path stays in Settings; a
// station is not the place to render an entire home-directory path (FR-95).
func folderDisplayName(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(trimmed))
}

// Compile-time checks that this adapter satisfies the registry's contracts.
var (
	_ workspacecapability.Runtime          = (*CapabilityRuntime)(nil)
	_ workspacecapability.LegacyStateProbe = (*CapabilityRuntime)(nil)
)
