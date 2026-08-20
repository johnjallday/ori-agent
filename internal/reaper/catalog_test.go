package reaper

import "testing"

func TestBuiltinActionsExposeCuratedCatalogAndConfirmationPolicy(t *testing.T) {
	actions := BuiltinActions()
	want := map[string]bool{
		"1007": false, "1016": false, "1013": true, "1008": false,
		"40026": true, "40001": true, "40364": true, "40029": true, "40030": true,
	}
	if len(actions) != len(want) {
		t.Fatalf("built-in action count = %d, want %d", len(actions), len(want))
	}
	for _, action := range actions {
		needsConfirmation, found := want[action.ID]
		if !found {
			t.Fatalf("unexpected built-in action: %+v", action)
		}
		if action.Source != ActionSourceBuiltin || action.Label == "" || action.Description == "" || action.NeedsConfirmation != needsConfirmation {
			t.Fatalf("action metadata = %+v", action)
		}
		if action.NeedsConfirmation != action.Mutates {
			t.Fatalf("built-in mutation policy is inconsistent: %+v", action)
		}
	}

	actions[0].Label = "changed"
	fresh, _ := BuiltinAction("1007")
	if fresh.Label != "Play" {
		t.Fatal("caller mutated the shared built-in catalog")
	}
}
