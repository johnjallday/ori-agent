package agenthttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Saving a draft is a reviewed, two-step action:
//
//  1. Review: the server reads the chosen message from the conversation,
//     resolves Personal HQ, and returns exactly what would be saved. No write.
//  2. Save: the user's reviewed title and body are committed as one Backlog
//     Ticket through the canonical Ticket service.
//
// Neither step calls a model, and neither is something a model, a transcript,
// or the draft's own text can trigger.

// Draft-save refusal codes.
const (
	PersonalAssistantDraftNotReady          = "assistant_not_ready"
	PersonalAssistantDraftUnavailable       = "draft_save_unavailable"
	PersonalAssistantDraftTargetUnavailable = "target_unavailable"
	PersonalAssistantDraftTargetChanged     = "target_changed"
	PersonalAssistantDraftSourceMissing     = "source_message_not_found"
	PersonalAssistantDraftSourceNotSavable  = "source_not_savable"
	PersonalAssistantDraftInvalid           = "invalid_draft"
	PersonalAssistantDraftOperationConflict = "operation_conflict"
)

// PersonalAssistantDraftSaver is the canonical draft-save service, narrowed.
type PersonalAssistantDraftSaver interface {
	Find(input workspace.AssistantDraftInput) (*workspace.AssistantDraftReceipt, error)
	Save(input workspace.AssistantDraftInput) (*workspace.AssistantDraftReceipt, error)
	Get(workspaceID, ticketID string) (*workspace.AssistantDraftLink, error)
	ListByConversation(workspaceID, conversationID string) ([]workspace.AssistantDraftLink, error)
	Update(input workspace.AssistantDraftUpdateInput) (*workspace.AssistantDraftUpdateReceipt, error)
}

// PersonalAssistantDraftTarget is the server-resolved destination of a save.
type PersonalAssistantDraftTarget struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
}

// PersonalAssistantDraftSource names the exact conversation message a draft
// came from, by canonical session and message ID.
type PersonalAssistantDraftSource struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

// PersonalAssistantDraftLimits are the canonical Ticket field limits.
type PersonalAssistantDraftLimits struct {
	TitleChars int `json:"title_chars"`
	BodyChars  int `json:"body_chars"`
}

// PersonalAssistantDraftReview is what the user reviews before a save. The
// operation ID is the save's one retry identity: the browser sends it back
// unchanged on every attempt and never mints another for the same review.
type PersonalAssistantDraftReview struct {
	OperationID string                       `json:"operation_id"`
	Title       string                       `json:"title"`
	Body        string                       `json:"body"`
	Target      PersonalAssistantDraftTarget `json:"target"`
	Source      PersonalAssistantDraftSource `json:"source"`
	Placement   string                       `json:"placement"`
	Notes       []string                     `json:"notes,omitempty"`
	Limits      PersonalAssistantDraftLimits `json:"limits"`
}

// PersonalAssistantDraftReceipt is the verified result of a save, read from the
// canonical Ticket.
type PersonalAssistantDraftReceipt struct {
	TicketID      string `json:"ticket_id"`
	Number        int64  `json:"number"`
	DisplayNumber string `json:"display_number"`
	Title         string `json:"title"`
	State         string `json:"state"`
	StateLabel    string `json:"state_label"`
	Version       int64  `json:"version"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	Href          string `json:"href"`
	// Created is false when this save had already been made; the same Ticket
	// is returned and nothing was written again.
	Created bool `json:"created"`
	// ChangedSince is true when that earlier Ticket has been edited or moved
	// since it was saved.
	ChangedSince bool `json:"changed_since"`
	Assigned     bool `json:"assigned"`
	Scheduled    bool `json:"scheduled"`
}

const personalAssistantDraftPlacement = "Backlog · unassigned · not scheduled"

// SetDraftSaver wires the canonical draft-save service.
func (h *HomeAssistantAskHandler) SetDraftSaver(saver PersonalAssistantDraftSaver) {
	h.Drafts = saver
}

// draftRefusal is a refused review or save: an HTTP status, a stable code, and
// a sentence for the user. Field is set for a validation refusal.
type draftRefusal struct {
	status  int
	code    string
	message string
	field   string
	// saved is the Ticket an earlier save under the same operation created,
	// reported with an operation conflict.
	saved *PersonalAssistantDraftReceipt
}

func (r *draftRefusal) write(w http.ResponseWriter) {
	body := map[string]any{"error": r.code, "message": r.message}
	if r.field != "" {
		body["field"] = r.field
	}
	if r.saved != nil {
		body["saved"] = r.saved
	}
	_ = orihttp.RespondJSON(w, r.status, body)
}

// draftScope resolves the relationship and its conversation scope for a draft
// action. Draft saves need both an HQ to save into and a conversation to save
// from, so a relationship that is not ready is refused outright.
func (h *HomeAssistantAskHandler) draftScope(ctx context.Context) (personalAssistantConversationScope, *draftRefusal) {
	if h.Drafts == nil || h.Conversations == nil {
		return personalAssistantConversationScope{}, &draftRefusal{http.StatusServiceUnavailable, PersonalAssistantDraftUnavailable, "Saving drafts is unavailable in this build. Nothing was saved.", "", nil}
	}
	workContext, err := h.resolvePersonalAssistantContext(ctx)
	if err != nil {
		return personalAssistantConversationScope{}, &draftRefusal{http.StatusServiceUnavailable, PersonalAssistantDraftUnavailable, "Your personal assistant state is unavailable right now. Nothing was saved.", "", nil}
	}
	scope, ok := conversationScope(workContext)
	if !ok {
		return personalAssistantConversationScope{}, &draftRefusal{http.StatusConflict, PersonalAssistantDraftNotReady, "Finish personal assistant setup before saving a draft. Nothing was saved.", "", nil}
	}
	return scope, nil
}

// draftTarget resolves the designated Personal HQ by its stable ID. It never
// falls back to a workspace with a similar name.
func (h *HomeAssistantAskHandler) draftTarget(scope personalAssistantConversationScope) (PersonalAssistantDraftTarget, *draftRefusal) {
	unavailable := &draftRefusal{http.StatusConflict, PersonalAssistantDraftTargetUnavailable, "Your Personal HQ could not be found, so there is nowhere to save this. Nothing was saved.", "", nil}
	if h.Sources.Workspaces == nil {
		return PersonalAssistantDraftTarget{}, unavailable
	}
	ws, err := h.Sources.Workspaces.Get(scope.workspaceID)
	if err != nil || !isHomeAssistantRoutableWorkspace(ws) || ws.ID != scope.workspaceID {
		return PersonalAssistantDraftTarget{}, unavailable
	}
	return PersonalAssistantDraftTarget{WorkspaceID: ws.ID, Name: strings.TrimSpace(ws.Name)}, nil
}

// draftSourceMessage returns the exact stored message a draft is saved from.
// The conversation must be in scope and the message must be one of its
// assistant replies.
func (h *HomeAssistantAskHandler) draftSourceMessage(ctx context.Context, scope personalAssistantConversationScope, source PersonalAssistantDraftSource) (PersonalAssistantConversationMessage, *draftRefusal) {
	none := PersonalAssistantConversationMessage{}
	conversationID, messageID := strings.TrimSpace(source.ConversationID), strings.TrimSpace(source.MessageID)
	if conversationID == "" || messageID == "" {
		return none, &draftRefusal{http.StatusBadRequest, PersonalAssistantDraftSourceMissing, "Choose the message to save. Nothing was saved.", "", nil}
	}
	record, err := h.Conversations.Get(ctx, conversationID)
	if errors.Is(err, ErrPersonalAssistantConversationNotFound) {
		return none, &draftRefusal{http.StatusNotFound, PersonalAssistantConversationNotFound, "That conversation no longer exists, so its draft cannot be saved from here. Nothing was saved.", "", nil}
	}
	if err != nil {
		return none, &draftRefusal{http.StatusServiceUnavailable, PersonalAssistantConversationUnavailable, "The conversation could not be read right now. Nothing was saved.", "", nil}
	}
	if !scope.owns(record) {
		return none, &draftRefusal{http.StatusConflict, PersonalAssistantConversationOutOfScope, "That conversation does not belong to your assistant's Personal HQ. Nothing was saved.", "", nil}
	}
	messages, err := h.Conversations.Messages(ctx, record.ID)
	if err != nil {
		return none, &draftRefusal{http.StatusServiceUnavailable, PersonalAssistantConversationUnavailable, "The conversation could not be read right now. Nothing was saved.", "", nil}
	}
	for _, message := range messages {
		if message.ID != messageID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), llm.RoleAssistant) || strings.TrimSpace(message.Content) == "" {
			return none, &draftRefusal{http.StatusUnprocessableEntity, PersonalAssistantDraftSourceNotSavable, "Only one of your assistant's replies can be saved as a draft. Nothing was saved.", "", nil}
		}
		return message, nil
	}
	return none, &draftRefusal{http.StatusNotFound, PersonalAssistantDraftSourceMissing, "That message is no longer in the conversation. Nothing was saved.", "", nil}
}

// buildDraftReview prepares the review of one stored message. It writes
// nothing. The body is the stored message text after the canonical
// normalization, which is only the removal of surrounding blank space; any such
// change, and any limit the text exceeds, is stated in the notes.
func buildDraftReview(target PersonalAssistantDraftTarget, conversationID string, message PersonalAssistantConversationMessage) *PersonalAssistantDraftReview {
	body := strings.TrimSpace(message.Content)
	review := &PersonalAssistantDraftReview{
		OperationID: uuid.NewString(),
		Title:       workspace.SuggestAssistantDraftTitle(body),
		Body:        body,
		Target:      target,
		Source:      PersonalAssistantDraftSource{ConversationID: conversationID, MessageID: message.ID},
		Placement:   personalAssistantDraftPlacement,
		Limits: PersonalAssistantDraftLimits{
			TitleChars: workspace.TicketTitleMaxLength, BodyChars: workspace.TicketDescriptionMaxLength,
		},
	}
	if body != message.Content {
		review.Notes = append(review.Notes, "Blank space at the start and end of the message was removed. Nothing else was changed.")
	}
	if utf8.RuneCountInString(body) > workspace.TicketDescriptionMaxLength {
		review.Notes = append(review.Notes, "This draft is longer than a backlog item can hold. Shorten it before saving; it will not be cut for you.")
	}
	return review
}

// reviewDraft validates a review request and builds the review.
func (h *HomeAssistantAskHandler) reviewDraft(ctx context.Context, source PersonalAssistantDraftSource) (*PersonalAssistantDraftReview, *draftRefusal) {
	scope, refusal := h.draftScope(ctx)
	if refusal != nil {
		return nil, refusal
	}
	target, refusal := h.draftTarget(scope)
	if refusal != nil {
		return nil, refusal
	}
	message, refusal := h.draftSourceMessage(ctx, scope, source)
	if refusal != nil {
		return nil, refusal
	}
	return buildDraftReview(target, strings.TrimSpace(source.ConversationID), message), nil
}

// DraftReviewHandler prepares a draft for review. It never writes.
// POST /api/home-assistant/drafts/review
func (h *HomeAssistantAskHandler) DraftReviewHandler(w http.ResponseWriter, r *http.Request) {
	var req PersonalAssistantDraftSource
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, refusal := h.reviewDraft(r.Context(), req)
	if refusal != nil {
		refusal.write(w)
		return
	}
	orihttp.WriteJSON(w, map[string]any{"review": review})
}

type personalAssistantDraftSaveRequest struct {
	OperationID       string                       `json:"operation_id"`
	Title             string                       `json:"title"`
	Body              string                       `json:"body"`
	TargetWorkspaceID string                       `json:"target_workspace_id"`
	Source            PersonalAssistantDraftSource `json:"source"`
}

func draftReceipt(receipt *workspace.AssistantDraftReceipt, target PersonalAssistantDraftTarget) *PersonalAssistantDraftReceipt {
	ticket := receipt.Ticket
	name := strings.TrimSpace(ticket.OwningWorkspaceName)
	if name == "" {
		name = target.Name
	}
	href := ""
	if slug := strings.TrimSpace(ticket.OwningWorkspaceSlug); slug != "" {
		href = workspaceHref(url.PathEscape(slug)) + "?ticket=" + url.QueryEscape(ticket.ID)
	}
	return &PersonalAssistantDraftReceipt{
		TicketID: ticket.ID, Number: ticket.Number, DisplayNumber: ticket.DisplayNumber,
		Title: ticket.Title, State: string(ticket.State), StateLabel: ticket.StateLabel, Version: ticket.Version,
		WorkspaceID: ticket.OwningWorkspaceID, WorkspaceName: name, Href: href,
		Created: receipt.Created, ChangedSince: receipt.ChangedSince,
		Assigned:  strings.TrimSpace(ticket.Assignee) != "",
		Scheduled: ticket.ScheduleEnabled || ticket.DueDate != nil,
	}
}

func draftSaveError(err error) *draftRefusal {
	if errors.Is(err, workspace.ErrAssistantDraftConflict) {
		return &draftRefusal{http.StatusConflict, PersonalAssistantDraftOperationConflict, "This review was already used to save different content. Nothing was changed; open the review again to save this version.", "", nil}
	}
	if validation, ok := workspace.IsTicketValidationError(err); ok {
		field := validation.Field
		if field == "description" {
			field = "body"
		}
		return &draftRefusal{http.StatusUnprocessableEntity, PersonalAssistantDraftInvalid, validation.Message + ". Nothing was saved.", field, nil}
	}
	logger.Warn("Personal assistant draft save failed", logger.Fields{"error": err})
	return &draftRefusal{http.StatusServiceUnavailable, PersonalAssistantDraftUnavailable, "The backlog could not be written right now. Nothing was saved; your draft is still here.", "", nil}
}

// saveDraft commits a reviewed draft. Order matters:
//
//  1. The relationship and the target are re-resolved, and a target that is no
//     longer the designated HQ is refused.
//  2. An earlier attempt of this same save is looked up first, so a retry after
//     a lost response gets its receipt even if the conversation has since been
//     deleted.
//  3. Only a new save validates the source message and writes.
func (h *HomeAssistantAskHandler) saveDraft(ctx context.Context, req personalAssistantDraftSaveRequest) (*PersonalAssistantDraftReceipt, *draftRefusal) {
	scope, refusal := h.draftScope(ctx)
	if refusal != nil {
		return nil, refusal
	}
	target, refusal := h.draftTarget(scope)
	if refusal != nil {
		return nil, refusal
	}
	if strings.TrimSpace(req.TargetWorkspaceID) != target.WorkspaceID {
		return nil, &draftRefusal{http.StatusConflict, PersonalAssistantDraftTargetChanged, "Your Personal HQ changed since this review opened. Nothing was saved; open the review again.", "", nil}
	}
	userID := strings.TrimSpace(h.UserID)
	if userID == "" {
		userID = "local"
	}
	input := workspace.AssistantDraftInput{
		WorkspaceID:    target.WorkspaceID,
		ConversationID: strings.TrimSpace(req.Source.ConversationID),
		MessageID:      strings.TrimSpace(req.Source.MessageID),
		OperationID:    strings.TrimSpace(req.OperationID),
		Title:          req.Title, Body: req.Body, ActorID: userID,
	}
	prior, err := h.Drafts.Find(input)
	if err != nil {
		refusal := draftSaveError(err)
		// A conflict names the Ticket the earlier save created, so the user can
		// open what is actually stored instead of guessing.
		if prior != nil && errors.Is(err, workspace.ErrAssistantDraftConflict) {
			refusal.saved = draftReceipt(prior, target)
		}
		return nil, refusal
	}
	if prior != nil {
		return draftReceipt(prior, target), nil
	}
	if _, refusal := h.draftSourceMessage(ctx, scope, req.Source); refusal != nil {
		return nil, refusal
	}
	receipt, err := h.Drafts.Save(input)
	if err != nil {
		return h.draftSaveOutcomeAfterError(input, target, err)
	}
	return draftReceipt(receipt, target), nil
}

// draftSaveOutcomeAfterError reports a failed save from what is stored now,
// not from the error alone: a write can fail after the Ticket was persisted.
// "Nothing was saved" is said only when a read confirms it.
func (h *HomeAssistantAskHandler) draftSaveOutcomeAfterError(input workspace.AssistantDraftInput, target PersonalAssistantDraftTarget, err error) (*PersonalAssistantDraftReceipt, *draftRefusal) {
	refusal := draftSaveError(err)
	if refusal.code != PersonalAssistantDraftUnavailable {
		return nil, refusal
	}
	prior, findErr := h.Drafts.Find(input)
	switch {
	case findErr == nil && prior != nil:
		return draftReceipt(prior, target), nil
	case findErr != nil:
		refusal.message = "The backlog could not be written, and Ori could not confirm whether the draft was saved. Try again: the same save is never applied twice."
	}
	return nil, refusal
}

// DraftSaveHandler commits a reviewed draft as one HQ Backlog Ticket.
// POST /api/home-assistant/drafts/save
func (h *HomeAssistantAskHandler) DraftSaveHandler(w http.ResponseWriter, r *http.Request) {
	var req personalAssistantDraftSaveRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	receipt, refusal := h.saveDraft(r.Context(), req)
	if refusal != nil {
		refusal.write(w)
		return
	}
	// A replay returns the Ticket an earlier attempt created; nothing was
	// written this time.
	if receipt.Created {
		h.recordMutation(r.Context(), homeAssistantConversationIntent.Key, HomeActionCreateBacklogItem)
	}
	orihttp.WriteJSON(w, map[string]any{"receipt": receipt})
}

// --- the contextual request: "save this draft and put it in my todo list" ---

var (
	draftSaveLead        = regexp.MustCompile(`^(save|keep|store|put|add)\s+(\S.*)$`)
	draftSaveDestination = regexp.MustCompile(`\b(to-?do|todos?|backlog|list|hq|for later)\b`)
	draftSaveQualifier   = regexp.MustCompile(`\b(earlier|previous|first|second|third|original|older|other|english|korean|japanese|chinese|spanish|french|german)\b`)
	draftReminderMention = regexp.MustCompile(`\b(remind|reminder|reminders|notify me|alert me)\b`)
	draftReminderLead    = regexp.MustCompile(`^(remind me|set (up )?a reminder|set (up )?an alarm)\b`)
)

// What a draft can be called, which words may describe a version of it, and
// the words that end the object of the request ("save this draft *to* …").
var (
	draftSaveNouns      = wordSet("draft version greeting message text note reply answer letter post one")
	draftSaveQualifiers = wordSet("earlier previous first second third original older other latest last new english korean japanese chinese spanish french german")
	draftSaveObjectEnds = wordSet("to in into on onto for as and so then please too now with under at")
)

// draftSaveObjectWords bounds the describing words in "this birthday greeting".
const draftSaveObjectWords = 4

func wordSet(words string) map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}

// isDraftSaveObject reports whether the words after the verb name something
// already in the conversation rather than new content to capture:
//
//	it                        save it for later
//	this / that               save this · save this birthday greeting
//	the / my + draft noun     save the draft · save the Korean version
//
// "the" and "my" may carry only a version qualifier, and "this"/"that" must end
// in a draft noun when they describe anything. So "add the pricing rewrite to
// the backlog in Website Redesign" and "add the milk order to my todo list"
// are literal content, and stay with the existing backlog capture.
func isDraftSaveObject(rest string) bool {
	words := strings.Fields(rest)
	if len(words) == 0 {
		return false
	}
	const punctuation = ".,!?;:"
	determiner := strings.TrimRight(words[0], punctuation)
	ended := determiner != words[0]
	var object []string
	for _, raw := range words[1:] {
		if ended {
			break
		}
		word := strings.TrimRight(raw, punctuation)
		if word == "" || draftSaveObjectEnds[word] {
			break
		}
		object = append(object, word)
		ended = word != raw
	}
	noun := len(object) > 0 && draftSaveNouns[object[len(object)-1]]
	switch determiner {
	case "it":
		return len(object) == 0
	case "this", "that":
		return len(object) == 0 || (noun && len(object) <= draftSaveObjectWords)
	case "the", "my":
		if !noun {
			return false
		}
		for _, word := range object[:len(object)-1] {
			if !draftSaveQualifiers[word] {
				return false
			}
		}
		return true
	}
	return false
}

// assistantDraftSaveRequest describes a typed request to save a draft.
type assistantDraftSaveRequest struct {
	// save: the prompt asks to keep a draft from the conversation.
	save bool
	// reminder: it also asks for a reminder, which Ori cannot deliver.
	reminder bool
	// pickVersion: it names a version other than "this one", which cannot be
	// resolved without guessing.
	pickVersion bool
}

// detectAssistantDraftSaveRequest recognizes "save this draft…" style
// requests. It is deliberately narrow: the object must be a pronoun or a draft
// noun, never literal content, so "add buy milk to my list" is not a draft save.
func detectAssistantDraftSaveRequest(prompt string) assistantDraftSaveRequest {
	text := stripCompositionPolitePrefixes(normalizeRouteToken(prompt))
	lead := draftSaveLead.FindStringSubmatch(text)
	if lead == nil || !isDraftSaveObject(lead[2]) {
		return assistantDraftSaveRequest{}
	}
	// "put" and "add" need somewhere to put it; "put this away" is not a save.
	if verb := lead[1]; (verb == "put" || verb == "add") && !draftSaveDestination.MatchString(text) {
		return assistantDraftSaveRequest{}
	}
	return assistantDraftSaveRequest{
		save:        true,
		reminder:    draftReminderMention.MatchString(text),
		pickVersion: draftSaveQualifier.MatchString(text),
	}
}

// isAssistantDraftSaveRequest reports whether the prompt is a draft save. The
// router uses it: a save is the assistant's own action, never a handoff.
func isAssistantDraftSaveRequest(prompt string) bool {
	return detectAssistantDraftSaveRequest(prompt).save
}

// isReminderOnlyRequest reports a request that opens by asking for a reminder.
func isReminderOnlyRequest(prompt string) bool {
	return draftReminderLead.MatchString(stripCompositionPolitePrefixes(normalizeRouteToken(prompt)))
}

const (
	draftReminderLimitation = "Ori cannot deliver reminders yet, so I won't promise one."
	draftReviewPrompt       = "Review the draft below, then choose Save to backlog. Nothing is saved yet."
)

// handleDraftSaveRequest answers a typed draft-save or reminder request without
// a model. A save request opens the same review the message action opens; it
// never writes. handled is false when the prompt is neither.
func (h *HomeAssistantAskHandler) handleDraftSaveRequest(prompt, intent string, identity *HomeAssistantIdentity, workContext *PersonalAssistantWorkContext, conversation *openConversation) (HomeAssistantAskResponse, bool) {
	if h.Drafts == nil || workContext == nil || !workContext.ReadyForWork() {
		return HomeAssistantAskResponse{}, false
	}
	reply := func(text string) (HomeAssistantAskResponse, bool) {
		return HomeAssistantAskResponse{Response: text, Intent: intent, Identity: identity}, true
	}
	request := detectAssistantDraftSaveRequest(prompt)
	if !request.save {
		if isReminderOnlyRequest(prompt) {
			return reply(draftReminderLimitation + " If it helps, save the draft to your Personal HQ backlog so it is on your list.")
		}
		return HomeAssistantAskResponse{}, false
	}

	// An explicitly named project stays the user's choice of destination. A
	// conversation draft can only go to Personal HQ, so decline instead of
	// redirecting it or capturing the wrong text there.
	if id, name, ok, ambiguous := matchRoutableWorkspaceInPrompt(h.Sources.Workspaces, prompt); ok && (ambiguous || id != workContext.HQWorkspaceID) {
		if ambiguous {
			return reply("That names more than one workspace, so I did not pick one. Nothing was saved. I can save a draft from this conversation to your Personal HQ backlog.")
		}
		return reply("I can save a draft from this conversation to your Personal HQ backlog only, not to " + name + ". Nothing was saved. Save it to Personal HQ, or add an item to " + name + " from its own backlog.")
	}

	if conversation == nil || conversation.id == "" {
		return reply("There is no draft in this conversation yet. Ask me to write something first, then save it.")
	}
	if request.pickVersion {
		return reply("I won't guess which version you mean. Use Save to HQ backlog on the reply you want to keep.")
	}
	var referent *PersonalAssistantConversationMessage
	for i := len(conversation.messages) - 1; i >= 0; i-- {
		message := conversation.messages[i]
		if strings.EqualFold(strings.TrimSpace(message.Role), llm.RoleAssistant) && strings.TrimSpace(message.Content) != "" {
			referent = &message
			break
		}
	}
	if referent == nil {
		return reply("There is no reply in this conversation to save yet. Ask me to write something first.")
	}
	target, refusal := h.draftTarget(conversation.scope)
	if refusal != nil {
		return reply(refusal.message)
	}
	review := buildDraftReview(target, conversation.id, *referent)
	text := draftReviewPrompt
	if request.reminder {
		review.Notes = append(review.Notes, draftReminderLimitation+" This saves the draft without a reminder.")
		text = draftReminderLimitation + " " + draftReviewPrompt
	}
	return HomeAssistantAskResponse{Response: text, Intent: intent, Identity: identity, DraftReview: review}, true
}
