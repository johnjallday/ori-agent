package reaperhttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/reaper"
)

const maxScriptProposals = 64

var (
	ErrProposalUnavailable = errors.New("REAPER script proposal is unavailable")
	ErrProposalLimit       = errors.New("too many REAPER script proposals")
)

type ScriptProposal struct {
	ID                 string                  `json:"id"`
	WorkspaceID        string                  `json:"workspace_id"`
	Filename           string                  `json:"filename"`
	Name               string                  `json:"name"`
	Description        string                  `json:"description"`
	NeedsConfirmation  bool                    `json:"needs_confirmation"`
	Code               string                  `json:"code"`
	TestedSuccessfully bool                    `json:"tested_successfully"`
	LastRun            *reaper.ScriptRunResult `json:"last_run,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
	agentInstanceID    string
}

func (p ScriptProposal) Input() reaper.ScriptInput {
	return reaper.ScriptInput{
		Filename: p.Filename, Name: p.Name, Description: p.Description,
		NeedsConfirmation: p.NeedsConfirmation, Code: p.Code,
	}
}

type proposalStore struct {
	mu      sync.Mutex
	entries map[string]ScriptProposal
	now     func() time.Time
}

func newProposalStore() *proposalStore {
	return &proposalStore{entries: make(map[string]ScriptProposal), now: time.Now}
}

func (s *proposalStore) create(workspaceID, agentInstanceID string, input reaper.ScriptInput) (ScriptProposal, error) {
	if s == nil || reaper.ValidateScriptInput(input) != nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(agentInstanceID) == "" {
		return ScriptProposal{}, reaper.ErrScriptInvalid
	}
	id, err := proposalID()
	if err != nil {
		return ScriptProposal{}, ErrProposalUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= maxScriptProposals {
		return ScriptProposal{}, ErrProposalLimit
	}
	proposal := ScriptProposal{
		ID: id, WorkspaceID: strings.TrimSpace(workspaceID), agentInstanceID: strings.TrimSpace(agentInstanceID),
		Filename: input.Filename, Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description),
		NeedsConfirmation: input.NeedsConfirmation, Code: input.Code, CreatedAt: s.now().UTC(),
	}
	s.entries[id] = proposal
	return cloneProposal(proposal), nil
}

func (s *proposalStore) list(workspaceID string) []ScriptProposal {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposals := make([]ScriptProposal, 0)
	for _, proposal := range s.entries {
		if proposal.WorkspaceID == strings.TrimSpace(workspaceID) {
			proposals = append(proposals, cloneProposal(proposal))
		}
	}
	sort.SliceStable(proposals, func(i, j int) bool { return proposals[i].CreatedAt.Before(proposals[j].CreatedAt) })
	return proposals
}

func (s *proposalStore) get(workspaceID, id string) (ScriptProposal, bool) {
	if s == nil {
		return ScriptProposal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, found := s.entries[strings.TrimSpace(id)]
	if !found || proposal.WorkspaceID != strings.TrimSpace(workspaceID) {
		return ScriptProposal{}, false
	}
	return cloneProposal(proposal), true
}

func (s *proposalStore) recordRun(workspaceID, id string, result reaper.ScriptRunResult) (ScriptProposal, bool) {
	if s == nil {
		return ScriptProposal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, found := s.entries[strings.TrimSpace(id)]
	if !found || proposal.WorkspaceID != strings.TrimSpace(workspaceID) {
		return ScriptProposal{}, false
	}
	copyResult := result
	proposal.LastRun = &copyResult
	if result.Outcome == "ok" {
		proposal.TestedSuccessfully = true
	}
	s.entries[proposal.ID] = proposal
	return cloneProposal(proposal), true
}

func (s *proposalStore) remove(workspaceID, id string) (ScriptProposal, bool) {
	if s == nil {
		return ScriptProposal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, found := s.entries[strings.TrimSpace(id)]
	if !found || proposal.WorkspaceID != strings.TrimSpace(workspaceID) {
		return ScriptProposal{}, false
	}
	delete(s.entries, proposal.ID)
	return cloneProposal(proposal), true
}

func cloneProposal(proposal ScriptProposal) ScriptProposal {
	if proposal.LastRun != nil {
		result := *proposal.LastRun
		proposal.LastRun = &result
	}
	return proposal
}

func proposalID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (h *Handler) proposeScript(workspaceID, agentInstanceID string, input reaper.ScriptInput) (ScriptProposal, error) {
	if h == nil || h.proposals == nil || !h.agentHasLiveControlGrant(workspaceID, agentInstanceID) {
		return ScriptProposal{}, ErrAgentRuntimeGrantRequired
	}
	return h.proposals.create(workspaceID, agentInstanceID, input)
}

func (h *Handler) ListProposals(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	_ = orihttp.RespondSuccess(w, h.proposals.list(ws.ID))
}

func (h *Handler) RunProposal(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	request, ok := decodeActionRunRequest(w, r)
	if !ok {
		return
	}
	response, status := h.runProposal(r.Context(), ws.ID, r.PathValue("proposalID"), request.Confirmed)
	_ = orihttp.RespondJSON(w, status, response)
}

type DraftRunResponse struct {
	ProposalID         string `json:"proposal_id"`
	Outcome            string `json:"outcome"`
	Code               string `json:"code,omitempty"`
	ErrorText          string `json:"error_text,omitempty"`
	TestedSuccessfully bool   `json:"tested_successfully"`
}

func (h *Handler) runProposal(ctx context.Context, workspaceID, proposalID string, confirmed bool) (DraftRunResponse, int) {
	response := DraftRunResponse{ProposalID: strings.TrimSpace(proposalID), Outcome: "error"}
	proposal, found := h.proposals.get(workspaceID, proposalID)
	if !found {
		response.Code = "reaper_proposal_not_found"
		response.ErrorText = "The script proposal was not found."
		return response, http.StatusNotFound
	}
	if !h.agentHasLiveControlGrant(workspaceID, proposal.agentInstanceID) {
		response.Code = "reaper_grant_required"
		response.ErrorText = "The proposing agent no longer has REAPER access. Nothing was run."
		return response, http.StatusForbidden
	}
	if proposal.NeedsConfirmation && !confirmed {
		response.Outcome = "confirmation_required"
		response.Code = "reaper_confirmation_required"
		response.ErrorText = "Confirm this draft script before running it in REAPER."
		return response, http.StatusConflict
	}
	if h.scriptRunner == nil {
		response.Code = "reaper_runner_unavailable"
		response.ErrorText = "The REAPER script runner is unavailable."
		return response, http.StatusServiceUnavailable
	}
	result, runErr := h.scriptRunner.RunScript(ctx, proposal.Code)
	updated, _ := h.proposals.recordRun(workspaceID, proposalID, result)
	response.Outcome = result.Outcome
	response.ErrorText = result.ErrorText
	response.TestedSuccessfully = updated.TestedSuccessfully
	if runErr != nil || result.Outcome != "ok" {
		response.Code = "reaper_script_failed"
		if response.ErrorText == "" {
			response.ErrorText = "The draft script failed in REAPER."
		}
		return response, http.StatusBadGateway
	}
	return response, http.StatusOK
}

func (h *Handler) SaveProposal(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if h.scriptLibrary == nil {
		h.respondUnavailable(w)
		return
	}
	request, ok := decodeActionRunRequest(w, r)
	if !ok {
		return
	}
	proposal, found := h.proposals.get(ws.ID, r.PathValue("proposalID"))
	if !found {
		_ = orihttp.RespondAPIError(w, http.StatusNotFound,
			orihttp.NewAPIError("reaper_proposal_not_found", "The script proposal was not found."))
		return
	}
	if !request.Confirmed {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"outcome": "confirmation_required", "code": "reaper_global_save_confirmation_required",
			"message":             "Saving makes this script available in every REAPER workspace on this Mac.",
			"tested_successfully": proposal.TestedSuccessfully,
		})
		return
	}
	script, err := h.scriptLibrary.Create(proposal.Input())
	if err != nil {
		h.respondScriptError(w, err)
		return
	}
	h.proposals.remove(ws.ID, proposal.ID)
	_ = orihttp.RespondCreated(w, map[string]any{
		"outcome": "saved", "script": script, "tested_successfully": proposal.TestedSuccessfully,
	})
}

func (h *Handler) DiscardProposal(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if _, found := h.proposals.remove(ws.ID, r.PathValue("proposalID")); !found {
		_ = orihttp.RespondAPIError(w, http.StatusNotFound,
			orihttp.NewAPIError("reaper_proposal_not_found", "The script proposal was not found."))
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"discarded": true})
}
