package folderagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStartAssignedWorkerRefusesChangedRoot(t *testing.T) {
	root := demo(t)
	h := newHarness(t, root)
	h.ws.anchors["saved"] = root
	var checks atomic.Int64
	h.svc.deps.AssignedRoot = func(context.Context, string, string, string, string) (string, string, bool) {
		return root, "Shelves", checks.Add(1) == 1 // allowed at start only
	}
	if err := h.svc.StartAssigned(t.Context(), "me", "saved", "Ada", "reviewed", "Review metadata"); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	rec, err := h.svc.deps.Store.Read("saved")
	if err != nil || rec.State != StateFailed || rec.Map != nil || rec.Turn != nil || len(h.tickets.opened) != 1 {
		t.Fatalf("changed root reached the walk: %+v, %v", rec, err)
	}
}

func TestStartAssignedClaimsOneLookWithoutHiringOrSettingUp(t *testing.T) {
	root := demo(t)
	h := newHarness(t, root)
	h.ws.anchors["saved"] = root
	h.svc.deps.AssignedRoot = func(_ context.Context, owner, id, agent, link string) (string, string, bool) {
		return root, "My workspace", owner == "me" && id == "saved" && agent == "Ada" && link == "verified"
	}
	start := func(link string) error {
		return h.svc.StartAssigned(t.Context(), "me", "saved", "Ada", link, "Suggest a setup.")
	}
	if err := start("stale"); err == nil {
		t.Fatal("accepted stale folder")
	}
	if len(h.tickets.opened) != 0 {
		t.Fatal("opened ticket without confirmation of current folder")
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := start("verified"); err != nil {
				t.Errorf("start: %v", err)
			}
		})
	}
	group.Wait()
	h.svc.Wait()
	view, err := h.svc.View(t.Context(), "me", "saved")
	if err != nil || view.State != StateReported || view.TicketID == "" || view.AgentName != "Ada" || view.Brief != "Suggest a setup." {
		t.Fatalf("report %+v err %v", view, err)
	}
	h.svc.deps.AssignedRoot = func(context.Context, string, string, string, string) (string, string, bool) { return "", "", false }
	if _, err := h.svc.LookFurther(t.Context(), "me", "saved", nil, ""); err == nil {
		t.Fatal("stale assignment could continue the walk")
	}
	if len(h.tickets.opened) != 1 || len(h.ws.created) != 0 || len(h.setups.runs) != 0 || len(h.tickets.backlog) != 0 {
		t.Fatalf("unexpected effects: %v", h.tickets.opened)
	}
}
