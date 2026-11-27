package folderagent

import (
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestView_FilingPermissionIsLiveNotPartOfTheSavedPlan(t *testing.T) {
	h := downloadsHarness(t, build(t, "a.pdf", "b.png"))
	v := h.start(t, "")
	shown, err := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	allowed := false
	h.svc.deps.FilingPermission = func(id string) (bool, error) {
		calls++
		if id != v.WorkspaceID {
			t.Fatal("wrong workspace", id)
		}
		return allowed, nil
	}
	if pending, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID); pending.FilingPermissionRequired || calls != 0 {
		t.Fatal("unfinished plan asks for filing permission")
	}
	pins := &fakePins{}
	h.svc.deps.Pins = pins
	if _, err := h.svc.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: shown.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if len(pins.pins) != 0 || len(pins.released) != 0 {
		t.Fatalf("recommendation changed the manager's toolbox: %v / %v", pins.pins, pins.released)
	}
	if recommended, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID); recommended.FilingPermissionRequired {
		t.Fatal("recommendation projected as an install")
	}
	// Old completed installs keep their separate permission repair.
	if _, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.Setup.Lines = []personalassistant.FolderPlanLine{{Kind: FolderPlanCapability, Step: "file-curator"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	saved, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	before := BuildPlan(*saved, nil).Digest
	pending, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	if !pending.FilingPermissionRequired {
		t.Fatal("completed filing install lost its separate permission ask")
	}
	allowed = true
	granted, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	if granted.FilingPermissionRequired || before != BuildPlan(*saved, nil).Digest {
		t.Fatal("grant changed the plan or still asks for permission")
	}
	after, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	if !reflect.DeepEqual(saved, after) || after.Version != RecordVersion {
		t.Fatal("permission view rewrote the record")
	}
	h.svc.deps.FilingPermission = func(string) (bool, error) { return false, errors.New("unavailable") }
	if unknown, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID); unknown.FilingPermissionRequired {
		t.Fatal("unknown state claimed permission was missing")
	}
	if _, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.Setup.Lines = []personalassistant.FolderPlanLine{{Kind: personalassistant.FolderPlanFolder}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.deps.FilingPermission = func(string) (bool, error) { t.Fatal("non-filing plan read filing settings"); return false, nil }
	_, _ = h.svc.View(t.Context(), "u1", v.WorkspaceID)
}
