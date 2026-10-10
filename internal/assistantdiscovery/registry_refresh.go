package assistantdiscovery

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
)

func (s *Service) RegistrySources(ctx context.Context) []mcpregistry.PublicSource {
	out := []mcpregistry.PublicSource{}
	store, ok := s.registry.(*mcpregistry.Store)
	if !ok || store == nil {
		return out
	}
	for _, source := range store.PublicSources(ctx) {
		source.Name, _ = referenceText(source.Name, 120)
		out = append(out, source)
	}
	return out
}
func (s *Service) ValidateRegistryLookup(ctx context.Context, lookup Lookup) error {
	_, err := s.registrySource(ctx, lookup)
	return err
}
func (s *Service) registrySource(ctx context.Context, lookup Lookup) (mcpregistry.PublicSource, error) {
	store, ok := s.registry.(*mcpregistry.Store)
	if !ok || store == nil {
		return mcpregistry.PublicSource{}, ErrReviewRefused
	}
	for _, source := range store.PublicSources(ctx) {
		if source.ID == lookup.SourceID && source.URL == lookup.URL && source.Version == lookup.SourceVersion {
			return source, nil
		}
	}
	return mcpregistry.PublicSource{}, ErrReviewRefused
}
func (s *Service) refreshRegistry(ctx context.Context, lookup Lookup, approval *Authorization) Result {
	result := Result{Availability: Unavailable, Candidates: []Candidate{}, Scope: "one exact reviewed public MCP registry; metadata only"}
	source, err := s.registrySource(ctx, lookup)
	if err != nil {
		result.Reason = "registry_source_changed"
		return result
	}
	store := s.registry.(*mcpregistry.Store)
	if err := store.RefreshPublic(ctx, source, s.documents, approval.revalidate); err != nil {
		result.Reason = "registry_refresh_unavailable"
		return result
	}
	result = s.MCPCatalog(ctx, lookup.Query)
	result.Scope = "one reviewed public MCP source refreshed; filtered compiled/cached listings, not inspected documentation"
	return result
}
