package reaper

import "testing"

func TestBuiltinActionsExposeCuratedCatalogAndConfirmationPolicy(t *testing.T) {
	actions := BuiltinActions()
	want := map[string]bool{
		"1007": false, "1016": false, "1013": true, "1008": false,
		"40026": true, "40001": false, "40364": false, "40029": false, "40030": false,
	}
	wantTier := map[string]string{
		"1007": TierSilent, "1016": TierSilent, "1013": TierConfirm, "1008": TierSilent,
		"40026": TierConfirm, "40001": TierUndoable, "40364": TierUndoable,
		"40029": TierUndoable, "40030": TierUndoable,
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
		if action.ResolveTier() != wantTier[action.ID] {
			t.Fatalf("action tier = %+v, want %q", action, wantTier[action.ID])
		}
	}

	// Record and Save project are the only builtins where confirmation still
	// tracks mutation directly; the rest are mutating but undo-forward.
	for _, id := range []string{"1013", "40026"} {
		action, _ := BuiltinAction(id)
		if !action.Mutates || !action.NeedsConfirmation {
			t.Fatalf("destructive action lost its confirmation gate: %+v", action)
		}
	}

	actions[0].Label = "changed"
	fresh, _ := BuiltinAction("1007")
	if fresh.Label != "Play" {
		t.Fatal("caller mutated the shared built-in catalog")
	}
}
