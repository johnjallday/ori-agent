package folderagent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestGroupStage_PlanIncludesExistingMembersAndCreatesTheRest(t *testing.T) {
	root := build(t, "Drafts/Night Train/outline.md", "Drafts/Salt Year/outline.md", "Drafts/The Orchard/outline.md")
	h := newHarness(t, root)
	// A workspace already anchors one of the drafts.
	h.ws.mu.Lock()
	h.ws.anchors["ws-existing"] = filepath.Join(root, "Drafts", "Salt Year")
	h.ws.mu.Unlock()
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.Stage != foldermap.StageGroup || !report.Plan.Runnable {
		t.Fatalf("stage %q runnable %v", report.Stage, report.Plan.Runnable)
	}
	var lines []string
	for _, l := range report.Plan.Lines {
		lines = append(lines, l.Name+"|"+l.Detail)
	}
	want := []string{
		"Turns Ori Music Demo into a group|Same workspace, same place on the map; Ori Music Demo Manager becomes its Group Manager.",
		"Creates Night Train, linked to its own folder|",
		"ws-existing|already a workspace · included as a member",
		"Creates The Orchard, linked to its own folder|",
		"Nothing in Ori Music Demo is moved, copied or read|",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s", strings.Join(lines, "\n"))
	}
	if report.StageLine != "Set it up: Turn Ori Music Demo into a group with one workspace per manuscript (3)" {
		t.Fatalf("stage line = %q", report.StageLine)
	}
}

func TestGroupStage_SoftwareMembersAndTheMemberLimit(t *testing.T) {
	var paths []string
	for i := range GroupMaxMembers + 3 {
		paths = append(paths, fmt.Sprintf("Repos/r%02d/go.mod", i))
	}
	h := newHarness(t, build(t, paths...))
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	creates, software, more := 0, "", ""
	for _, l := range report.Plan.Lines {
		switch l.Kind {
		case "member":
			creates++
		case FolderPlanSoftware:
			software = l.Name
		case FolderPlanMore:
			more = l.Name
		}
	}
	if creates != GroupMaxMembers || software != "24 software projects are set up when opened" || more != "3 more, add them later" {
		t.Fatalf("creates=%d software=%q more=%q", creates, software, more)
	}
}
