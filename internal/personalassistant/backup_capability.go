package personalassistant

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

// SetBackupReader binds only the same scoped stored reader used by Today and
// Daily Brief. Opening readiness cannot install, grant access or start a check.
func (s *CapabilityService) SetBackupReader(reader dailybrief.BackupSource, workspaces dailybrief.WorkspaceSource) {
	if s != nil {
		s.backups = reader
		s.backupWorkspaces = workspaces
	}
}
func (s *CapabilityService) backupCapabilityCard(ctx context.Context, owner string, relationship *Projection) CapabilityCard {
	card := CapabilityCard{Key: "backup-recovery", Label: "Backup & Recovery", Status: CapabilityNotConfigured, Reason: "not_in_saved_workspace_scope", CanRead: "Stored backup metadata and explicitly user-reported recovery results in your saved workspace scope, not files or passwords.", CanPropose: "Explain evidence limits and link to the owning Backup & Recovery workspace.", RequiresConfirmation: "Connection, periodic metadata access and recovery reports use the owning workspace's separate user controls. External software alone runs backups and restores.", MappedWrite: false, ActionLabel: "Choose Backup & Recovery", ActionRoute: backupChooserRoute}
	scope := dailybrief.ResolveWorkspaceScope(s.backupWorkspaces, todayFollowUpConfig(relationship), owner)
	read := dailybrief.ReadBackups(ctx, s.backups, owner, scope)
	if len(read.Sources) == 0 {
		if !read.Complete {
			card.Status = CapabilityUnavailable
			card.Reason = "backup_source_unavailable"
			card.ActionLabel = "Review Backup & Recovery setup"
		}
		return card
	}
	selected := read.Sources[0]
	_, _, rank := dailybrief.BackupProblem(selected)
	for _, candidate := range read.Sources[1:] {
		if _, _, other := dailybrief.BackupProblem(candidate); other > rank {
			selected = candidate
			rank = other
		}
	}
	route := dailybrief.BackupRoute(selected.Ref)
	if route == "" {
		card.Status = CapabilityUnavailable
		card.Reason = "backup_route_unavailable"
		return card
	}
	card.ActionRoute = route
	card.ActionLabel = "Review stored backup evidence"
	card.Status = CapabilityAvailable
	card.Reason = "stored_metadata_only"
	switch selected.Facts.Connection {
	case "not_configured":
		card.Status = CapabilityNotConfigured
		card.Reason = "connection_not_configured"
		card.ActionLabel = "Finish Backup & Recovery setup"
	case "unavailable":
		card.Status = CapabilityUnavailable
		card.Reason = "stored_metadata_unavailable"
	default:
		if code, _, _ := dailybrief.BackupProblem(selected); code != "" {
			card.Reason = code
		}
	}
	if !read.Complete {
		card.Status = CapabilityUnavailable
		card.Reason = "backup_sources_incomplete"
	}
	return card
}
