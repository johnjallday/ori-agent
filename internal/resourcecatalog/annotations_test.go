package resourcecatalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// PRD FR20 / AC4: favorites, tags and notes survive refresh and restart, are
// never rewritten by a refresh, and a stale write is refused with the
// current value rather than merged.
func TestAnnotations_SurviveRefreshAndRefuseAStaleWrite(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	saved, err := f.service.SetAnnotation("local", f.studio.ID, lyrics, AnnotationInput{
		Favorite: true, Tags: []string{" Live ", "live", "vocals", ""}, Note: "  Use on the vocal bus.\n  ",
	})
	if err != nil || saved.Revision != 1 || len(saved.Tags) != 2 || saved.Tags[0] != "Live" || saved.Note != "Use on the vocal bus." {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	f.refresh(t, "refresh-annotated")
	restarted := f.newService(f.dataDir)
	detail, err := restarted.Entry("local", f.studio.ID, lyrics)
	if err != nil || !detail.Favorite || detail.Note != "Use on the vocal bus." || detail.AnnotationRevision != 1 {
		t.Fatalf("after refresh and restart = %+v, %v", detail, err)
	}
	// Two tabs edit from revision 1; the second is refused with the first's text.
	if _, err := f.service.SetAnnotation("local", f.studio.ID, lyrics, AnnotationInput{Note: "First tab", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.SetAnnotation("local", f.studio.ID, lyrics, AnnotationInput{Note: "Second tab", BaseRevision: 1})
	var conflict *AnnotationConflictError
	if !errors.As(err, &conflict) || conflict.Current.Note != "First tab" || conflict.Current.Revision != 2 {
		t.Fatalf("stale write err = %v", err)
	}
	// Emptying an annotation removes it.
	if _, err := f.service.SetAnnotation("local", f.studio.ID, lyrics, AnnotationInput{BaseRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if rows := f.rows(t, Query{FavoritesOnly: true}); len(rows) != 0 {
		t.Fatalf("an emptied annotation is still a favorite: %+v", rows)
	}
}

func TestAnnotations_RefuseWhatIsOutOfBoundsOrUnknown(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	tooMany := make([]string, MaxTags+1)
	for index := range tooMany {
		tooMany[index] = "tag" + itoa(index)
	}
	cases := map[string]AnnotationInput{
		"too many tags":          {Tags: tooMany},
		"a long tag":             {Tags: []string{strings.Repeat("a", 41)}},
		"a multi-line tag":       {Tags: []string{"a\nb"}},
		"a long note":            {Note: strings.Repeat("n", MaxNoteBytes+1)},
		"a control in a note":    {Note: "bell\a"},
		"a stale first revision": {Note: "x", BaseRevision: 4},
	}
	for name, input := range cases {
		if _, err := f.service.SetAnnotation("local", f.studio.ID, lyrics, input); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := f.service.SetAnnotation("local", f.studio.ID, "rc_unknown", AnnotationInput{Note: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown entry err = %v", err)
	}
	if _, err := f.service.SetAnnotation("someone-else", f.studio.ID, lyrics, AnnotationInput{Note: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner err = %v", err)
	}
}

// PRD FR24 / AC5 / AC13: the Home's counts come from the Studio's published
// revision and count only present entries of sources still approved here.
func TestHomeSummary_CountsOnlyPresentApprovedEntriesFromTheSameRevision(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	if _, err := f.service.SetAnnotation("local", f.studio.ID, EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua"), AnnotationInput{Note: "private"}); err != nil {
		t.Fatal(err)
	}
	summary, err := f.service.HomeSummary("local", f.studio.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]CategorySummary{}
	for _, category := range summary.Categories {
		counts[category.ID] = category
	}
	if summary.Revision != f.overview(t).Revision || counts["templates"].Present != 2 || counts["templates"].ProfileRow != ProfileRowTemplates ||
		counts["scripts"].Present != 1 || counts["templates"].State != CategoryInspected {
		t.Fatalf("summary = %+v", summary)
	}
	review, err := f.service.ReviewWithdraw("local", f.studio.ID, "reaper_scripts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommitWithdraw("local", f.studio.ID, review.Token); err != nil {
		t.Fatal(err)
	}
	summary, _ = f.service.HomeSummary("local", f.studio.ID)
	for _, category := range summary.Categories {
		if category.ID == "scripts" && (category.Present != 0 || category.State != CategoryAccessWithdrawn) {
			t.Fatalf("withdrawn scripts on the Home = %+v", category)
		}
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "Application Support") {
		t.Fatalf("the Home summary carries a note or a location: %s", encoded)
	}
}

// PRD FR12: a moved resource is a new entry; its notes stay with the old one
// until the owner moves them to the suggested replacement.
func TestAnnotations_MoveOnlyByTheOwnersChoice(t *testing.T) {
	f := newCatalogFixture(t)
	f.provider.set(func(p *fakeProvider) {
		p.entries["project_templates"] = []InventoryEntry{{NativeID: "Lead.RPP", Kind: "project_template", Name: "Lead", Facts: []Fact{}}}
	})
	f.approveAll(t)
	old := EntryID("inst_machine_a1", "project_templates", "Lead.RPP")
	if _, err := f.service.SetAnnotation("local", f.studio.ID, old, AnnotationInput{Note: "My go-to"}); err != nil {
		t.Fatal(err)
	}
	f.provider.set(func(p *fakeProvider) {
		p.entries["project_templates"] = []InventoryEntry{{NativeID: "Archive/Lead.RPP", Kind: "project_template", Name: "Lead", Facts: []Fact{}}}
	})
	f.refresh(t, "refresh-moved")
	moved := EntryID("inst_machine_a1", "project_templates", "Archive/Lead.RPP")
	detail, err := f.service.Entry("local", f.studio.ID, old)
	if err != nil || detail.State != EntryNoLongerFound || detail.Note != "My go-to" ||
		detail.SuggestedReplacement == nil || detail.SuggestedReplacement.EntryID != moved {
		t.Fatalf("old entry = %+v, %v", detail, err)
	}
	if fresh, _ := f.service.Entry("local", f.studio.ID, moved); fresh.Note != "" {
		t.Fatal("the moved entry inherited the notes by itself")
	}
	if _, err := f.service.MoveAnnotation("local", f.studio.ID, old, moved, 1); err != nil {
		t.Fatalf("move: %v", err)
	}
	if fresh, _ := f.service.Entry("local", f.studio.ID, moved); fresh.Note != "My go-to" {
		t.Fatalf("after move = %+v", fresh)
	}
	if gone, _ := f.service.Entry("local", f.studio.ID, old); gone.Note != "" || gone.SuggestedReplacement != nil {
		t.Fatalf("old entry after move = %+v", gone)
	}
	// Moving onto an entry of another kind, or onto itself, is refused.
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	if _, err := f.service.MoveAnnotation("local", f.studio.ID, moved, lyrics, 1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-kind move err = %v", err)
	}
}
