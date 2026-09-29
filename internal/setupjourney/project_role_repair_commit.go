package setupjourney

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Terminal states of a project role repair operation. "claimed" is the only
// non-terminal state; it means the outcome is not yet known, never that the
// write happened.
const (
	ProjectRoleRepairSucceeded         = "succeeded"
	ProjectRoleRepairCancelled         = "cancelled"
	ProjectRoleRepairReconcileRequired = "reconcile_required"
)

// ErrProjectRoleRepairNotApplied reports a terminal operation whose write did
// not happen (cancelled) or must not be retried automatically
// (reconcile_required). The returned operation carries the exact state.
var ErrProjectRoleRepairNotApplied = errors.New("project role repair was not applied")

// CommitProjectRoleRepairOperation performs the single fenced child write a
// claimed operation exists for, or settles an operation whose earlier attempt
// ended uncertainly. It is the first and only writer behind the claimed
// intent, and it depends on the shared workspace fence: the child's two
// mirrors are written through SyncStore under the folder lock, the primary
// update is conditional on the version this call read, and a journal records
// the exact before/after images so a crash is classified rather than guessed.
//
// Before writing it rechecks, after admission, the owner, exact Home/child
// link, original reviewed creator evidence, installed provider pins and both
// mirrors; the roles written are recomputed from the installed blueprint and
// must match the digest the owner reviewed. It never adds an agent instance,
// role binding, root grant or catalog record; explicit staffing stays a later
// separate review. There is no HTTP route to this method yet.
func (r *ProjectRoleRepairReviewer) CommitProjectRoleRepairOperation(ctx context.Context, ownerID, homeID, projectID, key string) (ProjectRoleRepairOperation, error) {
	refuse := func() (ProjectRoleRepairOperation, error) {
		return ProjectRoleRepairOperation{}, ErrProjectRoleRepairUnavailable
	}
	if r == nil || r.workspaces == nil || r.db == nil || r.installed == nil || r.now == nil ||
		!validateCanonicalRef(key, false) || len(key) > MaxIdempotencyKeyBytes {
		return refuse()
	}
	operation, err := r.InspectProjectRoleRepairOperation(ctx, ownerID, homeID, projectID, key)
	if err != nil {
		return refuse()
	}
	switch operation.Status {
	case ProjectRoleRepairSucceeded:
		return operation, nil // read-only replay of a terminal receipt
	case ProjectRoleRepairCancelled, ProjectRoleRepairReconcileRequired:
		return operation, ErrProjectRoleRepairNotApplied
	case "claimed":
	default:
		return refuse()
	}

	// Settle from both independently read mirrors first: an earlier attempt
	// may have written the child and died before recording its receipt.
	expectedRoles, expectedDigest, ok := r.expectedProjectRoles(ownerID, homeID, projectID)
	if !ok {
		return refuse()
	}
	switch state := r.classifyProjectRoleRepairMirrors(projectID, expectedDigest); state {
	case workspace.FenceApplied:
		return r.finishProjectRoleRepair(ctx, operation, ProjectRoleRepairSucceeded, nil)
	case workspace.FenceReconcileRequired, workspace.FenceUnknown:
		return r.finishProjectRoleRepair(ctx, operation, ProjectRoleRepairReconcileRequired, ErrProjectRoleRepairNotApplied)
	case workspace.FenceNotApplied:
	default:
		return refuse()
	}

	// Nothing has been written. The write is admissible only while the exact
	// evidence the owner reviewed is still current.
	inspection, digest, err := r.currentProjectRoleRepairEvidence(ctx, ownerID, homeID, projectID)
	if err != nil || digest != operation.EvidenceDigest || inspection.RoleDigest != expectedDigest {
		return r.finishProjectRoleRepair(ctx, operation, ProjectRoleRepairCancelled, ErrProjectRoleRepairNotApplied)
	}

	writeErr := r.workspaces.Update(projectID, func(current *workspace.Workspace) error {
		link := current.GetAssistantProjectLink()
		provenance := current.GetTemplateProvenance()
		if current.ID != projectID || current.OwnerUserID != ownerID || current.ParentID != homeID ||
			current.Status != workspace.StatusActive || link == nil || provenance == nil ||
			link.ID != inspection.LinkID || link.StateRevision != inspection.LinkRevision ||
			link.StationWorkspaceID != homeID || len(link.ProjectRoles) != 0 || len(provenance.AssistantProjectRoles) != 0 ||
			len(link.ProjectBindings.Bindings) != 0 || len(current.GetAgentInstances()) != 0 ||
			current.Version != inspection.ProjectVersion {
			return workspace.ErrStaleWorkspaceVersion
		}
		link.ProjectRoles = cloneProjectRoleSpecs(expectedRoles)
		link.StateRevision++
		current.SetAssistantProjectLink(link)
		provenance.AssistantProjectRoles = cloneProjectRoleSpecs(expectedRoles)
		current.SetTemplateProvenance(provenance)
		return nil
	})
	switch {
	case writeErr == nil:
		return r.finishProjectRoleRepair(ctx, operation, ProjectRoleRepairSucceeded, nil)
	case errors.Is(writeErr, workspace.ErrStaleWorkspaceVersion):
		// Refused before any mirror changed; the reviewed consent is spent.
		return r.finishProjectRoleRepair(ctx, operation, ProjectRoleRepairCancelled, ErrProjectRoleRepairNotApplied)
	case errors.Is(writeErr, workspace.ErrWorkspaceMirrorsDiverged), errors.Is(writeErr, workspace.ErrWorkspaceFenceUnknown):
		return r.finishProjectRoleRepair(ctx, operation, ProjectRoleRepairReconcileRequired, ErrProjectRoleRepairNotApplied)
	default:
		// Uncertain: the slot stays claimed and a later call settles it from
		// the mirrors and the fence journal.
		return operation, fmt.Errorf("project role repair write did not complete: %w", writeErr)
	}
}

// expectedProjectRoles recomputes the split blueprint's project-owned roster
// from the installed providers pinned on the child's link.
func (r *ProjectRoleRepairReviewer) expectedProjectRoles(ownerID, homeID, projectID string) ([]workspace.AssistantProgramRoleSpec, string, bool) {
	installed, err := r.installed()
	if err != nil {
		return nil, "", false
	}
	home, homeErr := r.workspaces.Get(homeID)
	project, projectErr := r.workspaces.Get(projectID)
	if homeErr != nil || projectErr != nil || home == nil || project == nil ||
		home.OwnerUserID != ownerID || project.OwnerUserID != ownerID {
		return nil, "", false
	}
	state, link := home.GetAssistantProgramState(), project.GetAssistantProjectLink()
	if state == nil || state.HomeProvider == nil || link == nil || link.ProjectProvider == nil ||
		link.StationWorkspaceID != homeID {
		return nil, "", false
	}
	roles, ok := plugin.ExactIndependentProjectRoles(installed, state.HomeProvider, link.ProjectProvider)
	if !ok || len(roles) == 0 {
		return nil, "", false
	}
	return roles, projectRolesDigest(roles), true
}

func cloneProjectRoleSpecs(roles []workspace.AssistantProgramRoleSpec) []workspace.AssistantProgramRoleSpec {
	cloned := make([]workspace.AssistantProgramRoleSpec, len(roles))
	for index := range roles {
		cloned[index] = roles[index]
		cloned[index].Skills = append([]string(nil), roles[index].Skills...)
	}
	return cloned
}

func projectRolesDigest(roles []workspace.AssistantProgramRoleSpec) string {
	encoded, err := json.Marshal(roles)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// classifyProjectRoleRepairMirrors reads the primary and, when one exists, the
// folder mirror independently. Each is "after" only when its link carries
// exactly the expected roles (and, for the folder, the portable provenance
// does too), "before" when it carries none, otherwise unknown.
func (r *ProjectRoleRepairReviewer) classifyProjectRoleRepairMirrors(projectID, expectedDigest string) workspace.FenceRecoveryOutcome {
	classify := func(ws *workspace.Workspace, portable bool) workspace.FenceRecoveryOutcome {
		if ws == nil || ws.GetAssistantProjectLink() == nil {
			return workspace.FenceUnknown
		}
		link := ws.GetAssistantProjectLink()
		if len(link.ProjectRoles) == 0 {
			if portable {
				if provenance := ws.GetTemplateProvenance(); provenance != nil && len(provenance.AssistantProjectRoles) != 0 {
					return workspace.FenceUnknown
				}
			}
			return workspace.FenceNotApplied
		}
		if projectRolesDigest(link.ProjectRoles) != expectedDigest {
			return workspace.FenceUnknown
		}
		if portable {
			provenance := ws.GetTemplateProvenance()
			if provenance == nil || projectRolesDigest(provenance.AssistantProjectRoles) != expectedDigest {
				return workspace.FenceUnknown
			}
		}
		return workspace.FenceApplied
	}
	primary, err := r.workspaces.Get(projectID)
	if err != nil {
		return workspace.FenceUnknown
	}
	primaryState := classify(primary, false)
	mirrors, ok := r.workspaces.(workspace.MirrorWorkspaceProvider)
	if !ok {
		return primaryState
	}
	folder, mirrored, err := mirrors.GetMirrorWorkspace(projectID)
	if err != nil {
		return workspace.FenceUnknown
	}
	if !mirrored {
		return primaryState
	}
	folderState := classify(folder, true)
	switch {
	case primaryState == folderState && (primaryState == workspace.FenceApplied || primaryState == workspace.FenceNotApplied):
		return primaryState
	case primaryState == workspace.FenceUnknown || folderState == workspace.FenceUnknown:
		return workspace.FenceUnknown
	default:
		return workspace.FenceReconcileRequired
	}
}

// finishProjectRoleRepair records the terminal receipt. Only a claimed row
// moves; a concurrent settlement that already moved it is read back as-is.
func (r *ProjectRoleRepairReviewer) finishProjectRoleRepair(ctx context.Context, operation ProjectRoleRepairOperation, status string, outcome error) (ProjectRoleRepairOperation, error) {
	now := r.now().UTC()
	result, err := r.db.ExecContext(ctx, `UPDATE project_role_repair_operation SET status = ?, updated_at = ?
		WHERE token = ? AND owner_user_id = ? AND status = 'claimed'`, status, now, operation.Token, operation.OwnerID)
	if err != nil {
		return operation, fmt.Errorf("record project role repair outcome: %w", err)
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr == nil && changed == 0 {
		current, readErr := r.InspectProjectRoleRepairOperation(ctx, operation.OwnerID, operation.HomeID, operation.ProjectID, operation.IdempotencyKey)
		if readErr != nil {
			return operation, ErrProjectRoleRepairUnavailable
		}
		operation = current
	} else {
		operation.Status = status
	}
	if operation.Status == ProjectRoleRepairSucceeded {
		return operation, nil
	}
	if outcome == nil {
		outcome = ErrProjectRoleRepairNotApplied
	}
	return operation, outcome
}
