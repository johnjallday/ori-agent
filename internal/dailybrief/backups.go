package dailybrief

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
)

const EntityBackupObservation = "backup_observation"
const maxBackupSources = 20

var backupIdentity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var backupSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

// BackupSource is a stored-only read of the already resolved saved workspace
// scope. It exposes neither a metadata refresh nor any backup/restore action.
type BackupSource interface {
	BriefBackups(context.Context, string, WorkspaceScopeResult) (backuprecovery.SummaryRead, error)
}
type BackupSnapshot struct {
	Ref   SourceRef
	Facts backuprecovery.SummaryFacts
}
type BackupRead struct {
	Sources  []BackupSnapshot
	Complete bool
	Gaps     []string
}

func BackupRoute(ref SourceRef) string {
	if ref.EntityType != EntityBackupObservation || !backupIdentity.MatchString(ref.WorkspaceID) || !backupIdentity.MatchString(ref.EntityID) || !backupSlug.MatchString(ref.WorkspaceSlug) {
		return ""
	}
	return "/workspaces/" + url.PathEscape(ref.WorkspaceSlug) + "?panel=backup-recovery&tab=overview"
}
func ReadBackups(ctx context.Context, source BackupSource, owner string, scope WorkspaceScopeResult) BackupRead {
	out := BackupRead{Sources: []BackupSnapshot{}, Complete: true}
	if source == nil {
		return out
	}
	if owner != "local" {
		out.Complete = false
		out.Gaps = []string{"Backup & Recovery is unavailable"}
		return out
	}
	read, err := source.BriefBackups(ctx, owner, scope)
	if err != nil {
		out.Complete = false
		out.Gaps = []string{"Backup & Recovery observations could not be read"}
		return out
	}
	out.Complete = read.Complete && !read.Truncated && scope.Complete
	allowed := map[string]bool{}
	slugs := map[string]string{}
	for _, ws := range scope.Workspaces {
		if ws.Workspace != nil && ws.Workspace.ID == ws.ID {
			allowed[ws.ID] = true
			slugs[ws.ID] = ws.Slug
		}
	}
	seen := map[string]bool{}
	for _, facts := range read.Sources {
		if !allowed[facts.WorkspaceID] || !backupIdentity.MatchString(facts.WorkspaceID) || seen[facts.WorkspaceID] {
			out.Complete = false
			continue
		}
		if len(out.Sources) >= maxBackupSources {
			out.Complete = false
			break
		}
		generation := facts.Generation
		if generation == "" {
			generation = "setup"
		}
		if !backupIdentity.MatchString(generation) {
			out.Complete = false
			continue
		}
		ref := SourceRef{WorkspaceID: facts.WorkspaceID, WorkspaceSlug: slugs[facts.WorkspaceID], EntityType: EntityBackupObservation, EntityID: generation, Timestamp: facts.AsOf}
		// Ignore supplied routes and regenerate navigation from stable identity.
		facts.Route = BackupRoute(ref)
		if facts.Route == "" {
			out.Complete = false
		}
		out.Sources = append(out.Sources, BackupSnapshot{Ref: ref, Facts: facts})
		seen[facts.WorkspaceID] = true
	}
	for _, failure := range read.Failures {
		out.Complete = false
		if failure.WorkspaceID == "" || !allowed[failure.WorkspaceID] || !backupIdentity.MatchString(failure.WorkspaceID) || seen[failure.WorkspaceID] || len(out.Sources) >= maxBackupSources {
			continue
		}
		ref := SourceRef{WorkspaceID: failure.WorkspaceID, WorkspaceSlug: slugs[failure.WorkspaceID], EntityType: EntityBackupObservation, EntityID: "unavailable"}
		out.Sources = append(out.Sources, BackupSnapshot{Ref: ref, Facts: backuprecovery.SummaryFacts{WorkspaceID: failure.WorkspaceID, Connection: "unavailable", Health: backuprecovery.EvidenceHealth{Problems: []string{"stored_state_unavailable"}}, Route: BackupRoute(ref), RecoveryState: "unavailable"}})
		seen[failure.WorkspaceID] = true
	}
	sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].Ref.WorkspaceID < out.Sources[j].Ref.WorkspaceID })
	if !out.Complete {
		out.Gaps = append(out.Gaps, "Some scoped Backup & Recovery observations are unavailable or omitted")
	}
	for _, source := range out.Sources {
		if len(out.Gaps) >= 5 {
			break
		}
		switch source.Facts.Health.Observation {
		case "stale", "historical", "clock_suspect", "never_observed":
			out.Gaps = append(out.Gaps, "Backup & Recovery ["+source.Ref.WorkspaceID+"]: current metadata evidence is unavailable or stale")
		}
	}
	return out
}

// BackupProblem selects one primary current concern per configuration. All
// text is host-owned; expected pauses/disconnects never become backup failures.
func BackupProblem(source BackupSnapshot) (code, title string, rank int) {
	f := source.Facts
	if f.Connection == "not_configured" {
		return "setup_required", "Finish Backup & Recovery setup", 1
	}
	if f.Connection == "unavailable" {
		return "stored_state_unavailable", "Backup metadata is unavailable", 2
	}
	if f.Connection != "configured" {
		return "connection_review_required", "Review local backup metadata access", 2
	}
	codes := map[string]bool{}
	for _, code := range f.Health.Problems {
		codes[code] = true
	}
	if f.RecoveryState == "user_reported_failed" {
		codes["user_reported_recovery_failed"] = true
	}
	priorities := []struct {
		code, title string
		rank        int
	}{
		{"removal_pending", "Backup metadata access removal is pending", 4},
		{"destination_identity_mismatch", "Review the observed backup destination identity", 4},
		{"policy_unavailable", "Backup monitoring policy is unavailable", 3},
		{"clock_suspect", "Review clock-suspect backup evidence", 3},
		{"user_reported_recovery_failed", "Review your failed sample-recovery report", 3},
		{"backup_completion_overdue", "Review an overdue observed backup completion", 2},
		{"no_completed_snapshot", "No completed backup snapshot has been observed", 2},
		{"destination_space_low", "Review the last observed low backup space", 2},
		{"destination_disconnected_beyond_grace", "Review the last observed disconnected destination", 2},
		{"metadata_check_failed", "The last backup metadata check failed", 1},
		{"observation_stale", "Review stale stored backup evidence", 1},
		{"reader_unavailable", "Backup metadata reader is unavailable", 1},
		{"monitoring_review_required", "Review periodic metadata access", 1},
		{"completion_unavailable", "Backup completion time is unavailable", 1},
		{"observation_unavailable", "No current backup metadata has been observed", 1},
	}
	for _, candidate := range priorities {
		if !codes[candidate.code] {
			continue
		}
		if f.Monitoring == "paused" && (candidate.code == "observation_stale" || candidate.code == "metadata_check_failed" || candidate.code == "monitoring_review_required") {
			continue
		}
		return candidate.code, candidate.title, candidate.rank
	}
	return "", "", 0
}
func BackupDetail(source BackupSnapshot) string {
	detail := "Stored metadata, not proof of backup success or recovery."
	if source.Facts.LastSuccessfulRead != nil {
		detail = "Last successful metadata read " + source.Facts.LastSuccessfulRead.UTC().Format(time.RFC3339) + ". " + detail
	}
	if source.Facts.Monitoring == "paused" {
		detail += " Ori periodic checks are paused; external backups are independent."
	}
	return detail
}
func BackupEvidenceLine(source BackupSnapshot) string {
	f := source.Facts
	connection := map[string]string{"configured": "metadata connection observed", "not_configured": "not configured", "review_required": "local review required", "unavailable": "metadata unavailable"}[f.Connection]
	if connection == "" {
		connection = "metadata unavailable"
	}
	count := "snapshot count unavailable"
	if f.SnapshotCount != nil {
		count = fmt.Sprintf("%d stored snapshots observed", *f.SnapshotCount)
	}
	recovery := map[string]string{"not_tested": "recovery not tested", "user_reported_passed": "user reports sample recovery passed", "user_reported_failed": "user reports sample recovery failed", "review_required": "recovery report requires local context review"}[f.RecoveryState]
	if recovery == "" {
		recovery = "recovery evidence unavailable"
	}
	if f.RecoveryTestedAt != nil {
		recovery += " (" + f.RecoveryTestedAt.UTC().Format("2006-01-02") + ")"
	}
	observation := map[string]string{"current": "current stored observation", "stale": "stale stored observation", "historical": "historical observation only", "clock_suspect": "clock-suspect observation", "never_observed": "no observation"}[f.Health.Observation]
	if observation == "" {
		observation = "observation unavailable"
	}
	recency := map[string]string{"recent": "recent observed completion", "overdue": "overdue observed completion", "no_completed_snapshot": "no completed snapshot observed", "completion_unavailable": "completion time unavailable"}[f.Health.Recency]
	if recency == "" {
		recency = "completion recency unknown"
	}
	if f.LastCompletedAt != nil {
		recency += " (last stored completion " + f.LastCompletedAt.UTC().Format(time.RFC3339) + ")"
	}
	monitoring := map[string]string{"active": "Ori periodic metadata checks enabled", "paused": "Ori periodic checks paused", "manual_only": "manual-only metadata checks", "review_required": "periodic metadata access needs review", "unavailable": "Ori periodic reader unavailable"}[f.Monitoring]
	if monitoring == "" {
		monitoring = "Ori monitoring unavailable"
	}
	destination := map[string]string{"present": "destination present at last recorded check", "absent": "destination absent at last recorded check, not automatically a failed backup", "identity_mismatch": "destination identity needs review"}[f.Destination]
	if destination == "" {
		destination = "destination evidence unavailable"
	}
	capacity := map[string]string{"low": "low free space at the recorded check", "not_low": "free space not low at the recorded check", "stale": "stale capacity evidence", "historical": "historical capacity evidence"}[f.Health.Capacity]
	if capacity == "" {
		capacity = "capacity unavailable"
	}
	return "Backup & Recovery: " + connection + "; " + observation + "; " + recency + "; " + count + "; " + recovery + "; " + monitoring + "; " + destination + "; " + capacity + ". " + BackupDetail(source)
}
