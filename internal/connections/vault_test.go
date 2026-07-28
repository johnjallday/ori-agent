package connections

import (
	"context"
	"errors"
	"testing"
)

// fakeVaultCatalog is an in-memory VaultCatalog. listErr/statusErr let a test
// prove preflight surfaces store failures instead of inventing an outcome.
type fakeVaultCatalog struct {
	vaults    []VaultRef
	listErr   error
	statusErr error
	statusFor []string // records which ids had their availability checked
}

func (f *fakeVaultCatalog) ListVaults(context.Context) ([]VaultRef, error) {
	return f.vaults, f.listErr
}

func (f *fakeVaultCatalog) VaultAvailability(_ context.Context, vaultID string) (VaultAvailability, error) {
	f.statusFor = append(f.statusFor, vaultID)
	if f.statusErr != nil {
		return "", f.statusErr
	}
	for _, v := range f.vaults {
		if v.ID == vaultID {
			return v.Availability, nil
		}
	}
	return VaultMissing, nil
}

func vault(id, name string, a VaultAvailability) VaultRef {
	return VaultRef{ID: id, Name: name, Availability: a}
}

func TestPreflightVault(t *testing.T) {
	cases := []struct {
		name        string
		saved       string
		vaults      []VaultRef
		wantOutcome VaultOutcome
		wantID      string
		wantOptions int
	}{
		{
			name:        "saved and unlocked is ready",
			saved:       "v-saved",
			vaults:      []VaultRef{vault("v-saved", "Personal", VaultAvailable), vault("v-b", "Work", VaultAvailable)},
			wantOutcome: VaultOutcomeReady,
			wantID:      "v-saved",
		},
		{
			name:        "saved but locked asks for unlock",
			saved:       "v-saved",
			vaults:      []VaultRef{vault("v-saved", "Personal", VaultLocked)},
			wantOutcome: VaultOutcomeUnlock,
			wantID:      "v-saved",
		},
		{
			name:        "saved vault gone asks for repair with replacements",
			saved:       "v-gone",
			vaults:      []VaultRef{vault("v-a", "Personal", VaultAvailable), vault("v-b", "Work", VaultAvailable)},
			wantOutcome: VaultOutcomeRepair,
			wantID:      "v-gone",
			wantOptions: 2,
		},
		{
			name:        "saved vault present but storage missing asks for repair",
			saved:       "v-saved",
			vaults:      []VaultRef{vault("v-saved", "Personal", VaultMissing), vault("v-b", "Work", VaultAvailable)},
			wantOutcome: VaultOutcomeRepair,
			wantID:      "v-saved",
			wantOptions: 2,
		},
		{
			name:        "no vaults at all asks to create",
			saved:       "",
			wantOutcome: VaultOutcomeCreate,
		},
		{
			name:        "sole vault is auto-selected",
			saved:       "",
			vaults:      []VaultRef{vault("only", "Personal", VaultAvailable)},
			wantOutcome: VaultOutcomeReady,
			wantID:      "only",
		},
		{
			name:        "sole vault that is locked asks for unlock",
			saved:       "",
			vaults:      []VaultRef{vault("only", "Personal", VaultLocked)},
			wantOutcome: VaultOutcomeUnlock,
			wantID:      "only",
		},
		{
			name:        "several vaults with no saved choice asks the user",
			saved:       "",
			vaults:      []VaultRef{vault("v-a", "Personal", VaultAvailable), vault("v-b", "Work", VaultAvailable)},
			wantOutcome: VaultOutcomeChoose,
			wantOptions: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PreflightVault(context.Background(), &fakeVaultCatalog{vaults: tc.vaults}, tc.saved)
			if err != nil {
				t.Fatalf("PreflightVault: %v", err)
			}
			if got.Outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q", got.Outcome, tc.wantOutcome)
			}
			if got.VaultID != tc.wantID {
				t.Fatalf("vault id = %q, want %q", got.VaultID, tc.wantID)
			}
			if len(got.Options) != tc.wantOptions {
				t.Fatalf("options = %d, want %d", len(got.Options), tc.wantOptions)
			}
		})
	}
}

// A choose/create outcome must not spend a status lookup on a vault nobody has
// selected yet — the user has to answer first.
func TestPreflightVault_NoStatusLookupWithoutASelection(t *testing.T) {
	cat := &fakeVaultCatalog{vaults: []VaultRef{vault("v-a", "A", VaultAvailable), vault("v-b", "B", VaultAvailable)}}
	if _, err := PreflightVault(context.Background(), cat, ""); err != nil {
		t.Fatalf("PreflightVault: %v", err)
	}
	if len(cat.statusFor) != 0 {
		t.Fatalf("checked availability of %v before the user chose", cat.statusFor)
	}
}

func TestPreflightVault_SurfacesCatalogErrors(t *testing.T) {
	boom := errors.New("vault db unavailable")
	if _, err := PreflightVault(context.Background(), &fakeVaultCatalog{listErr: boom}, ""); !errors.Is(err, boom) {
		t.Fatalf("list error = %v, want %v", err, boom)
	}
	cat := &fakeVaultCatalog{vaults: []VaultRef{vault("only", "Personal", VaultAvailable)}, statusErr: boom}
	if _, err := PreflightVault(context.Background(), cat, ""); !errors.Is(err, boom) {
		t.Fatalf("status error = %v, want %v", err, boom)
	}
}

func TestPreflightVault_RequiresCatalog(t *testing.T) {
	if _, err := PreflightVault(context.Background(), nil, ""); err == nil {
		t.Fatal("nil catalog must error rather than silently proceed")
	}
}

func TestVaultPreflightError_MatchesSentinel(t *testing.T) {
	err := error(&VaultPreflightError{Preflight: VaultPreflight{Outcome: VaultOutcomeUnlock, VaultID: "v-1"}})
	if !errors.Is(err, ErrVaultActionRequired) {
		t.Fatal("preflight error must match ErrVaultActionRequired")
	}
	var pfErr *VaultPreflightError
	if !errors.As(err, &pfErr) || pfErr.Preflight.Outcome != VaultOutcomeUnlock {
		t.Fatalf("errors.As lost the preflight: %+v", pfErr)
	}
}

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
