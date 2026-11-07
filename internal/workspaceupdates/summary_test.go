package workspaceupdates

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type reviewReaderFunc func(context.Context, string, string) (filejanitor.FilingReview, error)

func (f reviewReaderFunc) ReadFilingReview(ctx context.Context, owner, id string) (filejanitor.FilingReview, error) {
	return f(ctx, owner, id)
}

func pendingReview() filejanitor.FilingReview {
	return filejanitor.FilingReview{State: StateReady, CapabilityID: workspace.CapabilityFileJanitor, RootID: "root", BatchID: "batch", ReviewCounts: &filejanitor.ReviewCounts{Pending: 10, NeedsReview: 2}}
}

func TestSummary_IndependentGroupedSourcesAndSeenOnlyChangesWelcome(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	reads := 0
	f.service.SetReviewReader(reviewReaderFunc(func(_ context.Context, owner, id string) (filejanitor.FilingReview, error) {
		if owner != "local" || id != "ws" {
			t.Fatal("reader was given another scope", owner, id)
		}
		reads++
		return pendingReview(), nil
	}))
	before, err := f.service.Summary(t.Context(), "local", "ws")
	if err != nil || before.Welcome.Unread != 0 || before.Review.Pending != 10 || f.calls.Load() != 0 {
		t.Fatalf("read started delivery: %+v %v", before, err)
	}
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Summary(t.Context(), "local", "ws")
	if err != nil || got.Welcome.Unread != 1 || got.Review.Pending != 10 || got.Review.BatchID != "batch" {
		t.Fatalf("grouping: %+v %v", got, err)
	}
	message := got.Welcome.Messages[0]
	seen, err := f.service.Acknowledge(t.Context(), "local", "ws", session.WelcomeAcknowledgement{SessionID: got.Welcome.SessionID, MessageID: message.MessageID, Revision: message.Revision})
	if err != nil || seen.Welcome.Unread != 0 || seen.Review.Pending != 10 || seen.Review.BatchID != "batch" {
		t.Fatalf("seen changed filing: %+v %v", seen, err)
	}
	if f.calls.Load() != 1 || reads != 3 {
		t.Fatal("unexpected delivery or source calls", f.calls.Load(), reads)
	}
}

func TestSummary_ReviewSurvivesMissingConversationOrLead(t *testing.T) {
	for _, mode := range []string{"deleted conversation", "missing lead", "replacement namesake", "duplicate names"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryFixture(t)
			f.create(false)
			if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
				t.Fatal(err)
			}
			delivery := f.read()
			f.service.SetReviewReader(reviewReaderFunc(func(context.Context, string, string) (filejanitor.FilingReview, error) { return pendingReview(), nil }))
			if mode == "deleted conversation" {
				if err := f.sessions.DeleteSession(t.Context(), delivery.SessionID); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := f.folders.Update("ws", func(ws *workspace.Workspace) error {
					switch mode {
					case "missing lead":
						ws.AgentInstances = nil
					case "replacement namesake":
						ws.AgentInstances[0].ID = "new-instance-same-name"
					case "duplicate names":
						other := ws.AgentInstances[0]
						other.ID = "duplicate-name"
						ws.AgentInstances = append(ws.AgentInstances, other)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.service.Summary(t.Context(), "local", "ws")
			if err != nil || got.Welcome.State == StateReady || got.Review.State != StateReady || got.Review.Pending != 10 {
				t.Fatalf("chat hid filing: %+v %v", got, err)
			}
			old := delivery.Messages[0]
			if _, err := f.service.Acknowledge(t.Context(), "local", "ws", session.WelcomeAcknowledgement{SessionID: delivery.SessionID, MessageID: old.MessageID, Revision: old.Revision}); err == nil {
				t.Fatal("stale identity acknowledged old delivery")
			}
			f.assessed.Store(true)
			if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
				t.Fatal(err)
			}
			if mode != "deleted conversation" {
				stored, err := f.sessions.GetSession(t.Context(), delivery.SessionID)
				if err != nil || len(stored.Messages) != 1 {
					t.Fatalf("recovery adopted or rewrote the old conversation: %+v %v", stored, err)
				}
			}
		})
	}
}

func TestSummary_ReviewFailureDoesNotHideWelcomeOrClaimZero(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	f.service.SetReviewReader(reviewReaderFunc(func(context.Context, string, string) (filejanitor.FilingReview, error) {
		return filejanitor.FilingReview{}, errors.New("private source error")
	}))
	got, err := f.service.Summary(t.Context(), "local", "ws")
	if err != nil || got.Welcome.Unread != 1 || got.Review.State != StateUnavailable || got.Review.ReviewCounts != nil {
		t.Fatalf("source failure isolation: %+v %v", got, err)
	}
}

func TestSummary_RetriesOneVersionRaceWithoutWeakeningTheFence(t *testing.T) {
	for _, churning := range []bool{false, true} {
		t.Run(fmt.Sprint(churning), func(t *testing.T) {
			f := newRecoveryFixture(t)
			f.create(false)
			reads := 0
			f.service.SetReviewReader(reviewReaderFunc(func(context.Context, string, string) (filejanitor.FilingReview, error) {
				reads++
				if reads == 1 || churning {
					if err := f.folders.Update("ws", func(ws *workspace.Workspace) error { ws.Name = "Current name"; return nil }); err != nil {
						t.Fatal(err)
					}
				}
				return pendingReview(), nil
			}))
			got, err := f.service.Summary(t.Context(), "local", "ws")
			if reads != 2 {
				t.Fatal("unbounded or missing retry", reads)
			}
			if churning {
				if !errors.Is(err, ErrUnavailable) || got.WorkspaceID != "" {
					t.Fatalf("version fence relaxed: %+v %v", got, err)
				}
			} else if err != nil || got.Name != "Current name" || got.Review.Pending != 10 {
				t.Fatalf("benign race did not recover: %+v %v", got, err)
			}
		})
	}
}

func TestSummary_RechecksOwnerAfterIndependentSources(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	if _, err := f.sessions.DB().ExecContext(t.Context(), `INSERT INTO users (id, created_at, updated_at) VALUES ('other', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	f.service.SetReviewReader(reviewReaderFunc(func(context.Context, string, string) (filejanitor.FilingReview, error) {
		if err := f.folders.Update("ws", func(ws *workspace.Workspace) error { ws.OwnerUserID = "other"; return nil }); err != nil {
			t.Fatal(err)
		}
		return pendingReview(), nil
	}))
	got, err := f.service.Summary(t.Context(), "local", "ws")
	if !errors.Is(err, ErrNotFound) || got.WorkspaceID != "" {
		t.Fatalf("late ownership transfer: %+v %v", got, err)
	}
}
