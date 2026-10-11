package mcpregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/publicread"
)

var ErrPublicSource = errors.New("public registry source changed or unavailable")
var githubSource = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

type PublicSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Version string `json:"version"`
}
type sourceObservation struct {
	Version  string    `json:"version"`
	ReadAt   time.Time `json:"read_at"`
	Hash     string    `json:"hash"`
	Revision string    `json:"revision,omitempty"`
}

func sourceVersion(source RegistrySource) string {
	data, _ := json.Marshal([]any{source.ID, source.Name, source.URL, source.SourceType, source.Enabled, source.IsBuiltin})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func publicSource(source RegistrySource) (PublicSource, error) {
	if !source.Enabled || source.IsBuiltin || source.ID == "" || len(source.ID) > 128 || len(source.Name) > 120 {
		return PublicSource{}, ErrPublicSource
	}
	target := source.URL
	switch source.SourceType {
	case "url":
	case "github":
		if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
			if !githubSource.MatchString(target) {
				return PublicSource{}, ErrPublicSource
			}
			target = githubRawURL(target)
		}
	default:
		return PublicSource{}, ErrPublicSource
	}
	if _, err := publicread.ValidateURL(target); err != nil {
		return PublicSource{}, ErrPublicSource
	}
	return PublicSource{ID: source.ID, Name: source.Name, URL: target, Version: sourceVersion(source)}, nil
}

// PublicSources lists only exactly reviewable configured sources. Reading this
// configuration does not resolve DNS, refresh anything or start a server.
func (s *Store) PublicSources(ctx context.Context) []PublicSource {
	out := []PublicSource{}
	if s == nil || ctx.Err() != nil {
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	for _, source := range s.sources[:min(len(s.sources), 64)] {
		item, err := publicSource(source)
		if err == nil && !seen[item.ID] {
			out = append(out, item)
			seen[item.ID] = true
		}
	}
	return out
}
func (s *Store) ValidatePublicSource(ctx context.Context, expected PublicSource) error {
	if ctx.Err() != nil {
		return ErrPublicSource
	}
	matches := 0
	for _, source := range s.PublicSources(ctx) {
		if source.ID == expected.ID {
			if source.URL != expected.URL || source.Version != expected.Version {
				return ErrPublicSource
			}
			matches++
		}
	}
	if matches != 1 {
		return ErrPublicSource
	}
	return nil
}

// ParseRemoteRegistry is the existing registry protocol parser, shared by the
// browser fetch owner and approved public refresh. Missing/null arrays are not
// verified empty; executable setup fields remain data owned by the setup UI.
func ParseRemoteRegistry(body []byte) (RemoteRegistry, error) {
	var envelope map[string]json.RawMessage
	if len(body) > publicread.MaxResponseBytes || json.Unmarshal(body, &envelope) != nil {
		return RemoteRegistry{}, ErrPublicSource
	}
	raw, ok := envelope["servers"]
	if !ok || string(raw) == "null" {
		return RemoteRegistry{}, ErrPublicSource
	}
	var registry RemoteRegistry
	if json.Unmarshal(body, &registry) != nil || len(registry.Servers) > MaxSearchEntries {
		return RemoteRegistry{}, ErrPublicSource
	}
	for _, entry := range registry.Servers {
		if entry.Name == "" || len(entry.Name) > 256 {
			return RemoteRegistry{}, ErrPublicSource
		}
	}
	return registry, nil
}

// RefreshPublic reads one exact reviewed source and updates only its portion
// of the existing cache. Other sources' timestamps never become fresh by proxy.
func (s *Store) RefreshPublic(ctx context.Context, expected PublicSource, reader publicread.Reader, revalidate func(context.Context) error) error {
	if reader == nil || revalidate == nil || revalidate(ctx) != nil || s.ValidatePublicSource(ctx, expected) != nil {
		return ErrPublicSource
	}
	response, err := reader.Read(ctx, publicread.Request{URL: expected.URL, ContentTypes: []string{"application/json", "text/plain"}})
	if err != nil || response.URL != expected.URL || response.ReadAt.IsZero() {
		return ErrPublicSource
	}
	registry, err := ParseRemoteRegistry(response.Body)
	if err != nil {
		return ErrPublicSource
	}
	if revalidate(ctx) != nil || s.ValidatePublicSource(ctx, expected) != nil {
		return ErrPublicSource
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, source := range s.sources {
		item, err := publicSource(source)
		if err == nil && item.ID == expected.ID && item.Version == expected.Version && item.URL == expected.URL {
			found = true
		}
	}
	if !found || ctx.Err() != nil {
		return ErrPublicSource
	}
	entries := make([]RegistryEntry, 0, min(len(s.cache)+len(registry.Servers), MaxSearchEntries))
	for _, entry := range s.cache {
		if entry.SourceID != expected.ID && (entry.SourceID != "" || entry.Source != expected.Name) {
			entries = append(entries, cloneEntry(entry))
		}
	}
	for _, entry := range registry.Servers {
		entry.SourceID, entry.Source = expected.ID, expected.Name
		entries = append(entries, cloneEntry(entry))
	}
	if len(entries) > MaxSearchEntries {
		return ErrPublicSource
	}
	observations := map[string]sourceObservation{}
	for id, observation := range s.observations {
		observations[id] = observation
	}
	observations[expected.ID] = sourceObservation{Version: expected.Version, ReadAt: response.ReadAt.UTC(), Hash: response.Hash, Revision: response.ETag}
	if err := s.writeCache(cacheFileData{FetchedAt: s.cacheAt, Entries: entries, Observations: observations}); err != nil {
		return ErrPublicSource
	}
	s.cache, s.observations = entries, observations
	return nil
}
