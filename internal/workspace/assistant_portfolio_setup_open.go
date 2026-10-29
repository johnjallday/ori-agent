package workspace

import (
	"errors"
	"strings"
	"time"
)

// AssistantPortfolioSetupOpenReceipt is the Home's own record that a folder
// setup's one Set up had its manager open a song the Home's library lists: the
// offer whose consent covered it, the catalog entry, the reciprocal link and
// the song's workspace. It is written in the same Home write as the song's
// catalog link, so the two never disagree. No agent tool reads it yet.
type AssistantPortfolioSetupOpenReceipt struct {
	OfferID            string    `json:"offer_id"`
	EntryID            string    `json:"entry_id"`
	LinkID             string    `json:"link_id"`
	ProjectWorkspaceID string    `json:"project_workspace_id"`
	RecordedAt         time.Time `json:"recorded_at"`
}

// ErrSetupOpenReceiptInvalid means a setup-open receipt is not one the Home can
// keep: an identifier missing, too long or not plain text, or no time.
var ErrSetupOpenReceiptInvalid = errors.New("setup-open receipt is invalid")

// Validate checks the receipt's shape.
func (r AssistantPortfolioSetupOpenReceipt) Validate() error {
	for _, id := range []string{r.OfferID, r.EntryID, r.LinkID, r.ProjectWorkspaceID} {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 200 || strings.ContainsAny(id, "/\\\x00\r\n") {
			return ErrSetupOpenReceiptInvalid
		}
	}
	if r.RecordedAt.IsZero() {
		return ErrSetupOpenReceiptInvalid
	}
	return nil
}

// RecordSetupOpen adds the receipt to the Home's portfolio state. A replay of
// the same offer and entry keeps the first record; the list is bounded like
// the Home's other portfolio receipts.
func (s *AssistantProgramState) RecordSetupOpen(receipt AssistantPortfolioSetupOpenReceipt) error {
	if s == nil {
		return ErrSetupOpenReceiptInvalid
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	for _, prior := range s.Portfolio.SetupOpenReceipts {
		if prior.OfferID == receipt.OfferID && prior.EntryID == receipt.EntryID {
			return nil
		}
	}
	out := append(append([]AssistantPortfolioSetupOpenReceipt(nil), s.Portfolio.SetupOpenReceipts...), receipt)
	if len(out) > assistantPortfolioMaxReceipts {
		out = out[len(out)-assistantPortfolioMaxReceipts:]
	}
	s.Portfolio.SetupOpenReceipts = out
	return nil
}

// SetupOpenReceipt is the Home's record of the song an offer's Set up opened.
func (s *AssistantProgramState) SetupOpenReceipt(offerID string) (AssistantPortfolioSetupOpenReceipt, bool) {
	if s == nil {
		return AssistantPortfolioSetupOpenReceipt{}, false
	}
	for _, receipt := range s.Portfolio.SetupOpenReceipts {
		if receipt.OfferID == offerID {
			return receipt, true
		}
	}
	return AssistantPortfolioSetupOpenReceipt{}, false
}
