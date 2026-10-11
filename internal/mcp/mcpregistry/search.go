package mcpregistry

import (
	"context"
	"strings"
	"time"
)

const MaxSearchEntries = 2000 // existing browser loads the whole bounded listing

// SearchQuery filters metadata only. SearchCached never starts an MCP server or
// refreshes a URL. The UI retains its separately owned refresh operation.
type SearchQuery struct {
	Text         string
	Category     string
	Source       string
	Limit        int
	MetadataOnly bool
}

type CachedMatch struct {
	Entry      RegistryEntry
	SourceID   string
	Kind       string // builtin_listing or cached_listing
	ObservedAt time.Time
	Stale      bool
	Truncated  bool
	Hash       string
	Revision   string
}

type CachedSearchResult struct {
	State     string
	Reason    string
	Matches   []CachedMatch
	Truncated bool
}

// SearchCached reads source configuration and cache together under one owner
// lock. It neither fills nor duplicates the registry's existing cache. Compiled
// curated entries remain available offline and are labelled as such; remote
// cache timestamps do not prove a live service or operational readiness.
func (s *Store) SearchCached(ctx context.Context, query SearchQuery) CachedSearchResult {
	result := CachedSearchResult{State: "empty", Matches: []CachedMatch{}}
	if s == nil || ctx.Err() != nil {
		result.State, result.Reason = "unavailable", "registry_unavailable"
		return result
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if query.Limit <= 0 || query.Limit > MaxSearchEntries {
		query.Limit = MaxSearchEntries
	}
	now := time.Now().UTC()
	age := now.Sub(s.cacheAt)
	stale := s.cacheInvalidated || s.cacheAt.IsZero() || age < 0 || age >= cacheTTL
	seen := map[string]bool{}
	add := func(entry RegistryEntry, sourceID, kind string, observed time.Time, old bool) {
		if len(sourceID) > 256 || len(entry.Name) > 256 {
			result.Truncated = true
			return
		}
		shortened := metadataClipped(entry)
		result.Truncated = result.Truncated || shortened
		if !MatchesSearch(entry, query) {
			return
		}
		key := sourceID + "\x00" + entry.Name
		if seen[key] {
			return
		}
		seen[key] = true
		if len(result.Matches) >= query.Limit {
			result.Truncated = true
			return
		}
		if query.MetadataOnly {
			entry = discoveryEntry(entry)
		} else {
			entry = cloneEntry(entry)
		}
		match := CachedMatch{Entry: entry, SourceID: sourceID, Kind: kind, ObservedAt: observed, Stale: old, Truncated: shortened}
		if observation, ok := s.observations[sourceID]; ok {
			match.Hash, match.Revision = observation.Hash, observation.Revision
		}
		result.Matches = append(result.Matches, match)
	}
	// Legacy caches stored display names, not IDs. Accept a unique configured
	// name only; renamed, removed, disabled or ambiguous sources are not exposed.
	byName := map[string][]RegistrySource{}
	byID := map[string]RegistrySource{}
	enabled, remote := 0, false
	remoteSources := map[string]bool{}
	remoteStale := false
	for i, source := range s.sources {
		if i == 64 {
			result.Truncated = true
			break
		}
		byName[source.Name] = append(byName[source.Name], source)
		byID[source.ID] = source
		if !source.Enabled || !matchesSource(source.Name, query.Source) {
			continue
		}
		enabled++
		if source.ID == "builtin-curated" && source.SourceType == "builtin" && source.IsBuiltin {
			for _, entry := range builtinServers {
				entry.Source, entry.SourceID = source.Name, source.ID
				add(entry, source.ID, "builtin_listing", now, false)
			}
		} else {
			remote = true
			observation, observed := s.observations[source.ID]
			observed = observed && observation.Version == sourceVersion(source) && !observation.ReadAt.IsZero()
			remoteSources[source.ID] = observed
			if observed {
				elapsed := now.Sub(observation.ReadAt)
				remoteStale = remoteStale || elapsed < 0 || elapsed >= cacheTTL
			}
		}
	}
	for i, entry := range s.cache {
		if i == MaxSearchEntries {
			result.Truncated = true
			break
		}
		if ctx.Err() != nil {
			return CachedSearchResult{State: "unavailable", Reason: "cancelled", Matches: []CachedMatch{}}
		}
		var source RegistrySource
		if entry.SourceID != "" {
			source = byID[entry.SourceID]
		} else if sources := byName[entry.Source]; len(sources) == 1 {
			source = sources[0]
		}
		if !source.Enabled || source.ID == "" || source.SourceType == "builtin" {
			continue
		}
		entry.Source = source.Name
		observed, old := s.cacheAt, stale
		if observation, ok := s.observations[source.ID]; ok {
			if observation.Version != sourceVersion(source) {
				continue
			}
			observed = observation.ReadAt
			elapsed := now.Sub(observed)
			old = observed.IsZero() || elapsed < 0 || elapsed >= cacheTTL
		}
		if _, selected := remoteSources[source.ID]; selected {
			remoteSources[source.ID] = true
			remoteStale = remoteStale || old
		}
		add(entry, source.ID, "cached_listing", observed, old)
	}
	missingRemote := false
	for _, covered := range remoteSources {
		missingRemote = missingRemote || !covered
	}
	switch {
	case enabled == 0:
		result.State, result.Reason = "disabled_source", "no_enabled_source"
	case remote && missingRemote:
		result.State, result.Reason = "unavailable", "unverified_empty_cache"
		if len(result.Matches) > 0 {
			result.State = "partial"
		}
	case remote && remoteStale:
		result.State, result.Reason = "stale_cache", "refresh_requires_review"
	case len(result.Matches) > 0:
		result.State = "available"
	}
	if result.Truncated && (result.State == "available" || result.State == "empty") {
		result.State, result.Reason = "partial", "candidate_limit"
	}
	return result
}

func MatchesSearch(entry RegistryEntry, query SearchQuery) bool {
	text := strings.ToLower(strings.TrimSpace(query.Text))
	category := strings.ToLower(strings.TrimSpace(query.Category))
	if text != "" && !strings.Contains(strings.ToLower(searchText(entry.Name, 256)+" "+searchText(entry.Description, 4096)+" "+searchText(entry.Category, 128)+" "+searchTags(entry.Tags)), text) {
		return false
	}
	return (category == "" || category == "all" || strings.EqualFold(category, entry.Category)) && matchesSource(entry.Source, query.Source)
}

func matchesSource(name, filter string) bool {
	filter = strings.TrimSpace(filter)
	return filter == "" || strings.EqualFold(filter, "all") || strings.EqualFold(name, filter)
}

func cloneEntry(entry RegistryEntry) RegistryEntry {
	entry.Args = append([]string(nil), entry.Args...)
	entry.Tags = append([]string(nil), entry.Tags...)
	entry.Env = cloneStrings(entry.Env)
	entry.EnvRequired = cloneStrings(entry.EnvRequired)
	return entry
}

func searchText(text string, bytes int) string {
	return text[:min(len(text), bytes)]
}

func searchTags(tags []string) string {
	bounded := make([]string, 0, min(len(tags), 16))
	for _, tag := range tags[:min(len(tags), 16)] {
		bounded = append(bounded, searchText(tag, 128))
	}
	return strings.Join(bounded, " ")
}

func metadataClipped(entry RegistryEntry) bool {
	return len(entry.Description) > 4096 || len(entry.Category) > 128 || len(entry.Homepage) > 2000 || len(entry.EnvRequired) > 16 || len(entry.Tags) > 16
}

func discoveryEntry(entry RegistryEntry) RegistryEntry {
	// Omit installation argv, fixed environment and secret-looking requirement
	// prose before returning metadata to the assistant composition service.
	out := RegistryEntry{Name: searchText(entry.Name, 256), Description: searchText(entry.Description, 4096),
		Category: searchText(entry.Category, 128), Homepage: searchText(entry.Homepage, 2000),
		Transport: searchText(entry.Transport, 32), Source: searchText(entry.Source, 128),
		SourceID: searchText(entry.SourceID, 256), EnvRequired: map[string]string{}}
	for key := range entry.EnvRequired {
		if len(out.EnvRequired) == 16 {
			break
		}
		out.EnvRequired[searchText(key, 120)] = ""
	}
	return out
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
