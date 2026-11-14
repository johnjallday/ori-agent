// Package folderagenthttp serves "Hire an agent for <folder>": choosing the
// folder, Start, the report and its actions. No request carries a path: the
// folder is chosen on the server (dialog, known folder, or a typed path's
// selection token) and Start names only the server's choice token.
package folderagenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const maxBodyBytes = 8 << 10

const unavailableSentence = "Starting an agent on a folder is unavailable"

// fail writes {"error": sentence}: the card shows the sentence as it is.
func fail(w http.ResponseWriter, status int, sentence string) {
	_ = orihttp.RespondError(w, status, sentence)
}

// Handler serves the folder agent endpoints.
type Handler struct {
	service     *folderagent.Service
	currentUser func(ctx context.Context) (string, error)
}

// New builds the handler.
func New(service *folderagent.Service, currentUser func(ctx context.Context) (string, error)) *Handler {
	return &Handler{service: service, currentUser: currentUser}
}

func (h *Handler) user(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil || h.currentUser == nil {
		fail(w, http.StatusServiceUnavailable, unavailableSentence)
		return "", false
	}
	userID, err := h.currentUser(r.Context())
	if err != nil || strings.TrimSpace(userID) == "" {
		fail(w, http.StatusInternalServerError, "Failed to resolve current user")
		return "", false
	}
	return userID, true
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		fail(w, http.StatusBadRequest, "Missing request")
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil && !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "The request is not valid")
		return false
	}
	return true
}

type chooseRequest struct {
	Chip           string `json:"chip"`
	Picker         bool   `json:"picker"`
	SelectionToken string `json:"selection_token"`
	// FromWorkspaceID names the workspace page the folder is fed from; the
	// server decides whether it is one of the owner's Homes.
	FromWorkspaceID string `json:"from_workspace_id"`
}

// Choose resolves the folder and returns the card ({chip}, {picker:true} or
// {selection_token}); a path is refused as an unknown field.
func (h *Handler) Choose(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req chooseRequest
	if !decode(w, r, &req) {
		return
	}
	if !req.Picker && strings.TrimSpace(req.Chip) == "" && strings.TrimSpace(req.SelectionToken) == "" {
		fail(w, http.StatusBadRequest, "Choose a folder")
		return
	}
	chosen, err := h.service.ChooseFrom(r.Context(), userID, personalassistant.FolderChoice{
		Chip: req.Chip, Picker: req.Picker, SelectionToken: req.SelectionToken,
	}, req.FromWorkspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	if chosen == nil {
		orihttp.Success(w, map[string]any{"cancelled": true})
		return
	}
	orihttp.Success(w, map[string]any{"card": chosen})
}

type projectHolderRequest struct {
	ReportKey   string `json:"report_key"`
	FolderIndex *int   `json:"folder_index"`
}

// ProjectHolder accepts only a saved walk position, never a relative or absolute
// path. The service checks ownership and that exact report before issuing a card.
func (h *Handler) ProjectHolder(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req projectHolderRequest
	if !decode(w, r, &req) {
		return
	}
	if req.FolderIndex == nil || *req.FolderIndex < 0 || req.ReportKey == "" {
		fail(w, http.StatusBadRequest, "Choose a project folder from this report")
		return
	}
	card, err := h.service.ChooseProjectHolder(r.Context(), userID, r.PathValue("workspaceID"), req.ReportKey, *req.FolderIndex)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"card": card})
}

type startRequest struct {
	ChoiceToken string       `json:"choice_token"`
	Brief       string       `json:"brief"`
	Hire        *hireRequest `json:"hire"`
}

// hireRequest is the card's Hire section: {existing} reuses one of the owner's
// agents; otherwise a new agent with {name, provider, model, mandate, character}.
type hireRequest struct {
	Existing  string `json:"existing"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Mandate   string `json:"mandate"`
	Character string `json:"character"`
}

// Start hires the agent the card names, creates the workspace with it and its
// first ticket, and starts the walk.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req startRequest
	if !decode(w, r, &req) {
		return
	}
	var hire folderagent.Hire
	if req.Hire != nil {
		hire = folderagent.Hire{Existing: req.Hire.Existing, Name: req.Hire.Name, Provider: req.Hire.Provider,
			Model: req.Hire.Model, Mandate: req.Hire.Mandate, Character: req.Hire.Character}
	}
	view, err := h.service.Start(r.Context(), userID, req.ChoiceToken, req.Brief, hire)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusCreated, map[string]any{"success": true, "folder_agent": view})
}

// List returns every folder agent the user has (the map's tile lines).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	views := h.service.List(r.Context(), userID)
	if views == nil {
		views = []folderagent.View{}
	}
	orihttp.Success(w, map[string]any{"folder_agents": views})
}

// Get returns one workspace's report.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	view, err := h.service.View(r.Context(), userID, r.PathValue("workspaceID"))
	if errors.Is(err, folderagent.ErrNoRecord) {
		// Any workspace page asks; one not started from a folder simply
		// has no report.
		orihttp.Success(w, map[string]any{"folder_agent": nil})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

// Keep puts the report aside.
func (h *Handler) Keep(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	view, err := h.service.Keep(r.Context(), userID, r.PathValue("workspaceID"))
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

type lookFurtherRequest struct {
	Folders []string `json:"folders"`
	// Hint is the owner's typed answer to the agent's question.
	Hint string `json:"hint"`
}

// LookFurther continues the walk, the named folders (from the report) first.
func (h *Handler) LookFurther(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req lookFurtherRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := h.service.LookFurther(r.Context(), userID, r.PathValue("workspaceID"), req.Folders, req.Hint)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

type setUpRequest struct {
	PlanDigest string   `json:"plan_digest"`
	Selected   []string `json:"selected"`
	// UseGoal and Goal are what an older card sent with Set it up. They are
	// accepted and ignored: Set it up writes no goal; the goal is offered
	// once the run is done (Goal).
	UseGoal bool   `json:"use_goal"`
	Goal    string `json:"goal"`
}

// SetUp confirms the plan the card showed and runs it.
func (h *Handler) SetUp(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req setUpRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := h.service.SetUp(r.Context(), userID, r.PathValue("workspaceID"), setUpOf(req))
	if err != nil {
		if errors.Is(err, personalassistant.ErrFolderPlanChanged) {
			current, _ := h.service.View(r.Context(), userID, r.PathValue("workspaceID"))
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error": "The plan changed since it was shown. Review it again.", "plan_changed": true, "folder_agent": current,
			})
			return
		}
		if errors.Is(err, folderagent.ErrPremiseOwed) {
			current, _ := h.service.View(r.Context(), userID, r.PathValue("workspaceID"))
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error": "Confirm what this folder is first.", "code": "premise_owed", "folder_agent": current,
			})
			return
		}
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

func setUpOf(req setUpRequest) folderagent.SetUpRequest {
	return folderagent.SetUpRequest{PlanDigest: req.PlanDigest, Selected: req.Selected}
}

type premiseRequest struct {
	// Answer is "confirmed" (Manage them as …) or "something_else".
	Answer string `json:"answer"`
}

// Premise records the owner's answer to "This looks like a <kind> folder".
func (h *Handler) Premise(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req premiseRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := h.service.AnswerPremise(r.Context(), userID, r.PathValue("workspaceID"), req.Answer)
	if folderagent.ErrPremiseAnswer(err) {
		fail(w, http.StatusBadRequest, "Answer confirmed or something_else")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

type goalRequest struct {
	// Answer is "use" (Use this goal, or Save after Edit) or "dismiss" (Not
	// now). Goal is the edited sentence; empty keeps the suggested one.
	Answer string `json:"answer"`
	Goal   string `json:"goal"`
}

// Goal answers the suggested-goal offer the done run shows.
func (h *Handler) Goal(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req goalRequest
	if !decode(w, r, &req) {
		return
	}
	var edited *string
	switch req.Answer {
	case "use":
		if strings.TrimSpace(req.Goal) != "" {
			goal := req.Goal
			edited = &goal
		}
	case "dismiss":
	default:
		fail(w, http.StatusBadRequest, "Answer use or dismiss")
		return
	}
	view, err := h.service.AnswerGoal(r.Context(), userID, r.PathValue("workspaceID"), req.Answer == "use", edited)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

type songsRequest struct {
	// Answer is "learn" (Learn them), "dismiss" (Not now) or "retry" (Try again
	// on a reading that stopped; the owner already said yes).
	Answer string `json:"answer"`
}

// Songs answers the offer to learn the songs the done run makes. Learn them
// grants the Home's song-details consent and reads the folder's songs in the
// background; Not now reads nothing. Nothing is read before this call.
func (h *Handler) Songs(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req songsRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Answer != "learn" && req.Answer != "dismiss" && req.Answer != "retry" {
		fail(w, http.StatusBadRequest, "Answer learn, dismiss or retry")
		return
	}
	var view folderagent.View
	var err error
	if req.Answer == "retry" {
		view, err = h.service.RetryLearning(r.Context(), userID, r.PathValue("workspaceID"))
	} else {
		view, err = h.service.AnswerSongs(r.Context(), userID, r.PathValue("workspaceID"), req.Answer == "learn")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

type planRequest struct {
	Selected []string `json:"selected"`
}

// Plan returns the plan card for the optional candidates the owner left
// selected, with the digest Set it up must confirm.
func (h *Handler) Plan(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req planRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Selected == nil {
		req.Selected = []string{}
	}
	plan, err := h.service.Plan(userID, r.PathValue("workspaceID"), req.Selected)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"plan": plan})
}

// StartNext starts the agent's first ticket from the receipt (Start this).
func (h *Handler) StartNext(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	view, err := h.service.StartNext(r.Context(), userID, r.PathValue("workspaceID"))
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_agent": view})
}

type chatSeenRequest struct {
	MessageID string `json:"message_id"`
}

// ChatSeen marks the report's message in the Commander's conversation seen,
// once the drawer has shown it (workspace chat sync).
func (h *Handler) ChatSeen(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	var req chatSeenRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MessageID) == "" {
		fail(w, http.StatusBadRequest, "The request is not valid")
		return
	}
	if err := h.service.ChatSeen(r.Context(), userID, r.PathValue("workspaceID"), req.MessageID); err != nil {
		if errors.Is(err, folderagent.ErrChatUnavailable) {
			fail(w, http.StatusNotFound, "That message is no longer in the conversation")
			return
		}
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"seen": true})
}

func writeError(w http.ResponseWriter, err error) {
	var refusal *folderagent.Refusal
	var rootErr *personalassistant.FolderRootError
	switch {
	case errors.As(err, &refusal):
		status := refusal.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		_ = orihttp.RespondJSON(w, status, map[string]any{"error": refusal.Message, "code": refusal.Code, "duplicate": refusal.Duplicate})
	case errors.As(err, &rootErr):
		_ = orihttp.RespondJSON(w, http.StatusBadRequest, map[string]any{"error": rootErr.Message, "repair": "choose_folder"})
	case errors.Is(err, folderagent.ErrChoiceLost):
		_ = orihttp.RespondJSON(w, http.StatusGone, map[string]any{"error": "That folder choice expired. Choose the folder again.", "repair": "choose_folder"})
	case errors.Is(err, personalassistant.ErrFolderChipUnknown):
		fail(w, http.StatusBadRequest, "That is not one of the folders Ori can look at")
	case errors.Is(err, personalassistant.ErrFolderChipMissing):
		fail(w, http.StatusBadRequest, "That folder is not on this computer")
	case errors.Is(err, personalassistant.ErrFolderPickerUnavailable):
		fail(w, http.StatusServiceUnavailable, "The folder dialog is unavailable here. Paste the folder's path instead")
	case errors.Is(err, personalassistant.ErrValidation):
		fail(w, http.StatusBadRequest, "Keep it to one line, without paths, under 300 characters")
	case errors.Is(err, folderagent.ErrNoRecord), errors.Is(err, folderagent.ErrNotOwner):
		fail(w, http.StatusNotFound, "That folder workspace is no longer here")
	case errors.Is(err, folderagent.ErrWrongState):
		fail(w, http.StatusConflict, "The report has moved on. Reload to see where it stands")
	case errors.Is(err, folderagent.ErrStageNotYet):
		fail(w, http.StatusConflict, "Setting up this stage is not available yet")
	case errors.Is(err, folderagent.ErrNothingToSet):
		fail(w, http.StatusConflict, "There is nothing to set up yet")
	case errors.Is(err, folderagent.ErrUnavailable), errors.Is(err, personalassistant.ErrRepairNeeded):
		fail(w, http.StatusServiceUnavailable, unavailableSentence)
	default:
		fail(w, http.StatusInternalServerError, "Something went wrong. Nothing else was changed")
	}
}
