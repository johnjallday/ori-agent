package mcpregistry

import (
	"context"
	"errors"
	"github.com/johnjallday/ori-agent/internal/publicread"
	"strings"
	"testing"
	"time"
)

type publicReaderFunc func(context.Context, publicread.Request) (publicread.Response, error)

func (f publicReaderFunc) Read(ctx context.Context, request publicread.Request) (publicread.Response, error) {
	return f(ctx, request)
}
func TestPublicRegistryRefreshExactSourceVerifiedEmptyAndRestart(t *testing.T) {
	root := t.TempDir()
	store := NewStoreAt(root)
	source := RegistrySource{ID: "fixture", Name: "Fixture", URL: "https://example.com/registry.json", SourceType: "url", Enabled: true}
	if err := store.AddSource(source); err != nil {
		t.Fatal(err)
	}
	descriptor, err := publicSource(source)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	reader := publicReaderFunc(func(_ context.Context, request publicread.Request) (publicread.Response, error) {
		calls++
		if request.URL != descriptor.URL || request.SameOriginRedirects {
			t.Fatal("refresh changed egress scope")
		}
		return publicread.Response{URL: request.URL, Body: []byte(`{"servers":[]}`), ReadAt: time.Now().UTC(), Hash: strings.Repeat("a", 64)}, nil
	})
	if err := store.RefreshPublic(context.Background(), descriptor, reader, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Store{store, NewStoreAt(root)} {
		result := s.SearchCached(context.Background(), SearchQuery{Source: "Fixture", Limit: 8})
		if result.State != "empty" || len(result.Matches) != 0 {
			t.Fatalf("verified empty lost: %+v", result)
		}
	}
	if calls != 1 {
		t.Fatal("implicit refresh on cache read")
	}
}
func TestPublicRegistryRefreshDoesNotFreshenOtherSourcesOrPersistRevokedReads(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	for _, source := range []RegistrySource{{ID: "one", Name: "One", URL: "https://example.com/one.json", SourceType: "url", Enabled: true}, {ID: "two", Name: "Two", URL: "https://example.com/two.json", SourceType: "url", Enabled: true}} {
		if err := store.AddSource(source); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetCache([]RegistryEntry{{Name: "old", SourceID: "two", Source: "Two"}}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.cacheAt = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()
	descriptor := store.PublicSources(context.Background())[0]
	reader := publicReaderFunc(func(_ context.Context, r publicread.Request) (publicread.Response, error) {
		return publicread.Response{URL: r.URL, Body: []byte(`{"servers":[{"name":"new","env":{"PRIVATE_KEY":"must not be provider data"}}]}`), ReadAt: time.Now().UTC(), Hash: strings.Repeat("b", 64)}, nil
	})
	if err := store.RefreshPublic(context.Background(), descriptor, reader, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "Two"}); got.State != "stale_cache" {
		t.Fatalf("other source freshened: %+v", got)
	}
	if got := store.SearchCached(context.Background(), SearchQuery{Source: "One", MetadataOnly: true}); got.State != "available" || len(got.Matches) != 1 || got.Matches[0].Entry.Env != nil || got.Matches[0].Hash != strings.Repeat("b", 64) {
		t.Fatalf("fresh metadata: %+v", got)
	}
	validations := 0
	if err := store.RefreshPublic(context.Background(), descriptor, reader, func(context.Context) error {
		validations++
		if validations > 1 {
			return errors.New("revoked")
		}
		return nil
	}); err == nil {
		t.Fatal("revoked read persisted")
	}
}
func TestPublicRegistryRefusesPrivateDisabledChangedAndMalformedSources(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	for _, source := range []RegistrySource{{ID: "private", Name: "Private", URL: "http://127.0.0.1/registry.json", SourceType: "url", Enabled: true}, {ID: "disabled", Name: "Disabled", URL: "https://example.com/registry.json", SourceType: "url"}} {
		if err := store.AddSource(source); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.PublicSources(context.Background())) != 0 {
		t.Fatal("private/disabled registry exposed")
	}
	source := RegistrySource{ID: "public", Name: "Public", URL: "https://example.com/registry.json", SourceType: "url", Enabled: true}
	if err := store.AddSource(source); err != nil {
		t.Fatal(err)
	}
	descriptor := store.PublicSources(context.Background())[0]
	changed := descriptor
	changed.URL = "https://example.com/other.json"
	calls := 0
	reader := publicReaderFunc(func(context.Context, publicread.Request) (publicread.Response, error) {
		calls++
		return publicread.Response{}, nil
	})
	if err := store.RefreshPublic(context.Background(), changed, reader, func(context.Context) error { return nil }); err == nil || calls != 0 {
		t.Fatal("changed source reached network")
	}
	for _, body := range []string{`{}`, `{"servers":null}`, `{"servers":[]} trailing`, `{"servers":[{}]}`, strings.Repeat("x", (1<<20)+1)} {
		if _, err := ParseRemoteRegistry([]byte(body)); err == nil {
			t.Fatal("malformed registry became verified empty")
		}
	}
}
