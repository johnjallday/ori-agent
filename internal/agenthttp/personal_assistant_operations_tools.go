package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
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
}

// operationTurn returns the turn's operating state, or nil when this turn may
// not operate: operations are not wired, the turn is not a stored hired-
// assistant conversation on a resolved panel context, the configured provider
// cannot run Ori's tools, or the user's own current words are not an explicit
// request. With nil, no operation tool is registered and no skill is loaded.
func (h *HomeAssistantAskHandler) operationTurn(prompt string, ref *HomeAssistantOperationRef, conversation *openConversation, work *PersonalAssistantWorkContext, scope *assistantWorkspaceTurn) *operationTurn {
	if h == nil || conversation == nil || scope == nil || !h.operationRequest(prompt) || !h.operationCapability().Available {
		return nil
	}
	owner, ok := operationOwner(scope.userID, work)
	if !ok {
		return nil
	}
	turn := &operationTurn{handler: h, scope: scope, owner: owner}
	// A malformed tab identity is treated as no tab: the request can still be
	// recorded, and it is then delivered to nobody until a tab opts in.
	if ref != nil && assistantops.ValidTabID(ref.TabID) {
		turn.ref = *ref
	}
	return turn
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
	return "\nFor this turn one exception applies to the limits above: Ori registered the operation the skill below names. Asking Ori to open a workspace or select an agent is allowed. It still saves, starts, schedules, installs and sends nothing." + instructions
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
	return append(r.base.Definitions(), llm.Tool{
		Name:        assistantops.ToolOpen,
		Description: "Ask Ori to open one of the user's existing workspaces in their app and, optionally, select one agent that already works there. Use only for an explicit request in the user's current message. It changes what the user's own tab shows and nothing else: no workspace, agent or task is created, changed or started. Ori reports that the request was recorded, not that the page changed. Use as the only tool call in this batch, once.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"workspace_id":   map[string]any{"type": "string", "maxLength": 80, "description": "Canonical ID of the workspace, from Ori's listings in this turn."},
			"workspace_name": map[string]any{"type": "string", "maxLength": 120, "description": "Exact workspace name, when no ID is known."},
			"agent_name":     map[string]any{"type": "string", "maxLength": 120, "description": "Exact name of an agent in that workspace to select. Omit unless the user asked for an agent."},
		}, "additionalProperties": false},
	})
}

func (r *operationRegistry) Execute(ctx context.Context, name, arguments string) (string, error) {
	if !assistantops.IsTool(name) {
		return r.base.Execute(ctx, name, arguments)
	}
	if err := r.turn.handler.revalidateWorkspaceTurn(ctx, r.turn.scope); err != nil {
		return "", err
	}
	var args struct {
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		AgentName     string `json:"agent_name"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(arguments)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil && strings.TrimSpace(arguments) != "" {
		return "", errors.New("invalid operation arguments")
	}
	return homeToolJSON(r.turn.open(strings.TrimSpace(args.WorkspaceID), strings.TrimSpace(args.WorkspaceName), strings.TrimSpace(args.AgentName)))
}

// operationChoice is one candidate Ori returns when a name is not one thing.
type operationChoice struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	Role        string `json:"role,omitempty"`
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
