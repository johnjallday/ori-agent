package agenthttp

import (
	"context"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const (
	assistantTodayLimit = 8
	openNeedsYou        = "/?panel=today"
)

// AssistantTodayReader is the very same service Home reads. Its broader DTO
// stays inside the host; none of it is registered as a model tool.
type AssistantTodayReader interface {
	Get(context.Context, string) (*personalassistant.TodayProjection, error)
}

type assistantTodayRow struct {
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Owner      string `json:"owner,omitempty"`
	Href       string `json:"href,omitempty"`
	Selection  string `json:"selection,omitempty"`
	ref        dailybrief.SourceRef
	state      string
	sourceKind string
}

type assistantTodayAnswer struct {
	Health    string              `json:"health"`
	Truncated bool                `json:"truncated,omitempty"`
	Rows      []assistantTodayRow `json:"rows"`
	View      string              `json:"view,omitempty"`
}

func isAssistantTodayQuestion(prompt string) bool {
	words := strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
	switch strings.TrimRight(words, "?.!") {
	case "what needs me today", "what needs my attention today", "what needs me", "what needs my attention":
		return true
	}
	return false
}

func assistantTodayScope(scope *assistantWorkspaceTurn, refs *HomeAssistantRouteContext) bool {
	if scope == nil || refs == nil || scope.hq == "" || refs.TaskID != "" || scope.projection.Status != assistantcontext.Available {
		return false
	}
	for _, ref := range []*assistantcontext.WorkspaceRef{scope.projection.Location, scope.projection.Subject} {
		if ref != nil && ref.ID != scope.hq {
			return false
		}
	}
	if refs.PagePath == "" || refs.PagePath == "/" {
		return true
	}
	location := scope.projection.Location
	return location != nil && refs.PagePath == "/workspaces/"+location.Slug
}

func (h *HomeAssistantAskHandler) answerAssistantToday(ctx context.Context, prompt string, refs *HomeAssistantRouteContext, work *PersonalAssistantWorkContext, scope *assistantWorkspaceTurn, conversation *openConversation) HomeAssistantAskResponse {
	reply := HomeAssistantAskResponse{Intent: homeAssistantConversationIntent.Key, Identity: homeAssistantIdentity(work), Conversation: unstoredConversation(conversation)}
	if conversation == nil || !assistantTodayScope(scope, refs) {
		reply.Response = "Ask about Needs you from Home or your Personal HQ, without a project pinned."
		return reply
	}
	if work.State != "active" {
		reply.Response = "Your assistant is paused. You can still open Needs you on Home."
		reply.Actions = []HomeAction{needsYouAction()}
		return reply
	}
	if _, model, err := h.resolveProvider(); err != nil || model == "" {
		reply.Response = "The Default model isn't available for chat. You can still open Needs you on Home."
		reply.ModelUnavailable = true
		reply.Actions = []HomeAction{needsYouAction()}
		return reply
	}
	answer := &assistantTodayAnswer{Health: "unavailable", Rows: []assistantTodayRow{}}
	if h.Today != nil {
		today, err := h.Today.Get(ctx, scope.userID)
		if err == nil && today != nil && today.HQWorkspaceID == scope.hq && today.StateVersion == work.StateVersion {
			answer = projectAssistantToday(today, h.WorkspaceContext.Source, scope.userID)
		}
	}
	reply.Response = answer.summary()
	reply.Conversation = h.storeTurn(ctx, conversation, prompt, answer.history())
	if reply.Conversation == nil || !reply.Conversation.Stored {
		reply.Response = "The Needs you answer couldn't be saved. Reopen this conversation or open Needs you on Home."
		reply.Actions = []HomeAction{needsYouAction()}
		return reply
	}
	h.issueTodaySelections(answer, work, scope, refs, conversation)
	reply.Today = answer
	return reply
}

func needsYouAction() HomeAction {
	return HomeAction{ID: "open-needs-you", Type: HomeActionNavigate, Label: "Open Needs you", Href: openNeedsYou}
}

func projectAssistantToday(today *personalassistant.TodayProjection, source AssistantWorkspaceSource, user string) *assistantTodayAnswer {
	section := today.NeedsYou
	answer := &assistantTodayAnswer{Health: section.Health.Status, Truncated: len(section.Items) > assistantTodayLimit, Rows: []assistantTodayRow{}}
	if today.HQWorkspaceSlug == "" {
		answer.Health = "unavailable"
		return answer
	}
	// The footer also names failures that can affect Needs you, even when a
	// source outside the section's health calculation returned no rows.
	for _, unavailable := range today.UnavailableSources {
		switch unavailable {
		case "follow-ups", "folder setups", "Daily Brief", "specialist setup", "Backup & Recovery":
			if answer.Health != "unavailable" {
				answer.Health = "partial"
			}
		}
	}
	for _, item := range section.Items[:min(len(section.Items), assistantTodayLimit)] {
		row := assistantTodayRow{Title: workspaceContextText(item.Title, 160), Kind: assistantTodayKind(item.Kind), sourceKind: item.Kind}
		if row.Title == "" {
			row.Title = row.Kind
		}
		ref := item.Ref
		if source != nil && ref.WorkspaceID != "" {
			ws, err := source.Get(ref.WorkspaceID)
			if err == nil && workspaceReadable(ws, user) && ws.ID == ref.WorkspaceID && workspaceReferenceToken.MatchString(ws.FolderSlug) {
				row.Owner = workspaceContextText(ws.Name, 100)
				base := "/workspaces/" + ws.FolderSlug
				key := ""
				switch ref.EntityType {
				case "follow_up":
					key = "follow_up"
				case "task":
					key = "ticket"
				}
				if key != "" && workspaceReferenceToken.MatchString(ref.EntityID) && item.Route == base+"?"+key+"="+url.QueryEscape(ref.EntityID) {
					row.Href, row.ref, row.state = item.Route, ref, item.State
				}
			}
		}
		answer.Rows = append(answer.Rows, row)
	}
	return answer
}

func assistantTodayKind(kind string) string {
	switch kind {
	case "follow_up", "follow_up_candidate":
		return "Follow-up"
	case "ticket", "input_request":
		return "Ticket"
	case "backup":
		return "Backup"
	case "first_look_report", "folder_agent_report":
		return "Report"
	case "workspace_build", "hq_setup", "setup_attention":
		return "Setup"
	default:
		return "Today item"
	}
}

func (a *assistantTodayAnswer) summary() string {
	switch a.Health {
	case personalassistant.TodaySectionHealthyEmpty:
		if len(a.Rows) == 0 {
			return "Nothing needs you in Today’s records right now. Open Needs you to see the full Home view."
		}
	case personalassistant.TodaySectionUnavailable, personalassistant.TodaySectionPartial:
		if len(a.Rows) == 0 {
			return "I couldn’t read all of Needs you. I can’t tell whether anything needs your attention."
		}
		return "Some Needs you sources are unavailable. Here’s what I could verify."
	case personalassistant.TodaySectionAvailable:
		if len(a.Rows) > 0 {
			return "Here’s what Needs you shows in Today’s records."
		}
	}
	return "The Needs you list isn’t available in chat right now."
}

func (a *assistantTodayAnswer) history() string {
	var text strings.Builder
	text.WriteString(a.summary())
	for _, row := range a.Rows {
		text.WriteString("\n• " + row.Title + " — " + row.Kind)
		if row.Owner != "" {
			text.WriteString(" · " + row.Owner)
		}
	}
	if a.Truncated {
		text.WriteString("\nMore rows were available on Home.")
	}
	return text.String()
}
