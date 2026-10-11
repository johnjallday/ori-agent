package agenthttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

type assistantResearchScopeValidator struct{ handler *HomeAssistantAskHandler }

type PersonalAssistantResearchFolderReader interface {
	ReadResearchFolder(context.Context, string, assistantcontext.SaveOwner) (assistantcontext.ResearchFolderRef, error)
}

func (h *HomeAssistantAskHandler) researchFolderDigest(ctx context.Context, id string, owner assistantcontext.SaveOwner, ref *HomeAssistantFolderRef) (string, error) {
	reader, ok := h.Conversations.(PersonalAssistantResearchFolderReader)
	if !ok {
		if ref != nil {
			return "", assistantdiscovery.ErrReviewRefused
		}
		return "", nil
	}
	folder, err := reader.ReadResearchFolder(ctx, id, owner)
	if err != nil {
		return "", assistantdiscovery.ErrReviewRefused
	}
	if ref != nil {
		if ref.SelectionID == "" || ref.SelectionID != folder.SelectionID || ref.Revision != folder.Revision || len(ref.FocusIDs) != len(folder.FocusIDs) {
			return "", assistantdiscovery.ErrReviewRefused
		}
		for i := range ref.FocusIDs {
			if ref.FocusIDs[i] != folder.FocusIDs[i] {
				return "", assistantdiscovery.ErrReviewRefused
			}
		}
	}
	data, err := json.Marshal(folder)
	if err != nil {
		return "", assistantdiscovery.ErrReviewRefused
	}
	return researchDigest(string(data)), nil
}

func (v assistantResearchScopeValidator) ValidateResearchLookup(ctx context.Context, lookup assistantdiscovery.Lookup) error {
	reader, ok := v.handler.Discovery.(interface {
		ValidateResearchLookup(context.Context, assistantdiscovery.Lookup) error
	})
	if !ok {
		return assistantdiscovery.ErrReviewRefused
	}
	return reader.ValidateResearchLookup(ctx, lookup)
}

// NewResearchReviewGate binds the gate to this handler's actual current-user,
// relationship, canonical Session and workspace owners. It loads no transcript,
// Profile or HQ memory bodies. No browser/model scope validator is accepted.
func (h *HomeAssistantAskHandler) NewResearchReviewGate() *assistantdiscovery.ReviewGate {
	return assistantdiscovery.NewReviewGate(assistantResearchScopeValidator{handler: h})
}

func (h *HomeAssistantAskHandler) researchRelationship(ctx context.Context) (*PersonalAssistantWorkContext, string, error) {
	user, err := h.currentAssistantUser(ctx)
	provider, ok := h.PersonalAssistantContext.(interface {
		ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error)
	})
	if err != nil || !ok || provider == nil || ctx.Err() != nil {
		return nil, "", assistantdiscovery.ErrReviewRefused
	}
	work, err := provider.ResolvePersonalAssistantRelationship(ctx, user)
	if err != nil || work == nil || !work.ReadyForWork() || work.HQWorkspaceID == "" || work.ConversationAgent == "" {
		return nil, "", assistantdiscovery.ErrReviewRefused
	}
	return work, user, nil
}

func researchConversationRevision(record PersonalAssistantConversationRecord) string {
	return assistantcontext.ConversationRevision(record.UpdatedAt, record.MessageCount, record.ContextEpoch)
}

func researchDigest(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

func (v assistantResearchScopeValidator) ValidateResearchScope(ctx context.Context, scope assistantdiscovery.Scope) error {
	h := v.handler
	if h == nil {
		return assistantdiscovery.ErrReviewRefused
	}
	work, user, err := h.researchRelationship(ctx)
	if err != nil || user != scope.Owner || work.HQWorkspaceID != scope.HQ || work.ConversationAgent != scope.Profile || work.StateVersion != scope.StateVersion {
		return assistantdiscovery.ErrReviewRefused
	}
	reader, ok := h.Conversations.(PersonalAssistantConversationOwnerReader)
	if !ok {
		return assistantdiscovery.ErrReviewRefused
	}
	owner := assistantcontext.SaveOwner{UserID: user, WorkspaceID: scope.HQ, AgentName: scope.Profile, StateVersion: scope.StateVersion}
	for _, id := range []string{scope.Location, scope.Subject} {
		if id != "" {
			owner.ContextWorkspaceIDs = append(owner.ContextWorkspaceIDs, id)
		}
	}
	record, err := reader.ReadConversationOwner(ctx, scope.Conversation, owner)
	if err != nil || record.ID != scope.Conversation || record.WorkspaceID != scope.HQ || !strings.EqualFold(record.AgentName, scope.Profile) || record.MessageCount < 2 || researchConversationRevision(record) != scope.ConversationRevision {
		return assistantdiscovery.ErrReviewRefused
	}
	for _, ref := range []struct {
		id      string
		version int64
	}{{scope.Location, scope.LocationVersion}, {scope.Subject, scope.SubjectVersion}} {
		if ref.id == "" {
			continue
		}
		if h.WorkspaceContext == nil || h.WorkspaceContext.Source == nil {
			return assistantdiscovery.ErrReviewRefused
		}
		ws, err := h.WorkspaceContext.Source.Get(ref.id)
		if err != nil || !workspaceReadable(ws, user) || ws.Version != ref.version {
			return assistantdiscovery.ErrReviewRefused
		}
	}
	digest, err := h.researchFolderDigest(ctx, scope.Conversation, owner, nil)
	if err != nil || digest != scope.FolderDigest {
		return assistantdiscovery.ErrReviewRefused
	}
	fresh, freshUser, err := h.researchRelationship(ctx)
	if err != nil || ctx.Err() != nil || freshUser != user || fresh.HQWorkspaceID != scope.HQ || fresh.ConversationAgent != scope.Profile || fresh.StateVersion != scope.StateVersion {
		return assistantdiscovery.ErrReviewRefused
	}
	return nil
}

// ResolveResearchScope derives fresh scope from accepted references, never from
// owner/state fields submitted by a browser. No latest-session inference or
// implicit HQ subject is possible. A failed/unpersisted first answer cannot
// authorize research because the canonical thread must already contain turns.
func (h *HomeAssistantAskHandler) ResolveResearchScope(ctx context.Context, conversation *HomeAssistantConversationRef, refs *HomeAssistantRouteContext, folderRefs ...*HomeAssistantFolderRef) (assistantdiscovery.Scope, error) {
	if conversation == nil || conversation.ID == "" || refs == nil || refs.Origin != "personal_assistant_panel" {
		return assistantdiscovery.Scope{}, assistantdiscovery.ErrReviewRefused
	}
	work, user, err := h.researchRelationship(ctx)
	if err != nil {
		return assistantdiscovery.Scope{}, err
	}
	turn := h.bindWorkspaceTurn(ctx, "", refs, work)
	if turn == nil || h.revalidateWorkspaceTurn(ctx, turn) != nil {
		return assistantdiscovery.Scope{}, assistantdiscovery.ErrReviewRefused
	}
	reader, ok := h.Conversations.(PersonalAssistantConversationOwnerReader)
	if !ok {
		return assistantdiscovery.Scope{}, assistantdiscovery.ErrReviewRefused
	}
	record, err := reader.ReadConversationOwner(ctx, conversation.ID, turn.saveOwner())
	if err != nil || record.ID != conversation.ID || record.MessageCount < 2 || record.WorkspaceID != work.HQWorkspaceID || !strings.EqualFold(record.AgentName, work.ConversationAgent) {
		return assistantdiscovery.Scope{}, assistantdiscovery.ErrReviewRefused
	}
	scope := assistantdiscovery.Scope{Owner: user, HQ: work.HQWorkspaceID, Profile: work.ConversationAgent, StateVersion: work.StateVersion, Conversation: conversation.ID, ConversationRevision: researchConversationRevision(record)}
	if ref := turn.projection.Location; ref != nil {
		scope.Location, scope.LocationVersion = ref.ID, ref.Version
	}
	if ref := turn.projection.Subject; ref != nil {
		scope.Subject, scope.SubjectVersion = ref.ID, ref.Version
	}
	var folderRef *HomeAssistantFolderRef
	if len(folderRefs) > 1 {
		return assistantdiscovery.Scope{}, assistantdiscovery.ErrReviewRefused
	}
	if len(folderRefs) == 1 {
		folderRef = folderRefs[0]
	}
	scope.FolderDigest, err = h.researchFolderDigest(ctx, conversation.ID, turn.saveOwner(), folderRef)
	if err != nil {
		return assistantdiscovery.Scope{}, err
	}
	// Bind only accepted context/reference data, not overview bodies/timestamps.
	data, err := json.Marshal(struct {
		Scope assistantdiscovery.Scope
		Refs  HomeAssistantRouteContext
	}{scope, *refs})
	if err != nil {
		return assistantdiscovery.Scope{}, assistantdiscovery.ErrReviewRefused
	}
	scope.ContextDigest = researchDigest(string(data))
	if err := (assistantResearchScopeValidator{h}).ValidateResearchScope(ctx, scope); err != nil {
		return assistantdiscovery.Scope{}, err
	}
	return scope, nil
}

type assistantResearchReviewRequest struct {
	Conversation  *HomeAssistantConversationRef `json:"conversation"`
	Context       *HomeAssistantRouteContext    `json:"context"`
	Lookup        assistantdiscovery.Lookup     `json:"lookup"`
	FolderContext *HomeAssistantFolderRef       `json:"folder_context,omitempty"`
}

func strictResearchBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid bounded research request", nil)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid bounded research request", nil)
		return false
	}
	return true
}

// ResearchReviewHandler prepares only a user-visible offer. It performs no
// lookup, provider call, cache refresh, transcript write, install or grant.
// Registration follows receipt/model integration; no premature external API is
// enabled by merely constructing the review gate.
func (h *HomeAssistantAskHandler) ResearchReviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var request assistantResearchReviewRequest
	if !strictResearchBody(w, r, &request) {
		return
	}
	scope, err := h.ResolveResearchScope(r.Context(), request.Conversation, request.Context, request.FolderContext)
	if err != nil || h.ResearchReviews == nil {
		orihttp.RespondErrorWithErr(w, http.StatusConflict, "Research review is unavailable or its conversation/context changed. Nothing was sent.", nil)
		return
	}
	lookup, err := resolveResearchProposal(r.Context(), h, request.Lookup)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusConflict, "That public lookup cannot be reviewed. Nothing was sent.", nil)
		return
	}
	review, err := h.ResearchReviews.Prepare(r.Context(), scope, lookup)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusConflict, "That public lookup cannot be reviewed. Nothing was sent.", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	orihttp.WriteJSON(w, map[string]any{"review": review})
}
