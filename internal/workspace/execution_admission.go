package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

var ErrWorkspaceExecutionInactive = errors.New("workspace execution requires local admission")

// ExecutionAdmission is installation-local policy, not a workspace flag. The
// automatic argument is selected by the compiled caller, never by portable data.
// The caller must separately retain its reset work permit for the whole run.
type ExecutionAdmission interface {
	CheckWorkspaceExecution(context.Context, string, bool) error
}

func RequireWorkspaceExecution(ctx context.Context, source Store, workspaceID string, automatic bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if admission, ok := source.(ExecutionAdmission); ok {
		return admission.CheckWorkspaceExecution(ctx, workspaceID, automatic)
	}
	// Stores without continuity composition retain native legacy behavior.
	return nil
}

// ContinuityGuard is the installation-local owner of discovery classification
// and execution admission for the ordinary workspace store. It never trusts a
// portable flag: both answers come from this installation's database.
type ContinuityGuard interface {
	// AdmitDiscovered reports whether a folder that carries a continuity
	// checkpoint already belongs to this installation. Unknown copies stay
	// invisible until a reviewed import registers them.
	AdmitDiscovered(workspaceID string) bool
	CheckWorkspaceExecution(ctx context.Context, workspaceID string, automatic bool) error
}

// HasContinuityCheckpoint reports whether folder holds the reserved continuity
// directory, complete or not. It does not follow a link at that name.
func HasContinuityCheckpoint(folder string) bool {
	info, err := os.Lstat(filepath.Join(folder, filepath.FromSlash(workspacecontinuity.Directory)))
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

func (s *FileStore) CheckWorkspaceExecution(ctx context.Context, workspaceID string, automatic bool) error {
	if !s.hasLocalConfig() {
		if s == nil || s.continuity == nil {
			return nil
		}
		if err := s.continuity.CheckWorkspaceExecution(ctx, workspaceID, automatic); err != nil {
			if errors.Is(err, workspacecontinuity.ErrExecutionInactive) {
				return ErrWorkspaceExecutionInactive
			}
			return err
		}
		return nil
	}
	release, err := s.localConfig.enterWork()
	if err != nil {
		return err
	}
	defer release()
	a, err := workspacecontinuity.NewLocalStore(s.localConfig.db).Attachment(ctx, workspaceID)
	if err != nil {
		return err
	}
	allowed := a.AllowsManual()
	if automatic {
		allowed = a.AllowsAutomatic()
	}
	if !allowed {
		return ErrWorkspaceExecutionInactive
	}
	return nil
}

func (s *SyncStore) CheckWorkspaceExecution(ctx context.Context, workspaceID string, automatic bool) error {
	if s.fileSync != nil {
		return s.fileSync.CheckWorkspaceExecution(ctx, workspaceID, automatic)
	}
	return RequireWorkspaceExecution(ctx, s.primary, workspaceID, automatic)
}

func (s *AgentSnapshotStore) CheckWorkspaceExecution(ctx context.Context, workspaceID string, automatic bool) error {
	return RequireWorkspaceExecution(ctx, s.Store, workspaceID, automatic)
}
