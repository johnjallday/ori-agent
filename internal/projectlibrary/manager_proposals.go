package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ManagerProposal is an inert, Home-owned suggestion. It is NOT a field review,
// a confirmation token, an activation, a child handoff or a source grant.
// Only an owner can request a separate canonical field review/commit.
type ManagerProposal struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind,omitempty"` // empty: legacy next_action; project_review: navigation only
	EntryID         string    `json:"entry_id"`
	EntryRevision   int64     `json:"entry_revision,omitempty"`
	FieldsRevision  int64     `json:"fields_revision"`
	BindingRevision int64     `json:"binding_revision"`
	AgentInstanceID string    `json:"agent_instance_id"`
	AgentName       string    `json:"agent_name"`
	NextAction      string    `json:"next_action"`
	Reason          string    `json:"reason,omitempty"`
	Digest          string    `json:"digest"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type ManagerProposalRow struct {
	Proposal ManagerProposal `json:"proposal"`
	Name     string          `json:"name"`
	Status   string          `json:"status"` // ready, stale, expired, unavailable
}

type ManagerProposalPage struct {
	Revision int64                `json:"revision"`
	Rows     []ManagerProposalRow `json:"rows"`
	Total    int                  `json:"total"`
}

func proposalDigest(scope Scope, authority ManagerAuthority, entryID, nextAction, reason string, fieldsRev, bindingRev int64) string {
	data, _ := json.Marshal(struct {
		Scope      Scope            `json:"scope"`
		Authority  ManagerAuthority `json:"authority"`
		EntryID    string           `json:"entry_id"`
		FieldsRev  int64            `json:"fields_rev"`
		BindingRev int64            `json:"binding_rev"`
		NextAction string           `json:"next_action"`
		Reason     string           `json:"reason"`
	}{scope, authority, entryID, fieldsRev, bindingRev, nextAction, reason})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ProposeNextAction persists a short-lived suggestion under exact local
// Manager policy, with the Home revision used for a compare-and-swap. An
// agent-supplied request key permits exact retries, never a user commit.
func (s *Store) ProposeNextAction(authority ManagerAuthority, entryID string, fieldsRevision int64,
	nextAction, reason, requestKey string) (ManagerProposal, bool, error) {
	if !validText(entryID, 160) || entryID == "" || fieldsRevision < 0 ||
		!validText(nextAction, 240) || nextAction == "" || !validText(reason, 500) ||
		!validText(requestKey, 160) || requestKey == "" {
		return ManagerProposal{}, false, ErrConflict
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil || state == nil {
		return ManagerProposal{}, false, ErrUnavailable
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Fields.Revision != fieldsRevision {
		return ManagerProposal{}, false, ErrConflict
	}
	bindingRev := state.HomeBindings.StateRevision
	digest := proposalDigest(scope, authority, entryID, nextAction, reason, fieldsRevision, bindingRev)
	for _, receipt := range doc.Operations {
		if receipt.Key == requestKey {
			if receipt.Action != "propose_next_action" || receipt.Digest != digest {
				return ManagerProposal{}, false, ErrConflict
			}
			for _, proposal := range doc.Proposals {
				if proposal.ID == receipt.ConsequenceID && proposal.Digest == digest &&
					proposal.ExpiresAt.After(s.now().UTC()) {
					return proposal, true, nil
				}
			}
			return ManagerProposal{}, false, ErrConflict // Expired and pruned; never claim a saved replay.
		}
	}
	at := s.now().UTC()
	proposal := ManagerProposal{ID: newID(), EntryID: entryID, FieldsRevision: fieldsRevision,
		BindingRevision: bindingRev, AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName,
		NextAction: nextAction, Reason: reason, Digest: digest, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour)}
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: requestKey, action: "propose_next_action", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && boundManager(current, home, authority)
		}, func(current *Document) (string, error) {
			selected := sessionEntry(*current, entryID)
			if selected == nil || selected.Fields.Revision != fieldsRevision {
				return "", ErrConflict
			}
			// Pruning changes no catalog entries or review receipts. Expired
			// suggestions cannot be revived by an old agent request key.
			kept := current.Proposals[:0]
			for _, old := range current.Proposals {
				if old.ExpiresAt.After(at) {
					kept = append(kept, old)
				}
			}
			current.Proposals = kept
			if len(current.Proposals) >= maxProposals {
				return "", ErrLimit
			}
			current.Proposals = append(current.Proposals, proposal)
			return proposal.ID, nil
		})
	if err != nil {
		return ManagerProposal{}, false, err
	}
	if replay {
		// A concurrent exact-key retry won the Home CAS. Read its saved
		// proposal instead of returning this invocation's newly generated ID.
		fresh, readErr := s.Read(scope)
		if readErr != nil {
			return ManagerProposal{}, false, readErr
		}
		for _, row := range fresh.Proposals {
			if row.ID == receipt.ConsequenceID && row.Digest == digest {
				return row, true, nil
			}
		}
		return ManagerProposal{}, false, ErrConflict
	}
	return proposal, false, nil
}

// Project-review navigation has no creator token or file/root input. The
// Manager can only point the owner to Details; that surface must recheck the
// installed blueprint, approved source and canonical creator separately.
// ProposeProjectReview saves an inert, short-lived suggestion to visit the
// exact Home entry's current Details. Neither the proposal nor its replay
// reads a source, issues an activation review or grants creator permission.
func (s *Store) ProposeProjectReview(authority ManagerAuthority, entryID string, entryRevision int64,
	reason, requestKey string) (ManagerProposal, bool, error) {
	if entryID == "" || !validText(entryID, 160) || entryRevision < 1 ||
		!validText(reason, 500) || requestKey == "" || !validText(requestKey, 160) {
		return ManagerProposal{}, false, ErrConflict
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil || state == nil {
		return ManagerProposal{}, false, ErrUnavailable
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Revision != entryRevision || entry.Link != nil {
		return ManagerProposal{}, false, ErrConflict
	}
	bindingRev := state.HomeBindings.StateRevision
	digest := projectReviewProposalDigest(scope, authority, entryID, reason, entryRevision, bindingRev)
	for _, receipt := range doc.Operations {
		if receipt.Key != requestKey {
			continue
		}
		if receipt.Action != "propose_project_review" || receipt.Digest != digest {
			return ManagerProposal{}, false, ErrConflict
		}
		for _, proposal := range doc.Proposals {
			if proposal.ID == receipt.ConsequenceID && proposal.Digest == digest && proposal.ExpiresAt.After(s.now().UTC()) {
				return proposal, true, nil
			}
		}
		return ManagerProposal{}, false, ErrConflict
	}
	at := s.now().UTC()
	proposal := ManagerProposal{ID: newID(), Kind: "project_review", EntryID: entryID, EntryRevision: entryRevision,
		BindingRevision: bindingRev, AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName,
		Reason: reason, Digest: digest, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour)}
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: requestKey, action: "propose_project_review", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && boundManager(current, home, authority)
		}, func(current *Document) (string, error) {
			selected := sessionEntry(*current, entryID)
			if selected == nil || selected.Link != nil || selected.Revision != entryRevision {
				return "", ErrConflict
			}
			kept := current.Proposals[:0]
			for _, old := range current.Proposals {
				if old.ExpiresAt.After(at) {
					kept = append(kept, old)
				}
			}
			current.Proposals = kept
			if len(current.Proposals) >= maxProposals {
				return "", ErrLimit
			}
			current.Proposals = append(current.Proposals, proposal)
			return proposal.ID, nil
		})
	if err != nil {
		return ManagerProposal{}, false, err
	}
	if replay {
		fresh, readErr := s.Read(scope)
		if readErr != nil {
			return ManagerProposal{}, false, readErr
		}
		for _, row := range fresh.Proposals {
			if row.ID == receipt.ConsequenceID && row.Digest == digest {
				return row, true, nil
			}
		}
		return ManagerProposal{}, false, ErrConflict
	}
	return proposal, false, nil
}

func projectReviewProposalDigest(scope Scope, authority ManagerAuthority, entryID, reason string, entryRev, bindingRev int64) string {
	data, _ := json.Marshal(struct {
		Scope      Scope            `json:"scope"`
		Authority  ManagerAuthority `json:"authority"`
		EntryID    string           `json:"entry_id"`
		EntryRev   int64            `json:"entry_rev"`
		BindingRev int64            `json:"binding_rev"`
		Reason     string           `json:"reason"`
	}{scope, authority, entryID, entryRev, bindingRev, reason})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) managerProposalStatus(scope Scope, doc Document, proposal ManagerProposal) string {
	if !proposal.ExpiresAt.After(s.now().UTC()) {
		return "expired"
	}
	entry := sessionEntry(doc, proposal.EntryID)
	if entry == nil {
		return "stale"
	}
	if proposal.Kind == "project_review" {
		if entry.Link != nil || entry.Revision != proposal.EntryRevision {
			return "stale"
		}
	} else if entry.Fields.Revision != proposal.FieldsRevision {
		return "stale"
	}
	authority := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: proposal.AgentInstanceID,
		AgentName: proposal.AgentName}
	_, err := s.authorizeManager(authority)
	if err != nil {
		return "unavailable"
	}
	state, getErr := s.workspaces.Get(scope.HomeID)
	if getErr != nil || state == nil || state.GetAssistantProgramState() == nil ||
		state.GetAssistantProgramState().HomeBindings.StateRevision != proposal.BindingRevision {
		return "stale"
	}
	if proposal.Kind == "project_review" {
		if proposal.Digest != projectReviewProposalDigest(scope, authority, proposal.EntryID,
			proposal.Reason, proposal.EntryRevision, proposal.BindingRevision) {
			return "stale"
		}
	} else if proposal.Digest != proposalDigest(scope, authority, proposal.EntryID, proposal.NextAction,
		proposal.Reason, proposal.FieldsRevision, proposal.BindingRevision) {
		return "stale"
	}
	return "ready"
}

// ListManagerProposals is an owner-only, bounded historical projection. Reads
// do not prune or mutate; a disabled/unbound Manager cannot keep its suggestion
// actionable, and the Home still retains already saved user metadata.
func (s *Store) ListManagerProposals(scope Scope) (ManagerProposalPage, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerProposalPage{}, err
	}
	roots := make(map[string]Root, len(doc.Roots))
	for _, root := range doc.Roots {
		roots[root.ID] = root
	}
	linked := make(map[string]bool, len(state.LinkedProjectIDs))
	for _, id := range state.LinkedProjectIDs {
		linked[id] = true
	}
	inactive := make(map[string]bool, len(state.ProjectLibraryInactiveRoots))
	for _, id := range state.ProjectLibraryInactiveRoots {
		inactive[id] = true
	}
	page := ManagerProposalPage{Revision: doc.Revision, Rows: []ManagerProposalRow{}, Total: len(doc.Proposals)}
	for i := len(doc.Proposals) - 1; i >= 0 && len(page.Rows) < 20; i-- {
		proposal := doc.Proposals[i]
		name := "Saved project"
		if entry := sessionEntry(doc, proposal.EntryID); entry != nil {
			if projected := s.projectSearchRow(scope, *entry, roots, linked, inactive).Name; projected != "" {
				name = projected
			}
		}
		page.Rows = append(page.Rows, ManagerProposalRow{Proposal: proposal, Name: name,
			Status: s.managerProposalStatus(scope, doc, proposal)})
	}
	return page, nil
}

// ReviewProposedNextAction derives the patch from the *saved* proposal, not
// a browser patch or agent review token. ReviewFields creates the canonical
// owner-only receipt; no project files, tasks or children are read or changed.
func (s *Store) ReviewProposedNextAction(scope Scope, proposalID, author string) (FieldReview, error) {
	proposal, err := s.actionableProposal(scope, proposalID)
	if err != nil {
		return FieldReview{}, err
	}
	if proposal.Kind != "" {
		return FieldReview{}, ErrConflict // navigation never yields a field/creator review token
	}
	return s.ReviewFields(scope, proposal.EntryID, proposal.FieldsRevision,
		FieldsPatch{NextAction: &proposal.NextAction}, author)
}

func (s *Store) actionableProposal(scope Scope, proposalID string) (ManagerProposal, error) {
	if !validText(proposalID, 160) || proposalID == "" {
		return ManagerProposal{}, ErrConflict
	}
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerProposal{}, err
	}
	for _, proposal := range doc.Proposals {
		if proposal.ID == proposalID {
			if s.managerProposalStatus(scope, doc, proposal) != "ready" {
				return ManagerProposal{}, ErrConflict
			}
			return proposal, nil
		}
	}
	return ManagerProposal{}, ErrConflict
}

// CommitProposedNextAction repeats Manager/provider/field checks at the final
// owner gesture. The existing field review binds exact values and writes one
// Home CAS. The agent never receives this endpoint's review token.
func (s *Store) CommitProposedNextAction(scope Scope, proposalID, reviewToken, key, author string) (Entry, bool, error) {
	// A reply can be lost after the canonical field write succeeds. Permit
	// exact operation-key replay without reviving a stale proposal for a new
	// write; commitFields verifies the original review digest and receipt.
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return Entry{}, false, err
	}
	var saved *ManagerProposal
	for i := range doc.Proposals {
		if doc.Proposals[i].ID == proposalID {
			saved = &doc.Proposals[i]
			break
		}
	}
	if saved == nil || saved.Kind != "" {
		return Entry{}, false, ErrConflict
	}
	proposal := *saved
	for _, op := range doc.Operations {
		if op.Key != key {
			continue
		}
		if op.Action != "edit_fields" || op.ConsequenceID != proposal.EntryID {
			return Entry{}, false, ErrConflict
		}
		return s.commitFields(scope, proposal.EntryID, reviewToken, key, proposal.FieldsRevision,
			FieldsPatch{NextAction: &proposal.NextAction}, author, nil)
	}
	if _, err := s.actionableProposal(scope, proposalID); err != nil {
		return Entry{}, false, err
	}
	return s.commitFields(scope, proposal.EntryID, reviewToken, key, proposal.FieldsRevision,
		FieldsPatch{NextAction: &proposal.NextAction}, author,
		func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return state.HomeBindings.StateRevision == proposal.BindingRevision &&
				boundManager(state, home, ManagerAuthority{HomeID: scope.HomeID,
					AgentInstanceID: proposal.AgentInstanceID, AgentName: proposal.AgentName})
		})
}
