//go:build darwin || linux

package backuprecovery

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBackupAdvisorFactsExcludePrivateAuthorityAndNeverProbe(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	facts, err := s.ReadBackupAdvisor(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"/Invented/Project", "/Volumes/Invented", "invented-vault", "invented-record", "record_revision", "grant_id", "repository_id", "review_token"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private context reached advisor", private)
		}
	}
	if _, err := s.ReadBackupAdvisor(t.Context(), "LOCAL", localFixtureWorkspace); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("case-folded actor read facts", err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("advisor summary executed a read/password lookup")
	}
}

func TestBackupAdvisorSharesHealthAndDatedAssertionsWithoutCreditingFutureEvidence(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, false)
	request := reportRequestFixture(t, s)
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request); err != nil {
		t.Fatal(err)
	}
	facts, err := s.ReadBackupAdvisor(t.Context(), "local", localFixtureWorkspace)
	summary, summaryErr := s.ReadBackupSummaries(t.Context(), "local", []string{localFixtureWorkspace})
	if err != nil || summaryErr != nil || facts.RecoveryState != "user_reported_passed" || facts.RecoveryTestedAt == nil || !facts.EvidenceCurrent || len(summary.Sources) != 1 || localDigest(facts.Health) != localDigest(summary.Sources[0].Health) {
		t.Fatal(facts, err, summaryErr)
	}
	now := s.clock()
	s.clock = func() time.Time { return now.Add(-48 * time.Hour) }
	future, err := s.ReadBackupAdvisor(t.Context(), "local", localFixtureWorkspace)
	if err != nil || future.RecoveryState != "review_required" || future.RecoveryTestedAt != nil || future.LastSuccessfulRead != nil || future.LastCompletedAt != nil {
		t.Fatal("future evidence credited", future, err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("advisor probed")
	}
}
