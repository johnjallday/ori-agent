package economy

import "context"

type ExpansionState string

const (
	ExpansionDisabled     ExpansionState = "disabled"
	ExpansionUnavailable  ExpansionState = "unavailable"
	ExpansionNeedsHQ      ExpansionState = "needs_hq"
	ExpansionAffordable   ExpansionState = "affordable"
	ExpansionInsufficient ExpansionState = "insufficient"
	ExpansionBuilt        ExpansionState = "already_built"
	ExpansionPending      ExpansionState = "pending"
	ExpansionRemoved      ExpansionState = "destination_unavailable"
)

type ExpansionDestination struct {
	Name  string `json:"name"`
	Route string `json:"route"`
}

// ExpansionQuote is a read-only projection, not permission to spend. Nil
// monetary fields mean not quoted or unknown, never zero Craft.
type ExpansionQuote struct {
	State         ExpansionState         `json:"state"`
	BaseCost      int64                  `json:"base_cost"`
	EffectiveCost *int64                 `json:"effective_cost,omitempty"`
	Balance       *int64                 `json:"balance,omitempty"`
	Missing       int64                  `json:"missing,omitempty"`
	Creative      bool                   `json:"creative"`
	Destinations  []ExpansionDestination `json:"destinations,omitempty"`
	ManualEarnURL string                 `json:"manual_earn_url,omitempty"`
	Charged       *int64                 `json:"charged,omitempty"`
	Waived        *int64                 `json:"waived,omitempty"`
}

// QuoteFilingExpansion prices one new workspace; the caller resolves HQ and
// existing workspaces separately. It reads only the ledger, not the Farm list.
func (s *Service) QuoteFilingExpansion(ctx context.Context) (ExpansionQuote, error) {
	quote := ExpansionQuote{State: ExpansionUnavailable, BaseCost: FilingExpansionCost}
	if !s.Available() {
		return quote, ErrStoreUnavailable
	}
	balances, err := s.store.Balances(ctx)
	if err != nil {
		return quote, err
	}
	cost := FilingExpansionCost
	quote.Creative = s.CreativeMode()
	if quote.Creative {
		cost = 0
	}
	quote.EffectiveCost, quote.Balance = &cost, &balances.Craft
	quote.State = ExpansionAffordable
	if balances.Craft < cost {
		quote.State = ExpansionInsufficient
		quote.Missing = cost - balances.Craft
	}
	return quote, nil
}
