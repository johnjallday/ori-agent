//go:build darwin || linux

package folderagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHiredStationNeedsTheSameLeadAnchorAndCompleteLook(t *testing.T) {
	root := build(t, "cover.png", "sound.mp3", "one.pdf")
	h := downloadsHarness(t, root)
	anchor := StationAnchor{root, "ref", "lead"}
	h.svc.deps.StationAnchor = func(string, string, string) (StationAnchor, bool) { return anchor, true }
	h.svc.deps.StationAvailable = func(owner, id string) bool { return owner == "u1" }
	v := h.start(t, "")
	v, err := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	if err != nil || !v.StationHire || v.FilingPile == nil || v.FilingPile.Count != 3 {
		t.Fatal(v.FilingPile, err)
	}
	look := func() error { _, err := h.svc.StationLook("u1", v.WorkspaceID); return err }
	if err := look(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.OrganizationPreview("u1", v.WorkspaceID); err == nil {
		t.Fatal("station metadata became content authority")
	}
	if _, err := h.svc.StationLook("someone-else", v.WorkspaceID); err == nil {
		t.Fatal("look was visible across owners")
	}
	anchor.LeadID = "replacement"
	if look() == nil {
		t.Fatal("adopted new lead")
	}
	anchor.LeadID = "lead"
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if look() == nil {
		t.Fatal("adopted stale pile")
	}
}
