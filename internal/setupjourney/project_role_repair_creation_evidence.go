package setupjourney

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// originalReviewedProjectConnection checks the actual completed setup journey
// that created this File-only, existing-file child. The project creator stores
// group provenance through CommitReviewed, which does NOT create a separate
// group_requirement_operations row. Its creation authority is instead the
// consumed project review, successful project operation and exact run result.
// Other creation routes need their own original-receipt verifier, not a guessed
// fallback from this one. No read here is permission to rewrite project roles.
func (r *ProjectRoleRepairReviewer) originalReviewedProjectConnection(ctx context.Context, ownerID, homeID, projectID string, inspection ProjectRoleRepairInspection) bool {
	project, err := r.workspaces.Get(projectID)
	if err != nil || project == nil {
		return false
	}
	portable := project
	if mirrors, ok := r.workspaces.(workspace.MirrorWorkspaceProvider); ok {
		folder, mirrored, mirrorErr := mirrors.GetMirrorWorkspace(projectID)
		if mirrorErr != nil || (mirrored && folder == nil) {
			return false
		}
		if mirrored {
			portable = folder
		}
	}
	provenance := portable.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil {
		return false
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(encoded)
	if hex.EncodeToString(sum[:]) != inspection.ProvenanceDigest {
		return false // changed between inspection and original-receipt read
	}
	snapshot := provenance.GroupRequirement
	link := project.GetAssistantProjectLink()
	if link == nil || link.ProjectProvider == nil {
		return false
	}
	rows, err := r.db.QueryContext(ctx, `SELECT operation.run_id, operation.result_json, operation.result_code,
			operation.input_digest, operation.review_digest, operation.run_revision_before, operation.run_revision_after,
			review.input_digest, review.owner_revision_digest, review.disclosure_digest, review.run_revision,
			run.run_kind, run.selected_mode_id
		FROM setup_journey_operation_receipt AS operation
		JOIN setup_journey_run AS run ON run.id = operation.run_id AND run.run_kind = operation.run_kind
		JOIN setup_journey_run AS root ON root.id = COALESCE(run.root_run_id, run.id) AND root.run_kind = 'root'
		JOIN setup_journey_review_receipt AS review ON review.run_id = operation.run_id
			AND review.run_kind = operation.run_kind AND review.action_id = operation.action_id
			AND review.step_id = operation.step_id AND review.consumed_by_idempotency_key = operation.idempotency_key
		WHERE operation.status = 'succeeded' AND operation.action_id = 'connect_existing_project'
			AND operation.step_id = 'project' AND operation.review_digest != ''
			AND review.consumed_at IS NOT NULL AND run.project_workspace_id = ?
			AND root.owner_user_id = ? AND root.home_workspace_id = ?
			AND root.integration_plugin_id = ? AND root.integration_version = ?
		LIMIT 2`, projectID, ownerID, homeID, link.ProjectProvider.PluginID, link.ProjectProvider.PluginVersion)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	count, valid := 0, false
	for rows.Next() {
		count++
		var runID, rawResult, resultCode, opInput, opDisclosure, reviewInput, ownerDigest, reviewDisclosure, runKind, mode string
		var before, after, reviewedAt int64
		if err := rows.Scan(&runID, &rawResult, &resultCode, &opInput, &opDisclosure, &before, &after,
			&reviewInput, &ownerDigest, &reviewDisclosure, &reviewedAt, &runKind, &mode); err != nil {
			return false
		}
		result, decoded := decodeCanonicalResult(rawResult)
		if !decoded || result.ProjectWorkspaceID != projectID || projectconnection.ProjectWorkspaceIDForRun(runID) != projectID ||
			(runKind == string(RunKindRoot) && result.HomeWorkspaceID != homeID) ||
			(runKind != string(RunKindRoot) && runKind != string(RunKindChild)) ||
			mode != "file_only" || (resultCode != string(ResultApplied) && resultCode != string(ResultReconciled) && resultCode != string(ResultAlreadyCurrent)) ||
			!validateDigest(opInput, false) || !validateDigest(ownerDigest, false) ||
			opInput != reviewInput || opDisclosure != reviewDisclosure || before != reviewedAt || after != before+1 {
			return false
		}
		creationReview, creationOperation := projectconnection.CreationEvidenceDigests(runID, projecttemplates.ProjectConnectionExistingProject, opInput, ownerDigest)
		if snapshot.ReviewDigest != creationReview || snapshot.OperationDigest != creationOperation {
			return false
		}
		valid = true
	}
	return count == 1 && valid && rows.Err() == nil
}
