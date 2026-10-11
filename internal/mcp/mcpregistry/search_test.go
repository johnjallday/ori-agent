package mcpregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistryCachedSearchUsesCuratedMetadataOfflineAndPreservesBrowserFilters(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	result := store.SearchCached(context.Background(), SearchQuery{Text: "SLACK", Category: "communication", Source: "ORI CURATED", Limit: 8, MetadataOnly: true})
	if result.State != "available" || len(result.Matches) != 1 {
		t.Fatalf("offline curated: %+v", result)
	}
	match := result.Matches[0]
	if match.Kind != "builtin_listing" || match.SourceID != "builtin-curated" || match.Stale || match.ObservedAt.IsZero() || match.Entry.Command != "" || match.Entry.EnvRequired["SLACK_BOT_TOKEN"] != "" {
		t.Fatalf("metadata: %+v", match)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Text: "not-present"}); got.State != "empty" || len(got.Matches) != 0 {
		t.Fatalf("curated empty: %+v", got)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Category: "database", Limit: 1}); got.State != "partial" || !got.Truncated || len(got.Matches) != 1 || got.Matches[0].Entry.Command == "" {
		t.Fatalf("UI-compatible listing: %+v", got)
	}
	_, cachePath := store.PersistencePaths()
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatal("read-only search wrote/refreshed the cache")
	}
}

func registryFixture(t *testing.T) *Store {
	t.Helper()
	store := NewStoreAt(t.TempDir())
	if err := store.AddSource(RegistrySource{ID: "remote", Name: "Example Catalog", URL: "http://127.0.0.1:1/private", SourceType: "url", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRegistryCachedSearchDistinguishesUnknownEmptyDisabledStaleAndRemovedSources(t *testing.T) {
	store := registryFixture(t)
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog"}); got.State != "unavailable" || got.Reason != "unverified_empty_cache" {
		t.Fatalf("missing cache: %+v", got)
	}
	if err := store.SetCache([]RegistryEntry{}); err != nil {
		t.Fatal(err)
	}
	if !store.IsCacheValid() {
		t.Fatal("empty cache cannot be used as an automatic refresh loop")
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog"}); got.State != "unavailable" {
		t.Fatalf("silent fetcher failure became verified empty: %+v", got)
	}
	entry := RegistryEntry{Name: "telegram", Description: "community management", Source: "Example Catalog", Command: "never-execute", Args: []string{"secret-argv"}, Env: map[string]string{"TOKEN": "SECRET_CONFIG_SENTINEL"}, EnvRequired: map[string]string{"TOKEN": "SECRET_DESCRIPTION_SENTINEL"}}
	if err := store.SetCache([]RegistryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	got := store.SearchCached(context.Background(), SearchQuery{Text: "telegram", MetadataOnly: true})
	if got.State != "available" || len(got.Matches) != 1 || got.Matches[0].Stale {
		t.Fatalf("fresh cache: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"SECRET_CONFIG_SENTINEL", "SECRET_DESCRIPTION_SENTINEL", "never-execute", "secret-argv", "127.0.0.1"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("metadata leaked %s: %s", forbidden, encoded)
		}
	}
	observed := got.Matches[0].ObservedAt
	store.InvalidateCache()
	got = store.SearchCached(context.Background(), SearchQuery{Text: "telegram"})
	if got.State != "stale_cache" || !got.Matches[0].Stale || !got.Matches[0].ObservedAt.Equal(observed) {
		t.Fatalf("invalidated timestamp lost: %+v", got)
	}
	if err := store.RemoveSource("remote"); err != nil {
		t.Fatal(err)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Text: "telegram"}); len(got.Matches) != 0 {
		t.Fatalf("removed source leaked: %+v", got)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog"}); got.State != "disabled_source" {
		t.Fatalf("disabled/removed: %+v", got)
	}
}

func TestRegistryCacheOwnsCopiesAndSurvivesRestartWithAttributedEntries(t *testing.T) {
	dir := t.TempDir()
	store := NewStoreAt(dir)
	if err := store.AddSource(RegistrySource{ID: "remote", Name: "Example Catalog", SourceType: "url", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	entry := RegistryEntry{Name: "test", Source: "old name", SourceID: "remote", Args: []string{"original"}, Env: map[string]string{"key": "original"}}
	if err := store.SetCache([]RegistryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	entry.Args[0], entry.Env["key"] = "mutated", "mutated"
	first := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog"})
	if len(first.Matches) != 1 || first.Matches[0].Entry.Source != "Example Catalog" || first.Matches[0].Entry.Env["key"] != "original" {
		t.Fatalf("ownership: %+v", first)
	}
	first.Matches[0].Entry.Env["key"] = "mutated"
	store.GetCachedEntries()[0].Args[0] = "mutated"
	second := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog"})
	if second.Matches[0].Entry.Env["key"] != "original" || second.Matches[0].Entry.Args[0] != "original" {
		t.Fatalf("cache shared mutable maps: %+v", second)
	}
	restored := NewStoreAt(dir).SearchCached(context.Background(), SearchQuery{Source: "Example Catalog"})
	if !reflect.DeepEqual(second, restored) {
		t.Fatalf("restart changed read: %+v %+v", second, restored)
	}
	builtin, err := NewFetcher().FetchSource(RegistrySource{ID: "builtin-curated", Name: "Ori Curated", SourceType: "builtin"})
	if err != nil || builtin[0].SourceID != "builtin-curated" {
		t.Fatalf("builtin characterization: %+v %v", builtin, err)
	}
	builtin[0].Args[0] = "mutated"
	if got := store.SearchCached(context.Background(), SearchQuery{Text: "filesystem"}); got.Matches[0].Entry.Args[0] == "mutated" {
		t.Fatal("curated data shares caller mutation")
	}
}

func TestRegistrySearchBoundsScanAndHandlesCancellationAndFutureCache(t *testing.T) {
	store := registryFixture(t)
	entries := make([]RegistryEntry, MaxSearchEntries+1)
	for i := range entries {
		entries[i] = RegistryEntry{Name: fmt.Sprintf("item-%d", i), SourceID: "remote", Source: "Example Catalog"}
	}
	if err := store.SetCache(entries); err != nil {
		t.Fatal(err)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog", Limit: 8}); !got.Truncated || len(got.Matches) != 8 || got.State != "partial" {
		t.Fatalf("cap: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := store.SearchCached(ctx, SearchQuery{}); got.State != "unavailable" || len(got.Matches) != 0 {
		t.Fatalf("cancel: %+v", got)
	}
	store.mu.Lock()
	store.cacheAt = time.Now().Add(time.Hour)
	store.mu.Unlock()
	if store.IsCacheValid() {
		t.Fatal("future timestamp is not fresh")
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "Example Catalog", Limit: 8}); got.State != "stale_cache" || !got.Matches[0].Stale {
		t.Fatalf("future timestamp: %+v", got)
	}
}

func TestRegistrySearchConcurrentInvalidationCannotShareForeignMutableCache(t *testing.T) {
	store := registryFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := store.SetCache([]RegistryEntry{{Name: "telegram", SourceID: "remote", Source: "Example Catalog", EnvRequired: map[string]string{"TOKEN": "not exposed"}}}); err != nil {
					t.Error(err)
				}
				store.InvalidateCache()
				result := store.SearchCached(context.Background(), SearchQuery{Text: "telegram", MetadataOnly: true, Limit: 8})
				for _, match := range result.Matches {
					if match.Entry.Env != nil || match.Entry.EnvRequired["TOKEN"] != "" {
						t.Error("unsafe metadata")
					}
				}
			}
		}()
	}
	wg.Wait()
}
