package economy

import (
	"errors"
	"testing"
	"time"
)

func newTestFilingClaim() FilingClaim {
	return FilingClaim{ID: "operation", RequestID: "request", Owner: "local", WorkspaceID: "workspace", BaseCost: FilingExpansionCost, Cost: FilingExpansionCost, PlanJSON: `{}`}
}

func TestFilingClaimCompetesWithFarmForTheSameCraft(t *testing.T) {
	s := newLedger(t)
	ctx := t.Context()
	if _, err := s.Credit(ctx, Entry{Resource: ResourceCraft, Amount: FarmBuildCost, Reason: ReasonQuest, RefKind: RefKindQuest, RefID: "seed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	filing, farm := make(chan error, 1), make(chan error, 1)
	go func() { filing <- s.ReserveFiling(ctx, newTestFilingClaim()) }()
	go func() {
		_, err := s.Debit(ctx, Entry{Resource: ResourceCraft, Amount: FarmBuildCost, Reason: ReasonBuild, RefKind: RefKindBuild, RefID: "farm"}, time.Now())
		farm <- err
	}()
	x, y := <-filing, <-farm
	if !(x == nil && errors.Is(y, ErrInsufficientBalance)) && !(y == nil && errors.Is(x, ErrInsufficientBalance)) {
		t.Fatalf("filing: %v; Farm: %v", x, y)
	}
	balances, err := s.Balances(ctx)
	want := int64(0)
	if x == nil {
		want = FarmBuildCost - FilingExpansionCost
	}
	if err != nil || balances.Craft != want {
		t.Fatalf("balance after competition: %+v %v, want %d", balances, err, want)
	}
	claim, err := s.FindFilingClaim(ctx, "local", "request")
	if err != nil || (claim != nil) != (x == nil) {
		t.Fatalf("insufficient balance left a claim: %+v %v", claim, err)
	}
}

func TestFilingClaimAndDebitOrRefundCommitTogether(t *testing.T) {
	s := newLedger(t)
	ctx := t.Context()
	if _, err := s.Credit(ctx, Entry{Resource: ResourceCraft, Amount: FilingExpansionCost, Reason: ReasonQuest, RefKind: RefKindQuest, RefID: "seed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec := func(query string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(craft int64, state string) {
		t.Helper()
		balances, err := s.Balances(ctx)
		if err != nil || balances.Craft != craft {
			t.Fatalf("balance: %+v %v, want %d", balances, err, craft)
		}
		claim, err := s.FindFilingClaim(ctx, "local", "request")
		if err != nil || (state == "" && claim != nil) || (state != "" && (claim == nil || claim.State != state)) {
			t.Fatalf("claim: %+v %v, want %q", claim, err, state)
		}
	}
	claim := newTestFilingClaim()
	exec(`CREATE TRIGGER fail_debit BEFORE INSERT ON economy_ledger WHEN NEW.ref_kind = 'filing_expansion_purchase' BEGIN SELECT RAISE(ABORT,'test debit failure'); END`)
	if err := s.ReserveFiling(ctx, claim); err == nil {
		t.Fatal("injected debit failed silently")
	}
	assert(FilingExpansionCost, "")
	exec(`DROP TRIGGER fail_debit`)
	if err := s.ReserveFiling(ctx, claim); err != nil {
		t.Fatal(err)
	}
	assert(0, FilingBuilding)
	exec(`CREATE TRIGGER fail_refund BEFORE INSERT ON economy_ledger WHEN NEW.ref_kind = 'filing_expansion_refund' BEGIN SELECT RAISE(ABORT,'test refund failure'); END`)
	if err := s.RefundFiling(ctx, claim); err == nil {
		t.Fatal("injected refund failed silently")
	}
	assert(0, FilingBuilding)
	exec(`DROP TRIGGER fail_refund`)
	if err := s.RefundFiling(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundFiling(ctx, claim); err == nil {
		t.Fatal("refunded twice")
	}
	assert(FilingExpansionCost, FilingRefunded)
}
