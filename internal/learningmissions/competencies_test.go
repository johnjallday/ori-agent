package learningmissions

import (
	"testing"
)

func TestCompetenciesKeepHistoricalSupportUnknownAndDeduplicateAttempts(t *testing.T) {
	out, seen := emptyCompetencies(), map[string]bool{}
	e := Evidence{Key: digest([]byte("old")), Competency: "evaluate", Guidance: GuidanceBasic, CreatedAt: 1}
	addCompetency(&out, e, seen)
	if out.Items[1].State != "support_unknown" || out.Items[1].Independent != 0 || out.Items[1].Attempts != 1 {
		t.Fatalf("legacy basic label became independence: %+v", out)
	}
	addCompetency(&out, e, seen)
	if out.Items[1].Attempts != 1 {
		t.Fatal("duplicate receipt/visit advanced practice")
	}
	e.Key, e.SupportVersion, e.Guidance = digest([]byte("guided")), GuidanceProtocolVersion, GuidanceGuided
	addCompetency(&out, e, seen)
	if out.Items[1].State != "practiced_with_guidance" {
		t.Fatalf("known guided practice: %+v", out)
	}
	e.Key, e.Guidance = digest([]byte("basic")), GuidanceBasic
	addCompetency(&out, e, seen)
	if out.Items[1].State != "practiced_independently" || out.Items[1].Independent != 1 || out.Items[1].Attempts != 3 {
		t.Fatalf("known independent practice: %+v", out)
	}
	e.Key, e.SupportVersion, e.Unavailable = digest([]byte("future-support")), GuidanceProtocolVersion+1, true
	addCompetency(&out, e, seen)
	if out.Items[1].Independent != 1 || out.Items[1].Unknown != 2 || len(out.Items[1].EvidenceKeys) != 3 {
		t.Fatalf("future support or source loss invented/erased practice: %+v", out)
	}
	e.Key, e.Competency, e.Reused = digest([]byte("reuse")), "reuse", true
	addCompetency(&out, e, seen)
	if out.Items[3].State != "reused_successfully" {
		t.Fatal("recorded reuse disappeared with unknown support")
	}
}

func TestPracticeEvidenceRequiresMatchingSupportProtocolAndCountsHintsConservatively(t *testing.T) {
	i, proof := resultFixture()
	i.Progress.Result = &proof.Ref
	i.Definition.Guidance, i.Definition.SupportVersion = GuidanceBasic, GuidanceProtocolVersion
	r := Review{ID: "review", InstanceID: i.ID, Revision: i.Revision, Epoch: i.Epoch, Structure: DelegateInspect, Version: 1,
		Result: proof.Ref, Decision: "reject", Reason: "incorrect", Guidance: GuidanceBasic, InputDigest: i.Progress.InputDigest,
		CreatedAt: proof.FinishedAt, ExpiresAt: proof.FinishedAt + 600000, DecidedAt: proof.FinishedAt + 1}
	if _, err := PracticeEvidence("local", i, r); err == nil {
		t.Fatal("missing support provenance accepted for a new protocol")
	}
	r.SupportVersion = GuidanceProtocolVersion
	rows, err := PracticeEvidence("local", i, r)
	if err != nil || rows[0].Guidance != GuidanceBasic || rows[0].SupportVersion != GuidanceProtocolVersion {
		t.Fatalf("known basic support: %+v %v", rows, err)
	}
	i.Progress.HintsShown, r.HintsShown = true, true
	rows, err = PracticeEvidence("local", i, r)
	if err != nil || rows[0].Guidance != GuidanceGuided {
		t.Fatalf("offered hints became independent practice: %+v %v", rows, err)
	}
	i.Definition.SupportVersion, r.SupportVersion = 0, 0
	rows, err = PracticeEvidence("local", i, r)
	if err != nil || rows[0].Guidance != GuidanceUnknown {
		t.Fatalf("legacy support was backfilled: %+v %v", rows, err)
	}
}

func TestCompetencyEvidenceInspectionIsExactReadOnlyAndRedactsMissingSources(t *testing.T) {
	s, db, instance := reviewFixture(t)
	s.source.(*reviewSource).proof.InputText = "reviewed input"
	prepared := prepareReview(t, s, instance)
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "decide", ExpectedRevision: 3}, InstanceRevision: 3,
		Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.Owner(t.Context(), "local")
	summary, _ := s.store.Competencies(t.Context(), "local")
	key := summary.Items[1].EvidenceKeys[0]
	for range 2 {
		view, err := s.EvidenceView(t.Context(), "local", key)
		if err != nil || view.ResultText != "Exact result" || view.InputText != "reviewed input" || view.Review.Decision != "reject" {
			t.Fatalf("exact read-only evidence: %+v %v", view, err)
		}
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if after.Revision != before.Revision {
		t.Fatal("inspection wrote a challenge or credit")
	}
	if _, err := s.EvidenceView(t.Context(), "foreign", key); err == nil {
		t.Fatal("foreign evidence disclosed")
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM learning_sources`); err != nil {
		t.Fatal(err)
	}
	view, err := s.EvidenceView(t.Context(), "local", key)
	if err == nil || view.ResultText != "" || view.Review.Note != "" {
		t.Fatal("unavailable source restored private review contents")
	}
}

func TestCompetencyReadsDoNotEnrollAndSourceRemovalDoesNotEraseEarnedFacts(t *testing.T) {
	s, db, instance := reviewFixture(t)
	prepared := prepareReview(t, s, instance)
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "decide", ExpectedRevision: 3}, InstanceRevision: 3,
		Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.store.Competencies(t.Context(), "local")
	if err != nil || before.Items[0].Guided != 1 || before.Items[1].Guided != 1 {
		t.Fatalf("persisted support projection: %+v %v", before, err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM learning_sources`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		after, err := s.store.Competencies(t.Context(), "local")
		if err != nil || after.Items[1].Attempts != before.Items[1].Attempts || after.Items[1].State != before.Items[1].State {
			t.Fatalf("source loss/visits erased or invented practice: %+v %v", after, err)
		}
	}
	foreign, err := s.store.Competencies(t.Context(), "foreign")
	if err != nil || foreign.Items[1].Attempts != 0 {
		t.Fatalf("foreign projection: %+v %v", foreign, err)
	}
	owner, _ := s.store.Owner(t.Context(), "foreign")
	if owner.Revision != 0 {
		t.Fatal("read enrolled an owner")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_evidence SET record=json_remove(record,'$.support_version')`); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.store.Competencies(t.Context(), "local")
	if err != nil || legacy.Items[1].State != "support_unknown" || legacy.Items[1].Attempts != 1 {
		t.Fatalf("legacy support guessed: %+v %v", legacy, err)
	}
}
