package filejanitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const SetupDisclosureVersion = 1

// LinkedSetupCandidate is an owner-only view. Path is intentional here, never
// included in generic wizard/model/coordinator payloads or log messages.
type LinkedSetupCandidate struct {
	ReferenceID string `json:"reference_id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
}

type LinkedSetupReview struct {
	State             string                 `json:"state"`
	WorkspaceID       string                 `json:"workspace_id"`
	Candidates        []LinkedSetupCandidate `json:"candidates"`
	SelectedReference string                 `json:"reference_id,omitempty"`
	Revision          string                 `json:"review_revision,omitempty"`
	DisclosureVersion int                    `json:"disclosure_version"`
	Monitoring        bool                   `json:"monitoring"`
	Terms             SetupReviewTerms       `json:"terms"`
	CurrentMode       string                 `json:"current_mode,omitempty"`
	OperationID       string                 `json:"operation_id,omitempty"`
	Result            *ScanOperationReceipt  `json:"result,omitempty"`
	// Private binding facts are computed again at commit, never supplied by the
	// client. A reference remains a candidate until that explicit confirmation.
	rootPath           string
	rootIdentity       string
	bindingRevision    string
	settingsDigest     string
	capabilityRevision string
}

type SetupReviewTerms struct {
	PrivacyMode        ContentMode `json:"privacy_mode"`
	FilingRootName     string      `json:"filing_root_name"`
	WatchEvents        []string    `json:"watch_events"`
	DebounceSeconds    int         `json:"debounce_seconds"`
	SettlingSeconds    int         `json:"settling_seconds"`
	DailyScanLocalTime string      `json:"daily_scan_local_time"`
	Timezone           string      `json:"timezone"`
	ExcludesFiled      bool        `json:"excludes_filed"`
}

// ReviewLinkedSetup only reads canonical state. In particular it must not call
// Status (whose persistence health probe creates a state directory), ConfirmSetup
// or an automation method. No raw client path can enter this boundary.
func (s *Service) ReviewLinkedSetup(workspaceID, ownerUserID, referenceID string, monitoring bool) (LinkedSetupReview, error) {
	review, err := s.reviewLinkedSetup(workspaceID, ownerUserID, referenceID, monitoring, false)
	if err != nil {
		return review, err
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return LinkedSetupReview{}, err
	}
	preparation := settings.SetupPreparation
	if preparation == nil {
		return review, nil
	}
	if preparation.OwnerUserID != ownerUserID {
		return LinkedSetupReview{State: "repair_required", WorkspaceID: workspaceID}, nil
	}
	current, _, err := s.validateLinkedPreparation(workspaceID, *preparation)
	if err != nil {
		return LinkedSetupReview{State: "repair_required", WorkspaceID: workspaceID}, nil
	}
	current.State, current.Revision, current.OperationID = "preparation_incomplete", preparation.ReviewRevision, preparation.ID
	receipt, found, err := s.ScanOperationReceipt(workspaceID, preparation.ID+":scan")
	if err != nil {
		return LinkedSetupReview{}, err
	}
	if found && receipt.RootID == settings.RootID && receipt.PrivacyMode == ContentModeMetadataOnly {
		current.State, current.Result = "prepared", &receipt
	}
	return current, nil
}

func (s *Service) reviewLinkedSetup(workspaceID, ownerUserID, referenceID string, monitoring, continuing bool) (LinkedSetupReview, error) {
	var review LinkedSetupReview
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil || !linkedSetupOwned(ws, ownerUserID) ||
		(ws.Status != "" && ws.Status != workspace.StatusActive) ||
		strings.EqualFold(ws.Kind, "group") || !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return review, setupErr(CodeWorkspaceMissing, "This workspace is unavailable for File Janitor setup.", "", nil)
	}
	// Folder snapshots carry capability provenance, but lifecycle may already
	// have changed in the primary store before a disk mirror is updated.
	primary, err := s.workspaces.Get(workspaceID)
	if err != nil || primary == nil || !linkedSetupOwned(primary, ownerUserID) ||
		(primary.Status != "" && primary.Status != workspace.StatusActive) {
		return review, setupErr(CodeWorkspaceMissing, "This workspace is unavailable for File Janitor setup.", "", nil)
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return review, err
	}
	automation := s.reviewAutomationSettings(settings)
	review = LinkedSetupReview{
		WorkspaceID: workspaceID, Candidates: []LinkedSetupCandidate{},
		DisclosureVersion: SetupDisclosureVersion, Monitoring: monitoring,
		Terms: setupReviewTerms(automation),
	}
	if settings.IsSetUp() && !continuing {
		review.State = "configured"
		review.CurrentMode = "pending"
		switch {
		case settings.ManualScansOnly():
			review.CurrentMode = "manual"
		case !settings.AutomationApprovedAt.IsZero() && settings.Paused:
			review.CurrentMode = "paused"
		case !settings.Paused:
			review.CurrentMode = "monitoring"
		}
		return review, nil
	}
	if !settings.AccessRevokedAt.IsZero() {
		review.State = "revoked"
		return review, nil
	}
	// A pre-tombstone revoke leaves an empty settings file, as can corruption.
	// Neither is evidence of never-configured state. Keep the explicit picker
	// repair instead of turning a surviving adopted reference into new consent.
	settingsPath, err := s.store.settingsPath(workspaceID)
	if err != nil {
		return review, err
	}
	if _, err := os.Stat(settingsPath); err == nil && !continuing {
		review.State = "repair_required"
		return review, nil
	} else if err != nil && !os.IsNotExist(err) {
		return review, err
	}
	folder, err := s.store.resolver.GetFolderPath(workspaceID)
	if err != nil {
		return review, err
	}
	folder, err = filepath.EvalSymlinks(folder)
	if err != nil {
		return review, err
	}
	for _, ref := range ws.DirectoryReferences {
		if ref.Purpose != "" || !filepath.IsAbs(ref.Path) || ref.ID == "" {
			continue
		}
		candidatePath := filepath.Clean(ref.Path)
		// Check both lexical and resolved containment. Unavailable outside links
		// stay visible as repairable candidates, not quietly ignored in favour
		// of a different same-named folder.
		if isInsideSetupFolder(folder, candidatePath) {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(candidatePath); err == nil && isInsideSetupFolder(folder, resolved) {
			continue
		}
		review.Candidates = append(review.Candidates, LinkedSetupCandidate{
			ReferenceID: ref.ID, Name: ref.Name, Path: candidatePath,
		})
	}
	sort.Slice(review.Candidates, func(i, j int) bool { return review.Candidates[i].ReferenceID < review.Candidates[j].ReferenceID })
	referenceID = strings.TrimSpace(referenceID)
	if referenceID == "" {
		switch len(review.Candidates) {
		case 0:
			review.State = "no_candidate"
			return review, nil
		case 1:
			referenceID = review.Candidates[0].ReferenceID
		default:
			review.State = "choose_folder"
			return review, nil
		}
	}
	var selected *LinkedSetupCandidate
	for i := range review.Candidates {
		if review.Candidates[i].ReferenceID == referenceID {
			selected = &review.Candidates[i]
			break
		}
	}
	if selected == nil {
		return LinkedSetupReview{}, setupErr(CodeFolderChanged, "That attached folder is no longer available. Review the current folder choices.", RepairChooseFolder, nil)
	}
	review.SelectedReference = referenceID
	root, err := resolveSetupRoot(selected.Path)
	if err != nil {
		review.State = "folder_unavailable"
		return review, nil
	}
	info, err := os.Stat(root) // #nosec G304 -- canonical root resolved from an owned directory reference, read-only identity check
	if err != nil || platformFileID(info) == "" {
		review.State = "folder_unavailable"
		return review, nil
	}
	if err := s.ensureRootAvailable(workspaceID, root); err != nil {
		var setupError *SetupError
		if errors.As(err, &setupError) && setupError.Code == CodeFolderConflict {
			review.State = "folder_conflict"
			return review, nil
		}
		return review, err
	}
	review.State, review.rootPath, review.rootIdentity = "review", root, platformFileID(info)
	install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	// Resource associations are the expected consequence of the reviewed grant,
	// not a new install. Identity/version/removal remain bound.
	install.OwnedResources = nil
	review.capabilityRevision, err = setupDigest(install)
	if err != nil {
		return LinkedSetupReview{}, err
	}
	// Bind the actual candidate set as well as the choice: adding/removing an
	// ambiguous link invalidates an earlier one-candidate confirmation.
	binding := struct {
		Owner, WorkspaceOwner, Root, Identity string
		Review                                LinkedSetupReview
		Install                               workspace.InstalledCapability
	}{strings.TrimSpace(ownerUserID), ws.OwnerUserID, root, review.rootIdentity, review, install}
	review.bindingRevision, err = setupDigest(binding)
	if err != nil {
		return LinkedSetupReview{}, err
	}
	review.settingsDigest, err = setupDigest(settings)
	if err != nil {
		return LinkedSetupReview{}, err
	}
	review.Revision, err = setupDigest([]string{review.bindingRevision, review.settingsDigest})
	if err != nil {
		return LinkedSetupReview{}, err
	}
	return review, nil
}

func setupDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func linkedSetupOwned(ws *workspace.Workspace, owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false
	}
	workspaceOwner := strings.TrimSpace(ws.OwnerUserID)
	if workspaceOwner == "" {
		workspaceOwner = userprofile.LocalUserID
	}
	return workspaceOwner == owner
}

func isInsideSetupFolder(folder, candidate string) bool {
	rel, err := filepath.Rel(folder, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
