package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type operationTurnKey struct{}

func operationTurnFromContext(ctx context.Context) *operationTurn {
	turn, _ := ctx.Value(operationTurnKey{}).(*operationTurn)
	return turn
}

// operationTurn is one accepted turn's operating state. A tool call only
// stages a request here: nothing is recorded, and nothing can reach a browser,
// until the turn that asked for it has been stored.
type operationTurn struct {
	handler *HomeAssistantAskHandler
	scope   *assistantWorkspaceTurn
	owner   assistantops.Owner
	ref     HomeAssistantOperationRef
	staged  *assistantops.CreateRequest
	// request is what the user's own words asked for (navigation or task). It
	// decides which legacy confirmation this turn replaces, nothing more.
	request string
}

// operationTurn returns the turn's operating state, or nil when this turn may
// not operate: operations are not wired, the turn is not a stored hired-
// assistant conversation on a resolved panel context, the configured provider
// cannot run Ori's tools, or the user's own current words are not an explicit
// request. With nil, no operation tool is registered and no skill is loaded.
func (h *HomeAssistantAskHandler) operationTurn(prompt string, ref *HomeAssistantOperationRef, conversation *openConversation, work *PersonalAssistantWorkContext, scope *assistantWorkspaceTurn) *operationTurn {
	if h == nil || conversation == nil || scope == nil {
		return nil
	}
	kind := h.operationRequestKind(prompt)
	if kind == "" || !h.operationCapability().Available {
		return nil
	}
	owner, ok := operationOwner(scope.userID, work)
	if !ok {
		return nil
	}
	turn := &operationTurn{handler: h, scope: scope, owner: owner, request: kind}
	// A malformed tab identity is treated as no tab: the request can still be
	// recorded, and it is then delivered to nobody until a tab opts in.
	if ref != nil && assistantops.ValidTabID(ref.TabID) {
		turn.ref = *ref
	}
	return turn
}

// owns reports whether this turn's operation replaces a legacy confirmation.
// Only one does: a request to prepare a task replaces the one-click task
// create. Starting work, and every workspace or agent change, keep their own
// confirmation.
func (t *operationTurn) owns(conf *HomeActionConfirmation) bool {
	return t != nil && conf != nil && t.request == operationRequestTask && conf.ActionType == HomeActionCreateTask
}

// prompt is the operating skill for this turn, loaded only when every
// operation it names was actually offered to the model.
func (t *operationTurn) prompt(offered []llm.Tool) string {
	skill, err := assistantops.LoadSkill()
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(offered))
	for _, tool := range offered {
		names = append(names, tool.Name)
	}
	instructions := skill.Prompt(names)
	if instructions == "" {
		return ""
	}
	// The conversation prompt says the assistant can run nothing itself. That
	// stays true; this names the one thing Ori will do when asked, for this turn.
	// It is sent as the request's system prompt, not inside a system-role
	// message (see runModel), so its wording does not depend on where a
	// provider places it relative to the rest.
	return "For this turn one exception applies to Ori's rule that you can run nothing in the app yourself: Ori registered the operations that the skill in this message names. Asking Ori to open a workspace, select an agent or prepare a task draft for the user to review is allowed. It still creates, saves, starts, schedules, installs and sends nothing: the user does that in the app." + instructions
}

// finalize records the staged operation once its turn is stored, and only
// then. A turn that was not saved leaves no operation behind and says so.
func (t *operationTurn) finalize(ctx context.Context, response *HomeAssistantAskResponse) {
	if t == nil || t.staged == nil {
		return
	}
	saved := response.Conversation
	if response.ModelUnavailable || saved == nil || !saved.Stored || strings.TrimSpace(saved.ID) == "" {
		response.OperationError = "turn_not_saved"
		return
	}
	request := *t.staged
	request.Owner = t.owner
	request.Owner.ConversationID, request.Owner.TurnMessageID = saved.ID, saved.AssistantMessageID
	snapshot, err := t.handler.Operations.Create(ctx, request)
	if err != nil {
		_, code, _ := operationErrorCode(err)
		response.OperationError = code
		logger.Warn("Assistant operation was not recorded", logger.Fields{"scope": "home_assistant.operation", "reason": code})
		return
	}
	response.Operation = snapshot
	// One control per destination: the operation owns opening this workspace,
	// so the ordinary link to the same place is not offered beside it.
	response.Actions = slices.DeleteFunc(response.Actions, func(action HomeAction) bool {
		return action.Type == HomeActionOpenWorkspace && action.WorkspaceID == snapshot.Context.WorkspaceID
	})
}

// operationRegistry adds the closed set of operation tools to a turn's
// registry. Every other tool is the base registry's, unchanged.
type operationRegistry struct {
	base modelToolRegistry
	turn *operationTurn
}

func (r *operationRegistry) Definitions() []llm.Tool {
	workspaceID := map[string]any{"type": "string", "maxLength": 80, "description": "Canonical ID of the workspace, from Ori's listings in this turn."}
	workspaceName := map[string]any{"type": "string", "maxLength": 120, "description": "Exact workspace name, when no ID is known."}
	return append(r.base.Definitions(), llm.Tool{
		Name:        assistantops.ToolOpen,
		Description: "Ask Ori to open one of the user's existing workspaces in their app and, optionally, select one agent that already works there. Use only for an explicit request in the user's current message. It changes what the user's own tab shows and nothing else: no workspace, agent or task is created, changed or started. Ori reports that the request was recorded, not that the page changed. Use as the only tool call in this batch, once.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"workspace_id":   workspaceID,
			"workspace_name": workspaceName,
			"agent_name":     map[string]any{"type": "string", "maxLength": 120, "description": "Exact name of an agent in that workspace to select. Omit unless the user asked for an agent."},
		}, "additionalProperties": false},
	}, llm.Tool{
		Name:        assistantops.ToolPrepareTask,
		Description: "Ask Ori to prepare one task draft for an agent that already works in one of the user's workspaces, and show it in the app's own task form for the user to review. Use only for an explicit request in the user's current message. It creates no task and starts nothing: the user edits the draft and saves it themselves. Only a title, a brief and the agent can be set; schedules, subtasks, workflows and file access are the user's to add in the form. Use as the only tool call in this batch, once.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"workspace_id":   workspaceID,
			"workspace_name": workspaceName,
			"agent_name":     map[string]any{"type": "string", "maxLength": 120, "description": "Exact name of the agent in that workspace who should do the task."},
			"agent_node_id":  map[string]any{"type": "string", "maxLength": 120, "description": "Only when Ori returned several instances with the same name: the node of the one to assign."},
			"title":          map[string]any{"type": "string", "minLength": 1, "maxLength": assistantops.MaxDraftTitleRunes, "description": "A short task title in the user's own terms."},
			"brief":          map[string]any{"type": "string", "maxLength": assistantops.MaxDraftBriefRunes, "description": "What the user asked for, in their words. Do not add requirements they did not state."},
		}, "required": []string{"title"}, "additionalProperties": false},
	})
}

func (r *operationRegistry) Execute(ctx context.Context, name, arguments string) (string, error) {
	if !assistantops.IsTool(name) {
		return r.base.Execute(ctx, name, arguments)
	}
	if err := r.turn.handler.revalidateWorkspaceTurn(ctx, r.turn.scope); err != nil {
		return "", err
	}
	var args operationArguments
	decoder := json.NewDecoder(bytes.NewReader([]byte(arguments)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil && strings.TrimSpace(arguments) != "" {
		return "", errors.New("invalid operation arguments")
	}
	args.trim()
	if name == assistantops.ToolPrepareTask {
		return homeToolJSON(r.turn.prepareTask(args))
	}
	// Task fields on a request to open something are a mistake, not a hint.
	if args.Title != "" || args.Brief != "" || args.AgentNodeID != "" {
		return "", errors.New("invalid operation arguments")
	}
	return homeToolJSON(r.turn.open(args.WorkspaceID, args.WorkspaceName, args.AgentName))
}

// operationArguments is everything an operation tool may be given. Nothing
// here is a URL, a selector or a script, and unknown fields are refused.
type operationArguments struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	AgentName     string `json:"agent_name"`
	AgentNodeID   string `json:"agent_node_id"`
	Title         string `json:"title"`
	Brief         string `json:"brief"`
}

func (a *operationArguments) trim() {
	for _, field := range []*string{&a.WorkspaceID, &a.WorkspaceName, &a.AgentName, &a.AgentNodeID, &a.Title, &a.Brief} {
		*field = strings.TrimSpace(*field)
	}
}

// prepareTask resolves the workspace and the exact roster instance, checks the
// draft's bounds and stages it. It writes no task: the draft is shown in the
// task form, and only the user's own save creates anything.
func (t *operationTurn) prepareTask(args operationArguments) map[string]any {
	if t.staged != nil {
		return map[string]any{"status": "unavailable", "reason": "one_operation_per_request", "note": "This request already has an operation. Nothing else was prepared."}
	}
	ws, result := t.resolveWorkspace(args.WorkspaceID, args.WorkspaceName)
	if ws == nil {
		return result
	}
	roster := rosterChoices(ws)
	if args.AgentName == "" {
		// A task with no agent would be handed to the workspace's entry agent
		// when saved. That choice is the user's, so ask instead.
		return map[string]any{"status": "choose", "reason": "agent_not_named", "choices": roster, "note": "Ask the user which agent in this workspace should do the task. Nothing was prepared."}
	}
	instances := rosterInstances(ws, args.AgentName, args.AgentNodeID)
	switch {
	case len(instances) == 0:
		return map[string]any{"status": "not_found", "reason": "agent_not_in_workspace", "closest": roster, "note": "No agent of that name works in this workspace. Nothing was prepared."}
	case len(instances) > 1:
		same := make([]operationChoice, 0, len(instances))
		for _, instance := range instances {
			same = append(same, operationChoice{Name: workspaceContextText(instance.Name, 120), Role: workspaceContextText(instance.Role, 120), NodeID: instance.NodeID})
		}
		return map[string]any{"status": "choose", "reason": "several_agents_match", "choices": same, "note": "Several instances share that name. Ask the user which one, then pass its agent_node_id. Nothing was prepared."}
	case strings.TrimSpace(instances[0].NodeID) == "":
		return map[string]any{"status": "unavailable", "reason": "agent_cannot_be_assigned", "note": "That agent cannot be assigned from here. The user can assign it in the task form. Nothing was prepared."}
	}
	draft := assistantops.Draft{Revision: 1, Title: args.Title, Brief: args.Brief}
	switch {
	case sensitive.ContainsSecretLikeText(args.Title) || sensitive.ContainsSecretLikeText(args.Brief):
		return map[string]any{"status": "unavailable", "reason": "secret_like_text", "note": "The draft looks like it contains a credential. Nothing was prepared; ask the user to leave secrets out."}
	case draft.Validate() != nil:
		return map[string]any{"status": "unavailable", "reason": "draft_out_of_bounds", "note": "A task needs a title of at most " + strconv.Itoa(assistantops.MaxDraftTitleRunes) + " characters and a brief of at most " + strconv.Itoa(assistantops.MaxDraftBriefRunes) + ". Nothing was prepared."}
	}
	instance := instances[0]
	t.staged = &assistantops.CreateRequest{
		Kind:    assistantops.KindTask,
		Context: assistantops.Context{WorkspaceID: ws.ID, AgentName: instance.Name, AgentNodeID: instance.NodeID},
		Actions: []assistantops.Action{assistantops.ActionOpenWorkspace, assistantops.ActionOpenTaskDraft},
		Draft:   &assistantops.Draft{Title: args.Title, Brief: args.Brief},
		TabID:   t.ref.TabID, Watch: t.ref.Watch && t.ref.TabID != "",
	}
	delivery := "open_button"
	if t.staged.Watch {
		delivery = "watching"
	}
	return map[string]any{
		"status":    "prepared",
		"workspace": map[string]any{"workspace_id": ws.ID, "name": workspaceContextText(ws.Name, 120), "kind": operationKind(ws)},
		"agent":     map[string]any{"name": workspaceContextText(instance.Name, 120), "role": workspaceContextText(instance.Role, 120)},
		"draft":     map[string]any{"title": args.Title},
		"delivery":  delivery,
		"note":      "Ori prepared this draft for the user to review in the task form. No task was created and nothing was started; do not say it was.",
	}
}

func rosterChoices(ws *workspace.Workspace) []operationChoice {
	roster := make([]operationChoice, 0, min(len(ws.AgentInstances), 8))
	for i := range ws.AgentInstances {
		if len(roster) == 8 {
			break
		}
		roster = append(roster, operationChoice{Name: workspaceContextText(ws.AgentInstances[i].Name, 120), Role: workspaceContextText(ws.AgentInstances[i].Role, 120)})
	}
	return roster
}

// operationChoice is one candidate Ori returns when a name is not one thing.
type operationChoice struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	Role        string `json:"role,omitempty"`
	NodeID      string `json:"agent_node_id,omitempty"`
}

// open resolves one workspace and, when asked, one of its agents from
// canonical state and stages the request. Anything but exactly one match
// stages nothing and says which case it is.
func (t *operationTurn) open(workspaceID, workspaceName, agentName string) map[string]any {
	if t.staged != nil {
		return map[string]any{"status": "unavailable", "reason": "one_operation_per_request", "note": "This request already has an operation. Nothing else was requested."}
	}
	ws, result := t.resolveWorkspace(workspaceID, workspaceName)
	if ws == nil {
		return result
	}
	accepted := assistantops.Context{WorkspaceID: ws.ID}
	actions := []assistantops.Action{assistantops.ActionOpenWorkspace}
	shown := map[string]any{"workspace_id": ws.ID, "name": workspaceContextText(ws.Name, 120), "kind": operationKind(ws)}
	var agent any
	if agentName != "" {
		instance, result := resolveRosterAgent(ws, agentName)
		if instance == nil {
			return result
		}
		accepted.AgentName, accepted.AgentNodeID = instance.Name, instance.NodeID
		actions = append(actions, assistantops.ActionSelectAgent)
		agent = map[string]any{"name": workspaceContextText(instance.Name, 120), "role": workspaceContextText(instance.Role, 120)}
	}
	t.staged = &assistantops.CreateRequest{
		Kind: assistantops.KindNavigation, Context: accepted, Actions: actions,
		TabID: t.ref.TabID, Watch: t.ref.Watch && t.ref.TabID != "",
	}
	delivery := "open_button"
	if t.staged.Watch {
		delivery = "watching"
	}
	return map[string]any{
		"status": "requested", "workspace": shown, "agent": agent, "delivery": delivery,
		"note": "Ori recorded this request. Ori has not confirmed that the page changed; do not say it is open or selected.",
	}
}

// resolveWorkspace finds exactly one workspace this user may open. A missing
// reference means the workspace this turn is about, never Personal HQ or a
// guess from history.
func (t *operationTurn) resolveWorkspace(id, name string) (*workspace.Workspace, map[string]any) {
	source := t.handler.WorkspaceContext.Source
	if id == "" && name == "" {
		if subject := t.scope.projection.Subject; subject != nil {
			id = subject.ID
		} else {
			return nil, map[string]any{"status": "not_found", "reason": "no_workspace_named", "note": "No workspace was named and this turn is not about one. Nothing was requested."}
		}
	}
	if id != "" {
		ws, err := source.Get(id)
		if err == nil && operationOpenable(ws, t.scope.userID) {
			return ws, nil
		}
		// An ID that does not resolve is not replaced by a name match.
		return nil, map[string]any{"status": "not_found", "reason": "workspace_not_found", "note": "No workspace with that ID can be opened. Nothing was requested."}
	}
	rows, err := source.ListActive()
	if err != nil {
		return nil, map[string]any{"status": "unavailable", "reason": "workspace_listing_unavailable", "note": "Ori could not list workspaces right now. Nothing was requested."}
	}
	var exact, near []*workspace.Workspace
	wanted := foldOperationName(name)
	for _, row := range rows {
		if !operationOpenable(row, t.scope.userID) {
			continue
		}
		candidate := foldOperationName(row.Name)
		switch {
		case candidate == wanted:
			exact = append(exact, row)
		case strings.Contains(candidate, wanted) || strings.Contains(wanted, candidate):
			near = append(near, row)
		}
	}
	if len(exact) == 1 {
		// Listings may be metadata only; the roster is read from the record.
		ws, err := source.Get(exact[0].ID)
		if err != nil || !operationOpenable(ws, t.scope.userID) {
			return nil, map[string]any{"status": "unavailable", "reason": "workspace_unavailable", "note": "That workspace could not be read right now. Nothing was requested."}
		}
		return ws, nil
	}
	if len(exact) > 1 {
		return nil, map[string]any{"status": "choose", "reason": "several_workspaces_match", "choices": workspaceChoices(exact), "note": "Several workspaces have that name. Ask the user which one. Nothing was requested."}
	}
	return nil, map[string]any{"status": "not_found", "reason": "workspace_not_found", "closest": workspaceChoices(near), "note": "No workspace has exactly that name. Nothing was requested."}
}

// resolveRosterAgent finds the named agent in this workspace's own roster. A
// same-named agent elsewhere in the app is not a match.
func resolveRosterAgent(ws *workspace.Workspace, name string) (*workspace.AgentInstance, map[string]any) {
	var matches []*workspace.AgentInstance
	roster := make([]operationChoice, 0, len(ws.AgentInstances))
	for i := range ws.AgentInstances {
		instance := &ws.AgentInstances[i]
		if foldOperationName(instance.Name) == foldOperationName(name) {
			matches = append(matches, instance)
		}
		if len(roster) < 8 {
			roster = append(roster, operationChoice{Name: workspaceContextText(instance.Name, 120), Role: workspaceContextText(instance.Role, 120)})
		}
	}
	if len(matches) == 0 {
		return nil, map[string]any{"status": "not_found", "reason": "agent_not_in_workspace", "closest": roster, "note": "No agent of that name works in this workspace. Nothing was requested."}
	}
	// Selection on the page is by agent name, so instances that share a name
	// are one selection. The first instance identifies it.
	return matches[0], nil
}

func operationOpenable(ws *workspace.Workspace, userID string) bool {
	return ws != nil && workspaceReadable(ws, userID) && strings.TrimSpace(ws.FolderSlug) != ""
}

func operationKind(ws *workspace.Workspace) string {
	if kind := strings.TrimSpace(ws.Kind); kind != "" {
		return kind
	}
	return "project"
}

func workspaceChoices(rows []*workspace.Workspace) []operationChoice {
	choices := make([]operationChoice, 0, min(len(rows), 5))
	for _, row := range rows {
		if len(choices) == 5 {
			break
		}
		choices = append(choices, operationChoice{WorkspaceID: row.ID, Name: workspaceContextText(row.Name, 120), Kind: operationKind(row)})
	}
	return choices
}

func foldOperationName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
