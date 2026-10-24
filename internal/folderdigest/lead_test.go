package folderdigest

import (
	"testing"
	"time"
)

// documentsLike is a root with one Go module beside the given loose files, as
// the digest scan sees it.
func documentsLike(now time.Time, loose int, kinds []string) Result {
	extensions := map[string]int{}
	for i := range loose {
		extensions[kinds[i%len(kinds)]]++
	}
	goMod, _ := MatchMarker("go.mod", false)
	root := Candidate{Name: "Documents", IsRoot: true, FileCount: loose + 6, LooseFiles: loose, LooseExtensions: extensions,
		NewestModTime: now.Add(-48 * time.Hour)}
	module := Candidate{Name: "widget", RelPath: "widget", Marker: &goMod, FileCount: 6, DominantExtension: ".go", DominantShare: 1,
		NewestModTime: now.Add(-96 * time.Hour)}
	return Result{Candidates: []Candidate{root, module}}
}

var sixKinds = []string{".pdf", ".png", ".zip", ".dmg", ".csv", ".mov"}

// FR12: the tidy hire leads when the root meets the dump rule (20 loose files of
// 5 kinds); otherwise the project does; swapping the evidence swaps the lead.
func TestMixedHireLead_TheDumpRuleDecidesWhoLeads(t *testing.T) {
	now := time.Now()
	lead, ok := MixedHireLead(documentsLike(now, 23, sixKinds), now, nil)
	if !ok || !lead.TidyLeads || lead.Project.Name != "widget" || lead.LooseFiles != 23 || lead.LooseKinds != 6 {
		t.Fatalf("a dump beside a Go module: %+v ok=%t", lead, ok)
	}
	lead, ok = MixedHireLead(documentsLike(now, 5, sixKinds), now, nil)
	if !ok || lead.TidyLeads || lead.Project.Name != "widget" || lead.LooseFiles != 5 {
		t.Fatalf("a few loose files beside a Go module: %+v ok=%t", lead, ok)
	}
	// Twenty files of only two kinds are not a dump: the project leads.
	if lead, ok = MixedHireLead(documentsLike(now, 24, sixKinds[:2]), now, nil); !ok || lead.TidyLeads {
		t.Fatalf("twenty-four files of two kinds: %+v ok=%t", lead, ok)
	}
	// Whether the digest calls the root a tidy depends on the same rule.
	if !IsInbox(documentsLike(now, 23, sixKinds), now, nil) || IsInbox(documentsLike(now, 5, sixKinds), now, nil) {
		t.Fatal("only the dump beside a project is an inbox folder")
	}
}

func TestMixedHireLead_HasNoLeadWhereAnotherCardOwnsTheFolder(t *testing.T) {
	now := time.Now()
	// Nothing loose, nothing to tidy beside the project.
	if _, ok := MixedHireLead(documentsLike(now, 0, sixKinds), now, nil); ok {
		t.Fatal("a root with nothing loose has no tidy to offer")
	}
	// A root the user declined.
	if _, ok := MixedHireLead(documentsLike(now, 23, sixKinds), now, func(c Candidate) bool { return c.IsRoot }); ok {
		t.Fatal("a declined root has no lead")
	}
	// A root that is itself a software project keeps its own card.
	repo := documentsLike(now, 23, sixKinds)
	goMod, _ := MatchMarker("go.mod", false)
	repo.Candidates[0].Marker = &goMod
	if _, ok := MixedHireLead(repo, now, nil); ok {
		t.Fatal("a repository is not a mixed folder")
	}
	// A collection keeps the group proposal.
	collection := documentsLike(now, 23, sixKinds)
	tex, _ := MatchMarker("main.tex", false)
	bib, _ := MatchMarker("x.bib", false)
	collection.Candidates = append(collection.Candidates,
		Candidate{Name: "Thesis", RelPath: "Thesis", Marker: &tex, FileCount: 4, NewestModTime: now},
		Candidate{Name: "Refs", RelPath: "Refs", Marker: &bib, FileCount: 4, NewestModTime: now})
	if _, ok := MixedHireLead(collection, now, nil); ok {
		t.Fatal("a collection is not a mixed folder")
	}
	// A project Ori knows no kind of is no project to hire for.
	dense := documentsLike(now, 23, sixKinds)
	dense.Candidates[1].Marker = nil
	dense.Candidates[1].DominantExtension, dense.Candidates[1].FileCount, dense.Candidates[1].NewestModTime = ".mov", 12, now
	if _, ok := MixedHireLead(dense, now, nil); ok {
		t.Fatal("a density-only project of no known kind is no lead")
	}
}
