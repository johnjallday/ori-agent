//go:build darwin || linux

package backuprecovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func coverageByWorkspace(t *testing.T, read CoverageRead) map[string]FolderCoverage {
	t.Helper()
	out := map[string]FolderCoverage{}
	for _, row := range read.Folders {
		out[row.WorkspaceID] = row
	}
	return out
}

func TestFolderCoverageMatchesSourceAncestryAndExportsNoPath(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, false)
	folders := []AdoptedFolder{
		{WorkspaceID: "song-inside", Path: "/Invented/Project/Song"},
		{WorkspaceID: "song-exact", Path: "/Invented/Project"},
		{WorkspaceID: "song-sibling", Path: "/Invented/Projects"},
		{WorkspaceID: "song-parent", Path: "/Invented"},
	}
	ids := []string{localFixtureWorkspace, "song-inside", "song-exact", "song-sibling", "song-parent"}
	read, err := s.ReadFolderCoverage(t.Context(), "local", ids, folders)
	if err != nil || !read.Complete || read.Truncated || len(read.Folders) != 4 {
		t.Fatal(read, err)
	}
	if len(read.Consoles) != 1 || read.Consoles[0] != (CoverageConsole{WorkspaceID: localFixtureWorkspace, Configured: true}) {
		t.Fatal(read.Consoles)
	}
	rows := coverageByWorkspace(t, read)
	for _, id := range []string{"song-inside", "song-exact"} {
		row := rows[id]
		if row.State != CoverageCovered || row.Reason != "completed_backup_observed" || row.Provider != "restic" || row.CoveringWorkspaceID != localFixtureWorkspace || row.LastCompletedAt == nil {
			t.Fatal(id, row)
		}
	}
	// A shared name prefix is not ancestry, and a folder that merely contains
	// a source is not itself covered.
	for _, id := range []string{"song-sibling", "song-parent"} {
		if row := rows[id]; row.State != CoverageNotCovered || row.Reason != "not_in_any_source" || row.CoveringWorkspaceID != "" || row.LastCompletedAt != nil {
			t.Fatal(id, row)
		}
	}
	encoded, _ := json.Marshal(read)
	cfg := inventedLocalConfiguration()
	for _, private := range []string{"/Invented", cfg.VolumePath, cfg.RepositoryPath, cfg.RepositoryID, cfg.Destination.VolumeID, cfg.Sources[0].Storage.VolumeID, cfg.Credential.VaultID, strings.Repeat("a", 64)} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private data crossed the coverage boundary", private)
		}
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("coverage read performed provider or credential IO")
	}
}

func TestFolderCoverageConfiguredButNeverCompletedIsNotCovered(t *testing.T) {
	s, _, _ := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s) // a real read that observed zero snapshots
	read, err := s.ReadFolderCoverage(t.Context(), "local", []string{localFixtureWorkspace, "song"}, []AdoptedFolder{{WorkspaceID: "song", Path: "/Invented/Project/Song"}})
	if err != nil || len(read.Folders) != 1 {
		t.Fatal(read, err)
	}
	if row := read.Folders[0]; row.State != CoverageNotCovered || row.Reason != "no_completed_backup_observed" || row.CoveringWorkspaceID != localFixtureWorkspace || row.LastCompletedAt != nil {
		t.Fatal(row)
	}
}

func TestFolderCoverageStaleEvidenceIsUnknown(t *testing.T) {
	s, _, _ := recoveryServiceFixture(t, false)
	now := time.Now()
	s.clock = func() time.Time { return now.Add(25 * time.Hour) }
	read, err := s.ReadFolderCoverage(t.Context(), "local", []string{localFixtureWorkspace, "song"}, []AdoptedFolder{{WorkspaceID: "song", Path: "/Invented/Project/Song"}})
	if err != nil || len(read.Folders) != 1 {
		t.Fatal(read, err)
	}
	if row := read.Folders[0]; row.State != CoverageUnknown || row.Reason != "evidence_stale" || row.LastCompletedAt != nil {
		t.Fatal("stale evidence became a protection claim", row)
	}
}

func TestFolderCoverageWithoutConfiguration(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	folders := []AdoptedFolder{{WorkspaceID: "song", Path: "/Invented/Project"}}
	// Installed but never configured: a console exists, no configuration does.
	read, err := s.ReadFolderCoverage(t.Context(), "local", []string{localFixtureWorkspace, "song"}, folders)
	if err != nil || !read.Complete || len(read.Consoles) != 1 || read.Consoles[0].Configured || read.Folders[0].State != CoverageNotCovered || read.Folders[0].Reason != "no_configuration" {
		t.Fatal(read, err)
	}
	// No Backup & Recovery workspace in scope at all.
	read, err = s.ReadFolderCoverage(t.Context(), "local", []string{"song"}, folders)
	if err != nil || !read.Complete || len(read.Consoles) != 0 || read.Folders[0].Reason != "no_configuration" {
		t.Fatal(read, err)
	}
	if provider.calls.Load() != 0 {
		t.Fatal("coverage probed a provider")
	}
}

func TestFolderCoverageUnreadableConfigurationIsUnknownNotUncovered(t *testing.T) {
	s, _, _ := recoveryServiceFixture(t, false)
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE backup_recovery_local SET ciphertext=zeroblob(28) WHERE workspace_id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	read, err := s.ReadFolderCoverage(t.Context(), "local", []string{localFixtureWorkspace, "song"}, []AdoptedFolder{{WorkspaceID: "song", Path: "/Elsewhere/Song"}})
	if err != nil || read.Complete || len(read.Folders) != 1 {
		t.Fatal(read, err)
	}
	if row := read.Folders[0]; row.State != CoverageUnknown || row.Reason != "configuration_unavailable" {
		t.Fatal(row)
	}
}

func TestFolderCoverageStaysInsideTheCallersScopeAndCap(t *testing.T) {
	s, _, _ := newBackupServiceFixture(t)
	read, err := s.ReadFolderCoverage(t.Context(), "local", []string{"song"}, []AdoptedFolder{{WorkspaceID: "song", Path: "/Invented/A"}, {WorkspaceID: "outside", Path: "/Invented/B"}})
	if err != nil || read.Complete || len(read.Folders) != 1 || read.Folders[0].WorkspaceID != "song" {
		t.Fatal("a folder outside the scope was evaluated", read, err)
	}
	if read.Consoles == nil || len(read.Consoles) != 0 {
		t.Fatal("a console outside the scope was read", read.Consoles)
	}
	ids := []string{}
	folders := []AdoptedFolder{}
	for i := 0; i < maxCoverageFolders+1; i++ {
		id := fmt.Sprintf("song-%02d", i)
		ids = append(ids, id)
		folders = append(folders, AdoptedFolder{WorkspaceID: id, Path: "/Invented/" + id})
	}
	read, err = s.ReadFolderCoverage(t.Context(), "local", ids, folders)
	if err != nil || !read.Truncated || read.Complete || len(read.Folders) != maxCoverageFolders || read.Folders[0].WorkspaceID != "song-00" {
		t.Fatal("cap was not explicit", read.Truncated, read.Complete, len(read.Folders), err)
	}
	unsafe, err := s.ReadFolderCoverage(t.Context(), "local", []string{"song"}, []AdoptedFolder{{WorkspaceID: "song", Path: "relative/../song"}})
	if err != nil || unsafe.Folders[0].State != CoverageUnknown || unsafe.Folders[0].Reason != "folder_unavailable" {
		t.Fatal(unsafe, err)
	}
	if _, err := s.ReadFolderCoverage(t.Context(), "LOCAL", ids, folders); err != ErrWorkspaceUnavailable {
		t.Fatal("folded actor widened access", err)
	}
}

func TestConfigurationCoverageNeedsCurrentCompletedEvidence(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	current := EvidenceHealth{Observation: "current", Recency: "recent"}
	cases := []struct {
		name          string
		status        Status
		state, reason string
	}{
		{"saved, never read", Status{Connection: "review_required"}, CoverageNotCovered, "no_completed_backup_observed"},
		{"current, nothing completed", Status{LastSuccessfulRead: &at, EvidenceCurrent: true, Health: current}, CoverageNotCovered, "no_completed_backup_observed"},
		{"current, completed", Status{LastSuccessfulRead: &at, EvidenceCurrent: true, LastCompletedAt: &at, Health: current}, CoverageCovered, "completed_backup_observed"},
		{"stale, completed", Status{LastSuccessfulRead: &at, EvidenceCurrent: true, LastCompletedAt: &at, Health: EvidenceHealth{Observation: "stale"}}, CoverageUnknown, "evidence_stale"},
		{"earlier configuration", Status{LastSuccessfulRead: &at, LastCompletedAt: &at, Health: EvidenceHealth{Observation: "historical"}}, CoverageUnknown, "evidence_unavailable"},
		{"clock suspect", Status{LastSuccessfulRead: &at, EvidenceCurrent: true, LastCompletedAt: &at, Health: EvidenceHealth{Observation: "clock_suspect"}}, CoverageUnknown, "evidence_unavailable"},
		{"removal pending", Status{RemovalPending: true, LastSuccessfulRead: &at, EvidenceCurrent: true, LastCompletedAt: &at, Health: current}, CoverageUnknown, "evidence_unavailable"},
	}
	for _, c := range cases {
		state, reason, completed := configurationCoverage(c.status)
		if state != c.state || reason != c.reason || (state == CoverageCovered) != (completed != nil) {
			t.Fatal(c.name, state, reason, completed)
		}
	}
}
