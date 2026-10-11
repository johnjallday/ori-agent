package agenthttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/assistantops"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HomeAssistantOperationRef is the only operation input a browser sends with a
// turn: which tab is asking and whether that tab chose to watch. Neither is
// authority. The tab identity scopes command delivery; Watch only says that
// this tab may have its view moved.
type HomeAssistantOperationRef struct {
	TabID string `json:"tab_id,omitempty"`
	Watch bool   `json:"watch,omitempty"`
}

// AssistantOperationCapability tells the drawer whether the assistant can
// operate the app on this installation right now, and why not when it cannot.
type AssistantOperationCapability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Reasons an operation cannot be offered. They are states, not errors: the
// manual controls keep working in every one of them.
const (
	operationsNotWired       = "operations_unavailable"
	operationsNoModel        = "model_not_configured"
	operationsProviderNoTool = "provider_cannot_run_ori_tools"
)

// SetOperationStore wires assistant operations. Without it no operation tool is
// registered, no skill is loaded and the endpoints report unavailable.
func (h *HomeAssistantAskHandler) SetOperationStore(store assistantops.Store) {
	if store == nil {
		h.Operations = nil
		return
	}
	if _, err := assistantops.LoadSkill(); err != nil {
		// A build whose own skill does not validate offers no operations rather
		// than tools without their instructions.
		logger.Error("Assistant operations disabled: embedded skill is invalid", logger.Fields{"error": err})
		h.Operations = nil
		return
	}
	h.Operations = assistantops.NewService(store, operationGuard{handler: h})
}

// operationCapability reports whether the configured system model can run
// Ori's operation tools. It never switches providers or enables native tools.
func (h *HomeAssistantAskHandler) operationCapability() AssistantOperationCapability {
	if h == nil || !h.Operations.Available() {
		return AssistantOperationCapability{Reason: operationsNotWired}
	}
	provider, _, err := h.resolveProvider()
	if err != nil || provider == nil {
		return AssistantOperationCapability{Reason: operationsNoModel}
	}
	if !provider.Capabilities().SupportsTools {
		return AssistantOperationCapability{Reason: operationsProviderNoTool}
	}
	return AssistantOperationCapability{Available: true}
}

// OperationCapability is the same answer for the relationship status the
// drawer already reads.
func (h *HomeAssistantAskHandler) OperationCapability() AssistantOperationCapability {
	return h.operationCapability()
}

// OperationRequestSupported is the router's question: is this prompt an
// explicit request to operate the app, on an installation that can do it? Ask
// checks the same thing again for itself.
func (h *HomeAssistantAskHandler) OperationRequestSupported(_ context.Context, prompt string) bool {
	return h != nil && h.operationRequest(prompt) && h.operationCapability().Available
}

var (
	quotedSpan      = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|`[^`]*`|«[^»]*»")
	clauseSeparator = regexp.MustCompile(`[.!?;\n]+|,?\s+and then\s+|,\s+then\s+`)
)

// Verbs that open a place and verbs that pick someone. A request must begin a
// clause with one of them; a verb somewhere inside a sentence is discussion.
var (
	operationOpenLeads   = []string{"open ", "go to ", "take me to ", "bring up ", "show me ", "show ", "navigate to ", "jump to ", "switch to "}
	operationSelectLeads = []string{"select ", "pick ", "choose ", "focus on ", "switch to "}
	operationReferents   = []string{"it", "that", "this", "one", "there", "them", "those", "these", "the same"}
	// A target that opens with one of these is a question about the app.
	operationQuestionTargets = []string{"how ", "what ", "where ", "which ", "why ", "when ", "who ", "whether ", "if "}
)

// operationRequest reports whether the user's own current words ask Ori to
// open a workspace or select an agent. It reads nothing but this message:
// earlier turns, quoted text and anything a reader returned can never make a
// turn an operation. A question, a hypothetical, a negation or a target that
// is only "it" or "that" is not a request.
func (h *HomeAssistantAskHandler) operationRequest(prompt string) bool {
	if h == nil || strings.TrimSpace(prompt) == "" || isCompositionRequest(prompt) || isAssistantWorkspaceReviewRequest(prompt) {
		return false
	}
	// What someone else said is not what the user asks now.
	unquoted := quotedSpan.ReplaceAllString(prompt, " ")
	for _, clause := range clauseSeparator.Split(unquoted, -1) {
		if h.operationClause(clause) {
			return true
		}
	}
	return false
}

func (h *HomeAssistantAskHandler) operationClause(clause string) bool {
	text := stripCompositionPolitePrefixes(normalizeRouteToken(clause))
	if text == "" || panelExploratoryRequest(text) {
		return false
	}
	lead := func(leads []string) (string, bool) {
		for _, candidate := range leads {
			if strings.HasPrefix(text, candidate) {
				return strings.TrimSpace(strings.TrimPrefix(text, candidate)), true
			}
		}
		return "", false
	}
	explicitTarget := func(target string) bool {
		target = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(target, "the "), "my "))
		if target == "" {
			return false
		}
		for _, referent := range operationReferents {
			// "open it", "select that one": the target is in an earlier turn,
			// and an earlier turn cannot supply it.
			if target == referent || strings.HasPrefix(target, referent+" ") {
				return false
			}
		}
		for _, question := range operationQuestionTargets {
			// "show me how to open a workspace" asks about the app.
			if strings.HasPrefix(target, question) {
				return false
			}
		}
		return true
	}
	if target, ok := lead(operationOpenLeads); ok && explicitTarget(target) {
		if _, _, named := matchWorkspaceInPrompt(h.Sources.Workspaces, target); named || slices.Contains(strings.Fields(target), "workspace") {
			return true
		}
	}
	if target, ok := lead(operationSelectLeads); ok && explicitTarget(target) {
		if _, named := matchAgentInPrompt(h.Sources.Agents, target); named || slices.Contains(strings.Fields(target), "agent") {
			return true
		}
	}
	return false
}

// operationGuard is the host side of assistantops.Guard: every answer is read
// from canonical state at the moment it is asked.
type operationGuard struct {
	handler *HomeAssistantAskHandler
}

func (g operationGuard) CurrentUser(ctx context.Context) (string, error) {
	return g.handler.currentAssistantUser(ctx)
}

func (g operationGuard) Relationship(ctx context.Context, userID string) (assistantops.Owner, error) {
	provider, ok := g.handler.PersonalAssistantContext.(interface {
		ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error)
	})
	if !ok || provider == nil {
		return assistantops.Owner{}, errors.New("relationship reader unavailable")
	}
	work, err := provider.ResolvePersonalAssistantRelationship(ctx, userID)
	if err != nil {
		return assistantops.Owner{}, err
	}
	owner, ok := operationOwner(userID, work)
	if !ok {
		return assistantops.Owner{}, fmt.Errorf("%w: the assistant is not ready for work", assistantops.ErrInvalidated)
	}
	return owner, nil
}

// operationOwner is the relationship an operation is bound to. It has no
// conversation yet; the stored turn supplies that.
func operationOwner(userID string, work *PersonalAssistantWorkContext) (assistantops.Owner, bool) {
	if work == nil || !work.ReadyForWork() {
		return assistantops.Owner{}, false
	}
	owner := assistantops.Owner{
		UserID: strings.TrimSpace(userID), AssistantID: strings.TrimSpace(work.AssistantID), StateVersion: work.StateVersion,
		HQWorkspaceID: strings.TrimSpace(work.HQWorkspaceID), ProfileName: strings.TrimSpace(work.ConversationAgent),
	}
	return owner, owner.Validate() == nil
}

func (g operationGuard) ConversationOwned(ctx context.Context, owner assistantops.Owner) error {
	reader, ok := g.handler.Conversations.(PersonalAssistantConversationOwnerReader)
	if !ok {
		return errors.New("conversation owner reader unavailable")
	}
	_, err := reader.ReadConversationOwner(ctx, owner.ConversationID, assistantcontext.SaveOwner{
		UserID: owner.UserID, WorkspaceID: owner.HQWorkspaceID, AgentName: owner.ProfileName, StateVersion: owner.StateVersion,
	})
	if errors.Is(err, ErrPersonalAssistantConversationNotFound) || errors.Is(err, ErrPersonalAssistantConversationOwnerChanged) {
		return fmt.Errorf("%w: the conversation is gone or no longer this assistant's", assistantops.ErrInvalidated)
	}
	return err
}

func (g operationGuard) Workspace(_ context.Context, userID, workspaceID string) (assistantops.Context, error) {
	resolver := g.handler.WorkspaceContext
	if resolver == nil || resolver.Source == nil {
		return assistantops.Context{}, errors.New("workspace source unavailable")
	}
	ws, err := resolver.Source.Get(workspaceID)
	if err != nil || ws == nil {
		// The store has no "not found" of its own. A workspace missing from a
		// successful listing is gone; anything else could not be checked.
		active, listErr := resolver.Source.ListActive()
		if listErr == nil && !slices.ContainsFunc(active, func(row *workspace.Workspace) bool { return row != nil && row.ID == workspaceID }) {
			return assistantops.Context{}, fmt.Errorf("%w: the workspace no longer exists", assistantops.ErrInvalidated)
		}
		return assistantops.Context{}, errors.New("workspace could not be read")
	}
	if !workspaceReadable(ws, userID) || strings.TrimSpace(ws.FolderSlug) == "" {
		return assistantops.Context{}, fmt.Errorf("%w: the workspace cannot be opened by this user", assistantops.ErrInvalidated)
	}
	return assistantops.Context{WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug, WorkspaceName: workspaceContextText(ws.Name, 120)}, nil
}

// operationResponse is what every operation endpoint returns on success.
type operationResponse struct {
	Operation *assistantops.Snapshot `json:"operation"`
}

// OperationCapabilityHandler: GET /api/home-assistant/operations/capability.
func (h *HomeAssistantAskHandler) OperationCapabilityHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	orihttp.WriteJSON(w, h.operationCapability())
}

// OperationHandler: GET /api/home-assistant/operations/{id}?tab=<tab id>.
// A tab that is not watching gets the recorded facts and no commands.
func (h *HomeAssistantAskHandler) OperationHandler(w http.ResponseWriter, r *http.Request) {
	if !h.Operations.Available() {
		respondOperationError(w, assistantops.ErrUnavailable)
		return
	}
	snapshot, err := h.Operations.Snapshot(r.Context(), r.PathValue("id"), r.URL.Query().Get("tab"))
	writeOperation(w, snapshot, err)
}

// OperationAckHandler: POST /api/home-assistant/operations/{id}/ack. It records
// what the watching tab observed. It can change no task and start nothing.
func (h *HomeAssistantAskHandler) OperationAckHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CommandID string                     `json:"command_id"`
		Revision  int64                      `json:"revision"`
		TabID     string                     `json:"tab_id"`
		Outcome   assistantops.CommandStatus `json:"outcome"`
		Observed  *assistantops.Observation  `json:"observed,omitempty"`
		Reason    string                     `json:"reason,omitempty"`
	}
	if !h.operationBody(w, r, &req) {
		return
	}
	snapshot, err := h.Operations.Acknowledge(r.Context(), assistantops.Acknowledgement{
		OperationID: r.PathValue("id"), CommandID: req.CommandID, Revision: req.Revision, TabID: req.TabID,
		Outcome: req.Outcome, Observed: req.Observed, Reason: req.Reason,
	})
	writeOperation(w, snapshot, err)
}

// OperationWatchHandler: POST /api/home-assistant/operations/{id}/watch. It
// turns watching on or off for the calling tab. Off cancels nothing.
func (h *HomeAssistantAskHandler) OperationWatchHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TabID    string `json:"tab_id"`
		Revision int64  `json:"revision"`
		Watch    bool   `json:"watch"`
		Adopt    bool   `json:"adopt,omitempty"`
	}
	if !h.operationBody(w, r, &req) {
		return
	}
	snapshot, err := h.Operations.SetWatch(r.Context(), r.PathValue("id"), req.TabID, req.Revision, req.Watch, req.Adopt)
	writeOperation(w, snapshot, err)
}

// OperationCancelHandler: POST /api/home-assistant/operations/{id}/cancel. It
// dismisses the operation. A task it created or started is not touched.
func (h *HomeAssistantAskHandler) OperationCancelHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TabID    string `json:"tab_id,omitempty"`
		Revision int64  `json:"revision"`
	}
	if !h.operationBody(w, r, &req) {
		return
	}
	snapshot, err := h.Operations.Cancel(r.Context(), r.PathValue("id"), req.TabID, req.Revision)
	writeOperation(w, snapshot, err)
}

func (h *HomeAssistantAskHandler) operationBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if !h.Operations.Available() {
		respondOperationError(w, assistantops.ErrUnavailable)
		return false
	}
	// Bodies are a handful of identifiers; nothing here needs more.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	return orihttp.ParseJSONBody(w, r, into)
}

func writeOperation(w http.ResponseWriter, snapshot *assistantops.Snapshot, err error) {
	// A snapshot is current tab-local state, never a reusable grant.
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		respondOperationError(w, err)
		return
	}
	orihttp.WriteJSON(w, operationResponse{Operation: snapshot})
}

// operationErrorCode maps a service error to its stable category. The browser
// switches on the code; the message is bounded, user-safe text.
func operationErrorCode(err error) (int, string, string) {
	switch {
	case errors.Is(err, assistantops.ErrNotFound), errors.Is(err, assistantops.ErrForbidden):
		return http.StatusNotFound, "operation_not_found", "That operation is not available."
	case errors.Is(err, assistantops.ErrExpired):
		return http.StatusGone, "operation_expired", "That operation ran out of time. Ask again to repeat it."
	case errors.Is(err, assistantops.ErrInvalidated):
		return http.StatusGone, "operation_invalidated", "That operation is no longer valid because the assistant, conversation or workspace changed."
	case errors.Is(err, assistantops.ErrTabMismatch):
		return http.StatusConflict, "operation_tab_mismatch", "Another tab is watching this operation."
	case errors.Is(err, assistantops.ErrTransition):
		return http.StatusConflict, "operation_ended", "That operation has already ended."
	case errors.Is(err, assistantops.ErrConflict):
		return http.StatusConflict, "operation_conflict", "The operation changed. Reading it again."
	case errors.Is(err, assistantops.ErrInvalidCommand), errors.Is(err, assistantops.ErrBounds):
		return http.StatusBadRequest, "operation_invalid", "That request does not match the operation."
	}
	return http.StatusServiceUnavailable, "operation_unavailable", "Operations could not be checked right now. Nothing was changed."
}

func respondOperationError(w http.ResponseWriter, err error) {
	status, code, message := operationErrorCode(err)
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(code, message))
}
