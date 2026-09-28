package server

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// automaticWorkspaces keeps the workspaces this installation runs background
// routines for. An imported workspace joins only after the user enables its
// routines here; without continuity every workspace keeps today's behavior.
func (b *ServerBuilder) automaticWorkspaces(ids []string) []string {
	if b.continuityLocal == nil {
		return ids
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admitted := make([]string, 0, len(ids))
	for _, id := range ids {
		if b.continuityLocal.CheckExecution(ctx, id, true) == nil {
			admitted = append(admitted, id)
		}
	}
	return admitted
}

// initializeContinuity composes portable workspace continuity once the
// folder store exists (Phase 18): the checkpoint preparation worker, the
// reviewed Import Folder coordinator, and upload-change tracking. Without a
// database it stays off and modern folders remain reviewable but not
// importable.
func (b *ServerBuilder) initializeContinuity(fileStore *workspace.FileStore) {
	if b.continuityLocal == nil || b.sessionStore == nil || fileStore == nil {
		return
	}
	db := b.sessionStore.DB()
	spool := filepath.Join(config.DefaultDataDir(), "continuity-spool")
	if err := os.MkdirAll(spool, 0o700); err != nil {
		logger.Warn("Workspace continuity preparation disabled: no private scratch directory", logger.Fields{"error": err.Error()})
		return
	}
	var uploadsBase func() string
	if b.sessionFilesStore != nil {
		uploadsBase = b.sessionFilesStore.BasePath
	}
	b.continuityWorker = &continuityprep.Worker{
		DB: db, Local: b.continuityLocal, Folders: fileStore, SpoolParent: spool, Gate: b.resetWork,
		Collector: &continuityprep.Collector{Sessions: session.NewSQLiteStore(db), Briefs: dailybrief.NewSQLiteStore(db),
			Assistants: personalassistant.NewSQLiteStore(db), UploadsBase: uploadsBase},
	}
	worker := b.continuityWorker
	local := b.continuityLocal
	if b.followUpService != nil {
		b.followUpService.SetAutomaticAdmission(func(ctx context.Context, workspaceID string) bool {
			return local.CheckExecution(ctx, workspaceID, true) == nil
		})
	}
	if b.sessionFilesStore != nil {
		// Uploads live outside the database; mark their owner dirty directly.
		b.sessionFilesStore.SetOnChange(func(sessionID string) { go worker.MarkSessionDirty(sessionID) })
	}
	if b.sessionHandler != nil {
		b.sessionHandler.SetContinuityImport(b.sessionFilesStore, worker.Kick)
		b.sessionHandler.SetContinuityStatus(worker)
	}
}

// wireContinuityTriggers lets an imported workspace's triggers catch up when
// its admission changes here, without a restart: its definitions load once it
// is imported, and its watches and webhook tokens start or stop with its
// routines. It runs once the trigger service exists.
func (b *ServerBuilder) wireContinuityTriggers() {
	if b.sessionHandler == nil || b.triggerService == nil || b.continuityLocal == nil {
		return
	}
	triggers := b.triggerService
	b.sessionHandler.SetContinuityAdmissionHook(func(workspaceID string) {
		if err := triggers.ReconcileWorkspace(workspaceID); err != nil {
			logger.Warn("Imported workspace triggers not reconciled", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		}
	})
}
