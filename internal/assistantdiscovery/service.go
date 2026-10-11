package assistantdiscovery

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/publicsearch"
	"github.com/johnjallday/ori-agent/internal/skills"
)

// Service reuses the actual source owners and their caches/parsers. These local
// reads are intentionally usable without egress. The private catalog will be
// called only by the reviewed broker, not offered as an unapproved tool method.
type SkillInventoryReader interface {
	PersonalSkillInventory(context.Context, int) skills.SkillInventory
}
type MCPInventoryReader interface {
	CapabilityInventory(context.Context, int) mcp.CapabilityInventory
}
type MCPCatalogReader interface {
	SearchCached(context.Context, mcpregistry.SearchQuery) mcpregistry.CachedSearchResult
}

type Service struct {
	skills   SkillInventoryReader
	servers  MCPInventoryReader
	registry MCPCatalogReader
	// Only the inspected host-safe source owner may perform assistant egress.
	// The legacy CLI's network fetch cannot acquire this permission by review.
	searchApproved   func(context.Context, string, int) skills.MarketplaceSearchResult
	documents        publicread.Reader
	publicSearch     *publicsearch.PublicWebSearchAdapter
	searchConfigured func() bool
}

func NewService(manager SkillInventoryReader, servers MCPInventoryReader, registry MCPCatalogReader, catalog skills.MarketplaceCatalog) *Service {
	service := &Service{skills: manager, servers: servers, registry: registry, documents: publicread.NewClient()}
	if safe, ok := catalog.(*skills.PublicMarketplaceSearcher); ok && safe != nil {
		service.searchApproved = safe.Search
	}
	return service
}

func (s *Service) Installed(ctx context.Context, query string) Result {
	result := Result{Availability: Empty, Candidates: []Candidate{}, Scope: "installed Skills folder and registered MCP configuration only; other locations unknown"}
	if query != "" && skills.ValidateMarketplaceQuery(query) != nil {
		result.Availability, result.Reason = Unavailable, "invalid_query"
		return result
	}
	inventory := skills.SkillInventory{State: "unavailable"}
	if s.skills != nil {
		inventory = s.skills.PersonalSkillInventory(ctx, 200)
	}
	servers := mcp.CapabilityInventory{State: "unavailable"}
	if s.servers != nil {
		servers = s.servers.CapabilityInventory(ctx, 200)
	}
	readAt := time.Now().UTC()
	for _, meta := range inventory.Skills {
		if !matches(query, meta.Name, meta.Description) {
			continue
		}
		candidate := Candidate{Kind: "installed_skill", Readiness: UnknownReadiness()}
		candidate.Name, candidate.Receipt.Truncated = referenceText(meta.Name, 120)
		var shortened bool
		candidate.Description, shortened = referenceText(meta.Description, 800)
		candidate.ID = identity(candidate.Kind, meta.SourceID)
		candidate.Readiness.Installed = Observed
		candidate.Readiness.Dependencies.MCPServers = safeFields(meta.RequiredMCPServers)
		candidate.Readiness.Dependencies.Tools = safeFields(meta.DeclaredTools)
		if len(candidate.Readiness.Dependencies.MCPServers)+len(candidate.Readiness.Dependencies.Tools) > 0 {
			candidate.Readiness.Dependencies.State = "declared"
		}
		if !meta.MetadataValid {
			result.Availability, result.Reason = Partial, "invalid_installed_metadata"
		}
		candidate.Receipt = metadataReceipt(candidate, "installed_skill_metadata", "current_observation", readAt, readAt,
			meta.Truncated || shortened || candidate.Receipt.Truncated)
		appendCandidate(&result, candidate)
	}
	for _, meta := range servers.Servers {
		if !matches(query, meta.Name) {
			continue
		}
		candidate := Candidate{Kind: "configured_mcp", Readiness: UnknownReadiness()}
		var shortened bool
		candidate.Name, shortened = referenceText(meta.Name, 120)
		candidate.ID = identity(candidate.Kind, meta.Name)
		candidate.Readiness.Configured = Observed
		candidate.Readiness.Enabled = NotObserved
		if meta.Enabled {
			candidate.Readiness.Enabled = Observed
		}
		// Running is not operational verification or a grant to this conversation.
		candidate.Description = "Registered configuration; status: " + string(meta.Status)
		candidate.Receipt = metadataReceipt(candidate, "mcp_configuration_metadata", "current_observation", readAt, readAt, shortened)
		appendCandidate(&result, candidate)
	}
	if inventory.State == "unavailable" || servers.State == "unavailable" {
		result.Availability, result.Reason = Unavailable, "inventory_unavailable"
		if len(result.Candidates) > 0 {
			result.Availability = Partial
		}
	} else if inventory.State == "partial" || servers.State == "partial" {
		result.Availability, result.Reason = Partial, "incomplete_inventory"
	}
	if ctx.Err() != nil {
		return Result{Availability: Unavailable, Reason: "cancelled", Candidates: []Candidate{}, Scope: result.Scope}
	}
	finishResult(&result)
	return result
}

func (s *Service) MCPCatalog(ctx context.Context, query string) Result {
	result := Result{Availability: Unavailable, Candidates: []Candidate{}, Scope: "compiled curated and enabled-source cached MCP listings; no external refresh"}
	if query != "" && skills.ValidateMarketplaceQuery(query) != nil {
		result.Reason = "invalid_query"
		return result
	}
	if s.registry == nil {
		result.Reason = "registry_unavailable"
		return result
	}
	search := s.registry.SearchCached(ctx, mcpregistry.SearchQuery{Text: query, Limit: MaxCandidates, MetadataOnly: true})
	result.Availability, result.Reason, result.Truncated = availability(search.State), search.Reason, search.Truncated
	readAt := time.Now().UTC()
	for _, match := range search.Matches {
		candidate := Candidate{Kind: "mcp_catalog", Readiness: UnknownReadiness()}
		var shortName, shortDescription bool
		candidate.Name, shortName = referenceText(match.Entry.Name, 120)
		candidate.Description, shortDescription = referenceText(match.Entry.Description, 800)
		candidate.ID = identity(candidate.Kind, match.SourceID, match.Entry.Name)
		candidate.URL, _ = PublicURL(match.Entry.Homepage)
		keys := make([]string, 0, len(match.Entry.EnvRequired))
		for key := range match.Entry.EnvRequired {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		candidate.Readiness.Dependencies.ConfigurationFields = safeFields(keys)
		if len(candidate.Readiness.Dependencies.ConfigurationFields) > 0 {
			candidate.Readiness.Dependencies.State = "declared"
		}
		freshness := "cached"
		if match.Kind == "builtin_listing" {
			freshness = "compiled"
		} else if match.Stale {
			freshness = "stale"
		}
		candidate.Receipt = metadataReceipt(candidate, match.Kind, freshness, readAt, match.ObservedAt, shortName || shortDescription || match.Truncated)
		if match.Hash != "" {
			candidate.Receipt.ContentHash = match.Hash
		}
		candidate.Receipt.Revision, _ = referenceText(match.Revision, 128)
		appendCandidate(&result, candidate)
	}
	if ctx.Err() != nil {
		return Result{Availability: Unavailable, Reason: "cancelled", Candidates: []Candidate{}, Scope: result.Scope}
	}
	finishResult(&result)
	return result
}

// skillsResult projects only a source owner's successful bounded listing.
// It cannot promote catalog availability into documentation/installed readiness.
func skillsResult(search skills.MarketplaceSearchResult) Result {
	result := Result{Availability: availability(search.State), Reason: search.Reason, Candidates: []Candidate{},
		Truncated: search.Truncated, Scope: "skills.sh catalog listing only"}
	if result.Availability != Available && result.Availability != Partial {
		return result
	}
	for _, match := range search.Results[:min(len(search.Results), MaxCandidates)] {
		// Reuse the source parser to validate/construct identity and destinations.
		validated := skills.ParseMarketplaceSearchOutput(match.Package, 1)
		if len(validated.Results) != 1 || search.ObservedAt.IsZero() {
			result.Availability, result.Reason = Partial, "invalid_listing_metadata"
			continue
		}
		canonical := validated.Results[0]
		candidate := Candidate{Kind: "skill_catalog", Name: canonical.Skill, Package: canonical.Package, Readiness: UnknownReadiness()}
		if target, err := PublicURL(match.URL); err == nil && target == canonical.URL {
			candidate.URL = target
		}
		candidate.ID = identity(candidate.Kind, match.Package)
		candidate.Receipt = metadataReceipt(candidate, "skill_catalog_listing", "current_observation", search.ObservedAt, search.ObservedAt, false)
		if search.SourceHash != "" {
			candidate.Receipt.ContentHash = search.SourceHash
		}
		candidate.Receipt.Revision, _ = referenceText(search.Revision, 128)
		appendCandidate(&result, candidate)
	}
	finishResult(&result)
	return result
}

func metadataReceipt(candidate Candidate, kind, freshness string, readAt, observed time.Time, truncated bool) Receipt {
	content, _ := json.Marshal(struct {
		Name        string    `json:"name"`
		Description string    `json:"description,omitempty"`
		Readiness   Readiness `json:"readiness"`
	}{candidate.Name, candidate.Description, candidate.Readiness})
	excerpt, shortened := referenceText(string(content), MaxExcerptRunes)
	return Receipt{SourceID: identity(kind, candidate.ID), CandidateID: candidate.ID, Kind: kind, Level: "metadata", URL: candidate.URL,
		ReadAt: readAt, ObservedAt: observed, ContentHash: contentHash(excerpt), Excerpt: excerpt, Truncated: truncated || shortened,
		Availability: Available, Freshness: freshness}
}

func matches(query string, fields ...string) bool {
	return query == "" || strings.Contains(strings.ToLower(strings.Join(fields, " ")), strings.ToLower(query))
}

func safeFields(fields []string) []string {
	out := make([]string, 0, min(len(fields), 16))
	for _, field := range fields[:min(len(fields), 16)] {
		text, _ := referenceText(field, 120)
		out = append(out, text)
	}
	return out
}

func appendCandidate(result *Result, candidate Candidate) {
	if len(result.Candidates) == MaxCandidates {
		result.Truncated = true
		return
	}
	result.Candidates = append(result.Candidates, candidate)
}

func finishResult(result *Result) {
	if result.Availability == Empty && len(result.Candidates) > 0 {
		result.Availability = Available
	}
	if result.Truncated {
		result.Availability = Partial
	}
	for len(result.Candidates) > 0 {
		data, err := json.Marshal(result)
		if err == nil && utf8.RuneCount(data) <= MaxResearchRunes {
			break
		}
		result.Candidates = result.Candidates[:len(result.Candidates)-1]
		result.Truncated, result.Availability, result.Reason = true, Partial, "research_text_limit"
	}
}

func availability(state string) Availability {
	switch Availability(state) {
	case Available, Partial, Empty, Unavailable, MissingRuntime, DisabledSource, MalformedOutput, StaleCache:
		return Availability(state)
	default:
		return Unavailable
	}
}
