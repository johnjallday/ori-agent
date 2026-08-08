package githubhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// workspaceRepoResolver adapts the workspace store to the broker's
// RepoResolver, so the broker reads the binding rather than caching it. A
// cached repository would let a proposal execute against a binding the
// workspace no longer has.
// It resolves the store on every call rather than capturing it. Server
// construction wires HTTP handlers (phase 17) before the workspace store
// exists (phase 18), so a resolver built with the store passed by value would
// capture nil and silently answer "no binding" forever -- which here means the
// proposal routes never register and the write path is quietly absent.
type workspaceRepoResolver struct {
	workspaces func() WorkspaceStore
}

// NewRepoResolver builds the broker's view of workspace repository bindings.
// The store is supplied as a function so it can be resolved after the builder
// has finished wiring it.
func NewRepoResolver(workspaces func() WorkspaceStore) RepoResolver {
	return &workspaceRepoResolver{workspaces: workspaces}
}

func (r *workspaceRepoResolver) BoundRepo(workspaceID string) (string, bool) {
	if r == nil || r.workspaces == nil {
		return "", false
	}
	store := r.workspaces()
	if store == nil {
		return "", false
	}
	ws, err := store.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return "", false
	}
	return BoundRepo(ws)
}

// LinkedWorkspace is one workspace depending on the global connection.
type LinkedWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Repo string `json:"repo"`
}

// WorkspaceLister enumerates workspaces so the connection surface can report
// which ones depend on it.
type WorkspaceLister interface {
	ListWorkspaceIDs() []string
}

// linkedWorkspaces returns every workspace bound to a GitHub repository.
//
// It is derived by walking the workspaces rather than kept as a list on the
// connection, for the same reason nothing else about the connection is
// cached: a stored list drifts the moment a workspace is deleted or
// rebound, and the first symptom would be a disconnect warning naming
// workspaces that no longer exist.
func (h *Handler) linkedWorkspaces() []LinkedWorkspace {
	if h == nil || h.workspaces == nil || h.lister == nil {
		return nil
	}
	var linked []LinkedWorkspace
	for _, id := range h.lister.ListWorkspaceIDs() {
		ws, err := h.workspaces.GetFolderWorkspace(id)
		if err != nil || ws == nil {
			continue
		}
		repo, ok := BoundRepo(ws)
		if !ok {
			continue
		}
		linked = append(linked, LinkedWorkspace{ID: ws.ID, Name: ws.Name, Repo: repo})
	}
	return linked
}

// linked serves GET /api/connections/github/linked — the workspaces that stop
// working if this connection goes away. It is what the Settings card warns
// with before a disconnect.
func (h *Handler) linked(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workspaces := h.linkedWorkspaces()
	if workspaces == nil {
		workspaces = []LinkedWorkspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": workspaces})
}

// RegisterProposalRoutes wires the proposal surface.
//
// There is no "apply" endpoint and no "propose and apply" shortcut: the only
// route from a proposal to GitHub is confirm, and it requires the hash of the
// content the user was shown.
func (h *Handler) RegisterProposalRoutes(mux *http.ServeMux) {
	if h == nil || h.broker == nil {
		return
	}
	mux.Handle("GET /api/workspaces/{workspaceID}/github/proposals",
		h.wrap(http.HandlerFunc(h.listProposals)))
	mux.Handle("POST /api/workspaces/{workspaceID}/github/proposals/{proposalID}/confirm",
		h.wrap(http.HandlerFunc(h.confirmProposal)))
	mux.Handle("POST /api/workspaces/{workspaceID}/github/proposals/{proposalID}/reject",
		h.wrap(http.HandlerFunc(h.rejectProposal)))
}

func (h *Handler) listProposals(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	proposals := h.broker.List(workspaceID)
	if proposals == nil {
		proposals = []*Proposal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": proposals})
}

func (h *Handler) confirmProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))

	var req struct {
		// ExpectedHash binds this approval to the exact content the user
		// was shown. It is required: without it, "you approved this
		// specific change" would be an assumption rather than a check.
		ExpectedHash string `json:"expected_hash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_request",
			"message": "Could not read the request.",
		})
		return
	}

	proposal, err := h.broker.Confirm(r.Context(), workspaceID, proposalID, req.ExpectedHash)
	if err != nil {
		writeProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposal": proposal})
}

func (h *Handler) rejectProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))

	proposal, err := h.broker.Reject(workspaceID, proposalID)
	if err != nil {
		writeProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposal": proposal})
}

// writeProposalError maps broker failures onto HTTP, with copy written for
// someone who just clicked Approve.
func writeProposalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrProposalNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error":   "proposal_not_found",
			"message": "That proposed change no longer exists.",
		})
	case errors.Is(err, ErrProposalNotDraft):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "proposal_not_pending",
			"message": "That change has already been handled.",
		})
	case errors.Is(err, ErrProposalExpired):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "proposal_expired",
			"message": "That proposal is too old to apply. Ask for a fresh one.",
		})
	case errors.Is(err, ErrProposalChanged):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "proposal_changed",
			"message": "This proposal changed since you reviewed it. Read it again before approving.",
		})
	case errors.Is(err, ErrProposalRepo):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "proposal_repo_mismatch",
			"message": "This workspace is no longer bound to that repository, so the change was not applied.",
		})
	default:
		// A write that reached GitHub and failed there: the executor's
		// message is already plain language written for this moment.
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "proposal_failed",
			"message": strings.TrimPrefix(err.Error(), "github: "),
		})
	}
}

// Broker exposes the proposal broker so server wiring can hand it to the
// workspace tools that create proposals.
func (h *Handler) Broker() *Broker {
	if h == nil {
		return nil
	}
	return h.broker
}

// compile-time assertion that the resolver satisfies the broker's contract.
var _ RepoResolver = (*workspaceRepoResolver)(nil)
