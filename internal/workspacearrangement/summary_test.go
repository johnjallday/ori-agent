package workspacearrangement

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordSummary_ExplicitCountsAreNotInventoryTotals(t *testing.T) {
	e, _ := evidenceFixture(t, 10)
	o, err := e.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	one := Selection{IDs: []string{o.Samples[0].ID}}
	r, err := e.NewRecord(t.Context(), Draft{Version: 1, SourceRevision: o.Revision, Goal: "Discuss without changing anything", Unchanged: one,
		NeedsReview: []Recommendation{{Items: one, Reason: "Which version?"}, {Items: one, Reason: "Who is the client?"}}}, Provenance{Origin: "manual", SourceID: "home"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.Summarize()
	if err != nil || preview.ExplicitUnchanged != 1 || preview.ExplicitNeedsReview != 1 || len(preview.ReviewQuestions) != 2 || preview.Operations != 0 {
		t.Fatalf("incorrect explicit counts: %+v %v", preview, err)
	}
	if preview.ReviewQuestions[1].Reason != "Who is the client?" || preview.ReviewQuestions[1].Members != 1 {
		t.Fatal("review question lost")
	}
	preview.ReviewQuestions[0].Reason = "edited"
	if r.Draft.NeedsReview[0].Reason != "Which version?" {
		t.Fatal("preview aliases stored questions")
	}
}

func TestRecordSummary_BudgetIncludesStoredCoverageBeyondWireDraft(t *testing.T) {
	e, _ := evidenceFixture(t, 1)
	o, err := e.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.NewRecord(t.Context(), Draft{Version: 1, SourceRevision: o.Revision, Goal: strings.Repeat("<", 2000), Rationale: strings.Repeat("<", 2000), Unknowns: []string{strings.Repeat("<", 500), strings.Repeat("<", 500)}}, Provenance{Origin: "manual", SourceID: "home"})
	if err != nil {
		t.Fatal(err)
	}
	r.Coverage.Note = strings.Repeat("<", 2000)
	preview, err := r.Summarize()
	if err != nil {
		t.Fatal("valid stored coverage prevented history reopening", err)
	}
	raw, err := json.Marshal(preview)
	if err != nil || len(raw) <= MaxDraftBytes || len(raw) > MaxSummaryBytes {
		t.Fatal("wrong browser budget", len(raw), err)
	}
	preview.Unknowns[0] = "edited"
	if r.Draft.Unknowns[0] == "edited" {
		t.Fatal("preview aliases stored unknowns")
	}
}
