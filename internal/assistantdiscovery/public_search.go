package assistantdiscovery

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/publicsearch"
)

var publicSearchVersion = contentHash(publicsearch.PublicSearchInstance + " GET " + publicsearch.PublicSearchDestination + " exact q; format=json; no_redirect=1; no_html=1; no fallback")

// ConfigurePublicSearch accepts only the audited concrete read-only owner and
// a current settings validator. It performs no network/configuration writes.
func (s *Service) ConfigurePublicSearch(adapter *publicsearch.PublicWebSearchAdapter, stillConfigured func() bool) {
	s.publicSearch = adapter
	s.searchConfigured = stillConfigured
}
func (s *Service) PublicSearchConfigured() bool {
	return s != nil && s.publicSearch != nil && s.searchConfigured != nil && s.searchConfigured()
}
func (s *Service) ResolvePublicSearchLookup(query string) (Lookup, error) {
	if !s.PublicSearchConfigured() || ValidatePublicQuery(query) != nil {
		return Lookup{}, ErrLookupUnsupported
	}
	return Lookup{Operation: "web_search", Query: query, SourceID: publicsearch.PublicSearchInstance, SourceVersion: publicSearchVersion}, nil
}
func (s *Service) ValidateResearchLookup(ctx context.Context, lookup Lookup) error {
	if lookup.Operation == "mcp_catalog_refresh" {
		return s.ValidateRegistryLookup(ctx, lookup)
	}
	if lookup.Operation != "web_search" || ctx.Err() != nil || !s.PublicSearchConfigured() || lookup.SourceID != publicsearch.PublicSearchInstance || lookup.SourceVersion != publicSearchVersion {
		return ErrReviewRefused
	}
	return nil
}
func (s *Service) readPublicSearch(ctx context.Context, lookup Lookup) Result {
	result := Result{Availability: Unavailable, Reason: "optional_search_unavailable", Candidates: []Candidate{}, Scope: "explicitly configured DuckDuckGo public search; listing/snippet metadata only"}
	if s.ValidateResearchLookup(ctx, lookup) != nil {
		return result
	}
	observation, err := s.publicSearch.Search(ctx, lookup.Query)
	if err != nil {
		return result
	}
	result.Availability, result.Reason = Empty, ""
	for _, item := range observation.Response.Results {
		target, err := PublicURL(item.URL)
		if err != nil {
			result.Availability, result.Reason = Partial, "invalid_search_destination"
			continue
		}
		name, shortName := referenceText(item.Title, 120)
		description, shortDescription := referenceText(item.Snippet, 800)
		candidate := Candidate{ID: identity("public_search", target), Kind: "public_search", Name: name, Description: description, URL: target, Readiness: UnknownReadiness()}
		candidate.Receipt = metadataReceipt(candidate, "public_search_listing", "current_observation", observation.ReadAt, observation.ReadAt, shortName || shortDescription)
		candidate.Receipt.ContentHash = observation.Hash
		candidate.Receipt.Revision, _ = referenceText(observation.Revision, 128)
		appendCandidate(&result, candidate)
	}
	finishResult(&result)
	return result
}
