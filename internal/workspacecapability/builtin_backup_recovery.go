package workspacecapability

import "github.com/johnjallday/ori-agent/internal/workspace"

const (
	BackupRecoveryDefinitionVersion = 1
	BackupRecoverySetupAdapterID    = "backup_recovery"
	BackupRecoveryAPIPrefix         = "backup-recovery"
	BackupRecoveryConsolePanelID    = "backup-recovery"
)

// BackupRecoveryDefinition is catalog metadata, not permission to inspect a
// folder, run restic or start monitoring. The ordinary reviewed template roster
// owns its advisor; the File Curator companion provisioner must not be used.
func BackupRecoveryDefinition() Definition {
	return Definition{
		ID: workspace.CapabilityBackupRecovery, Version: BackupRecoveryDefinitionVersion,
		Display: Display{
			Name: "Backup & Recovery", Tagline: "Observe backup evidence. Practice recovery.",
			Summary: "Connect an existing external-drive backup, review its metadata, and follow recovery guidance. Your backup application does the backups and restores.",
			Highlights: []string{
				"Selected folders on one supported Mac and one independent external drive.",
				"Restic 0.19.1 setup and backup execution happen outside Ori.",
				"Metadata access and periodic checks each need your explicit approval.",
				"Ori never installs software, runs a backup or restore, or modifies a drive.",
				"Published snapshots are not proof of backup success or recoverability.",
				"An external-drive-only backup is not off-site protection.",
			},
		},
		Requirements: Requirements{LocalFolderAccess: true, MaxInstallsPerWorkspace: 1, OrdinaryWorkspaceOnly: true},
		Setup:        SetupDescriptor{AdapterID: BackupRecoverySetupAdapterID},
		API:          APIDescriptor{Prefix: BackupRecoveryAPIPrefix},
		Station:      StationDescriptor{Title: "Backup & Recovery"},
		Console:      ConsoleDescriptor{PanelID: BackupRecoveryConsolePanelID, Tabs: []string{"overview", "history", "recovery", "settings"}, DefaultTab: "overview"},
		// No directory requirement, watcher, schedule, plugin or companion grant.
	}
}
