package learningmissionshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
)

func TestCapacityFailureHasDistinctSanitizedOwnerResponse(t *testing.T) {
	w := httptest.NewRecorder()
	respond(w, nil, learningmissions.ErrStorageLimit)
	if w.Code != http.StatusTooManyRequests || w.Body.String() != "{\"error\":\"storage_limit_reached\"}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDiagnosticsRouteNeverEnrollsDiscoversSourcesOrGenerates(t *testing.T) {
	f := newFixture(t)
	for range 3 {
		w := request(f, http.MethodGet, "/api/learning-missions/diagnostics", "")
		var data learningmissions.LocalDiagnostics
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Coverage != "available" || data.Offers != 0 || data.DesignCalls != 0 {
			t.Fatal("diagnostics", w.Code, w.Body.String())
		}
	}
	var owners int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_owners`).Scan(&owners); err != nil || owners != 0 || f.source.calls != 0 {
		t.Fatal("read caused enrollment or source access", err)
	}
}
