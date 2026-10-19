package backuprecovery

import (
	"context"
	"sort"
	"time"
)

// RecoveryView is owner-only stored evidence, not a restore tool or a manifest.
// Selected source paths are display data; none becomes an instruction/argument.
type RecoveryView struct {
	StateVersion
	ServerTime  time.Time          `json:"server_time"`
	Connection  string             `json:"connection"`
	Observation string             `json:"observation"`
	ObservedAt  *time.Time         `json:"observed_at,omitempty"`
	Sources     []string           `json:"source_paths"`
	Snapshots   []RecoverySnapshot `json:"snapshots"`
	Guide       SetupGuide         `json:"guide"`
	CanReport   bool               `json:"can_report"`
	Reports     []RecoveryReport   `json:"reports"`
	NoteChoices []RecoveryNote     `json:"note_choices"`
}
type RecoverySnapshot struct {
	ID                string     `json:"id"`
	Generation        string     `json:"generation"`
	Evidence          string     `json:"evidence"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	DeclaredPathCount int        `json:"declared_path_count"`
	ExclusionCount    int        `json:"exclusion_count"`
}

func ExternalRecoveryGuide() SetupGuide {
	setup := ExternalSetupGuide()
	return SetupGuide{
		Title: "Practice a sample recovery outside Ori", Provider: "restic", Version: "0.19.1",
		Boundary:      "Ori does not restore, repair, prune, inspect recovered contents or verify a recovery. A stored snapshot reference does not establish that its files are available now.",
		Prerequisites: []string{"Use the supported official restic 0.19.1 application outside Ori. Identify the exact repository and intended source yourself; unknown or stale metadata is not a fresh availability check.", "Have your separately stored password/key-recovery plan available in the external application. Do not paste secrets into Ori, chat or notes."},
		Steps: []GuideStep{
			{ID: "select-sample", Title: "1. Choose a snapshot and a known sample", Instructions: []string{"Use the labelled stored snapshot references and intended sources below as context only. Confirm the snapshot and sample in the external application; historical declarations and exclusions do not prove current file coverage.", "For unknown, stale, historical or clock-suspect evidence, first confirm availability outside Ori or explicitly request a separate metadata check in Overview. Opening this guide never performs that check."}},
			{ID: "confirm-destination", Title: "2. Confirm the external repository", Instructions: []string{"Using macOS and the external application, confirm the intended drive and repository. A matching display name or mount spelling does not establish identity. Ori will not mount, unlock or change the drive."}},
			{ID: "separate-target", Title: "3. Restore to a separate location outside Ori", Instructions: []string{"Follow the version-pinned official restore documentation in the external application. Select the intended snapshot and sample, then explicitly choose a separate empty recovery target yourself.", "Do not overwrite the working source or restore inside the backup repository as a test. Review target permissions and available space outside Ori. There is no Ori Restore, Repair or Prune action."}},
			{ID: "compare-sample", Title: "4. Open and compare the recovered sample yourself", Instructions: []string{"Use appropriate applications to open the recovered sample and compare it with known expected content or a known-good version. Check relevant format, version and permissions; an existing filename or matching size is not sufficient proof.", "Ori does not open or compare source or recovered file contents. One passed sample does not prove that all files or every version are recoverable."}},
			{ID: "key-recovery", Title: "5. Follow your separate key-recovery plan if needed", Instructions: []string{"Use securely retained recovery information in the external application. Ori cannot discover a forgotten password, recover a lost key or unlock a drive. Losing every usable password/key can make encrypted backups unrecoverable.", "Never put a password, recovery key, credential value or executable command into chat or a recovery report."}},
			{ID: "report-result", Title: "6. Record an honest dated user report", Instructions: []string{"Record only what you actually tested, the date and the configuration/snapshot association where known. Not tested, User reports passed and User reports failed are distinct; none means Ori verified.", "Reconfiguration makes reports about the previous setup historical. A reported pass is not complete protection or off-site evidence."}},
		},
		Limits:  []string{"External execution only. No backup, restore, integrity scan, repair, pruning, mounting, unlocking or file-content inspection is performed by this guide.", "External-drive-only backup is not off-site protection. Sample results do not establish exhaustive recoverability."},
		Sources: []GuideLink{setup.Sources[1], setup.Sources[3]},
	}
}

func (s *Service) GetRecovery(ctx context.Context, owner, workspace string) (RecoveryView, error) {
	release, err := s.enter()
	if err != nil {
		return RecoveryView{}, err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return RecoveryView{}, err
	}
	status := s.storedStatus(record)
	view := RecoveryView{StateVersion: revisionOf(record), ServerTime: status.ServerTime, Connection: status.Connection, Observation: status.Health.Observation, Sources: []string{}, Snapshots: []RecoverySnapshot{}, Guide: ExternalRecoveryGuide()}
	view.Reports = storedRecoveryReports(record)
	view.NoteChoices = recoveryNotes()
	if record.state == nil {
		return view, nil
	}
	view.CanReport = record.state.Configuration != nil && !pendingRemoval(record)
	if cfg := record.state.Configuration; cfg != nil {
		for _, source := range cfg.Sources {
			view.Sources = append(view.Sources, source.Path)
		}
	}
	observation := record.state.Observation
	if observation == nil {
		return view, nil
	}
	at := observation.ObservedAt
	view.ObservedAt = &at
	for _, snapshot := range observation.Snapshots {
		evidence := "stored"
		if !status.EvidenceCurrent {
			evidence = "historical"
		} else if status.Health.Observation != "current" {
			evidence = status.Health.Observation
		}
		item := RecoverySnapshot{ID: snapshot.ID, Generation: observation.Generation, Evidence: evidence, DeclaredPathCount: len(snapshot.DeclaredPaths), ExclusionCount: len(snapshot.Exclusions)}
		if snapshot.StartedAt.After(status.ServerTime.Add(5*time.Minute)) || snapshot.CompletedAt != nil && (snapshot.CompletedAt.After(status.ServerTime.Add(5*time.Minute)) || snapshot.CompletedAt.Before(snapshot.StartedAt)) {
			item.Evidence = "clock_suspect"
		} else {
			started := snapshot.StartedAt
			item.StartedAt = &started
			if snapshot.CompletedAt != nil {
				completed := *snapshot.CompletedAt
				item.CompletedAt = &completed
			}
		}
		view.Snapshots = append(view.Snapshots, item)
	}
	sort.Slice(view.Snapshots, func(i, j int) bool {
		a, b := view.Snapshots[i], view.Snapshots[j]
		if a.StartedAt == nil || b.StartedAt == nil {
			if a.StartedAt != nil {
				return true
			}
			if b.StartedAt != nil {
				return false
			}
		} else if !a.StartedAt.Equal(*b.StartedAt) {
			return a.StartedAt.After(*b.StartedAt)
		}
		return a.ID < b.ID
	})
	return view, nil
}
