package sessionhttp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

var errCreationBuildChanged = errors.New("attached-source build changed or is unavailable; reopen its current review before confirming")
var errCreationBuildReviewRequired = errors.New("review this attached-source build before creating; no creation has been accepted")

// The build retains its draft and source selections. The existing ordinary
// journal binds their exact version; no path, source handoff or second draft is
// persisted here. Specialized setup/preparation keeps its own acceptance owner.
func ordinaryCreationBuildMatches(build *personalassistant.WorkspaceBuildSession, req createWorkspaceRequest) bool {
	return req.Kind != "group" && build != nil && build.Status == personalassistant.WorkspaceBuildOpen &&
		build.Version == req.BuildReviewVersion && build.Setup == nil && build.Preparation == nil && build.PlacementSource == nil &&
		build.FolderRequestID == "" && build.LinkRequestID == "" && build.InferenceID == "" &&
		(build.Proposal == nil || (build.Proposal.Status != "pending" && build.Proposal.Status != "running")) &&
		(build.Folder != nil || build.Link != nil) &&
		(build.Folder == nil || (!build.Folder.Recognition.Recognized() && !isReviewedJanitorBlueprint(req.TemplateID)))
}

func (h *Handler) checkCreationBuildFolder(ctx context.Context, owner string, build *personalassistant.WorkspaceBuildSession) error {
	if build.Folder == nil {
		return nil
	}
	deps := h.workspaceBuild
	if deps.Folders == nil || deps.Assistant == nil {
		return errCreationBuildChanged
	}
	projection, err := deps.Assistant(ctx, owner)
	if err != nil || projection == nil {
		return errCreationBuildChanged
	}
	root, err := deps.Folders.ResolveRoot(ctx, buildFolderTarget(owner, projection, build.ID), build.Folder.Observation.ID)
	if err != nil || strings.TrimSpace(root) == "" {
		return errCreationBuildChanged
	}
	return nil
}

func (h *Handler) creationSourceDigest(ctx context.Context, req createWorkspaceRequest, template projecttemplates.Template, resolved bool) (string, error) {
	base, err := workspaceCreationSourceDigest(req, template, resolved)
	if err != nil || req.BuildReviewVersion == 0 {
		return base, err
	}
	owner, err := h.buildPreparationOwner(ctx)
	creationOwner, ownerErr := h.creationOperationOwner(ctx)
	if err != nil || ownerErr != nil || owner != creationOwner || h.workspaceBuild == nil || req.BuildReviewVersion < 1 || req.BuildSessionID == "" {
		return "", errCreationBuildChanged
	}
	doc, err := h.workspaceBuild.Store.Read(ctx, owner)
	if err != nil {
		return "", errCreationBuildChanged
	}
	build := doc.Session(req.BuildSessionID)
	if !ordinaryCreationBuildMatches(build, req) || h.checkCreationBuildFolder(ctx, owner, build) != nil {
		return "", errCreationBuildChanged
	}
	witness, err := json.Marshal(struct {
		Blueprint string
		ID        string
		Version   int64
		Folder    *personalassistant.BuildFolder
		Link      *personalassistant.BuildLink
	}{base, build.ID, build.Version, build.Folder, build.Link})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(witness)
	return hex.EncodeToString(digest[:]), nil
}

func (h *Handler) acceptedCreationForBuild(ctx context.Context, owner, id string) (string, error) {
	if id == "" || h.store == nil || h.store.DB() == nil {
		return "", nil
	}
	var operation string
	err := h.store.DB().QueryRowContext(ctx, `SELECT id FROM workspace_creation_operations
		WHERE owner_user_id=? AND state IN ('accepted','complete','reconciliation_required')
		AND json_extract(request_json,'$.build_session_id')=?
		AND json_extract(request_json,'$.build_review_version')>0 LIMIT 1`, owner, id).Scan(&operation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return operation, err
}

func (h *Handler) checkUnacceptedCreationBuild(ctx context.Context, owner, id string) error {
	accepted, err := h.acceptedCreationForBuild(ctx, owner, id)
	if err != nil {
		return errBuildTurn{http.StatusServiceUnavailable, "unavailable"}
	}
	if accepted != "" {
		return errBuildTurn{http.StatusConflict, "creation_build_accepted"}
	}
	return nil
}

// Same-process admission serializes a source mutation against confirmation.
// SQL acceptance is the durable cross-controller/process fence; neither this
// lease nor an in-memory promise is completion proof.
func (h *Handler) acquireCreationBuild(w http.ResponseWriter, r *http.Request, owner, id string) (func(), bool) {
	release, ok := h.buildReviews.enter(foldercontext.Target{UserID: owner, DraftID: "ordinary-create:" + id})
	if !ok {
		creationOperationError(w, http.StatusConflict, "creation_build_busy", "This build has another action in flight. Read its current review before confirming.")
		return nil, false
	}
	accepted, err := h.acceptedCreationForBuild(r.Context(), owner, id)
	if err != nil {
		release()
		creationOperationError(w, http.StatusServiceUnavailable, "creation_build_unavailable", "The original build acceptance is unreadable. Do not create or edit it until its status can be read.")
		return nil, false
	}
	if accepted != "" {
		release()
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"code": "creation_build_accepted", "error": "This build has an accepted creation. Read that original operation; do not create or edit it again.", "operation_id": accepted,
		})
		return nil, false
	}
	return release, true
}

// Recheck under the existing build-store lock, claim the existing journal, then
// invalidate late inference/observer reservations before releasing that lock.
// A failed write after SQL acceptance remains uncertain and never authorizes a
// retry; accepted journal reads also fence HTTP mutations after a restart.
func (h *Handler) acceptCreationBuild(ctx context.Context, op *workspaceCreationOperation, req createWorkspaceRequest, claim func() error) error {
	if h.workspaceBuild == nil {
		return errCreationBuildChanged
	}
	_, err := h.workspaceBuild.Store.Mutate(ctx, op.Owner, func(doc *personalassistant.WorkspaceBuildDocument) error {
		build := doc.Session(req.BuildSessionID)
		if !ordinaryCreationBuildMatches(build, req) || h.checkCreationBuildFolder(ctx, op.Owner, build) != nil {
			return errCreationBuildChanged
		}
		if err := claim(); err != nil {
			return err
		}
		build.InvalidateBuildInference(true)
		build.Touch(h.workspaceBuild.Store.Now())
		return nil
	})
	return err
}
