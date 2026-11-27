//go:build darwin || linux

package filejanitor

import (
	"errors"
	"testing"
)

func TestSingleSendNeverAdmitsTheRestOfThePicture(t *testing.T) {
	f := newSendFilingFixture(t, true)
	ctx := t.Context()
	send := SingleSend{CandidateID: candidateIDNamed(t, f, "lease.pdf"), ConnectionID: f.connection.ID}
	plan, err := f.s.PlanReview(ctx, "local", "ws-1", f.batch.ID, ReviewPlanQuery{SingleSend: &send})
	if err != nil || plan.Total != 1 || plan.SendCount != 1 || !f.at(f.downloads, "lease.pdf") {
		t.Fatalf("preview: %+v %v", plan, err)
	}
	req := FileRequest{ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision, PlanDigest: plan.PlanDigest, ClientKey: "single-send-000000001"}
	press := func() (FilingResult, error) { return f.s.FileReviewPlan(ctx, "local", "ws-1", f.batch.ID, req) }
	if _, err = press(); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatalf("single digest authorized batch? %v", err)
	}
	req.SingleSend = &send
	result, err := press()
	if err != nil || result.Sent != 1 || !f.at(f.documents, "lease.pdf") || !f.at(f.downloads, "invoice.pdf") || !f.at(f.downloads, "photo.png") {
		t.Fatalf("scoped send: %+v %v", result, err)
	}
	if again, err := press(); err != nil || !again.Replayed || again.FilingID != result.FilingID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	req.SingleSend = &SingleSend{CandidateID: candidateIDNamed(t, f, "invoice.pdf"), ConnectionID: f.connection.ID}
	if _, err := press(); !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("reused key with changed intent: %v", err)
	}
}

func TestSingleSendRequiresTheReviewedConnectionAndKind(t *testing.T) {
	f := newSendFilingFixture(t, true)
	ctx := t.Context()
	preview := func(name string) (ReviewPlan, *SingleSend, error) {
		send := &SingleSend{CandidateID: candidateIDNamed(t, f, name), ConnectionID: f.connection.ID}
		p, err := f.s.PlanReview(ctx, "local", "ws-1", f.batch.ID, ReviewPlanQuery{SingleSend: send})
		return p, send, err
	}
	if _, _, err := preview("photo.png"); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatalf("wrong kind: %v", err)
	}
	p, send, err := preview("lease.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.TurnOffConnection("local", "ws-2", send.ConnectionID); err != nil {
		t.Fatal(err)
	}
	// Same kind and same workspace, but a different connection identity.
	f.mustConnect("ws-1", "ws-2", "documents")
	_, err = f.s.FileReviewPlan(ctx, "local", "ws-1", f.batch.ID, FileRequest{SingleSend: send, ExpectedRevision: p.Revision, ExpectedGroupRevision: p.GroupRevision, PlanDigest: p.PlanDigest, ClientKey: "revoked-send-00000001"})
	if !errors.Is(err, ErrReviewPlanChanged) || !f.at(f.downloads, "lease.pdf") || f.at(f.documents, "lease.pdf") {
		t.Fatalf("substituted after revoke: %v", err)
	}
	state, err := f.s.store.LoadScanState("ws-1")
	if err != nil || len(state.Filings) != 0 || len(state.Actions) != 0 {
		t.Fatalf("revoked handoff journaled: %v", err)
	}
}
