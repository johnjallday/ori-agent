package folderagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestObservationRequiresUniqueVersionOverlappingCoverageAndNoOriAction(t *testing.T) {
	old := ReviewSource{Ref: "a", Path: "receipt.txt", Version: workspace.SourceVersion{Identity: "1:2", Links: 1, Modified: 3, Bytes: 4}}
	next := old
	next.Ref = "b"
	next.Path = "Maple Street/Invoices/receipt.txt"
	dirs := map[string]string{".": "root", "Maple Street/Invoices": "invoices"}
	before := ReviewCoverage{Authority: "auth", Files: []ReviewSource{old}, Directories: dirs, Reviewed: map[string]bool{"a": true}}
	after := ReviewCoverage{Authority: "auth", Files: []ReviewSource{next}, Directories: dirs, Reviewed: map[string]bool{}, At: time.Now()}
	d := observationChoices(&before, &after, "review", map[string]bool{})
	if len(d) != 1 || d[0].Intentional || d[0].Choice != next.Path || d[0].Outcome != "Observed on disk" || !after.Reviewed[next.Ref] {
		t.Fatalf("observation %+v", d)
	}
	for _, f := range []func(*ReviewCoverage){func(c *ReviewCoverage) { c.Files = append(c.Files, old) }, func(c *ReviewCoverage) { c.Directories = map[string]string{".": "root"} }, func(c *ReviewCoverage) { c.Files[0].Version.Identity = "" }, func(c *ReviewCoverage) { c.Files[0].Version.Links = 2 }, func(c *ReviewCoverage) { c.Files[0].Version.Modified++ }, func(c *ReviewCoverage) { c.Authority = "other" }} {
		c := after
		c.Files = slices.Clone(after.Files)
		f(&c)
		if len(observationChoices(&before, &c, "review", map[string]bool{})) != 0 {
			t.Fatal("uncertain observation was attributed")
		}
	}
	if len(observationChoices(&before, &after, "review", map[string]bool{"1:2": true})) != 0 {
		t.Fatal("Ori action re-counted")
	}
}
func TestPlacementSupportCountsIndependentConfirmedChoices(t *testing.T) {
	d := func(id, p string) OrganizingDecision {
		return OrganizingDecision{ID: id, Property: p, FileKey: id, Kind: "observed", Intentional: true, Choice: p + "/Invoices/i.pdf"}
	}
	a, b := d("a", "Maple Street"), d("b", "Oak Avenue")
	r := Record{OrganizationRuns: []OrganizationRun{{Authority: "root", Result: ReviewResult{Decisions: []OrganizingDecision{a, b}}}}}
	if explainPlacement(&r, "root", "Pine Street") != nil {
		t.Fatal("one bulk occasion counted twice")
	}
	r.OrganizationRuns = append(r.OrganizationRuns, OrganizationRun{Authority: "root", Result: ReviewResult{Decisions: []OrganizingDecision{b}}})
	p := explainPlacement(&r, "root", invoiceProperty("Invoice for Pine Street."))
	if p == nil || p.Destination != "Pine Street/Invoices" || !slices.Equal(p.Support, []string{"a", "b"}) {
		t.Fatalf("proposal %+v", p)
	}
	r.OrganizationRuns[1].Result.Decisions[0].Exception = true
	if explainPlacement(&r, "root", "Pine Street") != nil {
		t.Fatal("excluded receipt still supports")
	}
}

type reviewMemoryFolder string

func (f reviewMemoryFolder) GetFolderPath(string) (string, error) { return string(f), nil }
func TestConventionReflectsExactWorkspaceMemory(t *testing.T) {
	dir := t.TempDir()
	store := workspace.NewMemoryStore(reviewMemoryFolder(dir))
	s := &Service{deps: Deps{OrganizationMemory: store}}
	const id = "documents"
	const marker = "documents-test"
	empty, _, err := s.convention(id, marker)
	if err != nil || empty.Revision == "" || empty.Text != "" {
		t.Fatal(empty, err)
	}
	entry := workspace.MemoryEntry{Type: workspace.MemoryTypeFeedback, Date: "2026-01-15", Text: "Invoices go under the property.", Provenance: marker}
	_, err = store.AppendExact(id, empty.Revision, entry)
	if err != nil {
		t.Fatal(err)
	}
	saved, target, err := s.convention(id, marker)
	if err != nil || saved.Text != entry.Text {
		t.Fatal(saved, err)
	}
	entry.Text = "Keep invoices loose."
	if err := store.EditExact(id, target, &entry); err != nil {
		t.Fatal(err)
	}
	changed, target, err := s.convention(id, marker)
	if err != nil || changed.Text != entry.Text || changed.Revision == saved.Revision {
		t.Fatal(changed, err)
	}
	if err := store.EditExact(id, target, nil); err != nil {
		t.Fatal(err)
	}
	after, _, _ := s.convention(id, marker)
	if after.Text != "" {
		t.Fatal("forgotten wording survived")
	}
	if err := os.WriteFile(filepath.Join(dir, workspace.MemoryFileName), []byte(entry.Render()+"\n"+entry.Render()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ambiguous, _, _ := s.convention(id, marker)
	if !ambiguous.Conflict || ambiguous.Text != "" {
		t.Fatal("ambiguous memory used")
	}
}
