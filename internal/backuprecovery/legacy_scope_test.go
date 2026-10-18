//go:build darwin || linux

package backuprecovery

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestOrdinaryLegacyScopeNeedsFreshConsentAndAttachmentRoundTripFencesIt(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	if _, err := s.store.db.ExecContext(t.Context(), `DELETE FROM continuity_attachments WHERE workspace_id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Connection != "not_configured" || provider.calls.Load() != 0 || credentials.uses.Load() != 0 {
		t.Fatal("legacy scope granted access or lost setup", status, err)
	}
	connectBackupServiceFixture(t, s)
	request := refreshBackupServiceFixture(t, s)
	state, err := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.Review(t.Context(), "local", localFixtureWorkspace, ReviewRequest{Generation: request.Generation, Revision: request.Revision, UseSavedSelection: true, Credential: *state.Credential})
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls, beforeUses := provider.calls.Load(), credentials.uses.Load()
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES (?,'native','ordinary',CURRENT_TIMESTAMP)`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace); !errors.Is(err, ErrUnavailable) {
		t.Fatal("explicit attachment reused absent-row encrypted authority", err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `DELETE FROM continuity_attachments WHERE workspace_id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true}); !errors.Is(err, ErrReviewExpired) {
		t.Fatal("attachment round trip reused an unsaved review", err)
	}
	if _, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("attachment round trip reused receipt", err)
	}
	if provider.calls.Load() != beforeCalls || credentials.uses.Load() != beforeUses {
		t.Fatal("lifecycle transition repeated access")
	}
}
