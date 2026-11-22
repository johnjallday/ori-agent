package agenthttp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
)

type snoozeRowFixture struct{ row *followup.FollowUp }

func (f snoozeRowFixture) Get(_ context.Context, user, id string) (*followup.FollowUp, error) {
	if f.row != nil && f.row.UserID == user && f.row.ID == id {
		return f.row, nil
	}
	return nil, followup.ErrNotFound
}

func TestAssistantTodaySnooze_DuplicateTitlesDoNotChooseTheOnlyEligibleRow(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)
	owner := snoozeOwner{user: "local", assistant: "atlas", hq: "hq", conversation: "current", page: "/", version: 1}
	item := &followup.FollowUp{ID: "hq-item", UserID: "local", WorkspaceID: "hq", UpdatedAt: now, Status: followup.StatusActive}
	h := &HomeAssistantAskHandler{}
	h.SetTodayFollowUps(snoozeRowFixture{item})
	h.todayReviews.now = func() time.Time { return now }
	row := assistantTodayRow{Title: "Reply to Mina", Kind: "Follow-up", Owner: "My HQ", Href: "/workspaces/my-hq?follow_up=hq-item",
		Selection: "current-row", sourceKind: "follow_up", state: "active", ref: dailybrief.SourceRef{WorkspaceID: "hq", EntityType: "follow_up", EntityID: item.ID, Timestamp: now}}
	mail := row
	mail.Owner, mail.Selection, mail.ref.WorkspaceID = "Email Ops", "", "email"
	view := h.todayReviews.issue(owner, []assistantTodayRow{row, mail}, false)
	ctx := t.Context()
	choices := h.prepareAssistantSnooze(ctx, owner, assistantTodaySelection{View: view}, "Reply to Mina", "Monday", "America/New_York")
	if choices.Status != "choose" || len(choices.Choices) != 2 || choices.Choices[1].Selection != "" || choices.Choices[1].Owner != "Email Ops" {
		t.Fatalf("ambiguous owners: %+v", choices)
	}
	if invalid := h.prepareAssistantSnooze(ctx, owner, assistantTodaySelection{View: view, Selection: item.ID}, "", "Monday", "America/New_York"); invalid.Status == "review" {
		t.Fatal("guessed ID provided authority")
	}
	selected := assistantTodaySelection{View: view, Selection: row.Selection}
	for _, state := range []followup.Status{followup.StatusCandidate, followup.StatusSnoozed, followup.StatusCompleted, followup.StatusDismissed} {
		item.Status = state
		if out := h.prepareAssistantSnooze(ctx, owner, selected, "", "Monday", "America/New_York"); out.Status != "stale" {
			t.Fatalf("row's live state %q passed the review gate: %+v", state, out)
		}
	}
	item.Status = followup.StatusReopened
	out := h.prepareAssistantSnooze(ctx, owner, selected, "", "Monday", "America/New_York")
	if out.Status != "review" || !out.Suggested || !out.Until.Equal(time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)) ||
		!strings.Contains(out.Label, "Monday, 2 November 2026 at 09:00 EST (UTC-05:00)") {
		t.Fatalf("exact review: %+v", out)
	}
	foreign := owner
	foreign.conversation = "other"
	if _, ok := h.todayReviews.consume(foreign, out.Review); ok {
		t.Fatal("review escaped to another conversation")
	}
	corrected := h.prepareAssistantSnooze(ctx, owner, selected, "", "Monday at 14:30", "America/New_York")
	if _, ok := h.todayReviews.consume(owner, out.Review); ok || corrected.Review == out.Review || corrected.Suggested {
		t.Fatal("changing time did not retire the original review")
	}
	if _, ok := h.todayReviews.consume(owner, corrected.Review); !ok {
		t.Fatal("current review was not available")
	}
	if _, ok := h.todayReviews.consume(owner, corrected.Review); ok {
		t.Fatal("consumed review could be replayed")
	}
	expiring := h.prepareAssistantSnooze(ctx, owner, selected, "", "Monday", "America/New_York")
	now = now.Add(snoozeTTL)
	if _, ok := h.todayReviews.consume(owner, expiring.Review); ok {
		t.Fatal("expired review could be replayed")
	}
}

func TestAssistantTodaySnooze_LocalTimeRefusesGapsRepeatsAndPastDates(t *testing.T) {
	now := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	for _, input := range []string{"2026-03-08 02:30", "2026-11-01 01:30", "2026-03-02 09:00", "Monday after lunch", "maybe"} {
		if _, _, err := resolveSnoozeTime(input, "America/New_York", now); err == nil {
			t.Errorf("accepted unusable time %q", input)
		}
	}
	if _, _, err := resolveSnoozeTime("Monday", "", now); err == nil {
		t.Fatal("inferred a timezone")
	}
	monday := time.Date(2026, 3, 9, 0, 30, 0, 0, time.UTC) // still Sunday in HQ
	instant, _, err := resolveSnoozeTime("Monday", "America/New_York", monday)
	if err != nil || !instant.Equal(time.Date(2026, 3, 9, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("the saved timezone decides the week boundary: %v %v", instant, err)
	}
}
