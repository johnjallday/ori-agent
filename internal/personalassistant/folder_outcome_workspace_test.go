package personalassistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// outcomeWorkspaceHost is the host's read of where a workspace stands now, as
// a test sets it.
type outcomeWorkspaceHost struct {
	now   FolderOutcomeWorkspace
	read  bool
	asked []string
}

func (h *outcomeWorkspaceHost) lookup(_ context.Context, workspaceID string) (FolderOutcomeWorkspace, bool) {
	h.asked = append(h.asked, workspaceID)
	return h.now, h.read
}

var readyDesktopBatch = FolderFirstBatch{Outcome: FolderFirstBatchReady, BatchID: "batch-1", Eligible: 3,
	Route: "/workspaces/desktop?panel=file-janitor&batch_id=batch-1"}

// namedOffer reads one offer the way the drawer polls it.
func namedOffer(t *testing.T, f *folderDigestFixture, offerID string) FolderOfferView {
	t.Helper()
	view, err := f.service.CurrentOffer(context.Background(), "local", offerID)
	if err != nil || view.Offer == nil {
		t.Fatalf("read %s = %+v, %v", offerID, view, err)
	}
	return *view.Offer
}

// A receipt whose workspace was deleted leads nowhere: Review batch and the
// workspace link are dropped and the view says why. The stored receipt is kept
// as it was, so a restore brings the links back.
func TestFolderDigest_DeletedWorkspaceReceiptDropsItsLinks(t *testing.T) {
	for _, gone := range []string{FolderWorkspaceTrashed, FolderWorkspaceRemoved} {
		f, offerID := resolvedInboxHire(t, readyDesktopBatch)
		host := &outcomeWorkspaceHost{now: FolderOutcomeWorkspace{Gone: gone}, read: true}
		f.service.deps.OutcomeWorkspace = host.lookup

		offer := namedOffer(t, f, offerID)
		if offer.WorkspaceGone != gone || offer.Outcome.Route != "" || offer.Outcome.FirstLook.Batch.Route != "" {
			t.Fatalf("%s: workspace_gone %q, route %q, batch route %q", gone, offer.WorkspaceGone,
				offer.Outcome.Route, offer.Outcome.FirstLook.Batch.Route)
		}
		if offer.Outcome.FirstLook.Batch.Eligible != 3 || offer.Outcome.WorkspaceID != "ws-desktop" {
			t.Fatalf("%s: the receipt lost more than its links: %+v", gone, offer.Outcome.FirstLook.Batch)
		}
		if len(host.asked) != 1 || host.asked[0] != "ws-desktop" {
			t.Fatalf("%s: the host was asked about %v", gone, host.asked)
		}

		stored, batch := storedFirstBatch(t, f, offerID)
		if stored.Outcome.Route != "/workspaces/desktop" || batch.Route != readyDesktopBatch.Route {
			t.Fatalf("%s: the stored receipt was changed: route %q, batch %q", gone, stored.Outcome.Route, batch.Route)
		}

		host.now, host.asked = FolderOutcomeWorkspace{Slug: "desktop"}, nil
		restored := namedOffer(t, f, offerID)
		if restored.WorkspaceGone != "" || restored.Outcome.FirstLook.Batch.Route != readyDesktopBatch.Route {
			t.Fatalf("%s: a restored workspace's receipt = %q %q", gone, restored.WorkspaceGone, restored.Outcome.FirstLook.Batch.Route)
		}
	}
}

// A renamed workspace keeps its receipt's links working: they open it by the
// name it has now.
func TestFolderDigest_RenamedWorkspaceReceiptFollowsItsName(t *testing.T) {
	f, offerID := resolvedInboxHire(t, readyDesktopBatch)
	f.service.deps.OutcomeWorkspace = (&outcomeWorkspaceHost{now: FolderOutcomeWorkspace{Slug: "desktop-files"}, read: true}).lookup

	offer := namedOffer(t, f, offerID)
	if offer.WorkspaceGone != "" || offer.Outcome.Route != "/workspaces/desktop-files" ||
		offer.Outcome.FirstLook.Batch.Route != "/workspaces/desktop-files?panel=file-janitor&batch_id=batch-1" {
		t.Fatalf("renamed receipt: gone %q, route %q, batch %q", offer.WorkspaceGone, offer.Outcome.Route, offer.Outcome.FirstLook.Batch.Route)
	}
}

// A host that cannot say where the workspace stands, or has no read wired,
// leaves the receipt exactly as it was recorded: not knowing is not gone.
func TestFolderDigest_UnreadableWorkspaceKeepsTheRecordedLinks(t *testing.T) {
	for name, lookup := range map[string]func(context.Context, string) (FolderOutcomeWorkspace, bool){
		"unreadable": (&outcomeWorkspaceHost{now: FolderOutcomeWorkspace{Gone: FolderWorkspaceTrashed}}).lookup,
		"not wired":  nil,
	} {
		f, offerID := resolvedInboxHire(t, readyDesktopBatch)
		f.service.deps.OutcomeWorkspace = lookup
		offer := namedOffer(t, f, offerID)
		if offer.WorkspaceGone != "" || offer.Outcome.Route != "/workspaces/desktop" ||
			offer.Outcome.FirstLook.Batch.Route != readyDesktopBatch.Route {
			t.Fatalf("%s: gone %q, route %q, batch %q", name, offer.WorkspaceGone, offer.Outcome.Route, offer.Outcome.FirstLook.Batch.Route)
		}
	}
}

// Only links to the receipt's own workspace move: a parent Home's library and
// a workspace whose slug merely starts the same way are kept, and the outcome
// it was copied from is not touched.
func TestOutcomeOnWorkspace_MovesOnlyItsOwnWorkspaceLinks(t *testing.T) {
	recorded := FolderOutcome{
		Kind: FolderChoiceProject, WorkspaceID: "ws-desktop", Route: "/workspaces/desktop",
		HomeRoute: "/workspaces/music-home/assistant#projectLibraryPanel",
		Receipt: []FolderReceiptRow{
			{Kind: "workspace", Name: "Desktop", Route: "/workspaces/desktop/"},
			{Kind: "workspace", Name: "Desktop 2", Route: "/workspaces/desktop-2"},
			{Kind: "folder", Name: "Desktop"},
		},
	}
	gone := outcomeOnWorkspace(recorded, "desktop", "")
	if gone.Route != "" || gone.HomeRoute != recorded.HomeRoute || gone.Receipt[0].Route != "" ||
		gone.Receipt[1].Route != "/workspaces/desktop-2" || gone.Receipt[2].Route != "" {
		t.Fatalf("gone = %+v", gone)
	}
	moved := outcomeOnWorkspace(recorded, "desktop", "desk")
	if moved.Route != "/workspaces/desk" || moved.Receipt[0].Route != "/workspaces/desk/" || moved.Receipt[1].Route != "/workspaces/desktop-2" {
		t.Fatalf("moved = %+v", moved)
	}
	if recorded.Route != "/workspaces/desktop" || recorded.Receipt[0].Route != "/workspaces/desktop/" {
		t.Fatalf("the recorded outcome was changed: %+v", recorded)
	}
	for _, route := range []string{"", "/agents/desktop", "https://example.com/workspaces/desktop"} {
		if got := rerouteWorkspace(route, "desktop", "desk"); got != route {
			t.Fatalf("rerouteWorkspace(%q) = %q", route, got)
		}
	}
}

// Today's report opens the batch in the workspace by the name it has now.
func TestFirstBatchReport_OpensTheBatchByTheWorkspacesCurrentName(t *testing.T) {
	f, offerID := resolvedInboxHire(t, readyDesktopBatch)
	offer, _ := storedFirstBatch(t, f, offerID)
	ws := &workspace.Workspace{ID: "ws-desktop", Name: "Desktop", FolderSlug: "desktop-files"}
	needs, _ := firstBatchReportItems(offer, ws, offer.Outcome.FirstLook.At.Add(time.Minute))
	want := "/workspaces/desktop-files?panel=file-janitor&batch_id=batch-1"
	if len(needs) != 1 || needs[0].Route != want || needs[0].ReportRoute != want {
		t.Fatalf("report = %+v", needs)
	}
}

// A folder workspace the user deleted is not listed on Today: it has no page
// to open.
func TestToday_DeletedFolderWorkspaceIsNotListed(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, status := range []workspace.WorkspaceStatus{workspace.StatusActive, workspace.StatusTrashed} {
		store, _ := newTodayWorkspace(t, now)
		folder := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Thesis"})
		folder.ID, folder.FolderSlug, folder.Status = "project-1", "thesis", status
		if err := store.Save(folder); err != nil {
			t.Fatal(err)
		}
		setUp := now.Add(-2 * time.Hour)
		receipts := &stubFolderReceipts{offers: []FolderOffer{{
			ID: "project", ResolvedAt: &setUp, Subject: FolderCandidateRecord{Name: "thesis-draft"},
			Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "project-1"},
		}}}
		service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()}, stubTodayBrief{}, store, stubTodayFollowUps{})
		service.SetFolderReceiptReader(receipts)
		service.now = func() time.Time { return now }
		got, err := service.Get(context.Background(), "local")
		if err != nil {
			t.Fatal(err)
		}
		listed := findKind(got.WorkingOn.Items, "folder_workspace") != nil
		if listed != (status == workspace.StatusActive) {
			t.Fatalf("%s: folder workspace listed = %t", status, listed)
		}
		setUpListed := findKind(got.Done.Items, "folder_setup") != nil
		if setUpListed != (status == workspace.StatusActive) {
			t.Fatalf("%s: folder setup listed under Done = %t", status, setUpListed)
		}
		if status != workspace.StatusTrashed {
			continue
		}
		for _, items := range [][]TodayItem{got.NeedsYou.Items, got.WorkingOn.Items, got.Done.Items} {
			for _, item := range items {
				if strings.HasPrefix(item.Route, "/workspaces/thesis") {
					t.Fatalf("Today links to the deleted workspace: %+v", item)
				}
			}
		}
	}
}
