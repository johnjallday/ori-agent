package assistantdiscovery

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/publicsearch"
)

type configuredSearchAuthority struct {
	*scopeAuthority
	service *Service
}

func (a configuredSearchAuthority) ValidateResearchLookup(ctx context.Context, lookup Lookup) error {
	return a.service.ValidateResearchLookup(ctx, lookup)
}
func TestOptionalPublicSearchPinsConfiguredOwnerAndRevocationBeforeEgress(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	if _, err := service.ResolvePublicSearchLookup("Telegram"); err == nil {
		t.Fatal("unconfigured search acquired authority")
	}
	enabled := true
	service.ConfigurePublicSearch(publicsearch.NewPublicWebSearchAdapter(), func() bool { return enabled })
	scope, authority, _ := reviewFixture()
	gate := NewReviewGate(configuredSearchAuthority{authority, service})
	lookup, err := service.ResolvePublicSearchLookup("Telegram community management")
	if err != nil {
		t.Fatal(err)
	}
	review, err := gate.Prepare(context.Background(), scope, lookup)
	if err != nil || review.Destination != publicsearch.PublicSearchDestination || review.RedirectPolicy != "none" {
		t.Fatal(review, err)
	}
	forged := lookup
	forged.SourceID = "same display name, different method"
	if _, err := gate.Prepare(context.Background(), scope, forged); err == nil {
		t.Fatal("display name established operation authority")
	}
	enabled = false
	if _, err := gate.Approve(context.Background(), scope, review); err == nil {
		t.Fatal("revoked optional provider approved")
	}
	if service.PublicSearchConfigured() {
		t.Fatal("configuration snapshot ignored current revocation")
	}
}
func TestZeroLocalResearchBudgetFailsClosedWithoutPanic(t *testing.T) {
	if new(TurnBudget).ChargeLocal(Result{Availability: Available}) {
		t.Fatal("forged budget charged")
	}
}
