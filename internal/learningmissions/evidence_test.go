package learningmissions

import (
	"errors"
	"testing"
)

func resultFixture() (Instance, ResultProof) {
	i := testInstance("local", "mission")
	i.State = Active
	i.Progress = Progress{TicketID: "ticket", AttemptID: "attempt", RunID: "run", AcceptedAt: i.CreatedAt, InputDigest: digest([]byte("reviewed input"))}
	proof := ResultProof{OwnerID: "local", AttemptID: "attempt", InputDigest: i.Progress.InputDigest, Status: "succeeded", Text: "Exact result",
		Ref:       ResultRef{WorkspaceID: i.Definition.WorkspaceID, Incarnation: i.Definition.Incarnation, TicketID: "ticket", RunID: "run", ArtifactID: "artifact", Digest: digest([]byte("Exact result"))},
		StartedAt: i.CreatedAt + 1, FinishedAt: i.CreatedAt + 2}
	return i, proof
}

func TestResultVerificationRequiresCanonicalExactSuccessfulAttempt(t *testing.T) {
	i, proof := resultFixture()
	if err := VerifyResult("local", i, proof); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Instance, *ResultProof){
		"foreign owner":         func(_ *Instance, p *ResultProof) { p.OwnerID = "foreign" },
		"foreign workspace":     func(_ *Instance, p *ResultProof) { p.Ref.WorkspaceID = "foreign" },
		"reimported source":     func(_ *Instance, p *ResultProof) { p.Ref.Incarnation = "new-install" },
		"other ticket":          func(_ *Instance, p *ResultProof) { p.Ref.TicketID = "other" },
		"later run":             func(_ *Instance, p *ResultProof) { p.Ref.RunID = "later" },
		"automatic retry":       func(_ *Instance, p *ResultProof) { p.AttemptID = "retry" },
		"unknown start":         func(i *Instance, _ *ResultProof) { i.Progress.AttemptID = "" },
		"not accepted":          func(i *Instance, _ *ResultProof) { i.Progress.AcceptedAt = 0 },
		"failed":                func(_ *Instance, p *ResultProof) { p.Status = "failed" },
		"partial":               func(_ *Instance, p *ResultProof) { p.Status = "executing" },
		"result replaced":       func(_ *Instance, p *ResultProof) { p.Text = "Different bytes" },
		"missing artifact":      func(_ *Instance, p *ResultProof) { p.Ref.ArtifactID = "" },
		"unreviewed input":      func(_ *Instance, p *ResultProof) { p.InputDigest = digest([]byte("other input")) },
		"run before acceptance": func(_ *Instance, p *ResultProof) { p.StartedAt = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			i, p := resultFixture()
			change(&i, &p)
			if err := VerifyResult("local", i, p); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("unavailable proof counted: %v", err)
			}
		})
	}
}

func TestPracticeCountsInformedRejectionNotMasteryAndDeduplicatesIdentity(t *testing.T) {
	i, proof := resultFixture()
	i.Progress.Result = &proof.Ref
	r := Review{ID: "review", InstanceID: i.ID, Revision: i.Revision, Epoch: i.Epoch, Structure: DelegateInspect, Version: 1,
		Result: proof.Ref, Decision: "reject", Reason: "incorrect", Note: "The numbers do not match.",
		Guidance: GuidanceGuided, InputDigest: i.Progress.InputDigest, CreatedAt: proof.FinishedAt, ExpiresAt: proof.FinishedAt + 600000, DecidedAt: proof.FinishedAt + 1}
	evidence, err := PracticeEvidence("local", i, r)
	if err != nil || len(evidence) != 2 {
		t.Fatalf("informed rejection should count review practice: %+v %v", evidence, err)
	}
	r.ID = "repeated-receipt"
	again, err := PracticeEvidence("local", i, r)
	if err != nil || again[0].Key != evidence[0].Key || again[1].Key != evidence[1].Key {
		t.Fatal("new receipt for the same result created new credit identity")
	}
	r.Decision = ""
	if _, err := PracticeEvidence("local", i, r); err == nil {
		t.Fatal("mere inspection counted as a decision")
	}
}
