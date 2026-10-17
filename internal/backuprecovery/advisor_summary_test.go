//go:build darwin || linux

package backuprecovery

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
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
