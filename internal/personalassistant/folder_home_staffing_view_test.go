package personalassistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFolderHomeStaffingPresentationCannotRenewOrExposeSourceAuthority(t *testing.T) {
	review := folderHomeReviewFixture()
	view := &FolderHomeStaffingView{ContextDigest: review.ContextDigest, Revision: review.Revision, State: "needs_choice",
		Roles:  []FolderHomeRoleView{{RoleID: "manager", Label: "Manager", Required: true, Skills: []string{"collection"}}},
		Advice: review.Advice, Choices: review.Team.Choices(), ChoiceProblems: map[string]string{"manager": "Review this choice"}}
	plan := homeStaffingPlanFixture()
	original := plan.Stamped().Digest
	plan.HomeStaffing = view
	copy := plan.Stamped()
	if copy.Digest != original {
		t.Fatal("display state renewed the exact effect")
	}
	data, err := json.Marshal(copy)
	if err != nil || strings.Contains(string(data), "source_witness") || strings.Contains(string(data), "original-source") {
		t.Fatal("private proof escaped the plan", err)
	}
	copy.HomeStaffing.Roles[0].Skills[0] = "changed"
	copy.HomeStaffing.Choices[1].Appearance.Generated.Color = "#123456"
	copy.HomeStaffing.ChoiceProblems["manager"] = "changed"
	if view.Roles[0].Skills[0] != "collection" || view.Choices[1].Appearance.Generated.Color != "" || view.ChoiceProblems["manager"] != "Review this choice" || review.Advice.Roles[0].Choices[0].SourceWitness != "original-source" {
		t.Fatal("public presentation aliases/erases original evidence")
	}
}
