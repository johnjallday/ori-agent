package folderagent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestReportedObserver_OnlyAfterDurableLook(t *testing.T) {
	h := newHarness(t, build(t, "notes.txt"))
	calls := 0
	var observedErr error
	h.svc.deps.OnReported = func(_ context.Context, userID, workspaceID string) {
		calls++
		record, err := h.svc.deps.Store.Read(workspaceID)
		if err != nil || record == nil || record.State != StateReported || record.OwnerUserID != userID || userID != "u1" {
			observedErr = fmt.Errorf("observer preceded durable report: record=%+v err=%v user=%q", record, err, userID)
		}
	}
	chosen, err := h.svc.Choose(t.Context(), "u1", personalassistant.FolderChoice{Picker: true})
	if err != nil || chosen == nil || calls != 0 {
		t.Fatalf("choosing must not complete a mission: chosen=%+v err=%v calls=%d", chosen, err, calls)
	}
	view, err := h.svc.Start(t.Context(), "u1", chosen.Token, "Look once", Hire{})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if observedErr != nil || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, observedErr)
	}

	// A later failed look cannot manufacture another successful report.
	h.ws.mu.Lock()
	h.ws.anchors[view.WorkspaceID] = filepath.Join(t.TempDir(), "missing")
	h.ws.mu.Unlock()
	if _, err := h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error { r.Map = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.LookFurther(t.Context(), "u1", view.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if calls != 1 {
		t.Fatalf("failed look called observer: %d", calls)
	}
}
