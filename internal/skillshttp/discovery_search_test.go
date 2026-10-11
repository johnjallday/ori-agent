package skillshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/skills"
)

type fakeMarketplaceCatalog struct {
	result skills.MarketplaceSearchResult
	calls  int
	query  string
}

func (f *fakeMarketplaceCatalog) Search(_ context.Context, query string, _ int) skills.MarketplaceSearchResult {
	f.calls++
	f.query = query
	return f.result
}

func TestMarketplaceHTTPUsesSharedSearchWithoutInstallerRunnerOrQueryRewriting(t *testing.T) {
	f := newMarketplaceFixture(t)
	catalog := &fakeMarketplaceCatalog{result: skills.ParseMarketplaceSearchOutput("alice/community@telegram 100 installs", 8)}
	catalog.result.ObservedAt = time.Now().UTC()
	f.handler.marketplaceSearch = catalog
	if f.handler.MarketplaceCatalog() != catalog {
		t.Fatal("assistant cannot reuse HTTP's exact search owner")
	}
	rr := f.post(t, "marketplace/search", `{"query":"  Telegram communities  ","limit":12}`)
	if rr.Code != http.StatusOK || catalog.query != "  Telegram communities  " || catalog.calls != 1 {
		t.Fatalf("search: %d %s %+v", rr.Code, rr.Body.String(), catalog)
	}
	var response struct {
		Results []skills.MarketplaceMatch `json:"results"`
		Count   int                       `json:"count"`
		State   string                    `json:"availability"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 1 || len(response.Results) != 1 || response.State != "available" || response.Results[0].Package != "alice/community@telegram" {
		t.Fatalf("browser schema changed: %+v", response)
	}
	if len(f.tool.calls) != 0 {
		t.Fatalf("search invoked install/init runner: %v", f.tool.calls)
	}
	f.assertNothingUnderHome(t)
	f.assertDownloadsCleaned(t)
}

func TestMarketplaceHTTPReportsRuntimeFailureMalformedAndUnverifiedEmpty(t *testing.T) {
	for _, test := range []struct {
		state, reason string
		status        int
	}{
		{"missing_runtime", "node_required", http.StatusServiceUnavailable},
		{"missing_runtime", "skills_runtime_required", http.StatusServiceUnavailable},
		{"unavailable", "timeout", http.StatusBadGateway},
		{"unavailable", "unverified_empty", http.StatusBadGateway},
		{"malformed_output", "unrecognized_output", http.StatusBadGateway},
	} {
		t.Run(test.reason, func(t *testing.T) {
			f := newMarketplaceFixture(t)
			f.handler.marketplaceSearch = &fakeMarketplaceCatalog{result: skills.MarketplaceSearchResult{State: test.state, Reason: test.reason, Results: []skills.MarketplaceMatch{}}}
			rr := f.post(t, "marketplace/search", `{"query":"telegram"}`)
			if rr.Code != test.status || !strings.Contains(rr.Body.String(), test.reason) || strings.Contains(rr.Body.String(), "No skills matched") || len(f.tool.calls) != 0 {
				t.Fatalf("failure: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestMarketplaceHTTPRefusesControlOptionsAndLargeRequestBeforeSearch(t *testing.T) {
	f := newMarketplaceFixture(t)
	catalog := &fakeMarketplaceCatalog{}
	f.handler.marketplaceSearch = catalog
	for _, body := range []string{`{"query":"telegram --install"}`, `{"query":"telegram\ncommunity"}`, `{"query":"` + strings.Repeat("x", 257) + `"}`, `{"query":"telegram","padding":"` + strings.Repeat("x", 5000) + `"}`} {
		rr := f.post(t, "marketplace/search", body)
		if rr.Code != http.StatusBadRequest || catalog.calls != 0 || len(f.tool.calls) != 0 {
			t.Fatalf("invalid input reached source: %d %s", rr.Code, rr.Body.String())
		}
	}
}
