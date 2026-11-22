package economy

import (
	"context"
	"errors"
	"testing"
)

type unreadableBalance struct{ Store }

func (unreadableBalance) Balances(context.Context) (Balances, error) {
	return Balances{}, errors.New("balance unreadable")
}

func TestQuoteFilingExpansionUsesLiveLedgerAndCreativeSetting(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()
	quote := func(state ExpansionState, cost, balance int64, creative bool) {
		t.Helper()
		q, err := s.QuoteFilingExpansion(ctx)
		if err != nil || q.State != state || q.BaseCost != FilingExpansionCost ||
			q.EffectiveCost == nil || *q.EffectiveCost != cost || q.Balance == nil || *q.Balance != balance ||
			q.Creative != creative || q.Missing != max(cost-balance, 0) {
			t.Fatalf("quote = %+v, %v; want %s, price %d, balance %d", q, err, state, cost, balance)
		}
	}
	settings := &fakeSettings{}
	s.SetSettingsSource(settings)
	quote(ExpansionInsufficient, 7, 0, false)
	settings.creativeMode = true
	quote(ExpansionAffordable, 0, 0, true)
	settings.creativeMode = false
	s.AwardQuestCraft(ctx, "pa-meet-assistant")
	quote(ExpansionAffordable, 7, 7, false)
	quote(ExpansionAffordable, 7, 7, false)
}

func TestQuoteFilingExpansionDoesNotTurnBalanceErrorsIntoFreeBuilds(t *testing.T) {
	s := NewService(unreadableBalance{}, nil, &fakeSettings{creativeMode: true}, nil)
	q, err := s.QuoteFilingExpansion(t.Context())
	if err == nil || q.State != ExpansionUnavailable || q.Balance != nil || q.EffectiveCost != nil {
		t.Fatalf("unknown balance: %+v, %v", q, err)
	}
}
