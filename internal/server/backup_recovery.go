package server

import (
	"net/http"
	"os"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/backuprecoveryhttp"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pathselection"
)

// The operator supplies a binary location, never workspace/model data. A missing
// tool leaves the real stored-only service/guide available and the reader
// unavailable. No source, destination or Vault password is discovered at startup.
func (b *ServerBuilder) wireBackupRecovery() {
	b.backupRecoveryHandler = backuprecoveryhttp.NewHandler(nil, b.userProvider)
	if b.workspaceStore == nil || b.sessionStore == nil || b.configManager == nil || b.resetWork == nil {
		return
	}
	if b.pathSelectionStore == nil {
		b.pathSelectionStore = pathselection.NewStore()
	}
	service, err := backuprecovery.NewService(backuprecovery.ServiceOptions{
		Database: b.sessionStore.DB(), Secrets: b.configManager.SecretStore(), DataDir: config.DefaultDataDir(), WorkGate: b.resetWork,
		Credentials: b.vaultStore, Selections: b.pathSelectionStore, Picker: nativeFolderPicker{}, ExecutablePath: os.Getenv("ORI_BACKUP_RESTIC_EXECUTABLE"),
	})
	if err != nil {
		logger.Warn("Backup & Recovery runtime unavailable", logger.Fields{})
		return
	}
	b.backupRecoveryService = service
	b.backupRecoveryHandler = backuprecoveryhttp.NewHandler(service, b.userProvider)
	if b.chatHandler != nil {
		b.chatHandler.SetBackupAdvisorReader(service)
	}
}
func registerBackupRecoveryRoutes(mux *http.ServeMux, s *Server) {
	if s == nil || s.Handlers == nil || s.Handlers.BackupRecovery == nil {
		return
	}
	s.Handlers.BackupRecovery.Register(mux)
}
