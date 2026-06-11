package agenthttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
)

const homeMaxToolRounds = 4

var errHomeModelNotConfigured = errors.New("system model is not configured")

// Home action types (PRD 4.5 / 5.1).
const (
	HomeActionNavigate        = "navigate"
	HomeActionOpenWorkspace   = "open_workspace"
	HomeActionOpenTask        = "open_task"
	HomeActionOpenSession     = "open_session"
	HomeActionCreateWorkspace = "create_workspace"
	HomeActionCreateTask      = "create_task"
	HomeActionStartTask       = "start_task"
	HomeActionAskFollowup     = "ask_followup"
)

// homeMutatingActionTypes are the only action types that change state and thus
// require confirmation before execution.
var homeMutatingActionTypes = map[string]bool{
	HomeActionCreateWorkspace: true,
	HomeActionCreateTask:      true,
	HomeActionStartTask:       true,
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
}

// HomeAssistantAskResponse is the response for POST /api/home-assistant/ask.
type HomeAssistantAskResponse struct {
	Response             string                  `json:"response"`
	Intent               string                  `json:"intent"`
	SnapshotMeta         *HomeSnapshotMeta       `json:"snapshot_meta,omitempty"`
	Actions              []HomeAction            `json:"actions,omitempty"`
	RequiresConfirmation bool                    `json:"requires_confirmation,omitempty"`
	Confirmation         *HomeActionConfirmation `json:"confirmation,omitempty"`
}

// HomeActionMutator executes confirmed state-changing actions. The server wires a
// real implementation; a nil mutator degrades mutations gracefully.
type HomeActionMutator interface {
	CreateWorkspace(ctx context.Context, name, description string) (workspaceID, href string, err error)
	CreateTask(ctx context.Context, workspaceID, description string) (taskID, href string, err error)
	StartTask(ctx context.Context, workspaceID, taskID string) (href string, err error)
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
	Sources     HomeSnapshotSources
	LLMFactory  *llm.Factory
	SystemModel homeAskSystemModelReader
	Mutator     HomeActionMutator
	Trace       homeAskTraceEmitter
	now         func() time.Time
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

	// Confirmed mutation path: execute only known action types (FR #24).
	if req.ConfirmedAction != nil {
		return h.executeConfirmedAction(ctx, intent, *req.ConfirmedAction)
	}

	if prompt == "" {
		return HomeAssistantAskResponse{Response: "What would you like to know about your workspaces, tasks, or activity?", Intent: intent}
	}

	// Explicit, supported mutation request: ask for confirmation before doing
	// anything (FR #24). Execution happens only on a follow-up with ConfirmedAction.
	if conf := h.detectHomeMutationRequest(prompt); conf != nil {
		h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Outcome: "confirmation_required", ConfirmedType: conf.ActionType})
		return HomeAssistantAskResponse{
			Response:             conf.Summary,
			Intent:               intent,
			RequiresConfirmation: true,
			Confirmation:         conf,
		}
	}

	window := NormalizeHomeDateWindow(req.DateWindow, DefaultHomeDateWindowForPrompt(prompt))
	snapshot := BuildHomeSnapshot(ctx, h.Sources, window)

	answer, err := h.generateAnswer(ctx, prompt, intent, snapshot)
	if err != nil {
		return h.modelUnavailableResponse(ctx, prompt, intent, snapshot, err)
	}

	actions := h.buildNextStepActions(intent, prompt, snapshot)
	meta := snapshot.Meta
	h.emitTrace(ctx, HomeAskTrace{Prompt: prompt, Intent: intent, Window: string(window), Outcome: "answered", ActionCount: len(actions), Degraded: meta.Degraded})
	return HomeAssistantAskResponse{
		Response:     strings.TrimSpace(answer),
		Intent:       intent,
		SnapshotMeta: &meta,
		Actions:      actions,
	}
}

func (h *HomeAssistantAskHandler) generateAnswer(ctx context.Context, prompt, intent string, snapshot HomeSnapshot) (string, error) {
	provider, model, err := h.resolveProvider()
	if err != nil {
		return "", err
	}
	registry := newHomeToolRegistry(h.Sources)
	systemPrompt := buildHomeSystemPrompt()
	userPrompt := buildHomeUserPrompt(prompt, intent, snapshot)

	conversation := []llm.Message{
		llm.NewSystemMessage(systemPrompt),
		llm.NewUserMessage(userPrompt),
	}
	tools := registry.Definitions()

	for round := 0; round < homeMaxToolRounds; round++ {
		resp, chatErr := provider.Chat(ctx, llm.ChatRequest{
			Model:       model,
			Messages:    conversation,
			Tools:       tools,
			Temperature: 0.3,
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
	resp, chatErr := provider.Chat(ctx, llm.ChatRequest{Model: model, Messages: conversation, Temperature: 0.3})
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

func (h *HomeAssistantAskHandler) modelUnavailableResponse(ctx context.Context, prompt, intent string, snapshot HomeSnapshot, err error) HomeAssistantAskResponse {
	meta := snapshot.Meta
	msg := "I can't reach the system model right now, so I can't compose a written summary. "
	if errors.Is(err, errHomeModelNotConfigured) {
		msg = "No system model is configured yet, so I can't answer in writing. Set one up in Settings and try again. "
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
	return HomeAssistantAskResponse{Response: msg, Intent: intent, SnapshotMeta: &meta, Actions: actions}
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
	if h.Mutator == nil {
		return HomeAssistantAskResponse{Response: "I can't perform that change in this build yet.", Intent: intent}
	}
	args := action.Arguments
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
