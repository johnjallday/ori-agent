package publicsearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/skills"
)

const PublicSearchDestination = "https://api.duckduckgo.com/"
const PublicSearchInstance = "utility/duckduckgo-public-v1"

var ErrPublicSearch = errors.New("configured public search unavailable")

// PublicWebSearchAdapter is the existing read-only DuckDuckGo protocol through
// the strict host transport. It has no mutable endpoint, credentials, recency,
// proxy, fallback (including pollen), native tools or plugin/MCP dispatch.
type PublicWebSearchAdapter struct{ reader publicread.Reader }
type PublicSearchObservation struct {
	Response WebSearchResponse
	ReadAt   time.Time
	Hash     string
	Revision string
}

func NewPublicWebSearchAdapter() *PublicWebSearchAdapter {
	return &PublicWebSearchAdapter{reader: publicread.NewClient()}
}
func (a *PublicWebSearchAdapter) WebSearch(ctx context.Context, request WebSearchRequest) (WebSearchResponse, error) {
	if request.Recency != "" {
		return WebSearchResponse{}, ErrPublicSearch
	}
	observation, err := a.Search(ctx, request.Query)
	return observation.Response, err
}
func (a *PublicWebSearchAdapter) Search(ctx context.Context, query string) (PublicSearchObservation, error) {
	if a == nil || a.reader == nil || skills.ValidateMarketplaceQuery(query) != nil || publicread.ContainsCredentialMaterial(query) {
		return PublicSearchObservation{}, ErrPublicSearch
	}
	target, _ := url.Parse(PublicSearchDestination)
	values := target.Query()
	values.Set("q", query)
	values.Set("format", "json")
	values.Set("no_redirect", "1")
	values.Set("no_html", "1")
	target.RawQuery = values.Encode()
	read, err := a.reader.Read(ctx, publicread.Request{URL: target.String(), ContentTypes: []string{"application/json", "text/plain"}})
	if err != nil || read.URL != target.String() || read.ReadAt.IsZero() {
		return PublicSearchObservation{}, ErrPublicSearch
	}
	// A successful protocol envelope distinguishes an empty observation from an
	// error page or arbitrary JSON. The shared parser remains the source owner.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(read.Body, &envelope) != nil || envelope["RelatedTopics"] == nil || string(envelope["RelatedTopics"]) == "null" {
		return PublicSearchObservation{}, ErrPublicSearch
	}
	var payload DuckDuckGoResponse
	if json.Unmarshal(read.Body, &payload) != nil {
		return PublicSearchObservation{}, ErrPublicSearch
	}
	response := WebSearchResponse{Query: query, Results: DuckDuckGoResults(payload, query, 5), Source: "duckduckgo.com"}
	return PublicSearchObservation{Response: response, ReadAt: read.ReadAt, Hash: read.Hash, Revision: read.ETag}, nil
}
