package orchestrationhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TicketHandler serves the canonical, owner-scoped Ticket API over
// workspace.TicketService (tasks/prd-workspace-ticket-management.md FR-85
// through FR-94).
//
// Two shape rules distinguish it from the legacy task/backlog handlers it
// eventually replaces:
//
//   - Ownership is in the path, never in a body field or query parameter. The
//     route IS the mutation authority, so a parent workspace rolling up a
//     descendant's Tickets physically cannot mutate one under its own
//     identity (FR-12, FR-87, FR-90).
//   - State changes use an explicit transition operation, never a generic
//     context patch (FR-88). PATCH edits content; POST .../transition moves
//     lifecycle. There is deliberately no way to write `state` through PATCH.
//
// Every success response carries a canonical Ticket envelope rather than a
// legacy task/backlog shape (FR-92).
type TicketHandler struct {
	service *workspace.TicketService
}

// NewTicketHandler constructs a TicketHandler over the given service.
func NewTicketHandler(service *workspace.TicketService) *TicketHandler {
	return &TicketHandler{service: service}
}

// newTicketService builds the canonical service the handler serves.
// TicketService is a stateless holder of its store/bus references, so
// constructing one per handler is safe — the store it wraps is the shared
// source of truth.
func newTicketService(store workspace.Store, bus *workspace.EventBus) *workspace.TicketService {
	service := workspace.NewTicketService(store)
	if bus != nil {
		service.SetEventBus(bus)
	}
	return service
}

// --- Wire types -----------------------------------------------------------

// ticketCreateRequest is the POST body. State is required and explicit:
// FR-19 makes the Backlog/Ready choice a user decision, so the server does
// not fill it in.
type ticketCreateRequest struct {
	State         string     `json:"state"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Tags          []string   `json:"tags"`
	Priority      int        `json:"priority"`
	DueDate       *time.Time `json:"due_date"`
	ReferenceURL  string     `json:"reference_url"`
	Source        string     `json:"source"`
	SourceID      string     `json:"source_id"`
	LinkedNoteIDs []string   `json:"linked_note_ids"`
}

// ticketUpdateRequest is the PATCH body. Every field is a pointer so the
// handler can distinguish "absent" (leave untouched) from "sent as empty"
// (clear it) — a distinction a plain struct would collapse, silently wiping
// fields the client never mentioned.
//
// There is no `state` field by design; see the type comment on TicketHandler.
type ticketUpdateRequest struct {
	Title         *string    `json:"title"`
	Description   *string    `json:"description"`
	Tags          *[]string  `json:"tags"`
	Priority      *int       `json:"priority"`
	DueDate       *time.Time `json:"due_date"`
	ReferenceURL  *string    `json:"reference_url"`
	LinkedNoteIDs *[]string  `json:"linked_note_ids"`
	Version       *int64     `json:"version"`
}

type ticketTransitionRequest struct {
	To      string `json:"to"`
	Reason  string `json:"reason"`
	Actor   string `json:"actor"`
	ActorID string `json:"actor_id"`
	RunID   string `json:"run_id"`
	Version *int64 `json:"version"`
}

type ticketReorderRequest struct {
	State      string   `json:"state"`
	OrderedIDs []string `json:"ordered_ids"`
}

type ticketListResponse struct {
	Tickets []workspace.Ticket `json:"tickets"`
	Count   int                `json:"count"`
	// Total is how many Tickets matched before the limit was applied, so a
	// client can distinguish "that is all of them" from "there are more".
	Total     int  `json:"total"`
	Truncated bool `json:"truncated,omitempty"`
	// PartialOwners names descendant workspaces that could not be read during
	// a roll-up, so the UI can say the list is incomplete rather than present
	// it as whole (FR-133).
	PartialOwners      []string `json:"partial_owners,omitempty"`
	StudioID           string   `json:"studio_id"`
	IncludeDescendants bool     `json:"include_descendants"`
}

type ticketReorderResponse struct {
	Tickets  []workspace.Ticket `json:"tickets"`
	State    string             `json:"state"`
	StudioID string             `json:"studio_id"`
}

// --- Collection routes ----------------------------------------------------

// TicketCollectionHandler serves GET (list) and POST (create) on
// /api/workspaces/{studio_id}/tickets (FR-86).
func (th *TicketHandler) TicketCollectionHandler(w http.ResponseWriter, r *http.Request) {
	studioID := strings.TrimSpace(r.PathValue("studioID"))
	if studioID == "" {
		orihttp.BadRequest(w, "studio_id is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		th.handleList(w, r, studioID)
	case http.MethodPost:
		th.handleCreate(w, r, studioID)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (th *TicketHandler) handleList(w http.ResponseWriter, r *http.Request, studioID string) {
	query := workspace.TicketQuery{WorkspaceID: studioID}

	includeDescendants, err := parseOptionalBool(r.URL.Query().Get("include_descendants"))
	if err != nil {
		orihttp.BadRequest(w, "include_descendants must be a boolean")
		return
	}
	query.IncludeDescendants = includeDescendants

	includeSubtickets, err := parseOptionalBool(r.URL.Query().Get("include_subtickets"))
	if err != nil {
		orihttp.BadRequest(w, "include_subtickets must be a boolean")
		return
	}
	query.IncludeSubtickets = includeSubtickets

	// Multi-valued params may repeat or be comma-separated; both forms are
	// accepted so a client can build the query either way.
	for _, value := range multiValueParam(r, "state") {
		state, err := workspace.ParseTicketState(value)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		query.States = append(query.States, state)
	}
	query.Tags = multiValueParam(r, "tag")
	query.Assignees = multiValueParam(r, "assignee")
	query.Sources = multiValueParam(r, "source")
	query.OwnerIDs = multiValueParam(r, "owner")

	for _, value := range multiValueParam(r, "priority") {
		priority, err := strconv.Atoi(value)
		if err != nil {
			orihttp.BadRequest(w, "priority must be an integer between 1 and 5")
			return
		}
		query.Priorities = append(query.Priorities, priority)
	}

	if raw := r.URL.Query().Get("due"); strings.TrimSpace(raw) != "" {
		due, err := workspace.ParseTicketDueFilter(raw)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		query.Due = due
	}
	if raw := r.URL.Query().Get("archive"); strings.TrimSpace(raw) != "" {
		archive, err := workspace.ParseTicketArchiveFilter(raw)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		query.Archive = archive
	}
	if raw := r.URL.Query().Get("sort"); strings.TrimSpace(raw) != "" {
		field, err := workspace.ParseTicketSortField(raw)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		query.Sort = field
	}

	descending, err := parseOptionalBool(r.URL.Query().Get("desc"))
	if err != nil {
		orihttp.BadRequest(w, "desc must be a boolean")
		return
	}
	query.SortDescending = descending
	query.Search = r.URL.Query().Get("search")

	if raw := r.URL.Query().Get("limit"); strings.TrimSpace(raw) != "" {
		limit, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			orihttp.BadRequest(w, "limit must be an integer")
			return
		}
		query.Limit = limit
	}

	page, err := th.service.Search(query)
	if err != nil {
		// A bad filter value is the client's problem, not a missing workspace;
		// only fall through to 404 when the workspace itself cannot be read.
		if validationErr, ok := workspace.IsTicketValidationError(err); ok {
			_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
				orihttp.NewAPIError(orihttp.ErrCodeValidation, validationErr.Message).
					WithDetails(map[string]string{"field": validationErr.Field}))
			return
		}
		logger.Error("Failed to list tickets", logger.Fields{"error": err, "studio_id": studioID})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	_ = orihttp.RespondJSON(w, http.StatusOK, ticketListResponse{
		Tickets:            page.Tickets,
		Count:              len(page.Tickets),
		Total:              page.Total,
		Truncated:          page.Truncated,
		PartialOwners:      page.PartialOwners,
		StudioID:           studioID,
		IncludeDescendants: query.IncludeDescendants,
	})
}

// multiValueParam collects a repeated and/or comma-separated query parameter
// into a trimmed, non-empty list.
func multiValueParam(r *http.Request, name string) []string {
	var out []string
	for _, raw := range r.URL.Query()[name] {
		for value := range strings.SplitSeq(raw, ",") {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

func (th *TicketHandler) handleCreate(w http.ResponseWriter, r *http.Request, studioID string) {
	var req ticketCreateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.State) == "" {
		orihttp.BadRequest(w, "state is required and must be 'backlog' or 'ready'")
		return
	}
	state, err := workspace.ParseTicketState(req.State)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	ticket, err := th.service.Create(workspace.TicketCreateInput{
		WorkspaceID:   studioID,
		State:         state,
		Title:         req.Title,
		Description:   req.Description,
		Tags:          req.Tags,
		Priority:      req.Priority,
		DueDate:       req.DueDate,
		ReferenceURL:  req.ReferenceURL,
		Source:        req.Source,
		SourceID:      req.SourceID,
		LinkedNoteIDs: req.LinkedNoteIDs,
		Actor:         workspace.TicketActorUser,
	})
	if err != nil {
		th.respondTicketError(w, "create ticket", studioID, "", err)
		return
	}

	_ = orihttp.RespondJSON(w, http.StatusCreated, ticket)
}

// TicketReorderHandler serves POST
// /api/workspaces/{studio_id}/tickets/reorder (FR-91). It is a collection
// operation, not an item one: the whole ordering succeeds or nothing changes.
func (th *TicketHandler) TicketReorderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	studioID := strings.TrimSpace(r.PathValue("studioID"))
	if studioID == "" {
		orihttp.BadRequest(w, "studio_id is required")
		return
	}

	var req ticketReorderRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, err := workspace.ParseTicketState(req.State)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	tickets, err := th.service.Reorder(studioID, state, req.OrderedIDs)
	if err != nil {
		th.respondTicketError(w, "reorder tickets", studioID, "", err)
		return
	}

	_ = orihttp.RespondJSON(w, http.StatusOK, ticketReorderResponse{
		Tickets:  tickets,
		State:    string(state),
		StudioID: studioID,
	})
}

// --- Item routes ----------------------------------------------------------

// TicketItemHandler serves GET, PATCH, and DELETE on
// /api/workspaces/{studio_id}/tickets/{ticket_id} (FR-87).
func (th *TicketHandler) TicketItemHandler(w http.ResponseWriter, r *http.Request) {
	studioID := strings.TrimSpace(r.PathValue("studioID"))
	ticketID := strings.TrimSpace(r.PathValue("ticketID"))
	if studioID == "" || ticketID == "" {
		orihttp.BadRequest(w, "studio_id and ticket_id are required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		th.handleGet(w, studioID, ticketID)
	case http.MethodPatch:
		th.handleUpdate(w, r, studioID, ticketID)
	case http.MethodDelete:
		th.handleDelete(w, r, studioID, ticketID)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (th *TicketHandler) handleGet(w http.ResponseWriter, studioID, ticketID string) {
	ticket, err := th.service.Get(studioID, ticketID)
	if err != nil {
		th.respondTicketError(w, "get ticket", studioID, ticketID, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, ticket)
}

func (th *TicketHandler) handleUpdate(w http.ResponseWriter, r *http.Request, studioID, ticketID string) {
	// The body is decoded twice on purpose: once into the typed request and
	// once into a generic map. Only the map can tell "due_date": null (clear
	// it) apart from an omitted key (leave it), because both decode to a nil
	// pointer in the typed struct.
	body, err := readRequestBody(w, r)
	if err != nil {
		orihttp.BadRequest(w, "Invalid request body")
		return
	}

	var req ticketUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		orihttp.BadRequest(w, "Invalid JSON body")
		return
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(body, &present); err != nil {
		orihttp.BadRequest(w, "Invalid JSON body")
		return
	}

	input := workspace.TicketUpdateInput{
		Title:         req.Title,
		Description:   req.Description,
		Tags:          req.Tags,
		Priority:      req.Priority,
		ReferenceURL:  req.ReferenceURL,
		LinkedNoteIDs: req.LinkedNoteIDs,
	}
	if req.Version != nil {
		input.IfVersion = *req.Version
	}
	if _, sent := present["due_date"]; sent {
		due := req.DueDate
		input.DueDate = &due
	}

	ticket, err := th.service.Update(studioID, ticketID, input)
	if err != nil {
		th.respondTicketError(w, "update ticket", studioID, ticketID, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, ticket)
}

func (th *TicketHandler) handleDelete(w http.ResponseWriter, r *http.Request, studioID, ticketID string) {
	version, err := parseOptionalVersion(r.URL.Query().Get("version"))
	if err != nil {
		orihttp.BadRequest(w, "version must be an integer")
		return
	}

	if err := th.service.Delete(studioID, ticketID, version); err != nil {
		th.respondTicketError(w, "delete ticket", studioID, ticketID, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{
		"deleted":   true,
		"ticket_id": ticketID,
		"studio_id": studioID,
	})
}

// TicketTransitionHandler serves POST
// /api/workspaces/{studio_id}/tickets/{ticket_id}/transition — the ONLY way
// to change Ticket state (FR-88).
func (th *TicketHandler) TicketTransitionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	studioID := strings.TrimSpace(r.PathValue("studioID"))
	ticketID := strings.TrimSpace(r.PathValue("ticketID"))
	if studioID == "" || ticketID == "" {
		orihttp.BadRequest(w, "studio_id and ticket_id are required")
		return
	}

	var req ticketTransitionRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	to, err := workspace.ParseTicketState(req.To)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	input := workspace.TicketTransitionInput{
		To:      to,
		Reason:  req.Reason,
		Actor:   req.Actor,
		ActorID: req.ActorID,
		RunID:   req.RunID,
	}
	if strings.TrimSpace(input.Actor) == "" {
		// A transition arriving on this route is a user action by
		// construction; background callers use the service directly.
		input.Actor = workspace.TicketActorUser
	}
	if req.Version != nil {
		input.IfVersion = *req.Version
	}

	ticket, err := th.service.Transition(studioID, ticketID, input)
	if err != nil {
		th.respondTicketError(w, "transition ticket", studioID, ticketID, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, ticket)
}

// --- Error mapping --------------------------------------------------------

// respondTicketError maps a service error onto the actionable 4xx the PRD
// requires (FR-94). Each case tells the client what to do next: fix a field,
// stop asking, reload, or pick a legal destination.
func (th *TicketHandler) respondTicketError(w http.ResponseWriter, action, studioID, ticketID string, err error) {
	switch {
	case workspace.IsTicketNotFound(err):
		// Unknown and foreign IDs are reported identically so this route
		// cannot be used to probe another workspace's records.
		_ = orihttp.RespondAPIError(w, http.StatusNotFound,
			orihttp.NewAPIError(orihttp.ErrCodeNotFound, "Ticket not found in this workspace").
				WithDetails(map[string]string{"studio_id": studioID, "ticket_id": ticketID}))

	case workspace.IsTicketVersionConflict(err):
		// 409 plus the current record, so the client can show the other
		// editor's version instead of a dead end.
		details := map[string]any{"studio_id": studioID, "ticket_id": ticketID}
		if current, getErr := th.service.Get(studioID, ticketID); getErr == nil {
			details["current"] = current
		}
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError(orihttp.ErrCodeConflict, err.Error()).WithDetails(details))

	default:
		if transitionErr, ok := workspace.IsIllegalTicketTransition(err); ok {
			legal := workspace.LegalTicketTransitions(transitionErr.From)
			_ = orihttp.RespondAPIError(w, http.StatusConflict,
				orihttp.NewAPIError(orihttp.ErrCodeConflict, err.Error()).
					WithDetails(map[string]any{
						"studio_id":         studioID,
						"ticket_id":         ticketID,
						"current_state":     string(transitionErr.From),
						"requested_state":   string(transitionErr.To),
						"legal_transitions": legal,
					}))
			return
		}
		if validationErr, ok := workspace.IsTicketValidationError(err); ok {
			_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
				orihttp.NewAPIError(orihttp.ErrCodeValidation, validationErr.Message).
					WithDetails(map[string]string{"field": validationErr.Field}))
			return
		}

		logger.Error("Ticket operation failed", logger.Fields{
			"error": err, "action": action, "studio_id": studioID, "ticket_id": ticketID,
		})
		// Anything unclassified is still a client-actionable request problem
		// far more often than a server fault (an unknown workspace, a
		// malformed value the service rejected), so it stays a 400 rather
		// than masquerading as a 500.
		orihttp.BadRequest(w, err.Error())
	}
}

// --- Small parsing helpers ------------------------------------------------

func parseOptionalBool(raw string) (bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, nil
	}
	return strconv.ParseBool(trimmed)
}

func parseOptionalVersion(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	return strconv.ParseInt(trimmed, 10, 64)
}

// readRequestBody reads a bounded request body so PATCH can decode it twice —
// once into the typed request and once into a presence map. It applies the
// same size guard as orihttp.ParseJSONBody, which reads the body itself and
// so cannot be reused here.
func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errEmptyRequestBody
	}
	r.Body = http.MaxBytesReader(w, r.Body, orihttp.MaxJSONBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errEmptyRequestBody
	}
	return body, nil
}

var errEmptyRequestBody = errors.New("request body is required")
