package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const PersonalAssistantFolderConflict = "folder_context_conflict"
const PersonalAssistantFolderUnavailable = "folder_context_unavailable"

var ErrPersonalAssistantFolderConflict = errors.New("conversation folder revision changed")

// HomeAssistantFolderRef contains references only. Observation text and paths
// are never accepted, even by the legacy permissive Ask JSON decoder.
type HomeAssistantFolderRef struct {
	SelectionID string `json:"selection_id,omitempty"`
	Revision    string `json:"revision,omitempty"`
	DraftID     string `json:"draft_id,omitempty"`
	Historical  bool   `json:"historical,omitempty"`
}

func (ref *HomeAssistantFolderRef) UnmarshalJSON(data []byte) error {
	type fields HomeAssistantFolderRef
	var decoded fields
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if len(decoded.SelectionID) > 96 || len(decoded.Revision) > 96 || len(decoded.DraftID) > 96 {
		return foldercontext.ErrInvalid
	}
	*ref = HomeAssistantFolderRef(decoded)
	return nil
}

type PersonalAssistantFolderState struct {
	Revision    string                                     `json:"revision"`
	Observation *foldercontext.Observation                 `json:"observation,omitempty"`
	Authority   personalassistant.FolderContinuationReason `json:"authority,omitempty"`
	OfferID     string                                     `json:"offer_id,omitempty"`
	Historical  bool                                       `json:"historical,omitempty"`
}

// PersonalAssistantFolderObservations is the host-owned local observation seam.
// Production uses personalassistant.FolderObservationService; provider and setup
// access are deliberately absent from the selection interface.
type PersonalAssistantFolderObservations interface {
	Choices(context.Context, string) (personalassistant.FolderDigestView, error)
	Observe(context.Context, foldercontext.Target, string, string) (*foldercontext.Observation, error)
	Resolve(context.Context, foldercontext.Target, string) (*foldercontext.Observation, error)
	Status(context.Context, foldercontext.Target, foldercontext.Observation) personalassistant.FolderContinuationReason
	BindSaved(foldercontext.Target, string, string)
}

// PersonalAssistantFolderConversationStore is an optional extension of the
// canonical adapter, never a parallel state store. Read bypasses the LRU.
type PersonalAssistantFolderConversationStore interface {
	ReadFolderConversation(context.Context, string) (PersonalAssistantConversationRecord, []PersonalAssistantConversationMessage, error)
	AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]PersonalAssistantConversationMessage, error)
}

// folderRequestGate refuses overlapping operations rather than waiting behind
// a provider or picker. Entries exist only for the duration of an operation.
type folderRequestGate struct{ active sync.Map }

func (g *folderRequestGate) enter(target foldercontext.Target) (func(), bool) {
	key := target
	if _, busy := g.active.LoadOrStore(key, true); busy {
		return func() {}, false
	}
	return func() { g.active.Delete(key) }, true
}

func (h *HomeAssistantAskHandler) folderStore() PersonalAssistantFolderConversationStore {
	store, _ := h.Conversations.(PersonalAssistantFolderConversationStore)
	return store
}

func (h *HomeAssistantAskHandler) folderTarget(scope personalAssistantConversationScope, conversationID, draftID string) (foldercontext.Target, error) {
	userID := strings.TrimSpace(h.UserID)
	if userID == "" {
		userID = "local"
	}
	target := foldercontext.Target{UserID: userID, WorkspaceID: scope.workspaceID, AgentName: strings.ToLower(scope.agentName), ConversationID: conversationID, DraftID: draftID}
	if !target.Valid() {
		return target, foldercontext.ErrInvalid
	}
	if draftID != "" {
		if _, err := uuid.Parse(draftID); err != nil {
			return target, foldercontext.ErrInvalid
		}
	}
	if len(conversationID) > 96 {
		return target, foldercontext.ErrInvalid
	}
	return target, nil
}

// folderState reads the latest locally authored typed event. Text and imported
// messages cannot set a revision or become active evidence.
func (h *HomeAssistantAskHandler) folderState(ctx context.Context, target foldercontext.Target) (PersonalAssistantFolderState, error) {
	state := PersonalAssistantFolderState{}
	if target.ConversationID == "" {
		return state, nil
	}
	store := h.folderStore()
	if store == nil {
		return state, foldercontext.ErrInvalid
	}
	record, messages, err := store.ReadFolderConversation(ctx, target.ConversationID)
	if err != nil {
		return state, err
	}
	scope := personalAssistantConversationScope{workspaceID: target.WorkspaceID, agentName: target.AgentName}
	if !scope.owns(record) {
		return state, foldercontext.ErrInvalid
	}
	return folderStateFromMessages(messages), nil
}

func folderStateFromMessages(messages []PersonalAssistantConversationMessage) PersonalAssistantFolderState {
	state := PersonalAssistantFolderState{}
	for _, message := range messages {
		if message.Imported || message.FolderContext == nil {
			continue
		}
		state = PersonalAssistantFolderState{Revision: message.ID, Observation: message.FolderContext.Observation, OfferID: message.FolderContext.OfferID}
	}
	return state
}

func (h *HomeAssistantAskHandler) folderExpected(ctx context.Context, target foldercontext.Target, revision string) (PersonalAssistantFolderState, error) {
	state, err := h.folderState(ctx, target)
	if err != nil {
		return state, err
	}
	if state.Revision != revision {
		return state, ErrPersonalAssistantFolderConflict
	}
	return state, nil
}

func strictFolderBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeConversationError(w, http.StatusBadRequest, "invalid_folder_request", "Choose a folder using the folder controls; paths and observations are not accepted.")
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeConversationError(w, http.StatusBadRequest, "invalid_folder_request", "Send one folder request at a time.")
		return false
	}
	return true
}

func writeFolderError(w http.ResponseWriter, err error) {
	code, message := PersonalAssistantFolderUnavailable, "That folder selection is unavailable. Pick again; your message and previous folder have not been replaced."
	if errors.Is(err, ErrPersonalAssistantFolderConflict) {
		code, message = PersonalAssistantFolderConflict, "This conversation changed in another request. Reopen it before changing or sending folder context. Your draft is still here."
	} else if errors.Is(err, personalassistant.ErrFolderScanBusy) {
		message = "A folder selection or turn is still running. Wait for it to finish."
	} else if errors.Is(err, personalassistant.ErrFolderPickerUnavailable) {
		message = "The native folder picker is unavailable. Choose an available known folder, or keep chatting without one."
	} else {
		var rootError *personalassistant.FolderRootError
		if errors.As(err, &rootError) {
			message = rootError.Message
		}
	}
	writeConversationError(w, http.StatusConflict, code, message)
}

// FolderChoicesHandler reveals available approved choices, not an existing
// global offer and not a scan. GET /api/home-assistant/folder-context/choices
func (h *HomeAssistantAskHandler) FolderChoicesHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if h.FolderObservations == nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	target, err := h.folderTarget(scope, "", uuid.NewString())
	if err != nil {
		writeFolderError(w, err)
		return
	}
	choices, err := h.FolderObservations.Choices(r.Context(), target.UserID)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	orihttp.WriteJSON(w, choices)
}

type folderSelectionRequest struct {
	ConversationID string `json:"conversation_id,omitempty"`
	DraftID        string `json:"draft_id,omitempty"`
	Revision       string `json:"revision,omitempty"`
	Mode           string `json:"mode"`
	Chip           string `json:"chip,omitempty"`
}

// SelectFolderContextHandler stages local evidence only. No conversation is
// created until Send succeeds, and no shared setup offer is changed.
func (h *HomeAssistantAskHandler) SelectFolderContextHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if h.FolderObservations == nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	var req folderSelectionRequest
	if !strictFolderBody(w, r, &req) {
		return
	}
	target, err := h.folderTarget(scope, req.ConversationID, req.DraftID)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	release, ok := h.folderRequests.enter(target)
	if !ok {
		writeFolderError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	if _, err := h.folderExpected(r.Context(), target, req.Revision); err != nil {
		writeFolderError(w, err)
		return
	}
	observation, err := h.FolderObservations.Observe(r.Context(), target, req.Mode, req.Chip)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	// A scan/picker can outlive deletion or a relationship change. Never publish
	// the result merely because the old request was valid when it started.
	freshScope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if freshScope != scope {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	current, err := h.folderExpected(r.Context(), target, req.Revision)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	// A valid replacement immediately retires the previous active binding and
	// its unconfirmed setup reviews. The new metadata remains a local preview
	// until Send; cancellation/failure never reaches this write.
	revision := req.Revision
	if observation != nil && current.Observation != nil {
		rows, err := h.folderStore().AppendFolderTurn(r.Context(), target.ConversationID, target.WorkspaceID, target.AgentName, req.Revision, foldercontext.Event{Version: 1}, "", "")
		if err != nil {
			writeFolderError(w, err)
			return
		}
		revision = rows[0].ID
	}
	orihttp.WriteJSON(w, map[string]any{"observation": observation, "revision": revision, "cancelled": observation == nil})
}

// DetachFolderContextHandler changes only the active binding; it does not write
// a No-thanks preference, undo an outcome or delete earlier discussion.
func (h *HomeAssistantAskHandler) DetachFolderContextHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	var req struct {
		ConversationID string `json:"conversation_id"`
		Revision       string `json:"revision"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	target, err := h.folderTarget(scope, req.ConversationID, "")
	if err != nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	release, ok := h.folderRequests.enter(target)
	if !ok {
		writeFolderError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	if _, err := h.folderExpected(r.Context(), target, req.Revision); err != nil {
		writeFolderError(w, err)
		return
	}
	rows, err := h.folderStore().AppendFolderTurn(r.Context(), target.ConversationID, target.WorkspaceID, target.AgentName, req.Revision, foldercontext.Event{Version: 1}, "", "")
	if err != nil {
		writeFolderError(w, err)
		return
	}
	orihttp.WriteJSON(w, PersonalAssistantFolderState{Revision: rows[0].ID})
}
