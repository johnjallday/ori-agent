package learningmissions

import (
	"context"
	"errors"
	"testing"
	"time"
)

type preferenceSource struct {
	sourceStub
	incarnation string
}

func (f preferenceSource) LearningScope(_ context.Context, owner, id string) (string, error) {
	if owner != "local" || id != "workspace" {
		return "", ErrUnavailable
	}
	return f.incarnation, nil
}
func preferenceRequest(id string, revision int64) PreferencesRequest {
	return PreferencesRequest{MutationRequest: MutationRequest{OperationID: id, ExpectedRevision: revision}, Mode: "on_request", WorkspaceIDs: []string{"workspace"}, Observe: true, Guidance: GuidanceGuided, Effort: "small", ConsentVersion: DisclosureVersion}
}

func TestPreferenceMutationPinsScopeWithoutExecutorModelCallOrRetroactiveObservation(t *testing.T) {
	s, _ := testService(t)
	s.source = preferenceSource{sourceStub: sourceStub{missing: true}, incarnation: "incarnation"}
	d, _ := installDesigner(s)
	req := preferenceRequest("preferences", 0)
	req.FullGuidance = true
	out, err := s.UpdatePreferences(t.Context(), "local", req)
	if err != nil || out.Operation.Status != "succeeded" || d.calls.Load() != 0 {
		t.Fatalf("metadata-only preferences: %+v %v", out, err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if !owner.Preferences.FullGuidance || !owner.Preferences.Observes("workspace", "incarnation") || owner.Preferences.ObservationSince != s.now().UnixMilli() || owner.Preferences.ObservationRevision != owner.Revision {
		t.Fatalf("missing local consent: %+v", owner)
	}
	if rows, _ := s.store.Instances(t.Context(), "local"); len(rows) != 0 {
		t.Fatal("preferences manufactured a mission")
	}
	if _, err := s.UpdatePreferences(t.Context(), "local", req); err != nil {
		t.Fatalf("lost response did not recover: %v", err)
	}
	later, _ := s.store.Owner(t.Context(), "local")
	if later.Revision != owner.Revision {
		t.Fatal("replay wrote consent again")
	}
	req.OperationID, req.ExpectedRevision, req.Focus = "focus", owner.Revision, "Music"
	oldSince := owner.Preferences.ObservationSince
	now := s.now().Add(time.Hour)
	s.now = func() time.Time { return now }
	if _, err := s.UpdatePreferences(t.Context(), "local", req); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if owner.Preferences.ObservationSince != oldSince || owner.Preferences.ObservationRevision != 1 {
		t.Fatal("unrelated focus change silently reset observation")
	}
}

func TestPreferenceMutationRequiresFreshAutomaticModelAndOwnedScopes(t *testing.T) {
	s, _ := testService(t)
	s.source = preferenceSource{incarnation: "incarnation"}
	d, _ := installDesigner(s)
	req := preferenceRequest("automatic", 0)
	req.Mode = "automatic"
	if _, err := s.UpdatePreferences(t.Context(), "local", req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing model disclosure: %v", err)
	}
	req.ModelFingerprint = digest([]byte("stale model"))
	if _, err := s.UpdatePreferences(t.Context(), "local", req); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale model disclosure: %v", err)
	}
	req.ModelFingerprint, req.WorkspaceIDs = d.config.Fingerprint, []string{"foreign"}
	if _, err := s.UpdatePreferences(t.Context(), "local", req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign scope: %v", err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Revision != 0 || d.calls.Load() != 0 {
		t.Fatal("refused preferences enrolled or generated")
	}
	req.WorkspaceIDs = []string{"workspace"}
	if _, err := s.UpdatePreferences(t.Context(), "local", req); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if owner.Preferences.Mode != "automatic" || d.calls.Load() != 0 {
		t.Fatal("saving consent itself generated")
	}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("stale-tab", 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale tab overwrote consent: %v", err)
	}
}

func TestPreferenceReapprovalOfReplacedScopeStartsANewObservationWindow(t *testing.T) {
	s, db := testService(t)
	s.source = preferenceSource{incarnation: "old-local-source"}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("initial-consent", 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM learning_sources WHERE owner_id='local'`); err != nil {
		t.Fatal(err)
	}
	s.source = preferenceSource{incarnation: "new-local-source"}
	later := s.now().Add(time.Hour)
	s.now = func() time.Time { return later }
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Preferences.Observes("workspace", "new-local-source") {
		t.Fatal("old consent authorized a replaced source")
	}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("fresh-consent", owner.Revision)); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if !owner.Preferences.Observes("workspace", "new-local-source") || owner.Preferences.ObservationSince != later.UnixMilli() || owner.Preferences.ObservationRevision != owner.Revision {
		t.Fatal("new scope inherited the old observation window")
	}
}

func TestPreferenceRevocationAndReapprovalCannotReuseSameMillisecondConsent(t *testing.T) {
	s, _ := testService(t)
	s.source = preferenceSource{incarnation: "incarnation"}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("consent", 0)); err != nil {
		t.Fatal(err)
	}
	off := preferenceRequest("off", 1)
	off.Mode = "off"
	if _, err := s.UpdatePreferences(t.Context(), "local", off); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("new-consent", 2)); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Preferences.ObservationSince != s.now().UnixMilli() || owner.Preferences.ObservationRevision != 3 {
		t.Fatal("consent identity depended only on wall-clock precision")
	}
}

func TestClearPreferencesPreservesEarnedPracticeAndItsReview(t *testing.T) {
	s, db, instance := reviewFixture(t)
	prepared := prepareReview(t, s, instance)
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "decide", ExpectedRevision: 3}, InstanceRevision: 3,
		Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "clear-preferences", ExpectedRevision: 4}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_evidence`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("preferences erased practice: %d %v", count, err)
	}
	if review, err := s.store.Review(t.Context(), "local", prepared.Review.ID); err != nil || review.Decision != "reject" {
		t.Fatalf("preferences erased review: %+v %v", review, err)
	}
}

func TestPreferenceRevocationFencesHeldGenerationWithoutReplay(t *testing.T) {
	s, _ := testService(t)
	d, generation := installDesigner(s)
	entered, release := make(chan struct{}), make(chan struct{})
	d.generate = func(_ context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		close(entered)
		<-release
		return designReply(snapshot), nil
	}
	done := make(chan Outcome, 1)
	go func() { out, _ := s.Propose(t.Context(), "local", generation); done <- out }()
	<-entered
	owner, _ := s.store.Owner(t.Context(), "local")
	req := preferenceRequest("off", owner.Revision)
	req.Mode = "off"
	_, err := s.UpdatePreferences(t.Context(), "local", req)
	close(release)
	out := <-done
	if err != nil || out.Instance != nil || d.calls.Load() != 1 {
		t.Fatalf("off did not fence publication: %+v %v", out, err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if owner.Preferences.Observe || len(owner.Preferences.ScopeBindings) != 0 || owner.Preferences.ModelFingerprint != "" {
		t.Fatal("off retained ongoing consent")
	}
	generation.OperationID, generation.ExpectedRevision = "after-off", owner.Revision
	if _, err := s.Propose(t.Context(), "local", generation); err == nil || d.calls.Load() != 1 {
		t.Fatal("off generated again")
	}
}

func TestHistoryClearRetainsOnlyOngoingApprovedScopeIdentityNotUnapprovedHistory(t *testing.T) {
	s, db := testService(t)
	s.source = preferenceSource{incarnation: "incarnation"}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("consent", 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('unapproved','Unapproved',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Change(t.Context(), "local", 1, 1, false, func(m *Mutation) error { return m.BindSource("unapproved", "unapproved-source") }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear-history", ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	incarnation, err := s.store.SourceIncarnation(t.Context(), "local", "workspace")
	if err != nil || owner.Epoch != 2 || !owner.Preferences.Observes("workspace", incarnation) {
		t.Fatalf("history clear silently rewrote retained consent: %+v %v", owner, err)
	}
	if _, err := s.store.SourceIncarnation(t.Context(), "local", "unapproved"); err == nil {
		t.Fatal("unapproved historical identity survived clear")
	}
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "clear-preferences", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear-again", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SourceIncarnation(t.Context(), "local", "workspace"); err == nil {
		t.Fatal("revoked identity survived a later history clear")
	}
}

func TestClearPreferencesInvalidatesRetiredOffersButKeepsAcceptedContract(t *testing.T) {
	for _, accept := range []bool{false, true} {
		s, db := testService(t)
		out := propose(t, s)
		if accept {
			out, _ = s.Decide(t.Context(), "local", out.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "accept", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "accept"})
		} else {
			if _, err := db.ExecContext(t.Context(), `UPDATE learning_instances SET definition=json_set(definition,'$.version',999)`); err != nil {
				t.Fatal(err)
			}
		}
		owner, _ := s.store.Owner(t.Context(), "local")
		if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "clear-preferences", ExpectedRevision: owner.Revision}); err != nil {
			t.Fatal(err)
		}
		instance, err := s.store.Instance(t.Context(), "local", out.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if accept && (instance.State != Active || instance.Revision != out.Instance.Revision) {
			t.Fatal("clearing preferences rewrote accepted work")
		}
		if !accept && (instance.State != Invalidated || instance.Definition.Version != 999) {
			t.Fatal("retired offer blocked revocation or was rewritten")
		}
	}
}
