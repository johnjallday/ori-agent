package personalassistant

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestFolderUndecidedHomeTeamCannotBecomeLegacyConfirmation(t *testing.T) {
	plan := homeStaffingPlanFixture()
	plan.Intent.RequiresHomeStaffing = true
	complete := plan.Stamped()
	if complete.Digest == "" {
		t.Fatal("exact team cannot be confirmed")
	}
	plan.Intent.HomeStaffing = nil
	if pending := plan.Stamped(); pending.Digest != "" || !pending.Intent.RequiresHomeStaffing {
		t.Fatal("undecided team gained legacy authority")
	}
	doc := homeStaffingDocumentFixture()
	doc.Offers[0].Setup.Intent.RequiresHomeStaffing = true
	doc.Offers[0].Setup.Intent.HomeStaffing = nil
	if _, err := encodeFolderDigest(doc); err == nil {
		t.Fatal("undecided team became an accepted run")
	}
	for _, version := range []int{1, 2} {
		doc.SchemaVersion = version
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeFolderDigest(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatal("legacy document injected new staffing policy")
		}
	}
	plan.Intent.RequiresHomeStaffing = false
	if plan.Stamped().Digest == "" {
		t.Fatal("genuine legacy plan lost its separate contract")
	}
}
