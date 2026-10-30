package personalassistant

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestFolderEntryPlan_RequestHasOnlyTheVerifiedPrivateSubjectPath(t *testing.T) {
	f := newOneCardFixture(t)
	if len(f.setup.planned) == 0 || f.setup.planned[len(f.setup.planned)-1].Path != f.folder {
		t.Fatal("project planner did not receive the verified subject path")
	}
	for _, value := range []any{f.offer, f.offer.Plan} {
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), f.folder) {
			t.Fatalf("private path serialized: %T, %v", value, err)
		}
	}
	if err := os.Rename(f.folder, f.folder+"-away"); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Current(t.Context(), "local")
	if err != nil || current.Offer == nil || !current.Offer.NeedsPick {
		t.Fatalf("missing source: %+v %v", current, err)
	}
	if f.setup.planned[len(f.setup.planned)-1].Path != "" {
		t.Fatal("planner received a lost source path")
	}
}

func TestFolderEntryPlan_StampBindsTheChosenName(t *testing.T) {
	plan := NewFolderSetupPlan([]FolderPlanLine{{Kind: FolderPlanFolder, Name: "Links the folder"}})
	legacy := plan.Stamped().Digest
	if legacy != FolderPlanDigest(plan.Lines) {
		t.Fatal("changed a legacy plan")
	}
	plan.EntryName, plan.EntryCandidates = "Song.rpp", []string{"Song.rpp", "Alternate.rpp"}
	first := plan.Stamped()
	plan.EntryName = "Alternate.rpp"
	second := plan.Stamped()
	if first.Digest == legacy || first.Digest == second.Digest || second.Digest == "" || second.Stamped().Digest != second.Digest {
		t.Fatal("entry not stably bound independently of the wording")
	}
	first.EntryCandidates[0] = "changed.rpp"
	if plan.EntryCandidates[0] != "Song.rpp" {
		t.Fatal("plan aliases candidate storage")
	}
	for _, invalid := range []string{"Other.rpp", "../Song.rpp", "", "folder\\Song.rpp"} {
		plan.EntryName = invalid
		if plan.Stamped().Digest != "" {
			t.Fatalf("stamped an unlisted or unsafe entry %q", invalid)
		}
	}
}
