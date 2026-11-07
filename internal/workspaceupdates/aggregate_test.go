package workspaceupdates

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type aggregateFailingWorkspace struct{ WorkspaceReader }

func (r aggregateFailingWorkspace) Get(id string) (*workspace.Workspace, error) {
	if id == "ws" {
		return nil, errors.New("PRIVATE corrupted workspace source")
	}
	return r.WorkspaceReader.Get(id)
}

func TestAggregateFinalAuthorityAndPerWorkspaceFailure(t *testing.T) {
	for _, change := range []string{"transfer", "trash", "version", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			f := newRecoveryFixture(t)
			f.create(false)
			if err := f.folders.Save(&workspace.Workspace{ID: "zz", Name: "Other desk", OwnerUserID: "local", Status: workspace.StatusActive}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.sessions.DB().ExecContext(t.Context(), `INSERT INTO users (id,created_at,updated_at) VALUES ('other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if change == "unreadable" {
				f.service.workspaces = aggregateFailingWorkspace{f.folders}
			}
			f.service.SetReviewReader(reviewReaderFunc(func(_ context.Context, _, id string) (filejanitor.FilingReview, error) {
				if id == "zz" && change != "unreadable" {
					if err := f.folders.Update("ws", func(ws *workspace.Workspace) error {
						switch change {
						case "transfer":
							ws.OwnerUserID = "other"
						case "trash":
							ws.Status = workspace.StatusTrashed
						case "version":
							ws.Name = "Renamed"
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				return pendingReview(), nil
			}))
			page, err := f.service.Aggregate(t.Context(), "local", "", 100)
			if err != nil || page.HasMore {
				t.Fatal(page, err)
			}
			if change == "transfer" || change == "trash" {
				if len(page.Items) != 1 || page.Items[0].WorkspaceID != "zz" {
					t.Fatal("old authority survived later read", page)
				}
			} else {
				if len(page.Items) != 2 || page.Items[0].WorkspaceID != "ws" || page.Items[0].Welcome.State != StateUnavailable || page.Items[0].Welcome.WelcomeDelivery != nil || page.Items[0].Review.ReviewCounts != nil || page.Items[1].Review.Pending != 10 {
					t.Fatal("failed row omitted or became zero", page)
				}
				if change == "version" && page.Items[0].Name != "Renamed" {
					t.Fatal("stale navigation label")
				}
			}
			if f.calls.Load() != 0 {
				t.Fatal("aggregate composed a welcome")
			}
		})
	}
}

func TestAggregateEmptyRacedPageStillHasContinuation(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	if err := f.folders.Save(&workspace.Workspace{ID: "zz", Name: "Next", OwnerUserID: "local", Status: workspace.StatusActive}); err != nil {
		t.Fatal(err)
	}
	f.service.SetReviewReader(reviewReaderFunc(func(_ context.Context, _, id string) (filejanitor.FilingReview, error) {
		if id == "ws" {
			if err := f.folders.Update("ws", func(ws *workspace.Workspace) error { ws.Status = workspace.StatusTrashed; return nil }); err != nil {
				t.Fatal(err)
			}
		}
		return pendingReview(), nil
	}))
	first, err := f.service.Aggregate(t.Context(), "local", "", 1)
	if err != nil || len(first.Items) != 0 || !first.HasMore || first.NextAfter != "ws" {
		t.Fatal("raced empty page became all-clear", first, err)
	}
	last, err := f.service.Aggregate(t.Context(), "local", first.NextAfter, 1)
	if err != nil || len(last.Items) != 1 || last.Items[0].WorkspaceID != "zz" || last.HasMore {
		t.Fatal(last, err)
	}
}
