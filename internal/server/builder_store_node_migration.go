package server

import (
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// migrateStoreNodes rewrites, once, every workspace that still carries a retired
// canvas store node into plain ticket file targets (see session.StoreNodeMigration).
// A workspace is already rewritten wherever it is decoded; this is the pass that
// writes the rewrite back so the workspace file and the SQLite row stop carrying the
// retired keys. It never stops startup: a failure is logged and recorded in the
// marker, and the next start tries again.
func (b *ServerBuilder) migrateStoreNodes(ws workspace.Store, fileStore *workspace.FileStore) {
	migration := session.StoreNodeMigration{
		DataDir:    config.DefaultDataDir(),
		Workspaces: ws,
		Folders:    fileStore,
	}
	if b.sessionStore != nil {
		migration.DB = b.sessionStore.DB()
	}
	if _, err := migration.Run(); err != nil {
		logger.Warn("Startup store node migration did not run", logger.Fields{"error": err.Error()})
	}
}
