//go:build unix

package folderagent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func guidedLook(t *testing.T, root string) (*harness, string) {
	t.Helper()
	h := newHarness(t, root)
	h.svc.deps.ActiveHQ = func(context.Context, string) string { return "hq" }
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	chosen, err := h.svc.ChooseForReturn(t.Context(), "u1", personalassistant.FolderChoice{Picker: true}, "", "hq", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.svc.Start(t.Context(), "u1", chosen.Token, "", Hire{})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	return h, v.WorkspaceID
}

func TestDocumentsReportRequiresOriginalGuidedHireAndWalk(t *testing.T) {
	root := build(t, "Work/Thesis/main.tex")
	h, id := guidedLook(t, root)
	check := func(owner, hq, attempt, workspace string, want bool) {
		t.Helper()
		_, _, err := h.svc.documentsReport(t.Context(), owner, hq, attempt, workspace)
		if (err == nil) != want {
			t.Fatalf("admission for %q/%q/%q/%q = %v", owner, hq, attempt, workspace, err)
		}
	}
	check("u1", "hq", "attempt", id, true)
	check("u2", "hq", "attempt", id, false)
	check("u1", "other", "attempt", id, false)
	check("u1", "hq", "other", id, false)
	check("u1", "hq", "attempt", "elsewhere", false)
	update := func(fn func(*Record)) {
		t.Helper()
		if _, err := h.svc.deps.Store.Update(id, func(r *Record) error { fn(r); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []State{StateLooking, StateSettingUp, StateFailed, "unknown"} {
		update(func(r *Record) { r.State = state })
		check("u1", "hq", "attempt", id, false)
	}
	update(func(r *Record) { r.State = StateSetUp; r.Setup = &Setup{Status: personalassistant.FolderSetupDone} })
	check("u1", "hq", "attempt", id, true)
	other := filepath.Join(t.TempDir(), "Documents")
	if err := os.Mkdir(other, 0750); err != nil {
		t.Fatal(err)
	}
	h.ws.anchors[id] = other
	check("u1", "hq", "attempt", id, false)
	h.ws.anchors[id] = root
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	check("u1", "hq", "attempt", id, false)
	update(func(r *Record) { r.MapWitness = "" })
	before, err := h.svc.deps.Store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	check("u1", "hq", "attempt", id, false)
	after, err := h.svc.deps.Store.Read(id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read migrated old receipt", err)
	}
	if _, err := h.svc.LookFurther(t.Context(), "u1", id, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	rec, _, err := h.svc.documentsReport(t.Context(), "u1", "hq", "attempt", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.Map.Folder("Work/Thesis"); ok {
		t.Fatal("fresh walk inherited old evidence")
	}
}
