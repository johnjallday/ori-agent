package agenthttp

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/followup"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// TodayFollowUpReader is the canonical owner service. A Today row can point to
// it, not grant authority to change it.
type TodayFollowUpReader interface {
	Get(context.Context, string, string) (*followup.FollowUp, error)
}

type TodaySnoozeWriter interface {
	SnoozeReviewed(context.Context, followup.ReviewedSnooze) (*followup.FollowUp, error)
}

func (h *HomeAssistantAskHandler) SetTodaySnoozeWriter(writer TodaySnoozeWriter, enter func() (func(), error)) {
	h.snoozeWriter, h.enterSnoozeWrite = writer, enter
}

func (h *HomeAssistantAskHandler) SetTodayFollowUps(reader TodayFollowUpReader) {
	if reader != nil {
		h.snoozeRows, h.todayReviews = reader, newTodayReviewCache()
	}
}

type assistantTodaySelection struct {
	View      string `json:"view"`
	Selection string `json:"selection,omitempty"`
}

type assistantSnoozeView struct {
	Status     string              `json:"status"`
	Message    string              `json:"message,omitempty"`
	Choices    []assistantTodayRow `json:"choices,omitempty"`
	View       string              `json:"view,omitempty"`
	Selection  string              `json:"selection,omitempty"`
	When       string              `json:"when,omitempty"`
	Review     string              `json:"review,omitempty"`
	Title      string              `json:"title,omitempty"`
	Owner      string              `json:"owner,omitempty"`
	Href       string              `json:"href,omitempty"`
	Until      *time.Time          `json:"until,omitempty"`
	Label      string              `json:"until_label,omitempty"`
	Suggested  bool                `json:"suggested,omitempty"`
	CanConfirm bool                `json:"can_confirm,omitempty"`
}

func snoozeNotice(status, message string) *assistantSnoozeView {
	return &assistantSnoozeView{Status: status, Message: message}
}

func snoozeStale() *assistantSnoozeView {
	return snoozeNotice("stale", "That Needs you selection or review is no longer current. Read Needs you again before reviewing a snooze.")
}

func isAssistantSnoozePrompt(prompt string) bool {
	text := stripCompositionPolitePrefixes(normalizeRouteToken(prompt))
	return text == "snooze" || strings.HasPrefix(text, "snooze ")
}

var snoozePromptParts = regexp.MustCompile(`(?i)^(?:please\s+|can you\s+)?snooze(?:\s+(.+?))?\s+until\s+(.+)$`)

func snoozeKey(work *PersonalAssistantWorkContext, scope *assistantWorkspaceTurn, refs *HomeAssistantRouteContext, conversation *openConversation) (snoozeOwner, bool) {
	if conversation == nil || conversation.id == "" || work == nil || work.State != "active" || work.AssistantID == "" || !assistantTodayScope(scope, refs) {
		return snoozeOwner{}, false
	}
	page := refs.PagePath
	if page == "" {
		page = "/"
	}
	return snoozeOwner{user: scope.userID, assistant: work.AssistantID, hq: scope.hq, conversation: conversation.id, page: page, version: work.StateVersion}, true
}

func snoozableTodayRow(row assistantTodayRow, hq string) bool {
	return row.sourceKind == "follow_up" && row.ref.WorkspaceID == hq && row.ref.EntityType == "follow_up" &&
		row.ref.EntityID != "" && !row.ref.Timestamp.IsZero() && row.Href != "" && followUpCanReview(row.state)
}

func followUpCanReview(state string) bool {
	return state == string(followup.StatusActive) || state == string(followup.StatusReopened)
}

func (h *HomeAssistantAskHandler) issueTodaySelections(answer *assistantTodayAnswer, work *PersonalAssistantWorkContext, scope *assistantWorkspaceTurn, refs *HomeAssistantRouteContext, conversation *openConversation) {
	owner, ok := snoozeKey(work, scope, refs, conversation)
	if !ok || h.todayReviews == nil || h.snoozeRows == nil {
		return
	}
	for i := range answer.Rows {
		if snoozableTodayRow(answer.Rows[i], owner.hq) {
			answer.Rows[i].Selection = uuid.NewString()
		}
	}
	answer.View = h.todayReviews.issue(owner, answer.Rows, answer.Truncated || answer.Health == "partial" || answer.Health == "unavailable")
}

func (h *HomeAssistantAskHandler) answerAssistantSnooze(ctx context.Context, req HomeAssistantAskRequest, refs *HomeAssistantRouteContext, work *PersonalAssistantWorkContext, scope *assistantWorkspaceTurn, conversation *openConversation) HomeAssistantAskResponse {
	reply := HomeAssistantAskResponse{Intent: homeAssistantConversationIntent.Key, Identity: homeAssistantIdentity(work), Conversation: unstoredConversation(conversation)}
	owner, ok := snoozeKey(work, scope, refs, conversation)
	if req.ConfirmedAction != nil || req.Arrangement != nil || req.FolderContext != nil || req.LinkContext != nil || req.ResearchApproval != nil {
		reply.Snooze = snoozeNotice("stale", "A snooze must be reviewed on its own. Nothing was changed.")
	} else if !ok || req.TodaySelection == nil {
		reply.Snooze = snoozeStale()
	} else if lead := snoozePromptParts.FindStringSubmatch(strings.TrimSpace(req.Prompt)); lead != nil {
		reply.Snooze = h.prepareAssistantSnooze(ctx, owner, *req.TodaySelection, lead[1], lead[2], work.SnoozeTimezone)
	} else {
		reply.Snooze = snoozeNotice("clarify", "Ask to snooze a follow-up until a future day, for example: Snooze until Monday.")
	}
	reply.Response = reply.Snooze.Message
	return reply
}

func (h *HomeAssistantAskHandler) prepareAssistantSnooze(ctx context.Context, owner snoozeOwner, selection assistantTodaySelection, target, when, zone string) *assistantSnoozeView {
	if h.todayReviews == nil || h.snoozeRows == nil {
		return snoozeStale()
	}
	view, ok := h.todayReviews.beginReview(owner, selection.View)
	if !ok {
		return snoozeStale()
	}
	matches := matchingSnoozeRows(view.choices, selection.Selection, target)
	if len(matches) == 0 {
		return snoozeNotice("clarify", "I couldn't find that item in the current Needs you list. Select a current follow-up or open its workspace.")
	}
	if len(matches) != 1 || (selection.Selection == "" && view.incomplete) {
		return &assistantSnoozeView{Status: "choose", Message: "Which follow-up do you mean? Only a current follow-up owned by Personal HQ can be snoozed here.", Choices: matches, When: when, View: view.id}
	}
	row := matches[0]
	if row.Selection == "" || !snoozableTodayRow(row, owner.hq) {
		return &assistantSnoozeView{Status: "handoff", Message: "That item can't be snoozed in the Assistant. Open it in its owning workspace or in Needs you.", Choices: matches}
	}
	item, err := h.snoozeRows.Get(ctx, owner.user, row.ref.EntityID)
	if err != nil || item == nil || item.UserID != owner.user || item.WorkspaceID != owner.hq || item.ID != row.ref.EntityID ||
		!item.UpdatedAt.Equal(row.ref.Timestamp) || !followUpCanReview(string(item.Status)) {
		return snoozeStale()
	}
	until, suggested, err := resolveSnoozeTime(when, zone, h.todayReviews.now())
	if err != nil {
		return snoozeNotice("clarify", err.Error())
	}
	review, ok := h.todayReviews.finishReview(owner, view, snoozeReviewRecord{
		followUpID: item.ID, revision: item.UpdatedAt, until: until, suggested: suggested,
		title: row.Title, owner: row.Owner, href: row.Href, selection: row.Selection,
		label: until.Format("Monday, 2 January 2006 at 15:04 MST (UTC-07:00)") + " · " + zone,
	})
	if !ok {
		return snoozeStale()
	}
	return &assistantSnoozeView{Status: "review", Message: "Review this snooze. Nothing has changed yet.",
		Title: review.title, Owner: review.owner, Href: review.href, Until: &review.until, Label: review.label, Suggested: review.suggested,
		Review: review.id, View: view.id, Selection: review.selection, CanConfirm: h.snoozeWriter != nil}
}

func matchingSnoozeRows(rows []assistantTodayRow, selection, target string) []assistantTodayRow {
	target = strings.Trim(strings.TrimSpace(target), `"“”`)
	var matches []assistantTodayRow
	for _, row := range rows {
		if selection != "" {
			if row.Selection == selection && (target == "" || strings.EqualFold(target, row.Title)) {
				return []assistantTodayRow{row}
			}
			continue
		}
		if (target == "" && row.ref.EntityType == "follow_up") || (target != "" && strings.EqualFold(target, row.Title)) {
			matches = append(matches, row)
		}
	}
	return matches
}

type assistantTodayActionRequest struct {
	Action       string                        `json:"action"`
	Context      *HomeAssistantRouteContext    `json:"context"`
	Conversation *HomeAssistantConversationRef `json:"conversation"`
	View         string                        `json:"view,omitempty"`
	Selection    string                        `json:"selection,omitempty"`
	When         string                        `json:"when,omitempty"`
	Review       string                        `json:"review,omitempty"`
}

// TodayActionHandler handles controls belonging to a live Today reply. These
// are not new transcript turns and never enter the model or confirmed_action.
func (h *HomeAssistantAskHandler) TodayActionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		orihttp.BadRequest(w, "Expected a JSON request")
		return
	}
	var req assistantTodayActionRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	orihttp.WriteJSON(w, h.todayAction(r.Context(), req))
}

func (h *HomeAssistantAskHandler) todayAction(ctx context.Context, req assistantTodayActionRequest) *assistantSnoozeView {
	if h.todayReviews == nil || req.Context == nil || req.Context.Origin != "personal_assistant_panel" || req.Conversation == nil || req.Conversation.ID == "" {
		return snoozeStale()
	}
	work, err := h.resolvePersonalAssistantContext(ctx)
	if err != nil || work == nil || work.State != "active" {
		return snoozeStale()
	}
	conversation, reason := h.openConversation(ctx, req.Conversation, work)
	if reason != "" {
		return snoozeStale()
	}
	scope := h.bindWorkspaceTurn(ctx, "", req.Context, work)
	owner, ok := snoozeKey(work, scope, req.Context, conversation)
	if !ok || h.revalidateWorkspaceTurn(ctx, scope) != nil {
		return snoozeStale()
	}
	switch req.Action {
	case "review":
		if req.Selection == "" {
			return snoozeStale()
		}
		return h.prepareAssistantSnooze(ctx, owner, assistantTodaySelection{View: req.View, Selection: req.Selection}, "", req.When, work.SnoozeTimezone)
	case "confirm":
		return h.confirmAssistantSnooze(ctx, owner, req.Review)
	case "cancel":
		if _, ok := h.todayReviews.consume(owner, req.Review); !ok {
			return snoozeStale()
		}
		return snoozeNotice("cancelled", "Snooze cancelled. Nothing changed.")
	default:
		return snoozeStale()
	}
}

func (h *HomeAssistantAskHandler) confirmAssistantSnooze(ctx context.Context, owner snoozeOwner, reviewID string) *assistantSnoozeView {
	if h.snoozeWriter == nil {
		return snoozeStale()
	}
	if h.enterSnoozeWrite != nil {
		release, err := h.enterSnoozeWrite()
		if err != nil {
			return snoozeStale()
		}
		defer release()
	}
	review, ok := h.todayReviews.consume(owner, reviewID)
	if !ok {
		return snoozeStale()
	}
	item, err := h.snoozeWriter.SnoozeReviewed(ctx, followup.ReviewedSnooze{
		UserID: owner.user, HQWorkspaceID: owner.hq, AssistantID: owner.assistant, ConversationID: owner.conversation,
		FollowUpID: review.followUpID, RelationshipVersion: owner.version, ExpectedUpdatedAt: review.revision, Until: review.until,
	})
	if errors.Is(err, followup.ErrConflict) {
		return snoozeNotice("conflict", "This follow-up or its owner changed after review. This confirmation didn’t change it; read Needs you again to review the latest record.")
	}
	if err != nil || item == nil || item.Status != followup.StatusSnoozed || item.SnoozedUntil == nil || !item.SnoozedUntil.Equal(review.until) {
		return snoozeNotice("unknown", "I couldn’t verify whether the snooze was saved. Open the latest record before reviewing another snooze.")
	}
	return &assistantSnoozeView{Status: "saved", Message: "Snooze saved.", Title: review.title, Owner: review.owner, Href: review.href, Until: &review.until, Label: review.label}
}
