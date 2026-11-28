package blueprintintake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestReviewedFileConsentPrecedesParsing(t *testing.T) {
	s, store, ws := newSourceTestService(t, []string{".txt"})
	p := ws.GetTemplateProvenance()
	p.IntakeRequirements[0].Sources.ReviewFiles = true
	ws.SetTemplateProvenance(p)
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	s.SetProviderResolver(func(context.Context, string) (ModelProvider, error) { return ModelProvider{Name: "Claude"}, nil })
	r := strings.NewReader("Private syllabus")
	if _, err := s.AddFile(t.Context(), ws.ID, "materials", "Syllabus.txt", r); !errors.Is(err, ErrConsentNeeded) || r.Len() != len("Private syllabus") {
		t.Fatal("file touched before consent", err)
	}
	if l, err := s.ListSources(ws.ID, "materials"); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	if _, err := s.AcceptConsent(t.Context(), ws.ID, "materials", "student"); err != nil {
		t.Fatal(err)
	}
	if f, err := s.AddFile(t.Context(), ws.ID, "materials", "Syllabus.txt", r); err != nil || f.Status != "parsed" {
		t.Fatal(f, err)
	}
}

func TestReviewedDeadlineRequiresQuotedDateInSource(t *testing.T) {
	req := workspace.IntakeRequirement{ProposalKinds: []string{"ticket"}}
	raw := "```json\n" + `{"items":[{"kind":"ticket","key":"lab","title":"Lab","due_at":"2027-12-14","source":{"source_id":"s","quote":"Lab due December 14, 2027."}},{"kind":"ticket","key":"final","title":"Final","due_at":"2027-12-15","source":{"source_id":"s","quote":"Final: TBD."}},{"kind":"ticket","key":"fake","title":"Invented","source":{"source_id":"s","quote":"Not in this source"}}]}` + "\n```"
	items, n, e := ParseProposalOutput(raw, req, "s", time.UTC, false)
	if e != nil {
		t.Fatal(e)
	}
	out := supportedFileProposals(items, "Lab due December 14, 2027.\nFinal: TBD.", &n)
	if len(out) != 2 || out[0].DueInput != "2027-12-14" || out[1].DueAt != nil || out[1].DueInput != "" || n.Unsupported != 1 {
		t.Fatal(out, n)
	}
	if _, _, err := ParseProposalOutput(raw+"\nmore", req, "s", time.UTC, false); err == nil {
		t.Fatal("accepted trailing prose")
	}
}
