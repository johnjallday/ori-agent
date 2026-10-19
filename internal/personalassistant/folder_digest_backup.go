package personalassistant

import (
	"context"
	"errors"
)

// BackupCoverageDomain is the domain of Today's protection line. No folder
// offer and no migrated app-card answer uses it, so its first no is always a
// decline of the line itself.
const BackupCoverageDomain = "backup"

var errInvalidDomainDecline = errors.New("invalid domain decline")

// DomainDecline returns the user's saved no for domain, or nil.
func (s *FolderDigestService) DomainDecline(ctx context.Context, userID, domain string) (*FolderDomainDecline, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return nil, err
	}
	if decline := doc.DeclineFor(domain); decline != nil {
		copied := *decline
		return &copied, nil
	}
	return nil, nil
}

// DeclineDomain records a no for domain: the first keeps the evidence it was
// given (single_project or portfolio), a second is final. Declines are per
// user document, never per path.
func (s *FolderDigestService) DeclineDomain(ctx context.Context, userID, domain, evidence string) error {
	if s == nil || s.store == nil {
		return ErrRepairNeeded
	}
	if domain == "" || len(domain) > 64 || (evidence != "single_project" && evidence != "portfolio") {
		return errInvalidDomainDecline
	}
	now := s.now()
	_, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		if decline := d.DeclineFor(domain); decline != nil {
			if decline.Count < 2 {
				decline.Count++
			}
			decline.At = now
			return nil
		}
		d.DomainDeclines = append(d.DomainDeclines, FolderDomainDecline{Domain: domain, FirstEvidence: evidence, Count: 1, At: now})
		return nil
	})
	return err
}
