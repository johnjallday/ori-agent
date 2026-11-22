package folderagent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestHomeBranchesSelectionBindsTheSavedLook(t *testing.T) {
	root := demo(t)
	rec := homeStageRecord(nil)
	rec.WorkspaceID, rec.OwnerUserID = "feed", "owner"
	m, err := foldermap.Walk(root, foldermap.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec.Map, rec.Facts = m, foldermap.FactsFrom(m)
	all := homePlan(t, rec)
	if !all.Runnable || len(all.Branches) != 3 || all.BranchLook == "" || len(all.ListingFacts(rec.Facts).Members) != 8 {
		t.Fatalf("full plan: %+v", all.Branches)
	}
	var album, tracks string
	for _, b := range all.Branches {
		switch b.Name {
		case "Albums":
			album = b.ID
		case "Picker Songs":
			tracks = b.ID
		case "Filed":
			if b.ID != "" || b.Summary != "Empty in the look" {
				t.Fatal(b)
			}
		}
	}
	if album == "" || tracks == "" || album == tracks {
		t.Fatal(all.Branches)
	}
	without := func(ids []string, id string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(other string) bool { return other == id })
	}
	one := BuildPlan(rec, without(all.Selected, album))
	if !one.Runnable || one.Stage != foldermap.StageHome || !slices.Equal(ProjectParents(one.ListingFacts(rec.Facts)), []string{"Picker Songs"}) {
		t.Fatal(one)
	}
	none := BuildPlan(rec, without(one.Selected, tracks))
	if !none.Runnable || len(ProjectParents(none.ListingFacts(rec.Facts))) != 0 {
		t.Fatal(none)
	}
	if none.Digest == one.Digest || one.Digest == all.Digest {
		t.Fatal("digest ignored selection")
	}
	// Same number and format of projects, different branch identities.
	equal := rec
	equal.Facts.Members = nil
	for _, member := range rec.Facts.Members {
		if slices.Contains([]string{"Albums/Album-1", "Albums/Album-2", "Picker Songs/Track-1", "Picker Songs/Track-2"}, member.Path) {
			equal.Facts.Members = append(equal.Facts.Members, member)
		}
	}
	a, b := BuildPlan(equal, without(all.Selected, album)), BuildPlan(equal, without(all.Selected, tracks))
	if !a.Runnable || !b.Runnable || a.Digest == b.Digest {
		t.Fatal("equal-count swap must require a fresh confirmation")
	}
	// Same count and labels on a different saved look are never the reviewed selection.
	other := rec
	other.OwnerUserID = "another-owner"
	if got := BuildPlan(other, one.Selected); got.Runnable || got.Digest == one.Digest {
		t.Fatal("cross-owner handles accepted")
	}
	m.Walks++
	if got := BuildPlan(rec, one.Selected); got.Runnable {
		t.Fatal("stale look accepted")
	}
	m.Walks--
	if err := one.CheckBranchWitnesses(root, m); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "Picker Songs")
	if err := os.Rename(p, p+"-before"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0750); err != nil {
		t.Fatal(err)
	}
	if err := one.CheckBranchWitnesses(root, m); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("replacement: %v", err)
	}
}
