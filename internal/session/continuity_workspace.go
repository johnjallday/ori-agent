package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// SnapshotContinuityWorkspace binds file-owned work to canonical registration
// metadata in the caller's shared SQL read view. It does not read through a store
// pool or normalize/create missing metadata. Identity (owner, name, parent,
// slug, creation) must agree; folder-owned content travels as the file holds
// it, and SQL mirror differences are only recorded. The preparation
// coordinator must still fence mutations through final publication.
func (s *SQLiteStore) SnapshotContinuityWorkspace(ctx context.Context, q workspacecontinuity.Queryer, canonical []byte) (workspacecontinuity.Record, error) {
	if q == nil {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	record, err := workspace.SnapshotContinuityWorkspace(canonical)
	if err != nil {
		return record, err
	}
	ws, err := workspace.DecodeContinuityWorkspace(record, canonical)
	if err != nil {
		return record, err
	}
	var owner, name string
	var parent, slug, color sql.NullString
	var createdRaw any
	var order int
	err = q.QueryRowContext(ctx, `SELECT owner_user_id,name,parent_id,folder_slug,color,created_at,order_index
		FROM workspaces WHERE id=? AND deleted_at IS NULL`, ws.ID).Scan(&owner, &name, &parent, &slug, &color, &createdRaw, &order)
	if errors.Is(err, sql.ErrNoRows) {
		return record, workspacecontinuity.ErrIncomplete
	}
	if err != nil {
		return record, err
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return record, workspacecontinuity.ErrInvalid
	}
	if owner != "local" || owner != ws.OwnerUserID {
		return record, workspacecontinuity.ErrInvalid
	}
	// Content is compared field by field below. version and updated_at are each
	// store's own save counters: a file-only save (the folder is canonical)
	// advances them on disk without changing any SQL-mirrored content.
	if name != ws.Name || parent.String != ws.ParentID || slug.String != ws.FolderSlug || !createdAt.Equal(ws.CreatedAt) {
		return record, fmt.Errorf("%w: workspace identity fields differ between database and file (name=%t parent=%t slug=%t created=%t)",
			workspacecontinuity.ErrChanged, name != ws.Name, parent.String != ws.ParentID, slug.String != ws.FolderSlug,
			!createdAt.Equal(ws.CreatedAt))
	}
	differs, err := continuityMirrorDifferences(ctx, q, ws)
	if err != nil {
		return record, err
	}
	if len(differs) > 0 {
		logger.Debug("Workspace checkpoint uses workspace.json where the database mirror differs",
			logger.Fields{"workspace_id": ws.ID, "fields": strings.Join(differs, ",")})
	}
	var descriptor workspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &descriptor); err != nil {
		return record, err
	}
	descriptor.SQLMetadata = &workspace.ContinuityWorkspaceSQLMetadata{Color: color.String, OrderIndex: order}
	return workspacecontinuity.EncodeRecord(record.ID, descriptor)
}

// CollectContinuityWorkspace reads one trusted workspace file in the caller's
// shared SQL read view and adds its typed descriptor to a bounded spool. The
// preparation owner must hold a reset permit, select the canonical folder from
// an admitted local store, fence file mutations, and verify this fingerprint
// again through Publish before acknowledging a dirty sequence. This adapter
// alone does not prepare or publish a checkpoint.
func (s *SQLiteStore) CollectContinuityWorkspace(ctx context.Context, q workspacecontinuity.Queryer, folder, workspaceID string, spool *workspacecontinuity.Spool) (workspacecontinuity.Fingerprint, error) {
	var file workspacecontinuity.Fingerprint
	if spool == nil || !workspacecontinuity.ValidID(workspaceID) {
		return file, workspacecontinuity.ErrInvalid
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, workspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return file, err
	}
	record, err := s.SnapshotContinuityWorkspace(ctx, q, canonical)
	if err != nil {
		return file, err
	}
	if record.ID != workspaceID {
		return file, workspacecontinuity.ErrInvalid
	}
	if err := spool.AddRecord(ctx, "workspace", "workspaces", record); err != nil {
		return file, err
	}
	return workspacecontinuity.Fingerprint{Path: workspace.WorkspaceConfigFile, Digest: workspacecontinuity.Digest(canonical), Bytes: int64(len(canonical))}, nil
}

// RestoreContinuityWorkspace inserts the reviewed canonical file's projection
// under the import receipt. The coordinator owns its private directory staging,
// physical parent membership and final registration. It must roll back this
// caller-owned transaction on ANY error and must not expose it as runnable.
// It is not CreateWorkspace, an upsert, template application or HQ designation.
func (s *SQLiteStore) RestoreContinuityWorkspace(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope,
	record workspacecontinuity.Record, canonical []byte, parentID string) (bool, error) {
	return s.RestoreContinuityWorkspaceAt(ctx, tx, scope, record, canonical, parentID, "")
}

// RestoreContinuityWorkspaceAt is RestoreContinuityWorkspace for a member the
// coordinator installs under a different folder name (the source's name was
// taken here). An empty folderSlug keeps the reviewed file's slug.
func (s *SQLiteStore) RestoreContinuityWorkspaceAt(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope,
	record workspacecontinuity.Record, canonical []byte, parentID, folderSlug string) (bool, error) {
	if tx == nil || scope.UserID != "local" || record.ID != scope.WorkspaceID {
		return false, workspacecontinuity.ErrInvalid
	}
	// Validate the immutable evidence first, then honor exact prior claims before
	// consulting mutable destination state (including deletion and later edits).
	if _, err := workspace.DecodeContinuityWorkspace(record, canonical); err != nil {
		return false, err
	}
	var descriptor workspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &descriptor); err != nil {
		return false, err
	}
	if descriptor.SQLMetadata == nil {
		return false, workspacecontinuity.ErrIncomplete
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "workspace", "workspaces", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	projection, err := workspace.ProjectContinuityWorkspace(record, canonical, parentID)
	if err != nil {
		return false, err
	}
	if parentID != "" {
		parentScope := scope
		parentScope.WorkspaceID = parentID
		if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, parentScope); err != nil {
			return false, err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=?`, scope.WorkspaceID).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	// Ensure every nested value is serializable before using the ordinary
	// structural converter, whose legacy logging fallbacks are not restore policy.
	if _, err := workspace.MarshalContinuityWorkspace(projection); err != nil {
		return false, err
	}
	adapter := &WorkspaceStoreAdapter{}
	canonicalWorkspace := adapter.toSessionWorkspace(projection.Workspace)
	canonicalWorkspace.Color = descriptor.SQLMetadata.Color
	canonicalWorkspace.OrderIndex = descriptor.SQLMetadata.OrderIndex
	if folderSlug != "" {
		canonicalWorkspace.FolderSlug = folderSlug
	}
	if err := insertWorkspace(ctx, tx, canonicalWorkspace); err != nil {
		if errors.Is(err, ErrDuplicateID) || errors.Is(err, ErrWorkspaceSlugConflict) {
			return false, workspacecontinuity.ErrConflict
		}
		return false, err
	}
	return true, nil
}
