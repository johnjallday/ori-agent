package downloadsjanitor

import (
	"fmt"
	"strings"
	"time"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// The settings surface is where the user changes their mind, and every change
// here is reversible without recreating the workspace.
//
// Two of these operations are ordered rather than atomic, and the order is the
// safety property:
//
//   - Relink pauses the old automation and invalidates work bound to the old
//     folder *before* pointing anywhere new. A watcher briefly running against
//     the old folder while approvals for it are still valid is how a file gets
//     moved out of a folder the user just disconnected.
//   - Revoke stops the watcher and schedule *before* removing the access they
//     depend on, so nothing is left firing against a binding that is gone
//     (FR-117).

// SettingsUpdate carries the fields the settings surface can change. Each is a
// pointer so an unspecified field is left alone rather than reset.
type SettingsUpdate struct {
	DailyScanLocalTime *string
	Timezone           *string
	ContentMode        *ContentMode
	ContentProvider    *string
	Paused             *bool
}

// UpdateSettings applies a settings change.
//
// Changing the content mode or provider clears any prior consent: consent is
// given for a specific provider, and inheriting it across a change would mean
// the user consented to something they never saw (FR-55).
func (s *Service) UpdateSettings(workspaceID string, update SettingsUpdate) (Status, error) {
	_, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if update.DailyScanLocalTime != nil {
			normalized, err := workspace.NormalizeLocalTimeOfDay(*update.DailyScanLocalTime)
			if err != nil {
				return setupErr(CodeInvalidPath, "The daily scan time must be a 24-hour time such as 09:00.", RepairRetry, err)
			}
			settings.DailyScanLocalTime = normalized
		}
		if update.Timezone != nil {
			timezone := strings.TrimSpace(*update.Timezone)
			if timezone != "" {
				if _, err := time.LoadLocation(timezone); err != nil {
					return setupErr(CodeInvalidPath, "That timezone is not recognized.", RepairRetry, err)
				}
			}
			settings.Timezone = timezone
		}
		if update.Paused != nil {
			settings.Paused = *update.Paused
		}
		if update.ContentProvider != nil {
			provider := strings.TrimSpace(*update.ContentProvider)
			if !strings.EqualFold(provider, settings.ContentProvider) {
				// A different provider is a different disclosure.
				settings.ContentConsentProvider = ""
				settings.ContentConsentAt = time.Time{}
			}
			settings.ContentProvider = provider
		}
		if update.ContentMode != nil {
			mode := NormalizeContentMode(*update.ContentMode)
			if string(mode) != strings.ToLower(strings.TrimSpace(string(*update.ContentMode))) {
				return setupErr(CodeInvalidPath, "That content setting is not recognized.", RepairRetry, nil)
			}
			if mode != settings.ContentMode {
				// Turning inspection off, or switching where it happens, drops
				// consent. Turning it back on asks again — which is the point.
				settings.ContentConsentProvider = ""
				settings.ContentConsentAt = time.Time{}
			}
			settings.ContentMode = mode
			if !mode.ReadsFileContent() {
				settings.ContentProvider = ""
			}
		}
		return nil
	})
	if err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

// GrantContentConsent records the user's confirmation that file content may be
// sent to a specific named provider.
//
// The provider name must match what is configured: consent given for one
// provider does not transfer to another, and consent recorded without a
// provider would be consent to nothing in particular (FR-55).
func (s *Service) GrantContentConsent(workspaceID, provider string) (Status, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return Status{}, setupErr(CodeInvalidPath, "Ori needs to know which provider you are confirming.", RepairRetry, nil)
	}
	_, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if !settings.ContentMode.ReadsFileContent() {
			return setupErr(CodeInvalidPath, "Turn on content inspection before confirming a provider.", RepairRetry, nil)
		}
		if !strings.EqualFold(strings.TrimSpace(settings.ContentProvider), provider) {
			return setupErr(CodeInvalidPath, "That is not the provider this workspace is configured to use.", RepairRetry, nil)
		}
		settings.ContentConsentProvider = provider
		settings.ContentConsentAt = s.clock()
		return nil
	})
	if err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

// PrivacyState is the plain-language answer to "what can Ori see?", for the
// setup card, the review surface, and settings (FR-115).
type PrivacyState struct {
	Mode ContentMode `json:"mode"`
	// Headline is a single sentence stating what Ori reads.
	Headline string `json:"headline"`
	// Detail says where any inspected content goes.
	Detail string `json:"detail"`
	// Provider names the model provider, when one is involved.
	Provider string `json:"provider,omitempty"`
	// LeavesDevice is the fact users most need: does anything leave this
	// machine.
	LeavesDevice bool `json:"leaves_device"`
	// ConsentRequired reports that content inspection is configured but waiting
	// on the user's confirmation, so nothing is being read yet.
	ConsentRequired bool `json:"consent_required,omitempty"`
}

// Privacy describes the workspace's current content-inspection state.
func (s *Service) Privacy(settings JanitorSettings) PrivacyState {
	state := PrivacyState{
		Mode:            settings.ContentMode,
		Provider:        settings.ContentProvider,
		LeavesDevice:    settings.ContentMode.LeavesDevice(),
		ConsentRequired: settings.RequiresContentConsent(),
	}
	switch settings.ContentMode {
	case ContentModeLocalModel:
		state.Headline = "Ori reads file names, types, sizes, and dates, and may read a short extract from plain text documents."
		state.Detail = "Anything read stays on this device."
		if settings.ContentProvider != "" {
			state.Detail = "Anything read stays on this device, handled by " + settings.ContentProvider + "."
		}
	case ContentModeCloudModel:
		provider := settings.ContentProvider
		if provider == "" {
			provider = "the configured provider"
		}
		state.Headline = "Ori reads file names, types, sizes, and dates, and may read a short extract from plain text documents."
		state.Detail = "Extracts from those documents are sent to " + provider + ", which is outside this device."
		if state.ConsentRequired {
			state.Detail += " Nothing has been sent yet — Ori will ask you to confirm first."
		}
	default:
		state.Headline = "Ori reads file names, types, sizes, and dates only."
		state.Detail = "No file contents are opened or read."
	}
	return state
}

// RelinkRequest points a workspace at a different folder.
type RelinkRequest struct {
	WorkspaceID string
	Path        string
}

// Relink moves the workspace to a new folder, in an order chosen so nothing
// can act on the old one after the user has moved on.
//
// History is deliberately preserved: it records what happened to files in the
// old folder, and that record stays true regardless of where the workspace
// points now (FR-25).
func (s *Service) Relink(automation WatcherLifecycle, req RelinkRequest) (Status, error) {
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	current, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return Status{}, err
	}

	// 1. Stop the unattended work first. A watcher still firing on the old
	//    folder during a relink would scan a folder the user is leaving.
	if automation != nil {
		if err := automation.PauseWatcher(workspaceID); err != nil {
			return Status{}, setupErr(CodeBindingFailed, "Ori could not pause folder watching, so it did not change the folder.", RepairRetry, err)
		}
	}

	// 2. Invalidate everything bound to the old folder: pending candidates
	//    describe files there, and approvals authorize actions there. Neither
	//    means anything once the folder changes, and an approval that survived
	//    would authorize an action against the wrong folder entirely.
	if _, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		if strings.TrimSpace(current.RootPath) == "" {
			return nil
		}
		for i := range state.Candidates {
			if !state.Candidates[i].State.Terminal() {
				state.Candidates[i].State = CandidateStale
				state.Candidates[i].StateReason = "The folder changed"
			}
		}
		for i := range state.Batches {
			state.Batches[i] = SummarizeBatch(state.Batches[i], state.CandidatesFor(state.Batches[i].ID))
		}
		state.Approvals = nil
		state.Observations = nil
		return nil
	}); err != nil {
		return Status{}, err
	}

	// 3. Point at the new folder, which revalidates it and rebuilds the
	//    directory reference and read-only binding.
	status, err := s.ConfirmSetup(SetupRequest{WorkspaceID: workspaceID, Path: req.Path})
	if err != nil {
		return Status{}, err
	}

	// 4. Only now resume, and only if the new folder is actually usable.
	if automation != nil {
		if err := automation.EnsureWatcher(workspaceID); err != nil {
			return status, nil
		}
	}
	return s.Status(workspaceID)
}

// WatcherLifecycle is the automation control the settings operations need.
type WatcherLifecycle interface {
	EnsureWatcher(workspaceID string) error
	PauseWatcher(workspaceID string) error
	RemoveWatcher(workspaceID string) error
}

// RevokeAccess disconnects the workspace from its folder.
//
// The order matters: watching and scheduling stop before the access they run
// against is removed. History survives — it is the record of what Ori did while
// it had access, and revoking access does not make that record untrue (FR-116,
// FR-117).
func (s *Service) RevokeAccess(automation WatcherLifecycle, workspaceID string) (Status, error) {
	// 1. Stop the automation.
	if automation != nil {
		if err := automation.RemoveWatcher(workspaceID); err != nil {
			return Status{}, setupErr(CodeBindingFailed, "Ori could not stop folder watching, so it did not remove access.", RepairRetry, err)
		}
	}

	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return Status{}, err
	}

	// 2. Remove the binding and the directory reference.
	if s.workspaces != nil && settings.DirectoryReferenceID != "" {
		if err := s.workspaces.Update(workspaceID, func(ws *workspace.Workspace) error {
			references := ws.DirectoryReferences[:0:0]
			for _, ref := range ws.DirectoryReferences {
				if ref.ID != settings.DirectoryReferenceID {
					references = append(references, ref)
				}
			}
			ws.DirectoryReferences = references

			for _, binding := range ws.MCPBindings {
				if strings.EqualFold(strings.TrimSpace(binding.Alias), JanitorBindingAlias) {
					if err := ws.DeleteMCPBinding(binding.ID); err != nil {
						return err
					}
					break
				}
			}
			return nil
		}); err != nil {
			return Status{}, setupErr(CodeBindingFailed, "Ori could not remove this workspace's folder access.", RepairRetry, err)
		}
	}

	// 3. Clear the configuration, and with it any content consent. Pending
	//    candidates and approvals go too: they describe a folder Ori can no
	//    longer reach.
	if _, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		fresh := NewSettings(workspaceID)
		fresh.DailyScanLocalTime = settings.DailyScanLocalTime
		fresh.Timezone = settings.Timezone
		*settings = fresh
		return nil
	}); err != nil {
		return Status{}, err
	}
	if _, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		state.Approvals = nil
		state.Observations = nil
		for i := range state.Candidates {
			if !state.Candidates[i].State.Terminal() {
				state.Candidates[i].State = CandidateStale
				state.Candidates[i].StateReason = "Folder access was removed"
			}
		}
		for i := range state.Batches {
			state.Batches[i] = SummarizeBatch(state.Batches[i], state.CandidatesFor(state.Batches[i].ID))
		}
		return nil
	}); err != nil {
		return Status{}, err
	}

	return s.Status(workspaceID)
}

// SkippedItem is one remembered skip, for the settings list.
type SkippedItem struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	SkippedAt time.Time `json:"skipped_at"`
}

// ListSkipped returns the workspace's remembered skip decisions so the user can
// see what has been dismissed and undo it.
func (s *Service) ListSkipped(workspaceID string) ([]SkippedItem, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]SkippedItem, 0, len(state.Skipped))
	for _, skip := range state.Skipped {
		out = append(out, SkippedItem{
			Key:       skip.Key,
			Name:      DisplayFileName(skip.Name),
			SkippedAt: skip.SkippedAt,
		})
	}
	return out, nil
}

// ErrSettingsUnavailable reports a settings operation on a workspace that has
// no Janitor configuration.
var ErrSettingsUnavailable = fmt.Errorf("downloads janitor is not configured for this workspace")
