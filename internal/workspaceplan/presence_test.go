package workspaceplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// CountPlans answers "does this workspace have any Plan, and how many of each
// kind" without loading one. Both stores must agree, the way they do for every
// other read, and it must be scoped to the workspace asked about.
func TestStoreCountPlansSplitsActiveFromHistoryPerWorkspace(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		seed("ws-2")

		empty, err := store.CountPlans(ctx, "ws-1")
		if err != nil {
			t.Fatalf("count an empty workspace: %v", err)
		}
		if empty != (PlanCounts{}) || empty.Total() != 0 {
			t.Fatalf("an empty workspace counts %+v, want zero", empty)
		}

		for _, id := range []string{"plan-a", "plan-b", "plan-c"} {
			if err := store.CreatePlan(ctx, testPlan(id)); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
		}
		other := testPlan("plan-other")
		other.WorkspaceID = "ws-2"
		if err := store.CreatePlan(ctx, other); err != nil {
			t.Fatalf("create the other workspace's plan: %v", err)
		}
		if err := store.ArchivePlan(ctx, "ws-1", "plan-c", "cancelled", time.Now().UTC()); err != nil {
			t.Fatalf("archive: %v", err)
		}

		got, err := store.CountPlans(ctx, "ws-1")
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if got.Active != 2 || got.History != 1 || got.Total() != 3 {
			t.Fatalf("ws-1 counts %+v, want 2 active and 1 history", got)
		}
		if far, err := store.CountPlans(ctx, "ws-2"); err != nil || far.Total() != 1 || far.Active != 1 {
			t.Fatalf("ws-2 counts %+v (%v), want its own single active plan", far, err)
		}
		if none, err := store.CountPlans(ctx, "ws-unknown"); err != nil || none.Total() != 0 {
			t.Fatalf("an unknown workspace counts %+v (%v), want zero", none, err)
		}
	})
}

// The presence route is how the workspace chrome learns whether to link to the
// Plans list. It is read-only and content-free, and a history-only workspace
// counts as having plans: an archived plan is still a record someone may need.
func TestAPIPlanPresenceReportsActiveAndHistoryWithoutContent(t *testing.T) {
	// newTestAPI does not mount the route, so mount it on the same handler.
	service := NewService(NewMemoryStore())
	handler := NewHandler(service)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans", handler.PlanCollection)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}", handler.PlanItem)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/archive", handler.ArchivePlan)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plan-presence", handler.PlanPresence)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	status, none := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plan-presence", "")
	if status != http.StatusOK || none["has_plans"] != false || none["active"] != float64(0) || none["history"] != float64(0) {
		t.Fatalf("an empty workspace reports %v (%d)", none, status)
	}

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	status, one := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plan-presence", "")
	if status != http.StatusOK || one["has_plans"] != true || one["active"] != float64(1) || one["history"] != float64(0) {
		t.Fatalf("one active plan reports %v (%d)", one, status)
	}
	// Nothing about the plan itself is in the answer.
	for key := range one {
		switch key {
		case "studio_id", "has_plans", "active", "history":
		default:
			t.Errorf("presence leaks %q: %v", key, one)
		}
	}

	planID, _ := created["id"].(string)
	if status, body := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/archive", `{"reason":"done"}`); status != http.StatusOK {
		t.Fatalf("archive status = %d (%v)", status, body)
	}
	_, historyOnly := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plan-presence", "")
	if historyOnly["has_plans"] != true || historyOnly["active"] != float64(0) || historyOnly["history"] != float64(1) {
		t.Fatalf("a history-only workspace reports %v, want has_plans with one in history", historyOnly)
	}

	// Another workspace's plans are not this workspace's.
	_, elsewhere := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-2/plan-presence", "")
	if elsewhere["has_plans"] != false {
		t.Fatalf("ws-2 reports %v, want none", elsewhere)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if status, _ := doJSON(t, server, method, "/api/workspaces/ws-1/plan-presence", ""); status != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, status)
		}
	}
}

// Asking whether a workspace has plans must not change which of them are
// active. Listing applies retention (an idle draft moves to History), so a
// presence check built on the list would silently archive plans; this one is a
// pure read.
func TestPlanPresenceDoesNotApplyRetention(t *testing.T) {
	store := NewMemoryStore()
	idle := testPlan("plan-idle")
	long := time.Now().UTC().Add(-365 * 24 * time.Hour)
	idle.CreatedAt, idle.UpdatedAt, idle.LastActivityAt = long, long, long
	if err := store.CreatePlan(context.Background(), idle); err != nil {
		t.Fatalf("create: %v", err)
	}
	service := NewService(store)

	counts, err := service.Counts(context.Background(), testWorkspaceID)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Total() != 1 {
		t.Fatalf("counts %+v, want the one plan", counts)
	}
	again, _ := store.CountPlans(context.Background(), testWorkspaceID)
	if again.History != 0 {
		t.Fatalf("a presence read archived an idle plan: %+v", again)
	}

	// For contrast, a list is allowed to: it is the call that applies retention,
	// so the premise of this test (this plan is old enough to be archived) holds.
	if _, err := service.List(context.Background(), testWorkspaceID, ListFilter{Scope: ScopeActive}); err != nil {
		t.Fatalf("list: %v", err)
	}
	after, _ := store.CountPlans(context.Background(), testWorkspaceID)
	if after.History != 1 || after.Total() != 1 {
		t.Fatalf("listing should have archived the idle plan (premise): %+v", after)
	}
}
