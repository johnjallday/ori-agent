package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// SecondBrainService is the "connect your second brain" boundary. A folder is
// named by a detected vault's id or by a selection token the folder-picker
// endpoint issued; no handler here accepts a path (FR41).
type SecondBrainService interface {
	Status(ctx context.Context, userID string) (personalassistant.SecondBrainView, error)
	MarkPrompted(ctx context.Context, userID, requestID string) (personalassistant.SecondBrainView, error)
	DecideAsk(ctx context.Context, userID, decision, requestID string) (personalassistant.SecondBrainView, error)
	Connect(ctx context.Context, userID string, input personalassistant.SecondBrainConnectInput) (personalassistant.SecondBrainView, error)
	Create(ctx context.Context, userID string, input personalassistant.SecondBrainCreateInput) (personalassistant.SecondBrainView, error)
	Capture(ctx context.Context, userID, text, requestID string) (personalassistant.SecondBrainCaptureView, error)
	Look(ctx context.Context, userID string) (personalassistant.SecondBrainLookView, error)
	LookAgain(ctx context.Context, userID, requestID string) (personalassistant.SecondBrainLookView, error)
	AcceptFollowUps(ctx context.Context, userID, lookID string, input personalassistant.SecondBrainAcceptInput) (personalassistant.SecondBrainAcceptView, error)
	DismissLook(ctx context.Context, userID, lookID, requestID string) (personalassistant.SecondBrainLookView, error)
	ActOnItem(ctx context.Context, userID, lookID, itemID, action, requestID string) (personalassistant.SecondBrainLookView, error)
	Inbox(ctx context.Context, userID string) (personalassistant.SecondBrainInboxView, error)
	Disconnect(ctx context.Context, userID, requestID string, ifVersion int64) (personalassistant.SecondBrainView, error)
	FillInDecision(ctx context.Context, userID, decision, requestID string) (personalassistant.SecondBrainView, error)
	WriteProject(ctx context.Context, userID string, input personalassistant.SecondBrainProjectInput) (personalassistant.SecondBrainProjectNote, error)
	SkipProject(ctx context.Context, userID, requestID string) (personalassistant.SecondBrainView, error)
	DismissWeek(ctx context.Context, userID, requestID string) (personalassistant.SecondBrainView, error)
}

// SetSecondBrain adds the second-brain boundary. origin refuses a request that
// did not come from Ori's own page; every route checks it before anything
// else, reads included, since a status names the folder.
func (h *Handler) SetSecondBrain(service SecondBrainService, origin func(*http.Request) error) {
	if h != nil {
		h.secondBrain, h.secondBrainOrigin = service, origin
	}
}

const (
	// maxSecondBrainBodyBytes bounds every second-brain body but one.
	maxSecondBrainBodyBytes = 4 * 1024
	// maxSecondBrainItemsBodyBytes fits every item id a look keeps.
	maxSecondBrainItemsBodyBytes = 64 * 1024
	// maxSecondBrainCaptureBodyBytes fits 4,000 characters of any script, with
	// the JSON around them.
	maxSecondBrainCaptureBodyBytes = 24 * 1024
)

// secondBrainReady answers for an unavailable service or a foreign origin.
func (h *Handler) secondBrainReady(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.secondBrain == nil || h.secondBrainOrigin == nil {
		orihttp.ServiceUnavailable(w, "Notes are unavailable")
		return "", false
	}
	if err := h.secondBrainOrigin(r); err != nil {
		orihttp.Forbidden(w, "This request did not come from Ori's own page")
		return "", false
	}
	return h.currentUserID(w, r)
}

// GetSecondBrain returns the connection, the detected vaults and the ask.
func (h *Handler) GetSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	view, err := h.secondBrain.Status(r.Context(), userID)
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

type secondBrainRequestOnly struct {
	RequestID string `json:"request_id"`
}

// PromptedSecondBrain records that the ask was shown.
func (h *Handler) PromptedSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainRequestOnly
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id") {
		return
	}
	view, err := h.secondBrain.MarkPrompted(r.Context(), userID, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

type secondBrainAskRequest struct {
	Decision  string `json:"decision"`
	RequestID string `json:"request_id"`
}

// AskSecondBrain records Not now ("later") or I don't keep notes ("none").
func (h *Handler) AskSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainAskRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "decision", "request_id") {
		return
	}
	view, err := h.secondBrain.DecideAsk(r.Context(), userID, strings.TrimSpace(req.Decision), strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

type secondBrainFillInRequest struct {
	Decision  string `json:"decision"`
	RequestID string `json:"request_id"`
}

type secondBrainProjectRequest struct {
	Name      string `json:"name"`
	Answer    string `json:"answer"`
	RequestID string `json:"request_id"`
}

// ProjectSecondBrain handles POST …/second-brain/project: the drawer's answer to
// "What's the main thing you're working on right now?" becomes
// Projects/<name>/README.md. It names a project, never a folder: the body has a
// name and the user's own words, and the note's place is Ori's to choose.
func (h *Handler) ProjectSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainProjectRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "name", "answer", "request_id") {
		return
	}
	note, err := h.secondBrain.WriteProject(r.Context(), userID, personalassistant.SecondBrainProjectInput{
		Name: req.Name, Answer: req.Answer, RequestID: strings.TrimSpace(req.RequestID),
	})
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"project": note})
}

// SkipProjectSecondBrain handles POST …/second-brain/project/skip: the drawer
// stops asking, and the Profile page's interview is untouched.
func (h *Handler) SkipProjectSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainRequestOnly
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id") {
		return
	}
	view, err := h.secondBrain.SkipProject(r.Context(), userID, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

// DismissWeekSecondBrain handles POST …/second-brain/week/dismiss: the first-week
// recap row's one control. The row is shown once; this records that it was put
// away.
func (h *Handler) DismissWeekSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainRequestOnly
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id") {
		return
	}
	view, err := h.secondBrain.DismissWeek(r.Context(), userID, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

// FillInSecondBrain records the answer to the consent card: "allow" or "later".
func (h *Handler) FillInSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainFillInRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "decision", "request_id") {
		return
	}
	view, err := h.secondBrain.FillInDecision(r.Context(), userID, strings.TrimSpace(req.Decision), strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

type secondBrainConnectRequest struct {
	DetectedID     string `json:"detected_id"`
	SelectionToken string `json:"selection_token"`
	RequestID      string `json:"request_id"`
	Replace        bool   `json:"replace"`
}

// ConnectSecondBrain connects a detected vault or a picked folder and starts
// the first look.
func (h *Handler) ConnectSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainConnectRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "detected_id", "selection_token", "request_id", "replace") {
		return
	}
	req.DetectedID, req.SelectionToken = strings.TrimSpace(req.DetectedID), strings.TrimSpace(req.SelectionToken)
	if (req.DetectedID == "") == (req.SelectionToken == "") {
		orihttp.BadRequest(w, "Choose one detected vault or one picked folder")
		return
	}
	view, err := h.secondBrain.Connect(r.Context(), userID, personalassistant.SecondBrainConnectInput{
		DetectedID: req.DetectedID, SelectionToken: req.SelectionToken, RequestID: strings.TrimSpace(req.RequestID), Replace: req.Replace,
	})
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view, "look_id": view.LookID})
}

type secondBrainCreateRequest struct {
	// SelectionToken names the parent the folder picker returned; Name is the
	// new folder's own name, one path segment the service validates. Without a
	// token the folder goes in the default place under its default name.
	SelectionToken string `json:"selection_token"`
	Name           string `json:"name"`
	RequestID      string `json:"request_id"`
}

// CreateSecondBrain sets up a second brain inside the picked parent or the
// default place, connects it as created by Ori, and starts the first look.
func (h *Handler) CreateSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainCreateRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "selection_token", "name", "request_id") {
		return
	}
	req.SelectionToken = strings.TrimSpace(req.SelectionToken)
	view, err := h.secondBrain.Create(r.Context(), userID, personalassistant.SecondBrainCreateInput{
		SelectionToken: req.SelectionToken, Name: strings.TrimSpace(req.Name), RequestID: strings.TrimSpace(req.RequestID),
	})
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view, "look_id": view.LookID})
}

type secondBrainCaptureRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"request_id"`
}

// CaptureSecondBrain writes one note to Inbox in a second brain Ori set up,
// for the Notes station's capture box. The chat tool calls the service
// directly.
func (h *Handler) CaptureSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainCaptureRequest
	if !decodeClosedBody(w, r, maxSecondBrainCaptureBodyBytes, &req, "text", "request_id") {
		return
	}
	view, err := h.secondBrain.Capture(r.Context(), userID, req.Text, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"capture": view})
}

// GetSecondBrainLook reports the current look.
func (h *Handler) GetSecondBrainLook(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	view, err := h.secondBrain.Look(r.Context(), userID)
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"look": view})
}

// LookSecondBrainAgain starts Look again.
func (h *Handler) LookSecondBrainAgain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainRequestOnly
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id") {
		return
	}
	view, err := h.secondBrain.LookAgain(r.Context(), userID, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"look": view})
}

type secondBrainFollowUpsRequest struct {
	// Items is the string "suggested" or a list of item ids.
	Items     json.RawMessage `json:"items"`
	RequestID string          `json:"request_id"`
}

// AcceptSecondBrainFollowUps adds a look's items as HQ follow-ups.
func (h *Handler) AcceptSecondBrainFollowUps(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	lookID := strings.TrimSpace(r.PathValue("lookID"))
	if lookID == "" {
		orihttp.BadRequest(w, "Look id is required")
		return
	}
	var req secondBrainFollowUpsRequest
	if !decodeClosedBody(w, r, maxSecondBrainItemsBodyBytes, &req, "items", "request_id") {
		return
	}
	input := personalassistant.SecondBrainAcceptInput{RequestID: strings.TrimSpace(req.RequestID)}
	var word string
	if err := json.Unmarshal(req.Items, &word); err == nil {
		if word != "suggested" {
			orihttp.BadRequest(w, `Items must be "suggested" or a list of item ids`)
			return
		}
		input.Suggested = true
	} else if err := json.Unmarshal(req.Items, &input.ItemIDs); err != nil || len(input.ItemIDs) == 0 || len(input.ItemIDs) > secondbrain.MaxLookItems {
		orihttp.BadRequest(w, `Items must be "suggested" or a list of item ids`)
		return
	}
	view, err := h.secondBrain.AcceptFollowUps(r.Context(), userID, lookID, input)
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"follow_ups": view})
}

// DismissSecondBrainLook takes a finished result off Needs you.
func (h *Handler) DismissSecondBrainLook(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	lookID := strings.TrimSpace(r.PathValue("lookID"))
	if lookID == "" {
		orihttp.BadRequest(w, "Look id is required")
		return
	}
	var req secondBrainRequestOnly
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id") {
		return
	}
	view, err := h.secondBrain.DismissLook(r.Context(), userID, lookID, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"look": view})
}

type secondBrainItemRequest struct {
	Action    string `json:"action"`
	RequestID string `json:"request_id"`
}

// ActOnSecondBrainItem makes one open item a follow-up, marks it done, or
// dismisses it until the next look. It never edits a note.
func (h *Handler) ActOnSecondBrainItem(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	lookID, itemID := strings.TrimSpace(r.PathValue("lookID")), strings.TrimSpace(r.PathValue("itemID"))
	if lookID == "" || itemID == "" {
		orihttp.BadRequest(w, "Look id and item id are required")
		return
	}
	var req secondBrainItemRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "action", "request_id") {
		return
	}
	view, err := h.secondBrain.ActOnItem(r.Context(), userID, lookID, itemID, strings.TrimSpace(req.Action), strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"look": view})
}

// GetSecondBrainInbox lists the notes directly under Inbox in a second brain Ori
// set up. It reads on request only.
func (h *Handler) GetSecondBrainInbox(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	view, err := h.secondBrain.Inbox(r.Context(), userID)
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"inbox": view})
}

type secondBrainDisconnectRequest struct {
	RequestID string `json:"request_id"`
	IfVersion *int64 `json:"if_version"`
}

// DisconnectSecondBrain removes the connection; the follow-ups stay.
func (h *Handler) DisconnectSecondBrain(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.secondBrainReady(w, r)
	if !ok {
		return
	}
	var req secondBrainDisconnectRequest
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id", "if_version") {
		return
	}
	if req.IfVersion == nil {
		orihttp.BadRequest(w, "if_version is required")
		return
	}
	view, err := h.secondBrain.Disconnect(r.Context(), userID, strings.TrimSpace(req.RequestID), *req.IfVersion)
	if err != nil {
		writeSecondBrainError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"second_brain": view})
}

func writeSecondBrainError(w http.ResponseWriter, err error) {
	var folderErr *personalassistant.SecondBrainFolderError
	var tooSoon *personalassistant.SecondBrainTooSoonError
	switch {
	case errors.As(err, &folderErr):
		_ = orihttp.RespondJSON(w, http.StatusBadRequest, map[string]any{"error": folderErr.Message, "repair": "choose_folder", "no_notes": folderErr.NoNotes})
	case errors.As(err, &tooSoon):
		seconds := int(math.Ceil(tooSoon.RetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		_ = orihttp.RespondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Ori looked a moment ago. Look again in a few minutes.", "retry_after_seconds": seconds})
	case errors.Is(err, secondbrain.ErrReplaceNeedsConfirm):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "Another notes folder is connected. Confirm to replace it.", "needs_confirm": true})
	case errors.Is(err, secondbrain.ErrRequestReused):
		orihttp.Conflict(w, "That request id was already used for something else")
	case errors.Is(err, personalassistant.ErrSecondBrainUnknownVault):
		orihttp.NotFound(w, "That vault is no longer in Obsidian's list. Choose it again")
	case errors.Is(err, personalassistant.ErrSecondBrainSelection):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "Ori no longer has that folder open. Pick it again", "needs_pick": true, "needs_pick_reason": personalassistant.FolderPickReason(err)})
	case errors.Is(err, personalassistant.ErrSecondBrainNotConnected):
		orihttp.Conflict(w, "No notes folder is connected")
	case errors.Is(err, secondbrain.ErrNotOwned):
		orihttp.Conflict(w, "Ori only writes notes to a second brain it set up")
	case errors.Is(err, secondbrain.ErrCaptureSecretLike):
		orihttp.BadRequest(w, "That looks like a secret, so Ori did not save it")
	case errors.Is(err, secondbrain.ErrCaptureInvalid):
		orihttp.BadRequest(w, "A note is 1 to 4,000 characters")
	case errors.Is(err, secondbrain.ErrOutsideOwnedFolders), errors.Is(err, secondbrain.ErrUserModified),
		errors.Is(err, secondbrain.ErrNoteTooLarge), errors.Is(err, secondbrain.ErrNoteExists):
		orihttp.Conflict(w, "Ori could not write that note in your notes folder")
	case errors.Is(err, personalassistant.ErrSecondBrainAlreadyConnected):
		orihttp.Conflict(w, "A notes folder is already connected. Disconnect it before setting one up")
	case errors.Is(err, personalassistant.ErrSecondBrainLookNotFound):
		orihttp.NotFound(w, "That look is no longer here")
	case errors.Is(err, personalassistant.ErrSecondBrainItemNotFound):
		orihttp.NotFound(w, "That item is no longer here")
	case errors.Is(err, personalassistant.ErrSecondBrainNoInbox):
		orihttp.NotFound(w, "Only a second brain Ori set up has an Inbox here")
	case errors.Is(err, personalassistant.ErrSecondBrainProjectName):
		orihttp.BadRequest(w, "Choose a simple project name, without slashes.")
	case errors.Is(err, personalassistant.ErrSecondBrainFillInNotAllowed):
		orihttp.Conflict(w, "Let Ori keep project notes in your second brain first")
	case errors.Is(err, personalassistant.ErrSecondBrainSeededItem):
		orihttp.Conflict(w, "That item came from a follow-up you already have")
	case errors.Is(err, personalassistant.ErrValidation):
		orihttp.BadRequest(w, "The request is not valid")
	case errors.Is(err, personalassistant.ErrNeedsHQ):
		orihttp.Conflict(w, "Build Personal HQ before connecting your notes")
	case errors.Is(err, personalassistant.ErrRepairNeeded), errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalassistant.ErrNotFound):
		orihttp.Conflict(w, "The assistant relationship changed. Refresh and try again")
	default:
		orihttp.ServiceUnavailable(w, "Notes are temporarily unavailable")
	}
}
