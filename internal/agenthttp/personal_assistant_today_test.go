package agenthttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type todayReaderFixture struct {
	projection *personalassistant.TodayProjection
	reads      int
}

func (f *todayReaderFixture) Get(context.Context, string) (*personalassistant.TodayProjection, error) {
	f.reads++
	return f.projection, nil
}

func TestAssistantToday_ProjectsSafeOrderedRowsAndDistinctOwners(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "hq", "Personal HQ", nil)
	makeTestWorkspace(t, store, "email", "Email Ops", nil)
	items := []personalassistant.TodayItem{
		{Title: "Same title", Kind: "follow_up", Attribution: "Not an owner", Route: "/workspaces/personal-hq?follow_up=id",
			Ref: dailybrief.SourceRef{WorkspaceID: "hq", EntityID: "id", EntityType: "follow_up"}},
		{Title: "Same title", Kind: "follow_up", Route: "/workspaces/email-ops?follow_up=id",
			Ref: dailybrief.SourceRef{WorkspaceID: "email", EntityID: "id", EntityType: "follow_up"}},
		{Title: "Review /Users/example/private", Kind: "folder_agent_report", Route: "//other.example/", Detail: "private report"},
	}
	projection := &personalassistant.TodayProjection{HQWorkspaceSlug: "personal-hq", UnavailableSources: []string{"follow-ups"},
		NeedsYou: personalassistant.TodaySection{Items: append(items, make([]personalassistant.TodayItem, 10)...),
			Health: personalassistant.TodaySourceHealth{Status: personalassistant.TodaySectionAvailable}}}
	answer := projectAssistantToday(projection, store, "local")
	if answer.Health != "partial" || !answer.Truncated || len(answer.Rows) != assistantTodayLimit {
		t.Fatalf("answer: %+v", answer)
	}
	for i, owner := range []string{"Personal HQ", "Email Ops"} {
		row := answer.Rows[i]
		if row.Title != items[i].Title || row.Href != items[i].Route || row.Owner != owner || row.ref != items[i].Ref {
			t.Fatalf("row %d: %+v", i, row)
		}
	}
	if row := answer.Rows[2]; row.Href != "" || row.Owner != "" || row.Title != "Review [private path]" || strings.Contains(answer.history(), "private report") {
		t.Fatalf("unverified row: %+v", row)
	}
	projection.NeedsYou.Items = nil
	projection.NeedsYou.Health.Status = personalassistant.TodaySectionHealthyEmpty
	for _, source := range []string{"follow-ups", "folder offers", "workspace builds", "hires"} {
		projection.UnavailableSources = []string{source}
		if answer := projectAssistantToday(projection, store, "local"); strings.Contains(answer.summary(), "Nothing needs you") {
			t.Fatalf("partial empty read claimed an all-clear when %s failed", source)
		}
	}
	projection.UnavailableSources = nil
	if answer := projectAssistantToday(projection, store, "local"); !strings.Contains(answer.summary(), "Nothing needs you") {
		t.Fatal("healthy empty list did not say so")
	}
}

func TestAssistantToday_OnlyReadsOnTheScopedTurnAndDropsUnsavedCard(t *testing.T) {
	f := newOperationFixture(t)
	reader := &todayReaderFixture{projection: &personalassistant.TodayProjection{
		HQWorkspaceID: f.work.HQWorkspaceID, HQWorkspaceSlug: "my-hq", StateVersion: f.work.StateVersion,
		NeedsYou: personalassistant.TodaySection{Items: []personalassistant.TodayItem{{Title: "Verify invoice"}},
			Health: personalassistant.TodaySourceHealth{Status: personalassistant.TodaySectionAvailable}},
	}}
	f.handler.Today = reader
	ask := HomeAssistantAskRequest{Prompt: "What needs me today?", Intent: "assistant_conversation",
		Context:      &HomeAssistantRouteContext{Origin: "personal_assistant_panel", PagePath: "/", SubjectWorkspaceID: f.alpha.ID},
		Conversation: &HomeAssistantConversationRef{ID: "canonical"}}
	if out := f.handler.Ask(t.Context(), ask); out.Today != nil || reader.reads != 0 {
		t.Fatal("read Today for a pinned project")
	}
	ask.Context.SubjectWorkspaceID = ""
	ask.Prompt = "Good morning"
	f.handler.Ask(t.Context(), ask)
	if reader.reads != 0 {
		t.Fatal("read Today on an unrelated turn")
	}
	ask.Prompt = "What needs me today?"
	f.conversations.failSave = true
	if out := f.handler.Ask(t.Context(), ask); out.Today != nil || reader.reads != 1 || !strings.Contains(out.Response, "couldn't be saved") {
		t.Fatalf("unsaved answer escaped: %+v (%d reads)", out, reader.reads)
	}
}
