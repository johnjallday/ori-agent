package agenthttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const homeMaxToolRounds = 4

var errHomeModelNotConfigured = errors.New("system model is not configured")

// Home action types (PRD 4.5 / 5.1).
const (
	HomeActionNavigate          = "navigate"
	HomeActionOpenWorkspace     = "open_workspace"
	HomeActionOpenTask          = "open_task"
	HomeActionOpenSession       = "open_session"
	HomeActionCreateWorkspace   = "create_workspace"
	HomeActionCreateTask        = "create_task"
	HomeActionCreateBacklogItem = "create_backlog_item"
	HomeActionStartTask         = "start_task"
	HomeActionAssignAgent       = "assign_agent"
	HomeActionCreateAgent       = "create_agent"
	HomeActionRemoveAgent       = "remove_agent"
	HomeActionRemember          = "remember"
	HomeActionAskFollowup       = "ask_followup"
	// HomeActionBuildWorkspace opens "Build with your assistant" in the
	// browser. It is deliberately not a mutation: the server never executes it.
	HomeActionBuildWorkspace = "build_workspace"
)

// homeMutatingActionTypes are the only action types that change state and thus
// require confirmation before execution.
var homeMutatingActionTypes = map[string]bool{
	HomeActionCreateWorkspace:   true,
	HomeActionCreateTask:        true,
	HomeActionCreateBacklogItem: true,
	HomeActionStartTask:         true,
	HomeActionAssignAgent:       true,
	HomeActionCreateAgent:       true,
	HomeActionRemoveAgent:       true,
	HomeActionRemember:          true,
}

// HomeAction is a serializable next-step action descriptor returned to the
// frontend, which validates it before turning it into a button.
type HomeAction struct {
	ID                   string         `json:"id"`
	Type                 string         `json:"type"`
	Label                string         `json:"label"`
	Href                 string         `json:"href,omitempty"`
	WorkspaceID          string         `json:"workspace_id,omitempty"`
	TaskID               string         `json:"task_id,omitempty"`
	SessionID            string         `json:"session_id,omitempty"`
	RequiresConfirmation bool           `json:"requires_confirmation,omitempty"`
	ConfirmationSummary  string         `json:"confirmation_summary,omitempty"`
	Arguments            map[string]any `json:"arguments,omitempty"`
}

// HomeActionConfirmation describes a pending mutation awaiting the user's
// explicit confirmation (PRD 4.6 / FR #25).
type HomeActionConfirmation struct {
	ActionID   string         `json:"action_id"`
	ActionType string         `json:"action_type"`
	Summary    string         `json:"summary"`
	Arguments  map[string]any `json:"arguments,omitempty"`
}

// HomeAssistantAskRequest is the body for POST /api/home-assistant/ask.
type HomeAssistantAskRequest struct {
	Prompt          string                     `json:"prompt"`
	Intent          string                     `json:"intent"`
	Context         *HomeAssistantRouteContext `json:"context,omitempty"`
	DateWindow      string                     `json:"date_window,omitempty"`
	ConfirmedAction *HomeAction                `json:"confirmed_action,omitempty"`
	// Conversation asks for the turn to be part of a hired-assistant
	// conversation. Its ID is opaque; the server derives the owner.
	Conversation  *HomeAssistantConversationRef `json:"conversation,omitempty"`
	FolderContext *HomeAssistantFolderRef       `json:"folder_context,omitempty"`
	// Draft names the saved draft the conversation is working on, so the turn
	// sees its current text. It is read, never written, by a turn.
	Draft *HomeAssistantDraftRef `json:"draft,omitempty"`
}

// HomeAssistantAskResponse is the response for POST /api/home-assistant/ask.
type HomeAssistantIdentity struct {
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	State       string `json:"state"`
}

type HomeAssistantAskResponse struct {
	Response             string                  `json:"response"`
	Intent               string                  `json:"intent"`
	Identity             *HomeAssistantIdentity  `json:"identity,omitempty"`
	SnapshotMeta         *HomeSnapshotMeta       `json:"snapshot_meta,omitempty"`
	Actions              []HomeAction            `json:"actions,omitempty"`
	RequiresConfirmation bool                    `json:"requires_confirmation,omitempty"`
	Confirmation         *HomeActionConfirmation `json:"confirmation,omitempty"`
	// Conversation is set only for a hired-assistant conversation turn.
	Conversation  *HomeAssistantConversationState `json:"conversation,omitempty"`
	FolderContext *PersonalAssistantFolderState   `json:"folder_context,omitempty"`
	// ModelUnavailable marks a turn that got no model answer, so the browser
	// can keep the user's text instead of treating the reply as an answer.
	ModelUnavailable bool `json:"model_unavailable,omitempty"`
	// DraftReview opens the save-to-backlog review for a typed "save this
	// draft" request. Nothing has been written when it is set.
	DraftReview *PersonalAssistantDraftReview `json:"draft_review,omitempty"`
	// DraftContext reports whether the saved draft the request named was read
	// for this turn.
	DraftContext *HomeAssistantDraftContext `json:"draft_context,omitempty"`
	// MemoryReview opens the editable fact review for a typed "remember…"
	// request. Nothing has been written when it is set.
	MemoryReview *PersonalAssistantMemoryReview `json:"memory_review,omitempty"`
}

// HomeActionMutator executes confirmed state-changing actions. The server wires a
// real implementation; a nil mutator degrades mutations gracefully.
type HomeActionMutator interface {
	CreateWorkspace(ctx context.Context, name, description string) (workspaceID, href string, err error)
	CreateTask(ctx context.Context, workspaceID, description string) (taskID, href string, err error)
	// CreateBacklogItem adds an uncommitted Backlog item (PRD workspace-backlog
	// FR23-25) — unlike CreateTask, it must not assign an agent or become
	// runnable until an explicit Promote to Ready action.
	CreateBacklogItem(ctx context.Context, workspaceID, description string) (itemID, href string, err error)
	StartTask(ctx context.Context, workspaceID, taskID string) (href string, err error)
	AssignAgent(ctx context.Context, workspaceID, agentName string) (href string, err error)
	CreateAgent(ctx context.Context, name, description string) (href string, err error)
	RemoveAgent(ctx context.Context, workspaceID, agentName string) (href string, err error)
}

type PersonalAssistantMemoryWriter interface {
	Remember(ctx context.Context, userID string, request personalassistant.RememberRequest) (*personalassistant.RememberResult, error)
}

type homeAskSystemModelReader interface {
	GetSystemModel() (provider, model string)
}

// HomeAskTrace is one home-harness telemetry record (FR #28).
type HomeAskTrace struct {
	Prompt        string
	Intent        string
	Window        string
	Outcome       string // answered | confirmation_required | mutation_executed | model_unavailable
	ActionCount   int
	ConfirmedType string
	Degraded      []string
}

type homeAskTraceEmitter interface {
	RecordAskOutcome(ctx context.Context, trace HomeAskTrace)
}

// HomeAssistantAskHandler owns the home harness: snapshot construction, the
// engineered system prompt, read-only tool registration, model execution, and
// response/action generation. POST /api/home-assistant/ask.
type HomeAssistantAskHandler struct {
	Sources                  HomeSnapshotSources
	LLMFactory               *llm.Factory
	SystemModel              homeAskSystemModelReader
	Mutator                  HomeActionMutator
	Trace                    homeAskTraceEmitter
	PersonalAssistantContext PersonalAssistantContextProvider
	PersonalAssistantMemory  PersonalAssistantMemoryWriter
	// Conversations is the canonical session store behind hired-assistant
	// conversations; nil keeps every turn stateless.
	Conversations      PersonalAssistantConversationStore
	FolderObservations PersonalAssistantFolderObservations
	folderRequests     folderRequestGate
	// Drafts saves a reviewed conversation draft as one HQ Backlog Ticket; nil
	// leaves the save action unavailable.
	Drafts PersonalAssistantDraftSaver
	UserID string
	// WorkspaceBuildAvailable reports whether "Build with your assistant" can
	// take a create-workspace request; nil keeps the direct create.
	WorkspaceBuildAvailable func(ctx context.Context) bool
	now                     func() time.Time
}

// NewHomeAssistantAskHandler builds the handler from its data sources.
func NewHomeAssistantAskHandler(sources HomeSnapshotSources, factory *llm.Factory, systemModel homeAskSystemModelReader) *HomeAssistantAskHandler {
	now := sources.Now
	if now == nil {
		now = time.Now
	}
	return &HomeAssistantAskHandler{Sources: sources, LLMFactory: factory, SystemModel: systemModel, now: now}
}

// SetMutator wires the confirmed-action executor.
func (h *HomeAssistantAskHandler) SetMutator(m HomeActionMutator) { h.Mutator = m }

// SetTraceEmitter wires optional telemetry.
func (h *HomeAssistantAskHandler) SetTraceEmitter(t homeAskTraceEmitter) { h.Trace = t }

// SetPersonalAssistantContextProvider wires the canonical relationship context.
// A nil provider leaves the handler in its dependency-free fallback mode.
func (h *HomeAssistantAskHandler) SetPersonalAssistantContextProvider(provider PersonalAssistantContextProvider, userID string) {
	h.PersonalAssistantContext = provider
	h.UserID = strings.TrimSpace(userID)
}

func (h *HomeAssistantAskHandler) SetPersonalAssistantMemoryWriter(writer PersonalAssistantMemoryWriter) {
	h.PersonalAssistantMemory = writer
}

// SetWorkspaceBuildAvailable wires the "Build with your assistant" check. While
// it reports true, a request to create a workspace is offered as a build with
// the assistant instead of a direct create (FR41).
func (h *HomeAssistantAskHandler) SetWorkspaceBuildAvailable(available func(ctx context.Context) bool) {
	h.WorkspaceBuildAvailable = available
}

// buildInsteadOfCreate turns a detected create_workspace confirmation into a
// build_workspace one when the assistant can build it: accepting it opens the
// Create Workspace dialog in build mode with the user's sentence as the first
// turn, which goes through the blueprint pipeline. build_workspace is not a
// mutation, so the server never creates anything on its confirmation.
func (h *HomeAssistantAskHandler) buildInsteadOfCreate(ctx context.Context, conf *HomeActionConfirmation, prompt string, identity *HomeAssistantIdentity) *HomeActionConfirmation {
	if conf == nil || conf.ActionType != HomeActionCreateWorkspace || h.WorkspaceBuildAvailable == nil || !h.WorkspaceBuildAvailable(ctx) {
		return conf
	}
	assistant := "your assistant"
	if identity != nil && strings.TrimSpace(identity.DisplayName) != "" {
		assistant = strings.TrimSpace(identity.DisplayName)
	}
	arguments := map[string]any{"first_message": prompt}
	if name := actionArgString(conf.Arguments, "name"); name != "" {
		arguments["name"] = name
	}
	return &HomeActionConfirmation{
		ActionID:   "build-workspace",
		ActionType: HomeActionBuildWorkspace,
		Summary:    fmt.Sprintf("Build this workspace with %s? You'll see it set up the form and check it before anything is created.", assistant),
		Arguments:  arguments,
	}
}

func (h *HomeAssistantAskHandler) emitTrace(ctx context.Context, trace HomeAskTrace) {
	if h.Trace != nil {
		h.Trace.RecordAskOutcome(ctx, trace)
	}
}

// AskHandler is the HTTP entrypoint.
func (h *HomeAssistantAskHandler) AskHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req HomeAssistantAskRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	resp := h.Ask(r.Context(), req)
	orihttp.WriteJSON(w, resp)
}

// Ask runs the harness and always returns a renderable response (errors are
// surfaced as helpful text + next-step actions rather than HTTP failures).
func (h *HomeAssistantAskHandler) Ask(ctx context.Context, req HomeAssistantAskRequest) HomeAssistantAskResponse {
	prompt := strings.TrimSpace(req.Prompt)
	intent := strings.TrimSpace(req.Intent)
	if intent == "" {
		intent = homeAssistantAppIntrospectionIntent.Key
	}

	workContext, contextErr := h.resolvePersonalAssistantContext(ctx)
	if contextErr != nil {
		return HomeAssistantAskResponse{
			Response: "Your personal assistant state is unavailable right now. Nothing was changed; reload and try again.",
			Intent:   intent, Actions: []HomeAction{{ID: "nav-home", Type: HomeActionNavigate, Label: "Return Home", Href: "/"}},
		}
	}
	if workContext != nil && workContext.NeedsHireOrRepair() {
		guidance := personalAssistantSetupGuidance(workContext)
		return HomeAssistantAskResponse{
			Response: guidance.Response, Intent: intent,
			Actions: []HomeAction{{
				ID: guidance.ActionID, Type: guidance.ActionType,
				Label: guidance.Label, Href: guidance.Href,
			}},
		}
	}
	identity := homeAssistantIdentity(workContext)

	// A hired-assistant conversation is validated against the relationship
	// resolved above before anything else runs. A conversation that may not be
	// used stops the request: no model call, no write, no confirmed action.
	conversation, refusal := h.openConversation(ctx, req.Conversation, workContext)
	if refusal != "" {
		return conversationRefusal(refusal, intent, identity)
	}

	var folderTurn *preparedFolderTurn
	if req.FolderContext != nil {
		// Supplied but invalid context never degrades to a generic action or a
		// context-free model answer. A reference is not mutation confirmation.
		if conversation == nil || req.ConfirmedAction != nil || (req.Context != nil && req.Context.Origin != "personal_assistant_panel") {
			return folderTurnRefusal(foldercontext.ErrInvalid, identity, conversation)
		}
		target, err := h.folderTarget(conversation.scope, conversation.id, req.FolderContext.DraftID)
		if err != nil {
			return folderTurnRefusal(err, identity, conversation)
		}
		release, ok := h.folderRequests.enter(target)
		if !ok {
			return folderTurnRefusal(personalassistant.ErrFolderScanBusy, identity, conversation)
		}
		defer release()
		folderTurn, err = h.prepareFolderTurn(ctx, conversation, req.FolderContext)
		if err != nil {
			return folderTurnRefusal(err, identity, conversation)
		}
		intent = homeAssistantConversationIntent.Key
	}

	// Confirmed mutation path: execute only known action types (FR #24). The
	// relationship is freshly resolved above so stale/replaced HQ state cannot
	// execute a previously prepared action.
	// A conversation stores answered turns only. Action requests, confirmation
	// prompts, and their outcomes below are not stored in it, so every
	// assistant message in a conversation is a reply the user can point at, and
	// no stored text ever stands in for an approval.
	if req.ConfirmedAction != nil {
		resp := h.executeConfirmedAction(ctx, intent, *req.ConfirmedAction)
		resp.Identity = identity
		return resp
	}

	if prompt == "" {
		question := "What would you like to know about your workspaces, tasks, or activity?"
		if identity != nil {
			question = "What would you like help with today?"
		}
		return HomeAssistantAskResponse{Response: question, Intent: intent, Identity: identity}
	}

	// "Remember that…" never writes on its own. In a conversation it opens the
	// editable fact review; an allowlisted global preference keeps its existing
	// confirmation.
	if resp, handled := h.handleMemoryRequest(prompt, intent, identity, workContext, conversation != nil); handled {
		h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Outcome: "confirmation_required", ConfirmedType: HomeActionRemember})
		return resp
	}

	// "Save this draft…" opens a review of a reply already in the conversation.
	// It runs before backlog capture so a draft is never captured as the
	// literal words of the request, and before any model call.
	if resp, handled := h.handleDraftSaveRequest(prompt, intent, identity, workContext, conversation); handled {
		return resp
	}

	if folderTurn != nil {
		return h.answerFolderTurn(ctx, prompt, req.Draft, identity, workContext, conversation, folderTurn)
	}

	// Backlog capture (PRD workspace-backlog FR23-25) is checked as its own
	// step, separately from detectHomeMutationRequest, because a recognized-
	// but-unresolved workspace target must produce a user-readable decline
	// message rather than silently falling through to a normal chat answer.
	if conf, decline := h.detectBacklogCaptureRequest(prompt); conf != nil {
		h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Outcome: "confirmation_required", ConfirmedType: conf.ActionType})
		return HomeAssistantAskResponse{
			Response:             conf.Summary,
			Intent:               intent,
			Identity:             identity,
			RequiresConfirmation: true,
			Confirmation:         conf,
		}
	} else if decline != "" {
		return HomeAssistantAskResponse{Response: decline, Intent: intent, Identity: identity}
	}

	// Explicit, supported mutation request: ask for confirmation before doing
	// anything (FR #24). Execution happens only on a follow-up with ConfirmedAction.
	if conf := h.detectHomeMutationRequest(prompt); conf != nil {
		conf = h.buildInsteadOfCreate(ctx, conf, prompt, identity)
		h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Outcome: "confirmation_required", ConfirmedType: conf.ActionType})
		return HomeAssistantAskResponse{
			Response:             conf.Summary,
			Intent:               intent,
			Identity:             identity,
			RequiresConfirmation: true,
			Confirmation:         conf,
		}
	}

	promptSources := personalAssistantPromptSources(h.Sources, workContext)
	var history []llm.Message
	if conversation != nil {
		history = conversation.history
	}

	// An everyday request to the hired assistant: answer the request itself.
	// No Home Snapshot is injected and no app counts are reported; the
	// read-only home tools stay available in case the user asks about the app.
	if intent == homeAssistantConversationIntent.Key && workContext != nil && workContext.ReadyForWork() {
		// The saved draft the conversation is working on is read fresh for this
		// turn, so the assistant revises what is stored now, not a stale reply.
		savedDraft, draftContext := h.savedDraftPromptContext(req.Draft, workContext)
		answer, err := h.runModel(ctx, modelTurn{
			system:      buildAssistantConversationSystemPrompt(workContext),
			history:     history,
			user:        buildAssistantConversationUserPrompt(prompt, workContext) + savedDraft,
			sources:     promptSources,
			temperature: 0.6,
		})
		if err != nil {
			resp := h.conversationModelUnavailable(ctx, prompt, intent, workContext, conversation, err)
			resp.DraftContext = draftContext
			return resp
		}
		answer = strings.TrimSpace(answer)
		h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Outcome: "answered"})
		return HomeAssistantAskResponse{
			Response: answer, Intent: intent, Identity: identity,
			Conversation: h.storeTurn(ctx, conversation, prompt, answer),
			DraftContext: draftContext,
		}
	}

	window := NormalizeHomeDateWindow(req.DateWindow, DefaultHomeDateWindowForPrompt(prompt))
	snapshot := BuildHomeSnapshot(ctx, promptSources, window)
	snapshot = sanitizePersonalAssistantSnapshot(snapshot, workContext)

	answer, err := h.generateAnswer(ctx, prompt, intent, snapshot, promptSources, workContext, history)
	if err != nil {
		resp := h.modelUnavailableResponse(ctx, prompt, intent, snapshot, workContext, err)
		resp.Conversation = unstoredConversation(conversation)
		return resp
	}
	answer = strings.TrimSpace(answer)

	actions := h.buildNextStepActions(intent, prompt, snapshot)
	meta := snapshot.Meta
	h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Window: string(window), Outcome: "answered", ActionCount: len(actions), Degraded: meta.Degraded})
	return HomeAssistantAskResponse{
		Response:     answer,
		Intent:       intent,
		Identity:     identity,
		SnapshotMeta: &meta,
		Actions:      actions,
		Conversation: h.storeTurn(ctx, conversation, prompt, answer),
	}
}

// unstoredConversation reports a turn that was not stored, keeping the
// conversation the browser was in.
func unstoredConversation(conversation *openConversation) *HomeAssistantConversationState {
	if conversation == nil {
		return nil
	}
	return &HomeAssistantConversationState{ID: conversation.id, Title: conversation.title, HistoryTruncated: conversation.truncated}
}

// conversationModelUnavailable is the honest "no answer" for a conversation
// turn. Nothing is stored and no other provider is tried; the browser keeps
// the user's text so it can be sent again.
func (h *HomeAssistantAskHandler) conversationModelUnavailable(ctx context.Context, prompt, intent string, workContext *PersonalAssistantWorkContext, conversation *openConversation, err error) HomeAssistantAskResponse {
	name := boundedContextText(workContext.DisplayName, 100)
	if name == "" {
		name = "Your assistant"
	}
	msg := fmt.Sprintf("%s could not reach the system model, so there is no answer yet. Your message was not sent; try again in a moment.", name)
	if errors.Is(err, errHomeModelNotConfigured) {
		msg = fmt.Sprintf("%s needs a system model to answer. Choose one in Settings, then send your message again.", name)
	}
	h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Outcome: "model_unavailable", ActionCount: 1})
	return HomeAssistantAskResponse{
		Response: msg, Intent: intent, Identity: homeAssistantIdentity(workContext),
		Actions:          []HomeAction{{ID: "nav-settings", Type: HomeActionNavigate, Label: "Go to Settings", Href: "/settings"}},
		Conversation:     unstoredConversation(conversation),
		ModelUnavailable: true,
	}
}

func (h *HomeAssistantAskHandler) resolvePersonalAssistantContext(ctx context.Context) (*PersonalAssistantWorkContext, error) {
	if h == nil || h.PersonalAssistantContext == nil {
		return nil, nil
	}
	userID := strings.TrimSpace(h.UserID)
	if userID == "" {
		userID = "local"
	}
	resolved, err := h.PersonalAssistantContext.ResolvePersonalAssistantContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func homeAssistantIdentity(workContext *PersonalAssistantWorkContext) *HomeAssistantIdentity {
	if workContext == nil || !workContext.ReadyForWork() {
		return nil
	}
	return &HomeAssistantIdentity{
		DisplayName: boundedContextText(workContext.DisplayName, 100),
		Role:        "Personal Assistant",
		State:       workContext.State,
	}
}

func (h *HomeAssistantAskHandler) generateAnswer(ctx context.Context, prompt, intent string, snapshot HomeSnapshot, promptSources HomeSnapshotSources, workContext *PersonalAssistantWorkContext, history []llm.Message) (string, error) {
	// First-run greeting: a user with no workspaces yet is at "first contact".
	// The behavior naturally turns off once they create their first workspace.
	firstRun := snapshot.Meta.WorkspaceCount == 0
	return h.runModel(ctx, modelTurn{
		system:      buildHomeSystemPromptWithAssistant(firstRun, workContext),
		history:     history,
		user:        buildHomeUserPromptWithAssistant(prompt, intent, snapshot, workContext),
		sources:     promptSources,
		temperature: 0.3,
	})
}

// modelTurn is one prepared request to the system model: the system prompt,
// the bounded history of the current conversation (if any), and this turn.
type modelTurn struct {
	system      string
	history     []llm.Message
	user        string
	sources     HomeSnapshotSources
	temperature float64
}

// runModel sends one turn to the configured system model with the read-only
// home tools and returns the final text.
func (h *HomeAssistantAskHandler) runModel(ctx context.Context, turn modelTurn) (string, error) {
	provider, model, err := h.resolveProvider()
	if err != nil {
		return "", err
	}
	registry := newHomeToolRegistry(turn.sources)

	conversation := make([]llm.Message, 0, len(turn.history)+2)
	conversation = append(conversation, llm.NewSystemMessage(turn.system))
	conversation = append(conversation, turn.history...)
	conversation = append(conversation, llm.NewUserMessage(turn.user))
	tools := registry.Definitions()

	for round := 0; round < homeMaxToolRounds; round++ {
		resp, chatErr := provider.Chat(ctx, llm.ChatRequest{
			Model:       model,
			Messages:    conversation,
			Tools:       tools,
			Temperature: turn.temperature,
		})
		if chatErr != nil {
			return "", chatErr
		}
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) == "" {
				return "I couldn't find anything to report for that.", nil
			}
			return resp.Content, nil
		}

		calls := make([]llm.ToolCall, len(resp.ToolCalls))
		copy(calls, resp.ToolCalls)
		for i := range calls {
			if strings.TrimSpace(calls[i].ID) == "" {
				calls[i].ID = fmt.Sprintf("tool_%d_%d", round+1, i+1)
			}
		}
		conversation = append(conversation, llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: calls})
		for i, tc := range calls {
			result, toolErr := registry.Execute(ctx, resp.ToolCalls[i].Name, resp.ToolCalls[i].Arguments)
			if toolErr != nil {
				result = "ERROR: " + toolErr.Error()
			}
			conversation = append(conversation, llm.NewToolMessage(tc.ID, result))
		}
	}
	// Tool budget exhausted: make one final tool-free attempt for a summary.
	resp, chatErr := provider.Chat(ctx, llm.ChatRequest{Model: model, Messages: conversation, Temperature: turn.temperature})
	if chatErr != nil {
		return "", chatErr
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "I gathered some data but couldn't compose a final summary. Try narrowing the question.", nil
	}
	return resp.Content, nil
}

func (h *HomeAssistantAskHandler) resolveProvider() (llm.Provider, string, error) {
	if h.LLMFactory == nil || h.SystemModel == nil {
		return nil, "", errHomeModelNotConfigured
	}
	providerName, model := h.SystemModel.GetSystemModel()
	if strings.TrimSpace(providerName) == "" {
		return nil, "", errHomeModelNotConfigured
	}
	provider, err := h.LLMFactory.GetProvider(providerName)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(model) == "" {
		if models := provider.DefaultModels(); len(models) > 0 {
			model = models[0]
		}
	}
	return provider, model, nil
}

func (h *HomeAssistantAskHandler) modelUnavailableResponse(ctx context.Context, prompt, intent string, snapshot HomeSnapshot, workContext *PersonalAssistantWorkContext, err error) HomeAssistantAskResponse {
	meta := snapshot.Meta
	msg := "I can't reach the system model right now, so I can't compose a written summary. "
	if errors.Is(err, errHomeModelNotConfigured) {
		msg = "No system model is configured yet, so I can't answer in writing. Set one up in Settings and try again. "
		if workContext != nil && workContext.ReadyForWork() {
			msg = fmt.Sprintf("%s's conversational answers are paused until a system model is configured. Set one up in Settings and try again. ", workContext.DisplayName)
		}
	}
	msg += "Here's what I can point you to from your data: " + describeSnapshotBriefly(snapshot)
	actions := []HomeAction{{
		ID:    "nav-settings",
		Type:  HomeActionNavigate,
		Label: "Go to Settings",
		Href:  "/settings",
	}}
	actions = append(actions, h.buildNextStepActions(intent, "", snapshot)...)
	h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Window: string(meta.Window), Outcome: "model_unavailable", ActionCount: len(actions), Degraded: meta.Degraded})
	return HomeAssistantAskResponse{Response: msg, Intent: intent, Identity: homeAssistantIdentity(workContext), SnapshotMeta: &meta, Actions: actions, ModelUnavailable: true}
}

func describeSnapshotBriefly(s HomeSnapshot) string {
	parts := []string{
		fmt.Sprintf("%d workspace(s)", s.Meta.WorkspaceCount),
		fmt.Sprintf("%d task(s) active %s", s.Meta.TaskCount, s.Meta.WindowLabel),
	}
	if s.Meta.OpportunityCount > 0 {
		parts = append(parts, fmt.Sprintf("%d open opportunity(ies)", s.Meta.OpportunityCount))
	}
	return strings.Join(parts, ", ") + "."
}

// executeConfirmedAction performs a previously-confirmed mutation. Non-mutating
// types must never reach here (the frontend executes navigation directly).
func (h *HomeAssistantAskHandler) executeConfirmedAction(ctx context.Context, intent string, action HomeAction) HomeAssistantAskResponse {
	if !homeMutatingActionTypes[action.Type] {
		return HomeAssistantAskResponse{Response: "That action doesn't require confirmation.", Intent: intent}
	}
	args := action.Arguments
	if action.Type == HomeActionRemember {
		if h.PersonalAssistantMemory == nil {
			return HomeAssistantAskResponse{Response: "Memory is unavailable in this build. Nothing was saved.", Intent: intent}
		}
		version, err := strconv.ParseInt(actionArgString(args, "state_version"), 10, 64)
		if err != nil || version < 1 {
			return HomeAssistantAskResponse{Response: "This memory proposal is stale. Nothing was saved; ask me to remember it again.", Intent: intent}
		}
		result, err := h.PersonalAssistantMemory.Remember(ctx, h.UserID, personalassistant.RememberRequest{
			IfVersion: version, Destination: personalassistant.MemoryDestination(actionArgString(args, "destination")),
			Text: actionArgString(args, "text"), Preference: actionArgString(args, "preference"), Value: actionArgString(args, "value"),
		})
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't save that memory. Nothing was changed; refresh and try again.", Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionRemember)
		return HomeAssistantAskResponse{
			Response: "Saved that explicit memory. You can review, edit, or delete it at its source.", Intent: intent,
			Actions: []HomeAction{{ID: "open-saved-memory", Type: HomeActionNavigate, Label: "Review saved memory", Href: result.Href}},
		}
	}
	if h.Mutator == nil {
		return HomeAssistantAskResponse{Response: "I can't perform that change in this build yet.", Intent: intent}
	}
	switch action.Type {
	case HomeActionCreateWorkspace:
		name := actionArgString(args, "name")
		if name == "" {
			return HomeAssistantAskResponse{Response: "I need a name to create a workspace.", Intent: intent}
		}
		id, href, err := h.Mutator.CreateWorkspace(ctx, name, actionArgString(args, "description"))
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't create the workspace: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionCreateWorkspace)
		return HomeAssistantAskResponse{
			Response: fmt.Sprintf("Created workspace %q. Opening it now.", name),
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-created-ws", Type: HomeActionOpenWorkspace, Label: "Open " + name, Href: href, WorkspaceID: id}},
		}
	case HomeActionCreateTask:
		wsID := firstNonEmpty(action.WorkspaceID, actionArgString(args, "workspace_id"))
		desc := actionArgString(args, "description")
		if wsID == "" || desc == "" {
			return HomeAssistantAskResponse{Response: "I need a workspace and a description to create a task.", Intent: intent}
		}
		id, href, err := h.Mutator.CreateTask(ctx, wsID, desc)
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't create the task: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionCreateTask)
		return HomeAssistantAskResponse{
			Response: "Created the task.",
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-created-task", Type: HomeActionOpenWorkspace, Label: "Open workspace", Href: href, WorkspaceID: wsID, TaskID: id}},
		}
	case HomeActionCreateBacklogItem:
		wsID := firstNonEmpty(action.WorkspaceID, actionArgString(args, "workspace_id"))
		desc := actionArgString(args, "description")
		if wsID == "" || desc == "" {
			return HomeAssistantAskResponse{Response: "I need a workspace and a description to add to the backlog.", Intent: intent}
		}
		id, href, err := h.Mutator.CreateBacklogItem(ctx, wsID, desc)
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't add that to the backlog: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionCreateBacklogItem)
		return HomeAssistantAskResponse{
			Response: "Added to the backlog. It stays uncommitted until you promote it to Ready.",
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-created-backlog-item", Type: HomeActionOpenWorkspace, Label: "Open workspace", Href: href, WorkspaceID: wsID, TaskID: id}},
		}
	case HomeActionStartTask:
		wsID := firstNonEmpty(action.WorkspaceID, actionArgString(args, "workspace_id"))
		taskID := firstNonEmpty(action.TaskID, actionArgString(args, "task_id"))
		if wsID == "" || taskID == "" {
			return HomeAssistantAskResponse{Response: "I need a workspace and task to start.", Intent: intent}
		}
		href, err := h.Mutator.StartTask(ctx, wsID, taskID)
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't start the task: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionStartTask)
		return HomeAssistantAskResponse{
			Response: "Started the task.",
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-started-task", Type: HomeActionOpenWorkspace, Label: "Open workspace", Href: href, WorkspaceID: wsID, TaskID: taskID}},
		}
	case HomeActionAssignAgent:
		wsID := firstNonEmpty(action.WorkspaceID, actionArgString(args, "workspace_id"))
		agentName := actionArgString(args, "agent_name")
		if wsID == "" || agentName == "" {
			return HomeAssistantAskResponse{Response: "I need a workspace and an agent to assign.", Intent: intent}
		}
		href, err := h.Mutator.AssignAgent(ctx, wsID, agentName)
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't assign the agent: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionAssignAgent)
		return HomeAssistantAskResponse{
			Response: "Assigned " + agentName + " to the workspace.",
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-assigned-ws", Type: HomeActionOpenWorkspace, Label: "Open workspace", Href: href, WorkspaceID: wsID}},
		}
	case HomeActionCreateAgent:
		name := actionArgString(args, "name")
		description := actionArgString(args, "description")
		if name == "" {
			return HomeAssistantAskResponse{Response: "I need a name to create an agent.", Intent: intent}
		}
		href, err := h.Mutator.CreateAgent(ctx, name, description)
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't create the agent: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionCreateAgent)
		return HomeAssistantAskResponse{
			Response: "Created the agent " + name + ".",
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-agents", Type: HomeActionNavigate, Label: "Open Agents", Href: href}},
		}
	case HomeActionRemoveAgent:
		wsID := firstNonEmpty(action.WorkspaceID, actionArgString(args, "workspace_id"))
		agentName := actionArgString(args, "agent_name")
		if wsID == "" || agentName == "" {
			return HomeAssistantAskResponse{Response: "I need a workspace and an agent to remove.", Intent: intent}
		}
		href, err := h.Mutator.RemoveAgent(ctx, wsID, agentName)
		if err != nil {
			return HomeAssistantAskResponse{Response: "I couldn't remove the agent: " + err.Error(), Intent: intent}
		}
		h.recordMutation(ctx, intent, HomeActionRemoveAgent)
		return HomeAssistantAskResponse{
			Response: "Removed " + agentName + " from the workspace.",
			Intent:   intent,
			Actions:  []HomeAction{{ID: "open-removed-ws", Type: HomeActionOpenWorkspace, Label: "Open workspace", Href: href, WorkspaceID: wsID}},
		}
	}
	return HomeAssistantAskResponse{Response: "Unsupported action.", Intent: intent}
}

func (h *HomeAssistantAskHandler) recordMutation(ctx context.Context, intent, actionType string) {
	h.emitTrace(ctx, HomeAskTrace{Intent: intent, Outcome: "mutation_executed", ActionCount: 1, ConfirmedType: actionType})
}

func actionArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
