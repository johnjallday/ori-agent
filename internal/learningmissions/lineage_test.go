package learningmissions

import (
	"strings"
	"testing"
)

func originalProofFixture() (Definition, OriginalProof) {
	i, proof := resultFixture()
	proof.InputText, proof.Process = "reviewed input", PlainDraftProcess
	i.State, i.Revision, i.UpdatedAt = Completed, i.Revision+1, proof.FinishedAt+2
	i.Progress.Result, i.Progress.ReviewID = &proof.Ref, "prior-review"
	r := Review{ID: "prior-review", InstanceID: i.ID, Revision: i.Revision - 1, Epoch: i.Epoch, Structure: i.Definition.Structure, Version: 1,
		Result: proof.Ref, Decision: "use", Reason: "meets_scope", Guidance: i.Definition.Guidance, InputDigest: i.Progress.InputDigest,
		CreatedAt: proof.FinishedAt + 1, DecidedAt: proof.FinishedAt + 2, ExpiresAt: proof.FinishedAt + 600000}
	d := i.Definition
	d.Structure, d.OriginalReceipt = RepeatSuccess, r.ID
	return d, OriginalProof{Instance: i, Review: r, Result: proof}
}

func TestOriginalRequiresConsumedLocalReviewAndExactAvailableProcess(t *testing.T) {
	d, prior := originalProofFixture()
	if err := VerifyOriginal("local", d, prior); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Definition, *OriginalProof){
		"unreviewed": func(_ *Definition, p *OriginalProof) {
			p.Review.Decision, p.Review.Reason, p.Review.DecidedAt = "", "", 0
		},
		"actor label is not owner": func(_ *Definition, p *OriginalProof) { p.Instance.OwnerID = "foreign" },
		"unsupported structure":    func(d *Definition, _ *OriginalProof) { d.Structure = "invented" },
		"different receipt":        func(d *Definition, _ *OriginalProof) { d.OriginalReceipt = "forged" },
		"unconsumed instance":      func(_ *Definition, p *OriginalProof) { p.Instance.Progress.ReviewID = "other" },
		"same nonce revision":      func(_ *Definition, p *OriginalProof) { p.Instance.Revision = p.Review.Revision },
		"imported incarnation":     func(d *Definition, _ *OriginalProof) { d.Incarnation = "new-device" },
		"foreign workspace":        func(d *Definition, _ *OriginalProof) { d.WorkspaceID = "other" },
		"not useful":               func(_ *Definition, p *OriginalProof) { p.Review.Decision = "reject" },
		"process composition":      func(_ *Definition, p *OriginalProof) { p.Result.Process = RevisionDraftProcess },
		"changed old input":        func(_ *Definition, p *OriginalProof) { p.Result.InputText = "unrelated input" },
		"late replaced output":     func(_ *Definition, p *OriginalProof) { p.Result.Text = "a later result" },
		"backdated decision": func(_ *Definition, p *OriginalProof) {
			p.Review.CreatedAt, p.Review.DecidedAt = p.Result.StartedAt, p.Result.StartedAt
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, prior := originalProofFixture()
			change(&d, &prior)
			if err := VerifyOriginal("local", d, prior); err == nil {
				t.Fatal("invalid original qualified")
			}
		})
	}
	// An original rejected by its user may still be improved, not reused as a success.
	d.Structure, prior.Review.Decision = ImproveResult, "reject"
	if err := VerifyOriginal("local", d, prior); err != nil {
		t.Fatalf("rejected original cannot be improved: %v", err)
	}
}

func lineagePracticeFixture(structure StructureID) (Instance, Review) {
	i, proof := resultFixture()
	i.Definition.Structure, i.Definition.OriginalReceipt = structure, "prior-review"
	compiled, _ := Lookup(structure, 1)
	i.Definition.Steps = compiled.Steps
	original := proof.Ref
	original.TicketID, original.RunID, original.ArtifactID = "prior-ticket", "prior-run", "prior-artifact"
	i.Progress.Result, i.Progress.Original = &proof.Ref, &original
	i.Progress.Input, i.Progress.InputChange, i.Progress.OriginalInputDigest = "different input", "A different audience", normalizedInputDigest("old input")
	r := Review{ID: "new-review", InstanceID: i.ID, Revision: i.Revision, Epoch: i.Epoch, Structure: structure, Version: 1,
		Result: proof.Ref, Original: &original, OriginalReceipt: "prior-review", Decision: "reject", Reason: "missing_detail", Change: "It adds a time but still omits the location.",
		Guidance: i.Definition.Guidance, InputDigest: i.Progress.InputDigest, CreatedAt: proof.FinishedAt + 1, DecidedAt: proof.FinishedAt + 2, ExpiresAt: proof.FinishedAt + 600000}
	return i, r
}

func TestLineagePracticeRequiresDistinctIdentityComparisonAndMeaningfulReuseInput(t *testing.T) {
	for _, structure := range []StructureID{ImproveResult, RepeatSuccess} {
		i, review := lineagePracticeFixture(structure)
		evidence, err := PracticeEvidence("local", i, review)
		if err != nil || len(evidence) == 0 {
			t.Fatalf("review practice: %+v %v", evidence, err)
		}
		for _, e := range evidence {
			if e.Reused {
				t.Fatal("rejection claimed successful reuse")
			}
		}
		if structure == ImproveResult {
			review.Change = ""
			if _, err := PracticeEvidence("local", i, review); err == nil {
				t.Fatal("comparison without a change report")
			}
		} else {
			review.Decision = "use"
			evidence, err = PracticeEvidence("local", i, review)
			if err != nil || len(evidence) != 2 || !evidence[1].Reused {
				t.Fatalf("successful reuse: %+v %v", evidence, err)
			}
			i.Progress.Input = " old   input "
			if _, err := PracticeEvidence("local", i, review); err == nil {
				t.Fatal("whitespace-only change earned reuse")
			}
			i.Progress.Input = "different input"
			i.Progress.InputChange = strings.Repeat(" ", 10)
			if _, err := PracticeEvidence("local", i, review); err == nil {
				t.Fatal("byte differences alone claimed material difference")
			}
		}
		for _, field := range []string{"ticket", "run", "artifact"} {
			i, review := lineagePracticeFixture(structure)
			switch field {
			case "ticket":
				review.Result.TicketID = review.Original.TicketID
			case "run":
				review.Result.RunID = review.Original.RunID
			case "artifact":
				review.Result.ArtifactID = review.Original.ArtifactID
			}
			i.Progress.Result = &review.Result
			if _, err := PracticeEvidence("local", i, review); err == nil {
				t.Fatalf("replayed %s earned practice", field)
			}
		}
	}
}
