package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrExecutionInactive reports that a workspace exists locally but its local
// attachment does not permit the requested kind of work.
var ErrExecutionInactive = errors.New("workspace execution requires local admission")

// CheckExecution applies the installation-local admission policy used by the
// ordinary (non-private) workspace store. Attachment rows are written only by a
// reviewed import, discovery classification or native preparation, so a
// workspace without one is an existing native workspace from before
// continuity existed and keeps its previous behavior. A row that exists is
// authoritative: restoring, unreviewed and detached work is never runnable;
// an imported workspace allows manual work until it is explicitly activated.
func (s *LocalStore) CheckExecution(ctx context.Context, workspaceID string, automatic bool) error {
	if !ValidID(workspaceID) {
		return ErrInvalid
	}
	var state AttachmentState
	var provisioning bool
	err := s.db.QueryRowContext(ctx, `SELECT a.state,
		EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=a.workspace_id AND m.target='native-create')
		FROM continuity_attachments a WHERE a.workspace_id=?`, workspaceID).Scan(&state, &provisioning)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	attachment := Attachment{WorkspaceID: workspaceID, State: state, Provisioning: provisioning}
	allowed := attachment.AllowsManual()
	if automatic {
		allowed = attachment.AllowsAutomatic()
	}
	if !allowed {
		return ErrExecutionInactive
	}
	return nil
}

// AdmitDiscovered reports whether a folder carrying a checkpoint belongs to
// this installation: its canonical row is local and live, and no attachment
// marks it unreviewed, detached or still restoring. A copy from elsewhere, or
// a folder retained across an app-record reset, has no local row and stays
// invisible until a reviewed import completes. Errors fail closed.
func (s *LocalStore) AdmitDiscovered(workspaceID string) bool {
	if s == nil || !ValidID(workspaceID) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces w WHERE w.id=? AND w.owner_user_id='local' AND w.deleted_at IS NULL
		AND NOT EXISTS(SELECT 1 FROM continuity_attachments a WHERE a.workspace_id=w.id AND a.state IN ('unreviewed','detached','restoring'))`, workspaceID).Scan(&count)
	return err == nil && count == 1
}

// CheckWorkspaceExecution adapts CheckExecution to the workspace store guard.
func (s *LocalStore) CheckWorkspaceExecution(ctx context.Context, workspaceID string, automatic bool) error {
	return s.CheckExecution(ctx, workspaceID, automatic)
}

// ExecutionPolicy is the admission view a caller may display: whether saved
// work can be used by hand and whether background routines may run.
type ExecutionPolicy struct {
	Attached  bool            `json:"attached"`
	State     AttachmentState `json:"state"`
	Manual    bool            `json:"manual"`
	Automatic bool            `json:"automatic"`
	Imported  bool            `json:"imported"`
	Version   int64           `json:"version"`
}

// Policy reports the same decision as CheckExecution without failing.
func (s *LocalStore) Policy(ctx context.Context, workspaceID string) (ExecutionPolicy, error) {
	if !ValidID(workspaceID) {
		return ExecutionPolicy{}, ErrInvalid
	}
	a, err := readAttachment(ctx, s.db, workspaceID)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	if a.Version == 0 {
		return ExecutionPolicy{State: Native, Manual: true, Automatic: true}, nil
	}
	return ExecutionPolicy{Attached: true, State: a.State, Manual: a.AllowsManual(), Automatic: a.AllowsAutomatic(),
		Imported: a.State == ImportedInactive || a.State == ImportedActive, Version: a.Version}, nil
}
