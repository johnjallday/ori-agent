package setupjourney

import (
	"testing"
)

// A song inside a folder workspace inside its Home is inspected and repaired
// exactly like a song directly under the Home.
func TestProjectRoleRepair_NestedSongInspectsAndCommits(t *testing.T) {
	fx := newRepairCommitFixtureAt(t, true)
	child, err := fx.store.Get(fx.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID == fx.homeID {
		t.Fatal("fixture must nest the song below a folder group, not directly under the Home")
	}
	inspection, err := InspectMissingSplitProjectRoles(fx.store, fx.installed, fx.owner, fx.homeID, fx.projectID)
	if err != nil || len(inspection.Roles) != 1 {
		t.Fatalf("nested song inspection: %+v %v", inspection, err)
	}
	claim := fx.claim(t)
	done, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
	if err != nil || done.Token != claim.Token || done.Status != ProjectRoleRepairSucceeded {
		t.Fatalf("nested song repair commit: %+v %v", done, err)
	}
	repaired, err := fx.store.Get(fx.projectID)
	if err != nil || len(repaired.GetAssistantProjectLink().ProjectRoles) != 1 || repaired.ParentID != child.ParentID {
		t.Fatalf("nested song after repair: parent=%q link=%+v err=%v", repaired.ParentID, repaired.GetAssistantProjectLink(), err)
	}
}
