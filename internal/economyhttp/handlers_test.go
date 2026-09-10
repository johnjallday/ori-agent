package economyhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/economy"
)

func newHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewHandler(economy.NewService(economy.NewSQLiteStore(db), nil, nil, nil))
}

func TestOverviewReturnsBalances(t *testing.T) {
	handler := newHandler(t)

	recorder := httptest.NewRecorder()
	handler.GetOverview(recorder, httptest.NewRequest(http.MethodGet, "/api/economy", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var overview economy.Overview
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if overview.Craft != 0 || overview.Harvest != 0 {
		t.Fatalf("overview = %+v, want empty balances on a fresh install", overview)
	}
	if overview.Energy.DailyFigure != economy.DefaultDailyEnergyTokens {
		t.Fatalf("daily energy figure = %d, want the default %d",
			overview.Energy.DailyFigure, economy.DefaultDailyEnergyTokens)
	}
	if overview.PendingByWorkspace == nil {
		t.Fatal("pending_by_workspace is null, want an empty object the client can index")
	}
}

// With the flag off every route answers 404, so a client sees "this does not
// exist" rather than an empty economy it would render an empty HUD for (FR47).
func TestEveryRouteIs404WhenTheFlagIsOff(t *testing.T) {
	t.Setenv("ORI_ECONOMY_ENABLED", "false")
	handler := newHandler(t)

	overview := httptest.NewRecorder()
	handler.GetOverview(overview, httptest.NewRequest(http.MethodGet, "/api/economy", nil))
	if overview.Code != http.StatusNotFound {
		t.Fatalf("overview status = %d, want 404", overview.Code)
	}

	harvest := httptest.NewRecorder()
	handler.Harvest(harvest, httptest.NewRequest(http.MethodPost, "/api/economy/harvest",
		strings.NewReader(`{"workspace_id":"ws1","task_id":"task-1"}`)))
	if harvest.Code != http.StatusNotFound {
		t.Fatalf("harvest status = %d, want 404", harvest.Code)
	}
}

// An unwired economy answers exactly like a disabled one.
func TestUnwiredEconomyIs404(t *testing.T) {
	handler := NewHandler(nil)

	recorder := httptest.NewRecorder()
	handler.GetOverview(recorder, httptest.NewRequest(http.MethodGet, "/api/economy", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestHarvestBanksNothingWhenNothingIsPending(t *testing.T) {
	handler := newHandler(t)

	recorder := httptest.NewRecorder()
	handler.Harvest(recorder, httptest.NewRequest(http.MethodPost, "/api/economy/harvest",
		strings.NewReader(`{"workspace_id":"ws1","task_id":"task-1"}`)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var result economy.BankResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Banked != 0 {
		t.Fatalf("banked = %d, want 0", result.Banked)
	}
}

func TestHarvestRequiresATaskID(t *testing.T) {
	handler := newHandler(t)

	recorder := httptest.NewRecorder()
	handler.Harvest(recorder, httptest.NewRequest(http.MethodPost, "/api/economy/harvest",
		strings.NewReader(`{"workspace_id":"ws1"}`)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestOverviewRejectsTheWrongMethod(t *testing.T) {
	handler := newHandler(t)

	recorder := httptest.NewRecorder()
	handler.GetOverview(recorder, httptest.NewRequest(http.MethodPost, "/api/economy", nil))

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}
