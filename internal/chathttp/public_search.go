package chathttp

import "github.com/johnjallday/ori-agent/internal/publicsearch"

// The utility facade and assistant use one audited source owner/protocol. This
// constructor is not the legacy automatic/environment provider selector.
type PublicWebSearchAdapter = publicsearch.PublicWebSearchAdapter

func NewPublicWebSearchAdapter() *PublicWebSearchAdapter {
	return publicsearch.NewPublicWebSearchAdapter()
}

var _ WebSearchAdapter = (*PublicWebSearchAdapter)(nil)
