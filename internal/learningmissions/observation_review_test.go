package learningmissions

import (
	"testing"
	"time"
)

func observedResultFixture(t *testing.T) (*Service, *observedSourceStub, ObservedWork) {
	t.Helper()
	s, source := observationFixture(t)
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("consent", 0)); err != nil {
		t.Fatal(err)
	}
	work := observedAttempt(t, s, source, "start", "run")
	return s, source, work
}

func observedAttempt(t *testing.T, s *Service, source *observedSourceStub, token, run string) ObservedWork {
	t.Helper()
	grant, _ := s.CaptureObservationConsent(t.Context(), "local", "workspace")
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", token, grant); err != nil {
		t.Fatal(err)
	}
	work := source.bound[len(source.bound)-1]
	if err := s.ReserveObservedRun(t.Context(), "local", observationRun(work, token, run)); err != nil {
		t.Fatal(err)
	}
	op, err := s.store.Operation(t.Context(), "local", work.ID)
	if err != nil {
		t.Fatal(err)
	}
	work = *op.Observation
	input := source.input
	if input == "" {
		input = "reviewed input"
	}
	source.proof = ResultProof{OwnerID: "local", AttemptID: token, InputDigest: work.InputDigest, InputText: input, Process: PlainDraftProcess, Text: "Exact ordinary result", Status: "succeeded",
		StartedAt: work.CreatedAt + 1, FinishedAt: work.CreatedAt + 2, Ref: ResultRef{WorkspaceID: work.WorkspaceID, Incarnation: work.Incarnation, TicketID: work.TicketID, RunID: run, ArtifactID: "artifact-" + run, Digest: digest([]byte("Exact ordinary result"))}}
	s.now = func() time.Time { return time.UnixMilli(work.CreatedAt + 3) }
	return work
}

func decideObserved(t *testing.T, s *Service, work ObservedWork, prefix string, decisions ...string) (Outcome, ObservedReviewRequest) {
	t.Helper()
	owner, _ := s.store.Owner(t.Context(), "local")
	prepared, err := s.ReviewObserved(t.Context(), "local", work.ID, ObservedReviewRequest{MutationRequest: MutationRequest{OperationID: prefix + "-prepare", ExpectedRevision: owner.Revision}, ObservedRevision: work.Revision, Action: "prepare"})
	if err != nil || prepared.ResultText != "Exact ordinary result" || prepared.Review == nil || prepared.Observation == nil {
		t.Fatalf("ordinary exact review: %+v %v", prepared, err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	decision := "reject"
	if len(decisions) > 0 {
		decision = decisions[0]
	}
	request := ObservedReviewRequest{MutationRequest: MutationRequest{OperationID: prefix + "-submit", ExpectedRevision: owner.Revision}, ObservedRevision: prepared.Observation.Revision,
		Action: "submit", ReviewID: prepared.Review.ID, Decision: decision, Reason: "incorrect"}
	out, err := s.ReviewObserved(t.Context(), "local", work.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	return out, request
}

func TestObservedReviewCreditsExactOrdinaryWorkWithoutMissionOrKnownGuidance(t *testing.T) {
	s, _, work := observedResultFixture(t)
	out, request := decideObserved(t, s, work, "review")
	if out.Instance != nil || out.Review.InstanceID != "" || out.Review.ObservationID != work.ID || out.Review.Guidance != GuidanceUnknown {
		t.Fatalf("synthetic enrollment/support: %+v", out)
	}
	if _, err := s.ReviewObserved(t.Context(), "local", work.ID, request); err != nil {
		t.Fatal("lost response did not recover", err)
	}
	status, err := s.Status(t.Context(), "local")
	if err != nil || len(status.Instances) != 0 || len(status.Observations) != 0 || status.Competencies.Items[1].Attempts != 1 || status.Competencies.Items[1].State != "support_unknown" {
		t.Fatalf("practice projection: %+v %v", status, err)
	}
	key := status.Competencies.Items[1].EvidenceKeys[0]
	before := status.Owner.Revision
	view, err := s.EvidenceView(t.Context(), "local", key)
	if err != nil || view.InputText != "reviewed input" || view.ResultText != "Exact ordinary result" || view.Review.Decision != "reject" {
		t.Fatalf("ordinary evidence inspection: %+v %v", view, err)
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if after.Revision != before {
		t.Fatal("inspection created authority")
	}
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "off", ExpectedRevision: before}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvidenceView(t.Context(), "local", key); err != nil {
		t.Fatal("revoking observation erased earned inspection", err)
	}
}

func TestObservedUsefulOriginalUsesConsumedClaimWithoutAnInstance(t *testing.T) {
	s, source, work := observedResultFixture(t)
	out, _ := decideObserved(t, s, work, "original", "use")
	op, _ := s.store.Operation(t.Context(), "local", work.ID)
	prior := OriginalProof{Observation: &op, Review: *out.Review, Result: source.proof}
	definition := Definition{Structure: RepeatSuccess, WorkspaceID: work.WorkspaceID, Incarnation: work.Incarnation, OriginalReceipt: out.Review.ID}
	if err := VerifyOriginal("local", definition, prior); err != nil {
		t.Fatal("useful ordinary process was lost", err)
	}
	if prior.Instance.ID != "" {
		t.Fatal("manufactured a mission")
	}
	op.Status = "pending"
	if err := VerifyOriginal("local", definition, prior); err == nil {
		t.Fatal("unconsumed claim became an original")
	}
	op.Status = "succeeded"
	prior.Review.Decision = "reject"
	if err := VerifyOriginal("local", definition, prior); err == nil {
		t.Fatal("rejected ordinary result became useful reuse")
	}
	definition.Structure = ImproveResult
	if err := VerifyOriginal("local", definition, prior); err != nil {
		t.Fatal("rejected ordinary result could not be improved", err)
	}
}

func TestObservedReviewRetriesAndWhitespaceDoNotAdvanceDistinctPractice(t *testing.T) {
	s, source, work := observedResultFixture(t)
	decideObserved(t, s, work, "first")
	for n, input := range []string{"reviewed input", "reviewed   input", "different reviewed input"} {
		source.input = input
		token := []string{"retry", "whitespace", "new-input"}[n]
		work = observedAttempt(t, s, source, token, "run-"+token)
		decideObserved(t, s, work, token)
		progress, err := s.store.Competencies(t.Context(), "local")
		want := 1
		if n == 2 {
			want = 2
		}
		if err != nil || progress.Items[1].Attempts != want || progress.Items[1].Independent != 0 {
			t.Fatalf("retry practice: %+v %v", progress, err)
		}
	}
}

func TestObservedReviewRevocationAndFailedCreditCannotConsumeTheDecision(t *testing.T) {
	s, _, work := observedResultFixture(t)
	owner, _ := s.store.Owner(t.Context(), "local")
	prepared, err := s.ReviewObserved(t.Context(), "local", work.ID, ObservedReviewRequest{MutationRequest: MutationRequest{OperationID: "prepare", ExpectedRevision: owner.Revision}, ObservedRevision: work.Revision, Action: "prepare"})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	req := ObservedReviewRequest{MutationRequest: MutationRequest{OperationID: "submit", ExpectedRevision: owner.Revision}, ObservedRevision: prepared.Observation.Revision, Action: "submit", ReviewID: prepared.Review.ID, Decision: "use", Reason: "meets_scope"}
	if _, err := s.store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_observed_credit BEFORE INSERT ON learning_evidence BEGIN SELECT RAISE(ABORT,'receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewObserved(t.Context(), "local", work.ID, req); err == nil {
		t.Fatal("failed credit committed review")
	}
	receipt, _ := s.store.Review(t.Context(), "local", req.ReviewID)
	claim, _ := s.store.Operation(t.Context(), "local", work.ID)
	if receipt.Decision != "" || claim.Status != "pending" || claim.Observation.Revision != prepared.Observation.Revision {
		t.Fatal("partial ordinary decision committed")
	}
	if _, err := s.store.db.ExecContext(t.Context(), `DROP TRIGGER fail_observed_credit`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "off", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("again", owner.Revision)); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	req.ExpectedRevision = owner.Revision
	if _, err := s.ReviewObserved(t.Context(), "local", work.ID, req); err == nil {
		t.Fatal("new consent revived an old nonce")
	}
	visible := s.VisibleOutcome(t.Context(), "local", prepared)
	if visible.Review != nil || visible.ResultText != "" || visible.ObservedInput != "" || visible.Observation != nil {
		t.Fatal("revoked operation restored private contents")
	}
	progress, _ := s.store.Competencies(t.Context(), "local")
	if progress.Items[1].Attempts != 0 {
		t.Fatal("revoked/faulted review counted")
	}
}

func TestObservedReviewRejectsUnprovenCanonicalIdentity(t *testing.T) {
	s, source, work := observedResultFixture(t)
	original := source.proof
	for name, change := range map[string]func(*ResultProof){
		"owner": func(p *ResultProof) { p.OwnerID = "foreign" }, "workspace": func(p *ResultProof) { p.Ref.WorkspaceID = "foreign" },
		"incarnation": func(p *ResultProof) { p.Ref.Incarnation = "imported" }, "ticket": func(p *ResultProof) { p.Ref.TicketID = "other" },
		"run": func(p *ResultProof) { p.Ref.RunID = "latest" }, "start": func(p *ResultProof) { p.AttemptID = "copied" },
		"input": func(p *ResultProof) { p.InputText = "unreviewed" }, "bytes": func(p *ResultProof) { p.Text = "changed" },
		"partial": func(p *ResultProof) { p.Status = "executing" }, "unknown time": func(p *ResultProof) { p.StartedAt = 0 },
		"old execution": func(p *ResultProof) { p.StartedAt = work.CreatedAt - 1 }, "other process": func(p *ResultProof) { p.Process = RevisionDraftProcess },
	} {
		t.Run(name, func(t *testing.T) {
			source.proof = original
			change(&source.proof)
			owner, _ := s.store.Owner(t.Context(), "local")
			if _, err := s.ReviewObserved(t.Context(), "local", work.ID, ObservedReviewRequest{MutationRequest: MutationRequest{OperationID: "invalid", ExpectedRevision: owner.Revision}, ObservedRevision: work.Revision, Action: "prepare"}); err == nil {
				t.Fatal("unproven work counted")
			}
		})
	}
	progress, _ := s.store.Competencies(t.Context(), "local")
	if progress.Items[1].Attempts != 0 {
		t.Fatal("invalid proof recorded practice")
	}
}
