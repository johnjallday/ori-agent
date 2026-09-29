package sessionhttp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// continuityPlanMember is one reviewed workspace's install plan row.
type continuityPlanMember struct {
	WorkspaceID string
	ParentID    string
	SourceDir   string
	FolderSlug  string
	InPlace     bool
	Installed   bool
	Disposition workspacecontinuity.Disposition
}

// runContinuityImport confirms one reviewed tree. The request must carry the
// digests the user reviewed; both are recomputed here and any difference
// means a new review, before a single write.
func (h *Handler) runContinuityImport(ctx context.Context, req continuityImportRequest) (*continuityImportReport, int, error) {
	h.continuity.mu.Lock()
	defer h.continuity.mu.Unlock()
	if strings.TrimSpace(req.Path) == "" || req.TreeDigest == "" || req.DestinationDigest == "" {
		return nil, http.StatusBadRequest, &continuityUserError{"Review the folder before importing it."}
	}
	path, err := normalizeImportPath(req.Path)
	if err != nil {
		return nil, http.StatusBadRequest, &continuityUserError{"Choose a folder to import."}
	}
	review, err := inspectContinuityImportTree(ctx, path)
	if err != nil || review.Status != "review_required" || review.TreeDigest != req.TreeDigest {
		return nil, http.StatusConflict, errContinuityStale
	}
	db := h.store.DB()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, http.StatusServiceUnavailable, err
	}
	destination, err := workspacecontinuity.DestinationDigest(ctx, tx, userprofile.LocalUserID)
	if err == nil {
		err = analyzeContinuityDestination(ctx, tx, &review)
	}
	if rollbackErr := tx.Rollback(); err == nil {
		err = rollbackErr
	}
	if err != nil {
		return nil, http.StatusServiceUnavailable, err
	}
	if prior := review.AlreadyImported; prior != nil {
		if review.RecommendedAction == "open" {
			report, err := h.continuityImportReport(ctx, prior.OperationID)
			return report, http.StatusOK, err
		}
		return h.resumeLocked(ctx, prior.OperationID, &review)
	}
	if review.Conflict != "" {
		return nil, http.StatusConflict, &continuityUserError{"A workspace in this folder already exists in Ori. Nothing was imported and your existing work is unchanged."}
	}
	if !slices.Contains(review.Actions, req.Action) {
		return nil, http.StatusConflict, &continuityUserError{"That import choice is not available for this folder here. Review it again."}
	}
	if req.DestinationDigest != destination {
		return nil, http.StatusConflict, errContinuityStale
	}
	plan, err := h.planContinuityImport(ctx, path, &review, workspacecontinuity.ImportAction(req.Action))
	if err != nil {
		return nil, http.StatusConflict, err
	}
	members := make([]workspacecontinuity.ImportMember, 0, len(plan))
	byID := map[string]continuityImportMember{}
	for _, member := range review.members {
		byID[member.Inspection.Manifest.WorkspaceID] = member
	}
	for _, p := range plan {
		inspected := byID[p.WorkspaceID].Inspection
		members = append(members, workspacecontinuity.ImportMember{WorkspaceID: p.WorkspaceID,
			Generation: inspected.Pointer.Generation, Digest: inspected.Pointer.Digest, Disposition: p.Disposition})
	}
	local := workspacecontinuity.NewLocalStore(db)
	op, err := local.BeginReviewedImport(ctx, workspacecontinuity.Operation{ID: uuid.NewString(), TreeDigest: review.TreeDigest,
		DestinationDigest: destination, UserID: userprofile.LocalUserID, Action: workspacecontinuity.ImportAction(req.Action)}, members)
	if err != nil {
		return nil, http.StatusConflict, err
	}
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, p := range plan {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO continuity_installs(operation_id,workspace_id,parent_id,source_dir,folder_slug,in_place)
				VALUES (?,?,?,?,?,?)`, op.ID, p.WorkspaceID, p.ParentID, p.SourceDir, p.FolderSlug, p.InPlace); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return h.interruptContinuityImport(ctx, op.ID, err)
	}
	return h.executeContinuityImport(ctx, op, &review)
}

// resumeContinuityImport retries an interrupted operation with its original
// receipt and install plan. Records already restored are not written again.
func (h *Handler) resumeContinuityImport(ctx context.Context, operationID string) (*continuityImportReport, int, error) {
	h.continuity.mu.Lock()
	defer h.continuity.mu.Unlock()
	return h.resumeLocked(ctx, operationID, nil)
}

func (h *Handler) resumeLocked(ctx context.Context, operationID string, review *continuityImportReview) (*continuityImportReport, int, error) {
	op, err := workspacecontinuity.NewLocalStore(h.store.DB()).Operation(ctx, operationID, userprofile.LocalUserID)
	if err != nil {
		return nil, http.StatusNotFound, &continuityUserError{"That import was not found."}
	}
	if op.Status == "complete" {
		report, err := h.continuityImportReport(ctx, op.ID)
		return report, http.StatusOK, err
	}
	return h.executeContinuityImport(ctx, op, review)
}

// planContinuityImport decides each member's disposition and folder. A folder
// already inside the Workspace Directory is attached where it is; anything
// else is copied in beside the user's other workspaces. Folder names that are
// taken here get a free suffix, never another workspace's place.
func (h *Handler) planContinuityImport(ctx context.Context, path string, review *continuityImportReview, action workspacecontinuity.ImportAction) ([]continuityPlanMember, error) {
	base := h.workspaceStore.BasePath()
	// Compare resolved paths: the selected path is canonicalized (for example
	// /var → /private/var on macOS) while the configured root may not be.
	resolvedBase, resolvedPath := base, path
	if value, err := filepath.EvalSymlinks(base); err == nil {
		resolvedBase = value
	}
	if value, err := filepath.EvalSymlinks(path); err == nil {
		resolvedPath = value
	}
	inRoot := false
	if rel, err := filepath.Rel(resolvedBase, resolvedPath); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) && rel != "." {
		if strings.Contains(filepath.ToSlash(rel), "/") {
			return nil, &continuityUserError{"Move this folder directly into your Workspace Directory, or choose it from outside the Workspace Directory."}
		}
		inRoot = true
	}
	taken, err := h.takenFolderSlugs(ctx)
	if err != nil {
		return nil, err
	}
	plan := make([]continuityPlanMember, 0, len(review.members))
	for _, member := range review.members {
		id := member.Inspection.Manifest.WorkspaceID
		disposition := workspacecontinuity.Ordinary
		if member.Candidate != nil {
			disposition = workspacecontinuity.WorkspaceOnlyHQ
			if action == workspacecontinuity.Continue {
				disposition = workspacecontinuity.AdoptedHQ
			}
		}
		name := filepath.Base(member.Dir)
		slug := agentworkspace.Slugify(name)
		if !agentworkspace.IsCanonicalWorkspaceSlug(slug) {
			slug = "workspace"
		}
		if member.ParentID == "" {
			if !inRoot {
				if _, statErr := os.Lstat(filepath.Join(base, slug)); statErr == nil {
					taken[slug] = true
				}
			} else if slug != name {
				if _, statErr := os.Lstat(filepath.Join(base, slug)); statErr == nil {
					taken[slug] = true
				}
			}
		}
		slug = freeContinuitySlug(slug, taken)
		taken[slug] = true
		plan = append(plan, continuityPlanMember{WorkspaceID: id, ParentID: member.ParentID, SourceDir: member.Dir,
			FolderSlug: slug, InPlace: inRoot, Disposition: disposition})
	}
	return plan, nil
}

func freeContinuitySlug(slug string, taken map[string]bool) string {
	if !taken[slug] {
		return slug
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", slug, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func (h *Handler) takenFolderSlugs(ctx context.Context) (map[string]bool, error) {
	rows, err := h.store.DB().QueryContext(ctx, `SELECT COALESCE(folder_slug,'') FROM workspaces`)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{"agents": true, "skills": true}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			_ = rows.Close()
			return nil, err
		}
		taken[strings.ToLower(slug)] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, ws := range h.workspaceStore.CachedWorkspaces() {
		taken[strings.ToLower(ws.FolderSlug)] = true
	}
	return taken, nil
}

func (h *Handler) loadContinuityPlan(ctx context.Context, operationID string) ([]continuityPlanMember, error) {
	rows, err := h.store.DB().QueryContext(ctx, `SELECT i.workspace_id,i.parent_id,i.source_dir,i.folder_slug,i.in_place,i.installed,a.disposition
		FROM continuity_installs i JOIN continuity_attachments a ON a.workspace_id=i.workspace_id AND a.operation_id=i.operation_id
		WHERE i.operation_id=? ORDER BY i.rowid`, operationID)
	if err != nil {
		return nil, err
	}
	var plan []continuityPlanMember
	for rows.Next() {
		var p continuityPlanMember
		if err := rows.Scan(&p.WorkspaceID, &p.ParentID, &p.SourceDir, &p.FolderSlug, &p.InPlace, &p.Installed, &p.Disposition); err != nil {
			_ = rows.Close()
			return nil, err
		}
		plan = append(plan, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(plan) == 0 {
		return nil, workspacecontinuity.ErrConflict
	}
	return plan, nil
}

func (h *Handler) interruptContinuityImport(ctx context.Context, operationID string, cause error) (*continuityImportReport, int, error) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), continuityCleanupTimeout)
	defer cancel()
	if err := workspacecontinuity.NewLocalStore(h.store.DB()).Interrupt(cleanup, operationID, userprofile.LocalUserID); err != nil {
		logger.Warn("Failed to record interrupted workspace import", logger.Fields{"operation_id": operationID, "error": err.Error()})
	}
	report, _ := h.continuityImportReport(cleanup, operationID)
	status := http.StatusInternalServerError
	var user *continuityUserError
	if errors.Is(cause, errContinuityStale) || errors.Is(cause, workspacecontinuity.ErrChanged) ||
		errors.Is(cause, workspacecontinuity.ErrConflict) || errors.As(cause, &user) {
		status = http.StatusConflict
	}
	return report, status, cause
}

// executeContinuityImport runs (or resumes) the operation: restore records
// from the still-intact checkpoints, install the folders, attach the
// workspaces inactive and complete the receipt.
func (h *Handler) executeContinuityImport(ctx context.Context, op workspacecontinuity.Operation, review *continuityImportReview) (*continuityImportReport, int, error) {
	plan, err := h.loadContinuityPlan(ctx, op.ID)
	if err != nil {
		return h.interruptContinuityImport(ctx, op.ID, err)
	}
	local := workspacecontinuity.NewLocalStore(h.store.DB())
	pending := false
	for _, p := range plan {
		outcomes, err := local.ComponentOutcomes(ctx, workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: p.WorkspaceID, UserID: op.UserID})
		if err != nil {
			return h.interruptContinuityImport(ctx, op.ID, err)
		}
		for _, outcome := range outcomes {
			pending = pending || outcome.Status == "restoring" || outcome.Status == "failed" || outcome.Status == "interrupted"
		}
	}
	if pending {
		// Records come from the reviewed checkpoints, which must still be the
		// exact bytes the user confirmed.
		if review == nil {
			fresh, err := inspectContinuityImportTree(ctx, plan[0].SourceDir)
			if err != nil || fresh.Status != "review_required" || fresh.TreeDigest != op.TreeDigest {
				return h.interruptContinuityImport(ctx, op.ID, errContinuityStale)
			}
			review = &fresh
		}
		members := map[string]continuityImportMember{}
		for _, member := range review.members {
			members[member.Inspection.Manifest.WorkspaceID] = member
		}
		for _, p := range plan {
			member, ok := members[p.WorkspaceID]
			if !ok {
				return h.interruptContinuityImport(ctx, op.ID, errContinuityStale)
			}
			if err := h.restoreContinuityMember(ctx, op, p, member); err != nil {
				return h.interruptContinuityImport(ctx, op.ID, err)
			}
		}
	}
	if err := h.installContinuityTree(ctx, op, plan); err != nil {
		return h.interruptContinuityImport(ctx, op.ID, err)
	}
	if err := local.CompleteImport(ctx, op.ID, op.UserID); err != nil {
		return h.interruptContinuityImport(ctx, op.ID, err)
	}
	h.registerContinuityImport(plan)
	report, err := h.continuityImportReport(ctx, op.ID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return report, http.StatusCreated, nil
}

// registerContinuityImport makes the completed (inactive) workspaces visible:
// the folder store loads them now that the guard admits them, and their own
// agent copies are trusted for the roster so manual work can start.
func (h *Handler) registerContinuityImport(plan []continuityPlanMember) {
	if err := h.workspaceStore.Reload(); err != nil {
		logger.Warn("Failed to reload workspaces after import", logger.Fields{"error": err.Error()})
	}
	if h.workspaceAllowlist != nil {
		for _, p := range plan {
			if err := h.workspaceAllowlist.Add(p.WorkspaceID); err != nil {
				logger.Warn("Failed to trust imported workspace", logger.Fields{"workspace_id": p.WorkspaceID, "error": err.Error()})
			}
		}
	}
	if invalidator, ok := h.agentStore.(interface{ InvalidateWorkspaceAgents() }); ok {
		invalidator.InvalidateWorkspaceAgents()
	}
	for _, p := range plan {
		h.notifyContinuityAdmission(p.WorkspaceID)
	}
	if h.continuity.afterImport != nil {
		h.continuity.afterImport()
	}
}

func (h *Handler) continuityImportReport(ctx context.Context, operationID string) (*continuityImportReport, error) {
	db := h.store.DB()
	local := workspacecontinuity.NewLocalStore(db)
	op, err := local.Operation(ctx, operationID, userprofile.LocalUserID)
	if err != nil {
		return nil, err
	}
	report := &continuityImportReport{OperationID: op.ID, Status: op.Status, Action: string(op.Action), BackgroundOff: true, Members: []continuityMemberReport{}}
	plan, err := h.loadContinuityPlan(ctx, op.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range plan {
		scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: p.WorkspaceID, UserID: op.UserID}
		outcomes, err := local.ComponentOutcomes(ctx, scope)
		if err != nil {
			return nil, err
		}
		attachment, err := local.Attachment(ctx, p.WorkspaceID)
		if err != nil {
			return nil, err
		}
		member := continuityMemberReport{WorkspaceID: p.WorkspaceID, ParentID: p.ParentID, Disposition: string(p.Disposition),
			State: string(attachment.State), Components: outcomes}
		_ = db.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id=?`, p.WorkspaceID).Scan(&member.Name)
		if p.ParentID == "" && report.WorkspaceID == "" {
			report.WorkspaceID, report.WorkspaceSlug = p.WorkspaceID, p.FolderSlug
		}
		if p.Disposition == workspacecontinuity.AdoptedHQ {
			var name sql.NullString
			if err := db.QueryRowContext(ctx, `SELECT display_name FROM personal_assistant_state WHERE hq_workspace_id=? AND user_id=?`,
				p.WorkspaceID, op.UserID).Scan(&name); err == nil {
				report.Adopted, report.AssistantName = op.Status == "complete", name.String
			}
		}
		report.Members = append(report.Members, member)
	}
	return report, nil
}

// pipeContinuityBlob streams one reviewed object for an installer.
func pipeContinuityBlob(dir string, inspected workspacecontinuity.Inspection, domain string, ref workspacecontinuity.BlobRef) func(context.Context) (io.ReadCloser, error) {
	return func(ctx context.Context) (io.ReadCloser, error) {
		reader, writer := io.Pipe()
		go func() {
			writer.CloseWithError(workspacecontinuity.CopyInspectedBlob(ctx, dir, inspected, domain, ref, writer))
		}()
		return reader, nil
	}
}
