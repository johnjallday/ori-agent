package connections

import "testing"

func TestResolveVault(t *testing.T) {
	cases := []struct {
		name       string
		saved      string
		available  []string
		wantAction VaultAction
		wantID     string
	}{
		{"saved reused over choices", "v-saved", []string{"v-a", "v-b"}, VaultUseSaved, "v-saved"},
		{"saved reused with none available", "v-saved", nil, VaultUseSaved, "v-saved"},
		{"none exist -> create", "", nil, VaultCreate, ""},
		{"exactly one -> auto", "", []string{"only"}, VaultAutoSelect, "only"},
		{"several -> prompt", "", []string{"a", "b", "c"}, VaultPrompt, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveVault(tc.saved, tc.available)
			if got.Action != tc.wantAction || got.VaultID != tc.wantID {
				t.Fatalf("ResolveVault = %+v, want {%s %q}", got, tc.wantAction, tc.wantID)
			}
		})
	}
}

func TestVaultAvailability_RequiresRepair(t *testing.T) {
	if !VaultLocked.RequiresRepair() || !VaultMissing.RequiresRepair() {
		t.Fatal("locked/missing must require repair")
	}
	if VaultAvailable.RequiresRepair() {
		t.Fatal("available must not require repair")
	}
}
