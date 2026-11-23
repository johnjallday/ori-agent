//go:build unix

package folderagent

import (
	"context"
	"testing"
	"time"
)

func TestDismissedMemberStaysVoluntaryOnALaterReport(t *testing.T) {
	h, id := guidedLook(t, build(t, "Work/Thesis/main.tex", "Book/outline.md"))
	h.svc.deps.DocumentsScope = func(context.Context, string) (DocumentsScope, error) { return DocumentsScope{Setup: true}, nil }
	ctx := t.Context()
	offer := func() *DocumentsOffer {
		o, e := h.svc.DocumentsOffer(ctx, "u1", "hq", "attempt", id)
		if e != nil || o == nil {
			t.Fatal(e)
		}
		return o
	}
	before := offer()
	in := DocumentsDraftInput{HQ: "hq", Attempt: "attempt", Report: before.Report, Candidate: before.Projects[0].Ref}
	if err := h.svc.DismissDocuments(ctx, "u1", id, in); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.deps.Store.Update(id, func(r *Record) error { r.Map.Updated = r.Map.Updated.Add(time.Minute); return nil }); err != nil {
		t.Fatal(err)
	}
	after := offer()
	if after.Decision == nil || after.Decision.State != "dismissed" || before.Report == after.Report || len(after.Projects) != 2 {
		t.Fatal(after)
	}
	if err := h.svc.DismissDocuments(ctx, "u2", id, in); err == nil {
		t.Fatal("foreign dismissal accepted")
	}
}
