package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/johnjallday/ori-agent/internal/publicread"
)

// PublicMarketplaceSearcher uses the existing skills CLI's inspected public
// search protocol through the host's strict HTTP owner. Unlike the CLI's fetch,
// its DNS, redirects, response bytes and verified-empty state are enforceable.
// No paid provider, Node bootstrap, cache, installer or extra protocol is added.
type PublicMarketplaceSearcher struct{ reader publicread.Reader }

func NewPublicMarketplaceSearcher() *PublicMarketplaceSearcher {
	return &PublicMarketplaceSearcher{reader: publicread.NewClient()}
}

func MarketplaceSearchURL(query string) (string, error) {
	if err := ValidateMarketplaceQuery(query); err != nil {
		return "", err
	}
	target := MarketplaceAPI + "/api/search?" + url.Values{"q": []string{query}, "limit": []string{"8"}}.Encode()
	return publicread.ValidateURL(target)
}

func (s *PublicMarketplaceSearcher) Search(ctx context.Context, query string, limit int) MarketplaceSearchResult {
	result := MarketplaceSearchResult{State: "unavailable", Results: []MarketplaceMatch{}}
	target, err := MarketplaceSearchURL(query)
	if err != nil {
		result.State, result.Reason = "invalid_query", "invalid_query"
		return result
	}
	if s == nil || s.reader == nil {
		result.Reason = "catalog_unavailable"
		return result
	}
	response, err := s.reader.Read(ctx, publicread.Request{URL: target, ContentTypes: []string{"application/json"}})
	if err != nil {
		result.Reason = "catalog_unavailable"
		return result
	}
	if len(response.Body) > publicread.MaxResponseBytes || response.ReadAt.IsZero() {
		result.Reason = "invalid_response"
		return result
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(response.Body, &envelope) != nil || envelope == nil {
		result.State, result.Reason = "malformed_output", "invalid_catalog_json"
		return result
	}
	if failure, exists := envelope["error"]; exists && string(failure) != "null" && string(failure) != `""` {
		result.Reason = "catalog_reported_failure"
		return result
	}
	if success, exists := envelope["success"]; exists && string(success) == "false" {
		result.Reason = "catalog_reported_failure"
		return result
	}
	raw, exists := envelope["skills"]
	if !exists || string(raw) == "null" {
		result.State, result.Reason = "malformed_output", "invalid_catalog_json"
		return result
	}
	var entries []struct {
		ID       string `json:"id"`
		Source   string `json:"source"`
		Name     string `json:"name"`
		Installs int64  `json:"installs"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		result.State, result.Reason = "malformed_output", "invalid_catalog_json"
		return result
	}
	if limit <= 0 || limit > MarketplaceCandidates {
		limit = MarketplaceCandidates
	}
	result.State, result.ObservedAt, result.SourceHash, result.Revision = "empty", response.ReadAt, response.Hash, response.ETag
	seen, malformed := map[string]bool{}, false
	for _, entry := range entries {
		spec := entry.Source + "@" + entry.Name
		validated := ParseMarketplaceSearchOutput(spec, 1)
		if !marketplaceSpec.MatchString(spec) || len(validated.Results) != 1 || len(spec) > 200 || entry.Installs < 0 {
			malformed = true
			continue
		}
		match := validated.Results[0]
		// Only an actually returned, matching public listing URL is evidence.
		// An absent/different slug leaves the destination unknown, not guessed.
		match.URL = ""
		if entry.ID == entry.Source+"/"+entry.Name {
			match.URL = MarketplaceAPI + "/" + entry.ID
		}
		if entry.Installs > 0 {
			match.Installs = fmt.Sprintf("%d installs", entry.Installs)
		}
		if seen[spec] {
			continue
		}
		seen[spec] = true
		if len(result.Results) == limit {
			result.Truncated = true
			continue
		}
		result.Results = append(result.Results, match)
	}
	if len(result.Results) > 0 {
		result.State = "available"
	}
	if malformed || result.Truncated {
		result.State, result.Reason = "partial", "incomplete_listing"
	}
	if len(entries) > 0 && len(result.Results) == 0 {
		result.State, result.Reason = "malformed_output", "invalid_catalog_entries"
	}
	return result
}
