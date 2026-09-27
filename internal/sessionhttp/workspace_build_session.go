package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Build with your assistant: the Personal Assistant fills the Create
// Workspace wizard through a conversation. These endpoints hold the session
// (personalassistant.WorkspaceBuildSession) and run its turns:
//
//	GET   /api/workspaces/build-sessions/availability
//	POST  /api/workspaces/build-sessions                 create, or resume the open one
//	POST  /api/workspaces/build-sessions/{id}/turns      one answer: text or a chip
//	PATCH /api/workspaces/build-sessions/{id}/draft      the user's own form edits
//	POST  /api/workspaces/build-sessions/{id}/abandon    Start over / Discard
//
// The session's draft is the create request. The assistant proposes; the
// host validates every field (workspace_build_session_validate.go); the
// wizard renders; the user confirms with the ordinary Create.

// WorkspaceBuildStore persists build sessions.
type WorkspaceBuildStore interface {
	Read(ctx context.Context, userID string) (personalassistant.WorkspaceBuildDocument, error)
	Mutate(ctx context.Context, userID string, mutate func(*personalassistant.WorkspaceBuildDocument) error) (personalassistant.WorkspaceBuildDocument, error)
	Now() time.Time
}

// WorkspaceBuildDeps is what build sessions need from the server.
type WorkspaceBuildDeps struct {
	Store WorkspaceBuildStore
	// Assistant reads the Personal Assistant relationship (GET
	// /api/personal-assistant's projection).
	Assistant func(ctx context.Context, userID string) (*personalassistant.Projection, error)
	// Catalog is this user's creation catalog with readiness, the same one
	// GET /api/project-templates serves.
	Catalog func(ctx context.Context, userID string) ([]WorkspaceBuildCatalogEntry, error)
	// ResolveModel resolves the assistant's own model from its global agent
	// profile, falling back to the system model (FR30).
	ResolveModel func(ctx context.Context, profileName string) (WorkspaceBuildModel, error)
	// ModelAvailable reports whether a provider/model pair a team agent names
	// can be resolved.
	ModelAvailable func(provider, model string) bool
	CurrentUserID  func(ctx context.Context) (string, error)
}

// SetWorkspaceBuild wires build sessions. Without it every build endpoint
// reports the assistant as unavailable and the wizard stays manual.
func (h *Handler) SetWorkspaceBuild(deps WorkspaceBuildDeps) {
	if h == nil || deps.Store == nil || deps.Assistant == nil || deps.Catalog == nil || deps.ResolveModel == nil {
		return
	}
	h.workspaceBuild = &deps
}

// WorkspaceBuildWired reports whether build sessions are wired; build wiring
// tests use it.
func (h *Handler) WorkspaceBuildWired() bool {
	return h != nil && h.workspaceBuild != nil
}

// WorkspaceBuildAvailable reports, for the current user, what the
// availability endpoint would: a hired assistant with a model it can reach.
func (h *Handler) WorkspaceBuildAvailable(ctx context.Context) bool {
	if h == nil || h.workspaceBuild == nil {
		return false
	}
	return h.workspaceBuildReadiness(ctx, h.workspaceBuildUserID(ctx)).available
}

// The openers that may start a build (FR1.3). Every other surface keeps the
// manual wizard.
var workspaceBuildEntryPoints = map[string]bool{
	"home_cockpit_create":    true,
	"workspace_map_build":    true,
	"workspace_hub_create":   true,
	"personal_assistant_ask": true,
}

// Availability reasons (FR30).
const (
	buildReasonAssistantNotReady = "assistant_not_ready"
	buildReasonNoModel           = "no_model"
)

// Fixed copy the host writes into the transcript itself.
const (
	buildCopyModelFailure = "I couldn’t reach my model just now. Keep going on the form, or try again."
	buildCopyTryAgain     = "Try again"
	buildCopySubstitute   = "Which of these is closest?"
	buildCopyFolderAsk    = "Should I link a folder you already have?"
	buildCopyChooseFolder = "Choose a folder"
	buildCopyNoFolder     = "No folder"
	buildCopyFolderLater  = "This blueprint can’t link a folder you already have — after you create it, use “Explore a folder” on Home."
)

// Chip ids the host reserves.
const (
	buildChoiceTryAgain     = "try_again"
	buildChoiceChooseFolder = "folder_choose"
	buildChoiceNoFolder     = "folder_none"
)

const workspaceBuildMaxBody = 256 * 1024

func (h *Handler) handleWorkspaceBuildSessions(w http.ResponseWriter, r *http.Request, rest string) {
	rest = strings.Trim(rest, "/")
	switch {
	case rest == "availability":
		if r.Method != http.MethodGet {
			_ = orihttp.RespondMethodNotAllowed(w)
			return
		}
		h.getWorkspaceBuildAvailability(w, r)
	case rest == "":
		if r.Method != http.MethodPost {
			_ = orihttp.RespondMethodNotAllowed(w)
			return
		}
		h.createWorkspaceBuildSession(w, r)
	default:
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			_ = orihttp.RespondNotFound(w, "Build session not found")
			return
		}
		id, action := parts[0], parts[1]
		switch {
		case action == "turns" && r.Method == http.MethodPost:
			h.postWorkspaceBuildTurn(w, r, id)
		case action == "draft" && r.Method == http.MethodPatch:
			h.patchWorkspaceBuildDraft(w, r, id)
		case action == "abandon" && r.Method == http.MethodPost:
			h.abandonWorkspaceBuildSession(w, r, id)
		case action == "turns" || action == "draft" || action == "abandon":
			_ = orihttp.RespondMethodNotAllowed(w)
		default:
			_ = orihttp.RespondNotFound(w, "Build session not found")
		}
	}
}

func (h *Handler) workspaceBuildUserID(ctx context.Context) string {
	var resolve func(context.Context) (string, error)
	if h.workspaceBuild != nil && h.workspaceBuild.CurrentUserID != nil {
		resolve = h.workspaceBuild.CurrentUserID
	} else if h.currentUserID != nil {
		resolve = h.currentUserID
	}
	if resolve != nil {
		if id, err := resolve(ctx); err == nil && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return "local"
}

// buildReadiness is one availability decision: the relationship, the store
// behind it, and the model every turn will use.
type buildReadiness struct {
	available  bool
	reason     string
	projection *personalassistant.Projection
	model      WorkspaceBuildModel
}

func (h *Handler) workspaceBuildReadiness(ctx context.Context, userID string) buildReadiness {
	deps := h.workspaceBuild
	if deps == nil {
		return buildReadiness{reason: buildReasonAssistantNotReady}
	}
	projection, err := deps.Assistant(ctx, userID)
	if err != nil || projection == nil ||
		(projection.State != personalassistant.APIStateActive && projection.State != personalassistant.APIStatePaused) {
		return buildReadiness{reason: buildReasonAssistantNotReady}
	}
	// Sessions live beside the assistant's knowledge; without a readable
	// sidecar there is nowhere to keep a build.
	if _, err := deps.Store.Read(ctx, userID); err != nil {
		return buildReadiness{reason: buildReasonAssistantNotReady, projection: projection}
	}
	model, err := deps.ResolveModel(ctx, projection.GlobalAgentProfile)
	if err != nil || model.Provider == nil {
		return buildReadiness{reason: buildReasonNoModel, projection: projection}
	}
	return buildReadiness{available: true, projection: projection, model: model}
}

func (h *Handler) getWorkspaceBuildAvailability(w http.ResponseWriter, r *http.Request) {
	readiness := h.workspaceBuildReadiness(r.Context(), h.workspaceBuildUserID(r.Context()))
	body := map[string]any{"available": readiness.available}
	if !readiness.available {
		body["reason"] = readiness.reason
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, body)
}

func respondBuildUnavailable(w http.ResponseWriter, reason string) {
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
		"error":  "Your assistant can’t help with this right now.",
		"code":   "unavailable",
		"reason": reason,
	})
}

func respondBuildSession(w http.ResponseWriter, status int, session *personalassistant.WorkspaceBuildSession, extra map[string]any) {
	body := map[string]any{"session": session}
	for key, value := range extra {
		body[key] = value
	}
	_ = orihttp.RespondJSON(w, status, body)
}

func decodeBuildBody(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		_ = orihttp.RespondBadRequest(w, "Request body is required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, workspaceBuildMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid build request")
		return false
	}
	return true
}

type createBuildRequest struct {
	EntryPoint   string `json:"entry_point"`
	ParentID     string `json:"parent_id,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
}

// createWorkspaceBuildSession starts a build, or returns the one already
// open: there is at most one open build per user (FR10).
func (h *Handler) createWorkspaceBuildSession(w http.ResponseWriter, r *http.Request) {
	var req createBuildRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	entryPoint := strings.TrimSpace(req.EntryPoint)
	if !workspaceBuildEntryPoints[entryPoint] {
		_ = orihttp.RespondBadRequest(w, "This opener does not build with the assistant")
		return
	}
	message := strings.TrimSpace(req.FirstMessage)
	if utf8.RuneCountInString(message) > personalassistant.WorkspaceBuildMaxText {
		_ = orihttp.RespondBadRequest(w, "The message is too long")
		return
	}
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	readiness := h.workspaceBuildReadiness(ctx, userID)
	if !readiness.available {
		respondBuildUnavailable(w, readiness.reason)
		return
	}
	deps := h.workspaceBuild
	now := deps.Store.Now()
	groups := h.workspaceBuildGroups(ctx, userID)
	fresh := personalassistant.WorkspaceBuildSession{
		ID: uuid.NewString(), Status: personalassistant.WorkspaceBuildOpen, Version: 1,
		CreatedAt: now, UpdatedAt: now, EntryPoint: entryPoint,
		Assistant: personalassistant.BuildAssistant{
			DisplayName: strings.TrimSpace(readiness.projection.DisplayName),
			Appearance:  readiness.projection.Appearance.Clone(),
		},
		Transcript:   []personalassistant.BuildTranscriptEntry{},
		FurthestStep: 1,
	}
	// A pad or a hub inside a group opens with that group as the placement;
	// only one of the user's own groups is kept.
	for _, group := range groups {
		if group.ID == strings.TrimSpace(req.ParentID) {
			fresh.Draft.ParentID = group.ID
		}
	}
	var existing *personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		existing = nil
		if open := doc.Open(); open != nil {
			copied := *open
			existing = &copied
			return nil
		}
		doc.Add(fresh)
		return nil
	})
	if err != nil {
		logger.Warn("Workspace build could not start", logger.Fields{"error": err})
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	if existing != nil {
		respondBuildSession(w, http.StatusOK, existing, map[string]any{"resumed": true})
		return
	}
	logger.Info("Workspace build started", logger.Fields{"session_id": fresh.ID, "entry_point": entryPoint, "first_message": message != ""})
	if message == "" {
		respondBuildSession(w, http.StatusCreated, &fresh, nil)
		return
	}
	session, status, code := h.runWorkspaceBuildTurn(ctx, userID, fresh.ID, buildTurnRequest{Text: message, Version: fresh.Version}, readiness)
	if session == nil {
		_ = orihttp.RespondJSON(w, status, map[string]any{"error": "The build could not continue.", "code": code})
		return
	}
	respondBuildSession(w, http.StatusCreated, session, nil)
}

type buildTurnRequest struct {
	Text     string `json:"text,omitempty"`
	ChoiceID string `json:"choice_id,omitempty"`
	Version  int64  `json:"version"`
}

func (h *Handler) postWorkspaceBuildTurn(w http.ResponseWriter, r *http.Request, id string) {
	var req buildTurnRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	req.ChoiceID = strings.TrimSpace(req.ChoiceID)
	if (req.Text == "") == (req.ChoiceID == "") {
		_ = orihttp.RespondBadRequest(w, "Send either a message or a choice")
		return
	}
	if utf8.RuneCountInString(req.Text) > personalassistant.WorkspaceBuildMaxText {
		_ = orihttp.RespondBadRequest(w, "The message is too long")
		return
	}
	if req.ChoiceID == buildChoiceChooseFolder {
		_ = orihttp.RespondBadRequest(w, "Choosing a folder happens on the form")
		return
	}
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	readiness := h.workspaceBuildReadiness(ctx, userID)
	if !readiness.available {
		respondBuildUnavailable(w, readiness.reason)
		return
	}
	session, status, code := h.runWorkspaceBuildTurn(ctx, userID, id, req, readiness)
	if session == nil {
		_ = orihttp.RespondJSON(w, status, map[string]any{"error": buildErrorText(code), "code": code})
		return
	}
	respondBuildSession(w, http.StatusOK, session, nil)
}

func buildErrorText(code string) string {
	switch code {
	case "version":
		return "The build changed since you last saw it."
	case "not_found":
		return "Build session not found."
	case "closed":
		return "This build is no longer open."
	case "choice":
		return "That choice is no longer offered."
	default:
		return "The build could not continue."
	}
}

// errBuildTurn carries an HTTP status and a stable code out of a store mutation.
type errBuildTurn struct {
	status int
	code   string
}

func (e errBuildTurn) Error() string { return e.code }

// runWorkspaceBuildTurn records the user's answer, asks the model, and applies
// what the host accepts (FR13). The user's turn is stored before the model is
// called, so a failed call never loses it.
func (h *Handler) runWorkspaceBuildTurn(ctx context.Context, userID, id string, req buildTurnRequest, readiness buildReadiness) (*personalassistant.WorkspaceBuildSession, int, string) {
	deps := h.workspaceBuild
	var staged personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(id)
		if session == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if session.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		if session.Version != req.Version {
			return errBuildTurn{http.StatusConflict, "version"}
		}
		now := deps.Store.Now()
		if err := stageBuildUserTurn(session, req, now); err != nil {
			return err
		}
		session.Touch(now)
		staged = *session
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			return nil, turnErr.status, turnErr.code
		}
		logger.Warn("Workspace build turn could not be stored", logger.Fields{"session_id": id, "error": err})
		return nil, http.StatusServiceUnavailable, "unavailable"
	}

	catalog, catalogErr := deps.Catalog(ctx, userID)
	if catalogErr != nil {
		logger.Warn("Workspace build catalog unavailable", logger.Fields{"session_id": id, "error": catalogErr})
	}
	validation := buildValidation{
		catalog:   offerableCatalog(catalog),
		agents:    h.workspaceBuildAgents(),
		groups:    h.workspaceBuildGroups(ctx, userID),
		nameTaken: func(name string) bool { return h.workspaceNameTaken(ctx, name) },
		modelOK:   deps.ModelAvailable,
	}
	outcome := h.askWorkspaceBuildModel(ctx, readiness, &staged, validation)

	var result personalassistant.WorkspaceBuildSession
	_, err = deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(id)
		if session == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if session.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		finishBuildTurn(session, outcome, validation, deps.Store.Now())
		result = *session
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			return nil, turnErr.status, turnErr.code
		}
		logger.Warn("Workspace build turn could not be saved", logger.Fields{"session_id": id, "error": err})
		return nil, http.StatusServiceUnavailable, "unavailable"
	}
	logger.Info("Workspace build turn", logger.Fields{
		"session_id": id, "turn": result.TurnCount, "outcome": outcome.kind,
		"applied": len(result.Applied), "refused": len(result.Rejections), "asked": result.PendingQuestion != nil,
	})
	return &result, http.StatusOK, ""
}

// stageBuildUserTurn appends the user's answer: their text, or the label of
// the chip they chose. "Try again" replays the stored answer instead.
func stageBuildUserTurn(session *personalassistant.WorkspaceBuildSession, req buildTurnRequest, now time.Time) error {
	if req.ChoiceID == buildChoiceTryAgain {
		if session.Retry == nil {
			return errBuildTurn{http.StatusConflict, "choice"}
		}
		markBuildChoice(session, buildChoiceTryAgain)
		session.PendingQuestion = nil
		return nil
	}
	text := req.Text
	retry := &personalassistant.BuildRetry{Text: req.Text}
	if req.ChoiceID != "" {
		question := session.PendingQuestion
		label := ""
		if question != nil {
			for _, choice := range question.Choices {
				if choice.ID == req.ChoiceID {
					label = choice.Label
				}
			}
		}
		if label == "" {
			return errBuildTurn{http.StatusConflict, "choice"}
		}
		markBuildChoice(session, req.ChoiceID)
		text = label
		retry = &personalassistant.BuildRetry{ChoiceID: req.ChoiceID, Text: label}
	}
	session.Append(personalassistant.BuildTranscriptEntry{Role: personalassistant.BuildRoleUser, Text: text, At: now})
	session.PendingQuestion = nil
	session.TurnCount++
	if session.FirstRequest == "" && req.ChoiceID == "" {
		session.FirstRequest = truncateRunes(text, personalassistant.WorkspaceBuildMaxFirstRequest)
	}
	session.Retry = retry
	return nil
}

// markBuildChoice records which chip answered the newest question.
func markBuildChoice(session *personalassistant.WorkspaceBuildSession, choiceID string) {
	for i := len(session.Transcript) - 1; i >= 0; i-- {
		entry := &session.Transcript[i]
		if entry.Role != personalassistant.BuildRoleAssistant || len(entry.Choices) == 0 {
			continue
		}
		for _, choice := range entry.Choices {
			if choice.ID == choiceID {
				entry.Chosen = choiceID
				return
			}
		}
		return
	}
}

func offerableCatalog(catalog []WorkspaceBuildCatalogEntry) []WorkspaceBuildCatalogEntry {
	out := make([]WorkspaceBuildCatalogEntry, 0, len(catalog))
	validation := buildValidation{}
	for _, entry := range catalog {
		if strings.TrimSpace(entry.Template.ID) == "" || !validation.offerable(entry) {
			continue
		}
		// Building requires a hired assistant, who already has its Personal
		// HQ; that blueprint is not something to build a second of.
		if entry.Template.ID == personalhq.PersonalHQTemplateID {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// buildOutcome is what one turn's model work produced.
type buildOutcome struct {
	kind  string // "reply", "substitute", "failure"
	reply buildReply
	// substitute holds the fixed question's blueprint choices.
	substitute []WorkspaceBuildCatalogEntry
}

// askWorkspaceBuildModel asks the model, and holds it to the rule that every
// turn patches or asks (FR22): a reply that does neither is retried once with
// a nudge, and then replaced by a fixed question with the three closest
// blueprints as chips. The first reply to a description must also set the
// blueprint, the name, and the description.
func (h *Handler) askWorkspaceBuildModel(ctx context.Context, readiness buildReadiness, session *personalassistant.WorkspaceBuildSession, validation buildValidation) buildOutcome {
	input := buildPromptInput{
		AssistantName: session.Assistant.DisplayName,
		Session:       session,
		Catalog:       validation.catalog,
		Agents:        validation.agents,
		Groups:        validation.groups,
	}
	messages := buildMessages(session)
	prompted := cloneBuildSession(*session)
	input.Session = &prompted
	for attempt := 0; attempt < 2; attempt++ {
		input.Nudge = attempt > 0
		reply, err := callBuildModel(ctx, readiness.model, buildSystemPrompt(input), messages)
		if err != nil {
			logger.Warn("Workspace build model call failed", logger.Fields{"session_id": session.ID, "attempt": attempt + 1, "error": err})
			return buildOutcome{kind: "failure"}
		}
		works, refused := buildReplyDoesWork(session, reply, validation)
		if works {
			return buildOutcome{kind: "reply", reply: reply}
		}
		// The retry hears what the host refused, so it can fix it rather
		// than repeat it.
		prompted.Rejections = refused
	}
	return buildOutcome{kind: "substitute", substitute: closestBlueprints(validation.catalog, session, 3)}
}

// buildReplyDoesWork applies the reply to a copy of the session and reports
// whether anything was accepted or a question was asked, with what the host
// refused.
func buildReplyDoesWork(session *personalassistant.WorkspaceBuildSession, reply buildReply, validation buildValidation) (bool, []personalassistant.BuildRejection) {
	scratch := cloneBuildSession(*session)
	result := applyBuildReply(&scratch, reply.Patch, validation)
	asked := strings.TrimSpace(reply.Ask.Question) != "" || result.askFolder
	if !result.changed() && !asked {
		return false, result.rejections
	}
	// The first answer to the user's description sets the essentials.
	if session.TurnCount == 1 && !session.Draft.HasBlueprint() {
		draft := scratch.Draft
		ok := draft.HasBlueprint() && strings.TrimSpace(draft.Name) != "" && strings.TrimSpace(draft.Description) != ""
		return ok, result.rejections
	}
	return true, result.rejections
}

func cloneBuildSession(session personalassistant.WorkspaceBuildSession) personalassistant.WorkspaceBuildSession {
	data, err := json.Marshal(session)
	if err != nil {
		return session
	}
	var copied personalassistant.WorkspaceBuildSession
	if err := json.Unmarshal(data, &copied); err != nil {
		return session
	}
	return copied
}

// closestBlueprints ranks the catalog against what the user has said.
func closestBlueprints(catalog []WorkspaceBuildCatalogEntry, session *personalassistant.WorkspaceBuildSession, limit int) []WorkspaceBuildCatalogEntry {
	var said strings.Builder
	for _, entry := range session.Transcript {
		if entry.Role == personalassistant.BuildRoleUser {
			said.WriteString(entry.Text)
			said.WriteString(" ")
		}
	}
	ranked := relevantCatalog(catalog, said.String())
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// finishBuildTurn writes the turn's outcome into the stored session.
func finishBuildTurn(session *personalassistant.WorkspaceBuildSession, outcome buildOutcome, validation buildValidation, now time.Time) {
	session.CreateNow = false
	session.Ready = false
	session.AskFolder = false
	session.Applied = nil
	chipPrefix := fmt.Sprintf("c%d-", session.TurnCount)
	switch outcome.kind {
	case "failure":
		choices := []personalassistant.BuildChoice{{ID: buildChoiceTryAgain, Label: buildCopyTryAgain}}
		session.Append(personalassistant.BuildTranscriptEntry{
			Role: personalassistant.BuildRoleAssistant, Text: buildCopyModelFailure, Choices: choices, Fixed: true, At: now,
		})
		session.PendingQuestion = &personalassistant.BuildQuestion{Question: buildCopyModelFailure, Choices: choices, AllowFreeText: true}
		session.Touch(now)
		return
	case "substitute":
		choices := make([]personalassistant.BuildChoice, 0, len(outcome.substitute))
		for i, entry := range outcome.substitute {
			choices = append(choices, personalassistant.BuildChoice{ID: fmt.Sprintf("%s%d", chipPrefix, i+1), Label: truncateRunes(entry.Template.Name, 60)})
		}
		session.Append(personalassistant.BuildTranscriptEntry{
			Role: personalassistant.BuildRoleAssistant, Text: buildCopySubstitute, Choices: choices, Fixed: true, At: now,
		})
		session.PendingQuestion = &personalassistant.BuildQuestion{Question: buildCopySubstitute, Choices: choices, AllowFreeText: true}
		session.Rejections = nil
		session.Retry = nil
		session.Touch(now)
		return
	}

	reply := outcome.reply
	result := applyBuildReply(session, reply.Patch, validation)
	session.Rejections = result.rejections
	session.Applied = result.applied
	session.AskFolder = result.askFolder
	session.Ready = reply.Ready
	session.CreateNow = reply.CreateNow
	session.Retry = nil
	mergeBuildWhy(session, reply.Why)
	session.Alternatives = buildAlternatives(reply.Alternatives, validation, session.Draft.TemplateID)

	question := strings.TrimSpace(reply.Ask.Question)
	var choices []personalassistant.BuildChoice
	switch {
	case result.askFolder:
		choices = []personalassistant.BuildChoice{
			{ID: buildChoiceChooseFolder, Label: buildCopyChooseFolder},
			{ID: buildChoiceNoFolder, Label: buildCopyNoFolder},
		}
		if question == "" {
			question = buildCopyFolderAsk
		}
	case question != "":
		for i, choice := range reply.Ask.Choices {
			label := truncateRunes(choice.Label, 60)
			if label == "" || len(choices) == 4 {
				continue
			}
			// The model's ids never reach the browser (FR44).
			choices = append(choices, personalassistant.BuildChoice{ID: fmt.Sprintf("%s%d", chipPrefix, i+1), Label: label})
		}
	}
	question = truncateRunes(question, personalassistant.WorkspaceBuildMaxSay)
	say := truncateRunes(reply.Say, personalassistant.WorkspaceBuildMaxSay)
	// The model wrote its line before the host checked the proposal, so a
	// refused part gets a fixed note rather than standing as a claim, and a
	// team change is stated as the form now holds it.
	if note := buildRefusalNote(result.rejections); note != "" {
		say = strings.TrimSpace(say + " " + note)
	}
	if result.teamPatch != nil {
		if receipt := buildTeamReceipt(result.teamPatch, validation, session.Draft); receipt != "" {
			say = strings.TrimSpace(say + " " + receipt)
		}
	}
	text := strings.TrimSpace(say + " " + question)
	if say != "" && question != "" && strings.Contains(say, question) {
		text = say
	}
	if text == "" {
		text = "Done."
	}
	session.Append(personalassistant.BuildTranscriptEntry{Role: personalassistant.BuildRoleAssistant, Text: text, Choices: choices, At: now})
	if question != "" {
		session.PendingQuestion = &personalassistant.BuildQuestion{
			Question: question, Choices: choices, AllowFreeText: reply.Ask.AllowFreeText || len(choices) == 0 || result.askFolder,
		}
	} else {
		session.PendingQuestion = nil
	}
	session.FurthestStep = max(session.FurthestStep, buildDraftStep(session))
	session.Touch(now)
}

// buildTeamReceipt states the team the host accepted, in the form's terms:
// "On the form: Briefing Editor — Luna; Researcher — a new agent, Scout added."
// It is written from the accepted patch, never from the model's sentence.
func buildTeamReceipt(team *personalassistant.BuildTeamPatch, validation buildValidation, draft personalassistant.BuildDraft) string {
	if team == nil {
		return ""
	}
	if team.Mode == "agentless" {
		return "On the form: no agents."
	}
	labels := map[string]string{}
	if template, ok := validation.currentTemplate(draft); ok {
		for _, role := range buildRoleDeclarations(template) {
			labels[role.RoleID] = role.Label
		}
	}
	parts := []string{}
	for _, role := range team.Roles {
		label := labels[role.RoleID]
		if label == "" {
			label = role.RoleID
		}
		if role.Mode == "assign" {
			parts = append(parts, fmt.Sprintf("%s — %s", label, role.AgentName))
		} else {
			parts = append(parts, fmt.Sprintf("%s — a new agent, “%s”", label, role.AgentName))
		}
	}
	for _, name := range team.SavedAgents {
		parts = append(parts, name+" added")
	}
	if len(parts) == 0 {
		return ""
	}
	return "On the form: " + strings.Join(parts, "; ") + "."
}

// buildRefusalNote names, in plain words, the parts of the form the host did
// not accept this turn: "(The form didn't take the team yet — I'll adjust it.)"
func buildRefusalNote(rejections []personalassistant.BuildRejection) string {
	parts := []string{}
	seen := map[string]bool{}
	folder := ""
	for _, rejection := range rejections {
		field := rejection.Field
		part := ""
		switch {
		case field == buildFieldAskFolder:
			// A folder this blueprint cannot link has one fixed way forward.
			folder = buildCopyFolderLater
		case field == buildFieldBlueprint:
			part = "the blueprint"
		case field == buildFieldName:
			part = "the name"
		case field == buildFieldDescription:
			part = "the description"
		case strings.HasPrefix(field, buildFieldInputs):
			part = "a setting"
		case field == buildFieldParent:
			part = "the group"
		case strings.HasPrefix(field, buildFieldTeam):
			part = "the team"
		case field == buildFieldTags:
			part = "the tags"
		case field == buildFieldColor:
			part = "the color"
		}
		if part != "" && !seen[part] {
			seen[part] = true
			parts = append(parts, part)
		}
	}
	note := ""
	switch len(parts) {
	case 0:
	case 1:
		note = fmt.Sprintf("(The form didn’t take %s yet — I’ll adjust it.)", parts[0])
	default:
		note = fmt.Sprintf("(The form didn’t take %s or %s yet — I’ll adjust them.)",
			strings.Join(parts[:len(parts)-1], ", "), parts[len(parts)-1])
	}
	return strings.TrimSpace(note + " " + folder)
}

// buildDraftStep is the step the draft has reached, for resuming; the wizard's
// own gates still decide when the user can actually move on.
func buildDraftStep(session *personalassistant.WorkspaceBuildSession) int {
	draft := session.Draft
	switch {
	case !draft.HasBlueprint():
		return 1
	case strings.TrimSpace(draft.Name) == "" || strings.TrimSpace(draft.Description) == "":
		return 2
	case session.TeamPatch == nil && len(draft.RoleStaffing) == 0 && len(draft.ExistingAgentNames) == 0:
		return 3
	default:
		return 4
	}
}

var buildWhySections = map[string]bool{"blueprint": true, "details": true, "team": true, "placement": true}

// mergeBuildWhy replaces the reasons for each section the reply explains.
func mergeBuildWhy(session *personalassistant.WorkspaceBuildSession, whys []buildReplyWhy) {
	replaced := map[string]bool{}
	incoming := []personalassistant.BuildWhy{}
	for _, why := range whys {
		section := strings.ToLower(strings.TrimSpace(why.Section))
		text := truncateRunes(why.Text, 300)
		if !buildWhySections[section] || text == "" {
			continue
		}
		replaced[section] = true
		incoming = append(incoming, personalassistant.BuildWhy{Section: section, Text: text})
	}
	kept := []personalassistant.BuildWhy{}
	for _, why := range session.Why {
		if !replaced[why.Section] {
			kept = append(kept, why)
		}
	}
	merged := append(kept, incoming...)
	if len(merged) > 8 {
		merged = merged[len(merged)-8:]
	}
	session.Why = merged
}

func buildAlternatives(proposed []buildReplyAlternate, validation buildValidation, chosen string) []personalassistant.BuildAlternative {
	out := []personalassistant.BuildAlternative{}
	for _, alternative := range proposed {
		entry, ok := validation.lookup(alternative.BlueprintID)
		if !ok || entry.Template.ID == chosen || len(out) == 2 {
			continue
		}
		out = append(out, personalassistant.BuildAlternative{
			BlueprintID: entry.Template.ID, Label: entry.Template.Name, Reason: truncateRunes(alternative.Reason, 200),
		})
	}
	return out
}

// workspaceBuildAgents are the saved agents the assistant may assign.
func (h *Handler) workspaceBuildAgents() []buildSavedAgent {
	if h.agentStore == nil {
		return nil
	}
	out := []buildSavedAgent{}
	for _, name := range h.agentStore.ListAgents() {
		agent, ok := h.agentStore.GetAgent(name)
		if !ok || agent == nil {
			continue
		}
		out = append(out, buildSavedAgent{Name: name, Role: string(agent.Role), Model: agent.Settings.Model})
		if len(out) == 50 {
			break
		}
	}
	return out
}

// workspaceBuildGroups are the user's active groups.
func (h *Handler) workspaceBuildGroups(ctx context.Context, userID string) []buildGroup {
	if h.store == nil {
		return nil
	}
	rows, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return nil
	}
	out := []buildGroup{}
	for i := range rows {
		ws := &rows[i]
		if !ws.IsGroup() || ws.Status == session.WorkspaceStatusTrashed || ws.Status == session.WorkspaceStatusMissing {
			continue
		}
		if ws.OwnerUserID != "" && ws.OwnerUserID != userID {
			continue
		}
		out = append(out, buildGroup{ID: ws.ID, Name: ws.Name})
		if len(out) == 50 {
			break
		}
	}
	return out
}

// finishWorkspaceBuild closes the build a create finished (FR28): the session
// is marked created with the workspace id, and the workspace's provenance
// records how it was set up. A session id that is unknown, belongs to another
// user's store, or is already closed is logged and ignored; a create is never
// refused over it.
func (h *Handler) finishWorkspaceBuild(ctx context.Context, sessionID, workspaceID string) {
	sessionID, workspaceID = strings.TrimSpace(sessionID), strings.TrimSpace(workspaceID)
	deps := h.workspaceBuild
	if sessionID == "" || workspaceID == "" || deps == nil {
		return
	}
	var finished personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, h.workspaceBuildUserID(ctx), func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(sessionID)
		if session == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if session.Status == personalassistant.WorkspaceBuildCreated && session.CreatedWorkspaceID == workspaceID {
			finished = *session
			return nil
		}
		if session.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		session.Status = personalassistant.WorkspaceBuildCreated
		session.CreatedWorkspaceID = workspaceID
		session.PendingQuestion = nil
		session.CreateNow = false
		session.Touch(deps.Store.Now())
		finished = *session
		return nil
	})
	if err != nil {
		logger.Info("Workspace build not recorded for a create", logger.Fields{"session_id": sessionID, "workspace_id": workspaceID, "reason": err.Error()})
		return
	}
	logger.Info("Workspace build created", logger.Fields{"session_id": sessionID, "workspace_id": workspaceID, "turns": finished.TurnCount})
	if h.workspaceTaskStore == nil {
		return
	}
	summary := buildSummaryFor(finished)
	if err := h.workspaceTaskStore.Update(workspaceID, func(w *agentworkspace.Workspace) error {
		provenance := w.GetTemplateProvenance()
		if provenance == nil {
			provenance = &agentworkspace.TemplateProvenance{}
		}
		provenance.BuildSummary = summary
		w.SetTemplateProvenance(provenance)
		return nil
	}); err != nil {
		logger.Warn("Workspace build summary could not be recorded", logger.Fields{"workspace_id": workspaceID, "error": err})
	}
}

// buildSummaryFor is the "How this was set up" record: the assistant, the
// user's own request, and the reasons behind each choice, never the whole
// conversation.
func buildSummaryFor(session personalassistant.WorkspaceBuildSession) *agentworkspace.BuildSummary {
	summary := &agentworkspace.BuildSummary{
		AssistantName: session.Assistant.DisplayName,
		SessionID:     session.ID,
		CreatedAt:     session.UpdatedAt,
		TurnCount:     session.TurnCount,
		UserRequest:   truncateRunes(session.FirstRequest, personalassistant.WorkspaceBuildMaxFirstRequest),
	}
	// The team line states the team the workspace was created with, taken from
	// the create request itself; the model's own sentence about the team may
	// not match what the user finally confirmed.
	team := teamDecisionFromDraft(session.Draft)
	for _, why := range session.Why {
		if why.Section == "team" && team != "" {
			continue
		}
		summary.Decisions = append(summary.Decisions, agentworkspace.BuildDecision{Section: why.Section, Text: why.Text})
	}
	if team != "" {
		summary.Decisions = append(summary.Decisions, agentworkspace.BuildDecision{Section: "team", Text: team})
	}
	return summary
}

// teamDecisionFromDraft says who staffs the workspace, from the role staffing
// and saved teammates the create request carried.
func teamDecisionFromDraft(draft personalassistant.BuildDraft) string {
	parts := []string{}
	var staffing []struct {
		RoleID string `json:"role_id"`
		Mode   string `json:"mode"`
		Name   string `json:"name"`
	}
	_ = json.Unmarshal(draft.RoleStaffing, &staffing)
	for _, fill := range staffing {
		role := humanizeRoleID(fill.RoleID)
		name := strings.TrimSpace(fill.Name)
		if role == "" || name == "" {
			continue
		}
		if fill.Mode == "assign" {
			parts = append(parts, fmt.Sprintf("%s: %s", role, name))
		} else {
			parts = append(parts, fmt.Sprintf("%s: a new agent, “%s”", role, name))
		}
	}
	for _, name := range draft.ExistingAgentNames {
		if name = strings.TrimSpace(name); name != "" && !strings.Contains(strings.Join(parts, "\n"), ": "+name) {
			parts = append(parts, name+" joins the team")
		}
	}
	return strings.Join(parts, "; ")
}

// humanizeRoleID reads a role id as words: "research-lead" → "Research lead".
func humanizeRoleID(id string) string {
	words := strings.ReplaceAll(strings.TrimSpace(id), "-", " ")
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// workspaceNameTaken applies the create path's duplicate-folder rule to a
// name before it reaches the form, so the assistant can propose another.
func (h *Handler) workspaceNameTaken(ctx context.Context, name string) bool {
	return h.workspaceSlugOccupied(ctx, strings.ToLower(workspaceSlugFor(name)), "")
}
