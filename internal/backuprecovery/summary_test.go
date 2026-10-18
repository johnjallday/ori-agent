//go:build darwin || linux

package backuprecovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func summaryWorkspaceFixture(t *testing.T, s *Service, id, owner, kind, status, caps string) {
	t.Helper()
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT OR IGNORE INTO users(id,created_at,updated_at) VALUES(?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,folder_slug,owner_user_id,kind,status,installed_capabilities_json,created_at,updated_at) VALUES (?,'private /Volumes/not-a-prompt',?,?, ?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, "summary-"+id, owner, kind, status, caps); err != nil {
		t.Fatal(err)
	}
}
func TestBackupSummaryRechecksExactOwnerScopeInstallAndExportsNoPrivateFields(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, false)
	request := reportRequestFixture(t, s)
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request); err != nil {
		t.Fatal(err)
	}
	ids := []string{localFixtureWorkspace, localFixtureWorkspace}
	for _, check := range []struct{ owner, kind, status, caps string }{
		{"other", "workspace", "active", localFixtureInstall}, {"LOCAL", "workspace", "active", localFixtureInstall}, {"local", "workspace", "archived", localFixtureInstall}, {"local", "group", "active", localFixtureInstall}, {"local", "workspace", "active", "[]"},
	} {
		id := uuid.NewString()
		summaryWorkspaceFixture(t, s, id, check.owner, check.kind, check.status, check.caps)
		ids = append(ids, id)
	}
	result, err := s.ReadBackupSummaries(t.Context(), "local", ids)
	if err != nil || !result.Complete || len(result.Sources) != 1 || len(result.Failures) != 0 {
		t.Fatal("scope widened or valid source disappeared", result, err)
	}
	source := result.Sources[0]
	if source.WorkspaceID != localFixtureWorkspace || source.RecoveryState != "user_reported_passed" || !source.EvidenceCurrent || source.SnapshotCount == nil || *source.SnapshotCount != 1 {
		t.Fatal(source)
	}
	encoded, _ := json.Marshal(result)
	cfg := inventedLocalConfiguration()
	for _, private := range []string{cfg.Sources[0].Path, cfg.VolumePath, cfg.RepositoryPath, cfg.Credential.VaultID, cfg.Credential.RecordID, strings.Repeat("a", 64), "untrusted-provider-exclusion", "private /Volumes/not-a-prompt", "sample_compared"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private data crossed summary boundary", private)
		}
	}
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET name='renamed --ignore-security /private' WHERE id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.ReadBackupSummaries(t.Context(), "local", []string{localFixtureWorkspace})
	if err != nil || renamed.Sources[0].Route != source.Route || renamed.Sources[0].Generation != source.Generation {
		t.Fatal("rename changed summary identity", err)
	}
	if _, err := s.ReadBackupSummaries(t.Context(), "LOCAL", ids); err != ErrWorkspaceUnavailable {
		t.Fatal("folded actor widened access", err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("summary read performed IO")
	}
}
func TestBackupSummaryDistinguishesUnknownEmptyAndOneUnreadableSource(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	inert := uuid.NewString()
	summaryWorkspaceFixture(t, s, inert, "local", "workspace", "active", localFixtureInstall)
	result, err := s.ReadBackupSummaries(t.Context(), "local", []string{localFixtureWorkspace, inert})
	if err != nil || !result.Complete || len(result.Sources) != 2 {
		t.Fatal(result, err)
	}
	for _, source := range result.Sources {
		if source.WorkspaceID == inert {
			if source.Connection != "not_configured" || source.SnapshotCount != nil {
				t.Fatal("unknown became zero/protection", source)
			}
		} else if source.SnapshotCount == nil || *source.SnapshotCount != 0 || source.Health.Recency != "no_completed_snapshot" {
			t.Fatal("empty became unavailable", source)
		}
	}
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE backup_recovery_local SET ciphertext=zeroblob(28) WHERE workspace_id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	partial, err := s.ReadBackupSummaries(t.Context(), "local", []string{localFixtureWorkspace, inert})
	if err != nil || partial.Complete || len(partial.Sources) != 1 || len(partial.Failures) != 1 || partial.Failures[0].Code != "stored_state_unavailable" {
		t.Fatal("one unreadable source hid another", partial, err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("empty/unavailable summary probed")
	}
}
func TestBackupSummaryConsoleRouteUsesCurrentCanonicalSlugNotWorkspaceID(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	for _, slug := range []string{"canonical-backup-console", "renamed-backup-console"} {
		if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET folder_slug=? WHERE id=?`, slug, localFixtureWorkspace); err != nil {
			t.Fatal(err)
		}
		read, err := s.ReadBackupSummaries(t.Context(), "local", []string{localFixtureWorkspace})
		if err != nil || len(read.Sources) != 1 || read.Sources[0].Route != "/workspaces/"+slug+"?panel=backup-recovery&tab=overview" || read.Sources[0].WorkspaceID != localFixtureWorkspace {
			t.Fatal(read, err)
		}
		advisor, err := s.ReadBackupAdvisor(t.Context(), "local", localFixtureWorkspace)
		if err != nil || advisor.Route != read.Sources[0].Route {
			t.Fatal(advisor, err)
		}
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("navigation probed")
	}
}

func TestBackupSummaryOutputCapDoesNotSpendSlotsOnExcludedWorkspaces(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	ids := []string{}
	for i := 0; i < maxSummaryWorkspaces+1; i++ {
		id := fmt.Sprintf("source-%02d", i)
		summaryWorkspaceFixture(t, s, id, "local", "workspace", "active", localFixtureInstall)
		ids = append(ids, id)
	}
	for i := 0; i < maxSummaryWorkspaces+1; i++ {
		id := fmt.Sprintf("excluded-%02d", i)
		summaryWorkspaceFixture(t, s, id, "other", "workspace", "active", localFixtureInstall)
		ids = append(ids, id)
	}
	result, err := s.ReadBackupSummaries(t.Context(), "local", ids)
	if err != nil || !result.Truncated || result.Complete || len(result.Sources) != maxSummaryWorkspaces || result.Sources[0].WorkspaceID != "source-00" || provider.calls.Load() != 0 {
		t.Fatal("cap widened, starved sources or probed", result, err)
	}
	empty, err := s.ReadBackupSummaries(t.Context(), "local", nil)
	if err != nil || !empty.Complete || len(empty.Sources) != 0 {
		t.Fatal("empty selected scope enumerated", empty, err)
	}
}
