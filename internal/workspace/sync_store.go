package workspace

import (
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// TrashSharedDataKey is the SharedData key under which trash metadata
// ({original_path, trashed_path, deleted_at}) is stored while a workspace is
// trashed, so its folder can be moved back on restore. The generic workspace
// delete in sessionhttp writes the same key.
const TrashSharedDataKey = "_trash"

// ErrTrashUnsupported reports that a store cannot move a workspace to the
// system Trash. A caller that needs the workspace gone falls back to Delete.
var ErrTrashUnsupported = errors.New("workspace trash is not supported by this store")

// WorkspaceTrasher is implemented by stores that can soft-delete a workspace:
// its folder moves to the system Trash and its record stays, marked trashed,
// so a later restore is high fidelity. Stores that only know how to Delete do
// not implement it, and callers that need a removal either way fall back.
type WorkspaceTrasher interface {
	// TrashSupported reports whether Trash can succeed here at all.
	TrashSupported() bool
	// Trash soft-deletes the workspace, returning ErrTrashUnsupported when the
	// platform or store layout cannot.
	Trash(id string) error
}

// SyncStore wraps a primary Store and writes through to a FileStore
// so that workspace.json on disk stays in sync with the primary store.
// Reads are served from the primary; the FileStore is a sync target only.
type SyncStore struct {
	primary  Store
	fileSync *FileStore
}

// NewSyncStore creates a store that writes to both the primary store
// and the file-based store. The primary store is authoritative for reads;
// the file store provides portable workspace folders on disk.
func NewSyncStore(primary Store, fileSync *FileStore) *SyncStore {
	if fileSync != nil {
		fileSync.mirrored.Store(true)
	}
	return &SyncStore{primary: primary, fileSync: fileSync}
}

// MirrorWorkspaceProvider is implemented only by a store with a distinct
// folder-write mirror. A normal FileStore's GetFolderWorkspace reads itself
// and must not be called recursively while its own Update holds the lock.
// Decorators forward this capability without confusing it with folder reads.
type MirrorWorkspaceProvider interface {
	GetMirrorWorkspace(string) (folder *Workspace, mirrored bool, err error)
}

func (s *SyncStore) GetMirrorWorkspace(id string) (*Workspace, bool, error) {
	if s == nil || s.fileSync == nil {
		return nil, false, nil
	}
	folder, err := s.GetFolderWorkspace(id)
	return folder, true, err
}

// FileStore returns the underlying FileStore used for disk sync.
func (s *SyncStore) FileStore() *FileStore {
	return s.fileSync
}

// TrashSupported reports whether Trash can move a workspace folder to the
// system Trash: there has to be a folder store, and the platform has to have a
// trash to move it to.
func (s *SyncStore) TrashSupported() bool {
	return s != nil && s.primary != nil && s.fileSync != nil && platform.TrashSupported()
}

// Trash soft-deletes a workspace the same way the workspace API's delete does:
// the folder moves to the system Trash and the primary record is kept, marked
// trashed, with the paths a restore needs stashed under TrashSharedDataKey.
// The same protection rules as Delete apply, so a live Assistant Home or a
// required group cannot be trashed by accident.
//
// It returns ErrTrashUnsupported when there is no folder store or the platform
// has no trash, so callers can fall back to a permanent Delete.
func (s *SyncStore) Trash(id string) error {
	if !s.TrashSupported() {
		return ErrTrashUnsupported
	}
	if protected, checkErr := storeContainsProtectedAssistantProgram(s.primary, id); checkErr != nil {
		return checkErr
	} else if protected {
		return ErrAssistantProgramProtected
	}
	if protected, checkErr := storeContainsRequiredGroupRequirement(s.fileSync, id); checkErr != nil {
		return checkErr
	} else if protected {
		return ErrGroupRequirementProtected
	}
	ws, err := s.primary.Get(id)
	if err != nil {
		return err
	}
	if ws == nil {
		return fmt.Errorf("workspace %s not found", id)
	}
	if ws.Status == StatusTrashed {
		return fmt.Errorf("workspace %s is already in the trash", id)
	}

	originalPath, trashedPath, err := s.fileSync.Trash(id)
	if err != nil {
		return err
	}
	ws.SetSharedData(TrashSharedDataKey, map[string]any{
		"original_path": originalPath,
		"trashed_path":  trashedPath,
		"deleted_at":    time.Now().UTC().Format(time.RFC3339),
	})
	ws.Status = StatusTrashed
	// Save skips the folder mirror for a trashed workspace, so this only
	// touches the primary record; the folder is already in the Trash.
	if err := s.Save(ws); err != nil {
		// Roll the folder back out of the Trash so the workspace isn't stranded.
		if _, restoreErr := s.fileSync.RestoreFromTrash(originalPath, trashedPath); restoreErr != nil {
			return fmt.Errorf("failed to record trashed workspace: %w (folder rollback failed: %v)", err, restoreErr)
		}
		return err
	}
	return nil
}

// GetFolderPath exposes the canonical disk folder when SyncStore is used by a
// runtime handler that needs workspace-root containment rather than files/.
func (s *SyncStore) GetFolderPath(workspaceID string) (string, error) {
	if s.fileSync == nil {
		return "", fmt.Errorf("workspace folder storage is unavailable")
	}
	return s.fileSync.GetFolderPath(workspaceID)
}

// GetFolderWorkspace reads the canonical workspace.json record rather than
// the SQLite-primary mirror returned by Get.
func (s *SyncStore) GetFolderWorkspace(workspaceID string) (*Workspace, error) {
	if s.fileSync == nil {
		return nil, fmt.Errorf("workspace folder storage is unavailable")
	}
	return s.fileSync.Get(workspaceID)
}

// MoveWorkspaceFolder delegates to the canonical folder move and then aligns
// the primary parent projection. Typed domain links, not physical nesting,
// remain authoritative for membership.
func (s *SyncStore) MoveWorkspaceFolder(workspaceID, parentID string) ([]MovedWorkspace, error) {
	if s == nil || s.fileSync == nil || s.primary == nil {
		return nil, fmt.Errorf("workspace folder storage is unavailable")
	}
	current, currentErr := s.primary.Get(workspaceID)
	if currentErr != nil {
		return nil, currentErr
	}
	healingExactLink := current.AssistantProjectLink != nil && current.AssistantProjectLink.StationWorkspaceID == parentID
	if protected, checkErr := storeContainsProtectedAssistantProgram(s.primary, workspaceID); checkErr != nil {
		return nil, checkErr
	} else if protected && !healingExactLink {
		return nil, ErrAssistantProgramProtected
	}
	moved, err := s.fileSync.MoveWorkspaceFolder(workspaceID, parentID)
	if err != nil {
		return nil, err
	}
	type explicitParentSetter interface {
		SetWorkspaceParent(workspaceID, parentID string) error
	}
	if setter, ok := s.primary.(explicitParentSetter); ok {
		if err := setter.SetWorkspaceParent(workspaceID, parentID); err != nil {
			return moved, err
		}
	} else if err := s.primary.Update(workspaceID, func(current *Workspace) error {
		current.ParentID = parentID
		return nil
	}); err != nil {
		return moved, err
	}
	return moved, nil
}

// ErrWorkspaceMirrorsDiverged reports that a protected workspace's folder and
// primary records no longer carry the same version: an earlier write reached
// one mirror and not the other. New writes fail closed until the split is
// reconciled by an explicit, reviewed action; neither mirror is chosen here.
var ErrWorkspaceMirrorsDiverged = errors.New("workspace mirrors diverged; reconcile before writing")

// Save persists the workspace. FileStore still runs first so its monotonic
// Version bump reaches the primary record, but disk failures are no longer
// best-effort: slug conflicts and other folder errors abort the operation. A
// primary failure after the disk write restores the prior folder record (or
// removes a newly created folder), preventing split registration state.
//
// The folder fence is held across both mirror writes. For an Assistant Home or
// split project child the write is conditional on the version the caller read
// in both mirrors (a stale base is refused before anything is written and the
// primary update is a single conditional statement), and a folder rollback
// restores the exact previous bytes rather than a further bumped version.
func (s *SyncStore) Save(ws *Workspace) error {
	if s.fileSync != nil && ws != nil && ws.Status != StatusTrashed && ws.Status != StatusMissing {
		fence, err := s.fileSync.openFence(ws.ID)
		if err != nil {
			return fmt.Errorf("failed to fence workspace folder: %w", err)
		}
		defer fence.close()

		var primaryBefore *Workspace
		if existing, err := s.primary.Get(ws.ID); err == nil && existing != nil {
			primaryBefore, _ = cloneWorkspaceForRebind(existing)
		}
		diskBefore := fence.Before()

		if resolver, ok := s.primary.(SlugResolver); ok && IsCanonicalWorkspaceSlug(ws.FolderSlug) {
			if owner, err := resolver.ResolveSlug(ws.FolderSlug); err == nil && owner != nil && owner.ID != ws.ID {
				return &FolderSlugConflictError{Slug: ws.FolderSlug}
			}
		}

		base := ws.Version
		protected := WorkspaceFenceProtected(ws) || WorkspaceFenceProtected(diskBefore) || WorkspaceFenceProtected(primaryBefore)

		// An interrupted earlier protected write is classified from its durable
		// journal and both mirrors before anything else happens.
		var journal *fenceJournal
		if protected && diskBefore != nil {
			pending, err := fence.pendingJournal()
			if err != nil {
				return err
			}
			if pending != nil {
				switch outcome := classifyFenceRecovery(pending, fence.beforeRaw, primaryBefore); outcome {
				case FenceApplied, FenceNotApplied:
					if err := fence.clearJournal(); err != nil {
						return err
					}
				case FenceReconcileRequired:
					return fmt.Errorf("%w: workspace %s interrupted write %s (version %d to %d) reached one mirror only",
						ErrWorkspaceMirrorsDiverged, ws.ID, pending.OperationID, pending.BaseVersion, pending.TargetVersion)
				default:
					return fmt.Errorf("%w: workspace %s interrupted write %s", ErrWorkspaceFenceUnknown, ws.ID, pending.OperationID)
				}
			}
			journal = fence.beginJournal(base)
			fence.onWrite = func(data []byte) error {
				journal.FolderAfter = fenceDigest(data)
				return fence.writeJournal(journal)
			}
		}

		if err := fence.refuseStale(ws); err != nil {
			return err
		}
		if protected && primaryBefore != nil {
			// The primary's version is the sequence. Refusing here, under the
			// folder lock and before any mirror changes, keeps a stale writer from
			// ever placing its old record on disk; the conditional primary update
			// below remains the authoritative check.
			if primaryBefore.Version != base {
				return fmt.Errorf("%w: workspace %s is at version %d in the primary store, this write was based on version %d",
					ErrStaleWorkspaceVersion, ws.ID, primaryBefore.Version, base)
			}
			// Both mirrors must already agree on the protected envelope; a Home
			// or link that reached one mirror only is a split to reconcile, not
			// something a new write may silently resolve. Only a primary with
			// conditional saves (SQLite) hands back an independent before image;
			// a pointer-sharing in-memory store may already reflect the update
			// callback's mutation and cannot be compared.
			if _, versioned := s.primary.(VersionedSaver); versioned && diskBefore != nil && !protectedEnvelopesAgree(primaryBefore, diskBefore) {
				return fmt.Errorf("%w: workspace %s carries a different Home state or project link in its folder than in the primary store",
					ErrWorkspaceMirrorsDiverged, ws.ID)
			}
		}

		// ProjectPath, Designation, TemplateProvenance, SetupWizardProgress, and
		// RuntimeState are canonical workspace.json fields not represented by the SQLite
		// workspace table. A workspace fetched from SQLite (the primary store's
		// Get) therefore always carries these as zero values, and must not erase
		// values written directly to the folder store by an unrelated
		// Update/Save cycle (e.g. the template-setup first-open auto-start saving
		// a task status change would otherwise silently wipe out the template
		// provenance persisted moments earlier at workspace-creation time — or,
		// for setup progress, hand the user a wizard that has forgotten the
		// folder they just approved). There is no generic "empty means clear"
		// operation through SyncStore; intentional removals must update the
		// canonical FileStore explicitly.
		//
		// InstalledCapabilities joins them for a different reason: it IS mirrored
		// into SQLite, but several read paths still yield a partial workspace
		// (a legacy row written before the column existed, a record built by a
		// caller that never loaded it). Restoring it from disk is what makes
		// FR-144 hold — "a stale workspace snapshot must not silently erase a
		// capability install".
		//
		// Unlike the fields above, though, this one has a legitimate empty
		// value: uninstall. capabilitiesExplicit distinguishes the two, so an
		// intentional removal writes through while an incidental absence is
		// refilled — which is why removal does NOT need to bypass SyncStore.
		// Toolbox state joins the same list, and its failure mode is the worst of
		// them: a partial workspace that erased the assignments would leave every
		// agent instance implicit again, silently re-inheriting every workspace
		// binding — the exact behavior named Toolboxes exist to stop (FR-32).
		// It has no legitimate empty value the way an uninstall does, because a
		// migrated workspace always has at least one assignment, so "empty" here
		// always means "this record never loaded it".
		// The Goal joins the list for legacy records only. Its envelope is
		// written unconditionally now, so a record that carries one — including
		// a Goal the user cleared — is authoritative; only a record that
		// predates the column has none, and that is the single case where the
		// canonical workspace.json should refill it.
		if portableWorkspaceStateMissing(ws) && diskBefore != nil {
			restorePortableWorkspaceState(ws, diskBefore)
		}
		if err := s.fileSync.saveFenced(ws, fence); err != nil {
			return fmt.Errorf("failed to sync workspace to disk: %w", err)
		}
		var primaryErr error
		if saver, ok := s.primary.(VersionedSaver); ok && protected {
			primaryErr = saver.SaveExpecting(ws, base)
		} else {
			primaryErr = s.primary.Save(ws)
		}
		if primaryErr != nil {
			if rollbackErr := fence.restore(); rollbackErr != nil {
				// The journal stays: the next fenced write classifies this split
				// instead of trusting either mirror.
				return fmt.Errorf("primary workspace save failed: %w (folder rollback failed: %v)", primaryErr, rollbackErr)
			}
			// The folder is back at the caller's base version; keep the caller's
			// record consistent with it so a fresh read and retry agree.
			ws.Version = base
			if journal != nil {
				if err := fence.clearJournal(); err != nil {
					return fmt.Errorf("primary workspace save failed: %w (journal not closed: %v)", primaryErr, err)
				}
			}
			// primaryBefore is normally still present because the failed Save is
			// transactional. Restore it defensively for custom primary stores.
			if primaryBefore != nil {
				_ = s.primary.Save(primaryBefore)
			}
			return primaryErr
		}
		if journal != nil {
			// Both mirrors hold the after image; a journal that outlives this
			// point only ever classifies as applied.
			if err := fence.clearJournal(); err != nil {
				logger.Warn("workspace fence journal left behind after an applied write", logger.Fields{
					"workspace_id": ws.ID, "operation_id": journal.OperationID, "error": err.Error(),
				})
			}
		}
		return nil
	}
	return s.primary.Save(ws)
}

func portableWorkspaceStateMissing(ws *Workspace) bool {
	return ws != nil && (ws.ProjectPath == "" || ws.Designation == "" || ws.TemplateProvenance == nil ||
		ws.SetupWizardProgress == nil || ws.RuntimeState == nil || capabilitiesMissing(ws) ||
		toolboxStateMissing(ws) || missionStateMissing(ws))
}

func restorePortableWorkspaceState(ws, diskWorkspace *Workspace) {
	if ws == nil || diskWorkspace == nil {
		return
	}
	if ws.ProjectPath == "" {
		ws.ProjectPath = diskWorkspace.ProjectPath
	}
	if ws.Designation == "" {
		ws.Designation = diskWorkspace.Designation
	}
	if ws.TemplateProvenance == nil {
		ws.TemplateProvenance = diskWorkspace.TemplateProvenance
	}
	if ws.SetupWizardProgress == nil {
		ws.SetupWizardProgress = CloneSetupWizardProgress(diskWorkspace.SetupWizardProgress)
	}
	if ws.RuntimeState == nil {
		ws.RuntimeState = CloneWorkspaceRuntimeState(diskWorkspace.RuntimeState)
	}
	if capabilitiesMissing(ws) {
		ws.InstalledCapabilities = CloneInstalledCapabilities(diskWorkspace.InstalledCapabilities)
	}
	if toolboxStateMissing(ws) {
		ws.Toolboxes = cloneToolboxDefinitions(diskWorkspace.Toolboxes)
		ws.ToolboxAssignments = cloneToolboxAssignments(diskWorkspace.ToolboxAssignments)
		ws.ToolboxMigration = diskWorkspace.ToolboxMigration
		ws.GoalBrief = diskWorkspace.GoalBrief.Clone()
		ws.GoalToolboxPolicy = diskWorkspace.GoalToolboxPolicy.Clone()
	}
	if missionStateMissing(ws) {
		restoreMissionFromDisk(ws, diskWorkspace)
	}
}

// capabilitiesMissing reports whether ws carries no capability records AND did
// not mean to. An empty collection that was deliberately written (an uninstall,
// or a rollback of a failed install) is a real value and must not be refilled
// from disk.
func capabilitiesMissing(ws *Workspace) bool {
	return len(ws.InstalledCapabilities) == 0 && !ws.InstalledCapabilitiesExplicit()
}

// toolboxStateMissing reports whether ws carries no Toolbox state at all.
//
// Unlike installed capabilities, this needs no "was it deliberate?" flag: a
// workspace that has been made explicit always keeps at least one assignment
// per agent instance, and deleting the last Toolbox is blocked while anything
// references it (FR-21). So an entirely empty toolbox state can only mean this
// record never loaded it.
func toolboxStateMissing(ws *Workspace) bool {
	return len(ws.Toolboxes) == 0 && len(ws.ToolboxAssignments) == 0 && ws.ToolboxMigration == nil &&
		ws.GoalBrief == nil && ws.GoalToolboxPolicy == nil
}

// missionStateMissing reports whether ws arrived without its Goal
// configuration, as opposed to arriving with an empty one.
//
// The distinction is the whole point. A Goal the user cleared and a record that
// predates the mission_state_json column both leave Mission empty, but only the
// second should be refilled from disk — refilling the first would resurrect a
// Goal somebody deliberately turned off. MarkMissionLoaded is what tells them
// apart; see Workspace.missionLoaded.
func missionStateMissing(ws *Workspace) bool {
	return !ws.MissionLoaded() && ws.Mission == "" && !ws.MissionEnabled &&
		ws.Cadence == nil && ws.NextMissionRunAt == nil
}

// restoreMissionFromDisk refills a legacy record's Goal from the canonical
// workspace.json. Config and counters move together — a cadence without its
// NextMissionRunAt reads as permanently due.
func restoreMissionFromDisk(ws, disk *Workspace) {
	if ws == nil || disk == nil {
		return
	}
	ws.Mission = disk.Mission
	ws.MissionEnabled = disk.MissionEnabled
	ws.AutonomyPolicy = disk.AutonomyPolicy
	ws.Cadence = disk.Cadence
	ws.NotificationPolicy = disk.NotificationPolicy
	ws.LastMissionRunAt = disk.LastMissionRunAt
	ws.NextMissionRunAt = disk.NextMissionRunAt
	ws.MissionExecutionCount = disk.MissionExecutionCount
	ws.MissionFailureCount = disk.MissionFailureCount
	ws.MissionCadenceHeartbeat = disk.MissionCadenceHeartbeat
}

func cloneToolboxDefinitions(definitions []ToolboxDefinition) []ToolboxDefinition {
	if len(definitions) == 0 {
		return nil
	}
	out := make([]ToolboxDefinition, len(definitions))
	for i := range definitions {
		out[i] = definitions[i].Clone()
	}
	return out
}

func cloneToolboxAssignments(assignments []AgentToolboxAssignment) []AgentToolboxAssignment {
	if len(assignments) == 0 {
		return nil
	}
	out := make([]AgentToolboxAssignment, len(assignments))
	for i := range assignments {
		out[i] = assignments[i].Clone()
	}
	return out
}

// Get retrieves a workspace from the primary store.
func (s *SyncStore) Get(id string) (*Workspace, error) {
	return s.primary.Get(id)
}

// ResolveSlug delegates to the primary store because it includes DB-only
// registrations that the folder sync target cannot see.
func (s *SyncStore) ResolveSlug(slug string) (*Workspace, error) {
	resolver, ok := s.primary.(SlugResolver)
	if !ok {
		return nil, ErrWorkspaceSlugNotFound
	}
	return resolver.ResolveSlug(slug)
}

// List returns all workspace IDs from the primary store.
func (s *SyncStore) List() ([]string, error) {
	return s.primary.List()
}

// DeleteReviewedGroupRequirementOperation delegates the exact-digest rollback
// through both mirrors without exposing an unrestricted Required delete.
func (s *SyncStore) DeleteReviewedGroupRequirementOperation(id, operationDigest, operationStatus string) error {
	if s == nil || s.primary == nil {
		return ErrGroupRequirementProtected
	}
	canonical, err := s.GetFolderWorkspace(id)
	if err != nil || !reviewedGroupRequirementOperationOwned(canonical, operationDigest, operationStatus) {
		return ErrGroupRequirementProtected
	}
	live, liveErr := s.primary.Get(id)
	if liveErr != nil || live == nil || live.GetAssistantProjectLink() != nil {
		return ErrGroupRequirementProtected
	}
	provenance := canonical.GetTemplateProvenance()
	if provenance != nil && provenance.GroupRequirement != nil && provenance.GroupRequirement.HomeWorkspaceID != "" {
		if home, homeErr := s.primary.Get(provenance.GroupRequirement.HomeWorkspaceID); homeErr == nil && home != nil {
			if state := home.GetAssistantProgramState(); state != nil && containsAssistantProjectID(state.LinkedProjectIDs, id) {
				return ErrGroupRequirementProtected
			}
		}
	}
	type reviewedOperationDeleter interface {
		DeleteReviewedGroupRequirementOperation(id, operationDigest, operationStatus string) error
	}
	if deleter, ok := s.primary.(reviewedOperationDeleter); ok {
		if err := deleter.DeleteReviewedGroupRequirementOperation(id, operationDigest, operationStatus); err != nil {
			return err
		}
	} else if err := s.primary.Delete(id); err != nil {
		return err
	}
	if s.fileSync != nil {
		if err := s.fileSync.DeleteReviewedGroupRequirementOperation(id, operationDigest, operationStatus); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a workspace from the primary store and the disk folder.
func (s *SyncStore) Delete(id string) error {
	if protected, checkErr := storeContainsProtectedAssistantProgram(s.primary, id); checkErr != nil {
		return checkErr
	} else if protected {
		return ErrAssistantProgramProtected
	}
	if s.fileSync != nil {
		if protected, checkErr := storeContainsRequiredGroupRequirement(s.fileSync, id); checkErr != nil {
			return checkErr
		} else if protected {
			return ErrGroupRequirementProtected
		}
	} else if protected, checkErr := storeContainsRequiredGroupRequirement(s.primary, id); checkErr != nil {
		return checkErr
	} else if protected {
		return ErrGroupRequirementProtected
	}
	err := s.primary.Delete(id)
	if s.fileSync != nil {
		if delErr := s.fileSync.Delete(id); delErr != nil {
			logger.Debug("FileStore delete during sync (may not exist on disk)", logger.Fields{
				"workspace_id": id,
				"error":        delErr,
			})
		}
	}
	return err
}

func storeContainsRequiredGroupRequirement(store Store, rootID string) (bool, error) {
	ids, err := store.List()
	if err != nil {
		return false, err
	}
	all := make(map[string]*Workspace, len(ids))
	for _, id := range ids {
		candidate, getErr := store.Get(id)
		if getErr != nil {
			return false, getErr
		}
		all[id] = candidate
	}
	return requiredGroupRequirementSubtree(all, rootID), nil
}

func storeContainsProtectedAssistantProgram(store Store, rootID string) (bool, error) {
	ids, err := store.List()
	if err != nil {
		return false, err
	}
	all := make(map[string]*Workspace, len(ids))
	for _, id := range ids {
		candidate, getErr := store.Get(id)
		if getErr != nil {
			return false, getErr
		}
		all[id] = candidate
	}
	return protectedAssistantProgramSubtree(all, rootID), nil
}

// ListActive returns all active workspaces from the primary store.
func (s *SyncStore) ListActive() ([]*Workspace, error) {
	return s.primary.ListActive()
}

// ListActiveForScheduling returns active workspaces for the scheduler, delegating
// to the primary store's lighter (Messages-omitting) scan when it supports one and
// falling back to the full ListActive otherwise.
func (s *SyncStore) ListActiveForScheduling() ([]*Workspace, error) {
	if sl, ok := s.primary.(schedulingLister); ok {
		return sl.ListActiveForScheduling()
	}
	return s.primary.ListActive()
}

// GetFilesPath returns the files path from the FileStore so uploads
// go to the correct workspace folder on disk.
func (s *SyncStore) GetFilesPath(workspaceID string) string {
	if s.fileSync != nil {
		return s.fileSync.GetFilesPath(workspaceID)
	}
	return s.primary.GetFilesPath(workspaceID)
}

// GetOutputsPath returns the outputs path from the FileStore so auto-saved
// task results go to the correct workspace folder on disk.
func (s *SyncStore) GetOutputsPath(workspaceID string) string {
	if s.fileSync != nil {
		return s.fileSync.GetOutputsPath(workspaceID)
	}
	return s.primary.GetOutputsPath(workspaceID)
}

// GetWorkspaceAgent reads a workspace-local agent snapshot. Reads prefer the
// FileStore (which holds the on-disk snapshot) so an imported workspace folder
// can resolve its entry agent before the primary store is hydrated.
func (s *SyncStore) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	if s.fileSync != nil {
		if ag, ok, err := s.fileSync.GetWorkspaceAgent(workspaceID, agentName); err == nil && ok {
			return ag, true, nil
		} else if err != nil {
			logger.Debug("FileStore GetWorkspaceAgent failed, falling back to primary", logger.Fields{
				"workspace_id": workspaceID,
				"agent":        agentName,
				"error":        err,
			})
		}
	}
	return s.primary.GetWorkspaceAgent(workspaceID, agentName)
}

// Lock delegates per-workspace serialization to the primary store so that
// Update calls remain atomic regardless of which Store value the caller holds.
func (s *SyncStore) Lock(wsID string) func() { return s.primary.Lock(wsID) }

// Update applies fn under the primary's per-workspace lock, then routes the
// resulting Save through SyncStore.Save (which writes to both primary and the
// disk sync target). Folder-only state must be restored before fn runs: a
// mutation such as adding one runtime grant needs to see and preserve the
// already-selected runtime mode rather than constructing a grant-only state
// from the lean SQLite projection.
func (s *SyncStore) Update(wsID string, fn func(*Workspace) error) error {
	unlock := s.Lock(wsID)
	defer unlock()

	ws, err := s.primary.Get(wsID)
	if err != nil {
		return err
	}
	if s.fileSync != nil && portableWorkspaceStateMissing(ws) {
		if diskWorkspace, diskErr := s.fileSync.Get(wsID); diskErr == nil && diskWorkspace != nil {
			restorePortableWorkspaceState(ws, diskWorkspace)
		}
	}
	if err := fn(ws); err != nil {
		return err
	}
	return s.Save(ws)
}

// SaveWorkspaceAgent writes the snapshot to the primary store and to disk.
func (s *SyncStore) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	if ws, err := s.primary.Get(workspaceID); err == nil && ws != nil && (ws.Status == StatusTrashed || ws.Status == StatusMissing) {
		return nil
	}
	if err := s.primary.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
		return err
	}
	if s.fileSync != nil {
		if err := s.fileSync.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
			logger.Warn("Failed to sync workspace agent to disk", logger.Fields{
				"workspace_id": workspaceID,
				"agent":        agentName,
				"error":        err,
			})
		}
	}
	return nil
}
