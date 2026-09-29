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
	ID               string     `json:"id"`
	Kind             string     `json:"kind,omitempty"` // empty: next_action; *_review: navigation; session_goal/session_recap: drafts
	EntryID          string     `json:"entry_id"`
	EntryRevision    int64      `json:"entry_revision,omitempty"`
	FieldsRevision   int64      `json:"fields_revision"`
	BindingRevision  int64      `json:"binding_revision"`
	AgentInstanceID  string     `json:"agent_instance_id"`
	AgentName        string     `json:"agent_name"`
	NextAction       string     `json:"next_action"`
	Goal             *GoalInput `json:"goal,omitempty"`
	GoalSessionCount int        `json:"goal_session_count,omitempty"`
	SessionID        string     `json:"session_id,omitempty"`
	SessionRevision  int64      `json:"session_revision,omitempty"`
	Recap            string     `json:"recap,omitempty"`
	RootSetDigest    string     `json:"root_set_digest,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	Digest           string     `json:"digest"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
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

// ProposeRootReview suggests only navigation to the owner's current discovery
// controls. It accepts no structured path, picker token, root ID or grant. The private
// root-set witness invalidates the suggestion when connections change; the
// Manager never receives the root list or its paths.
func (s *Store) ProposeRootReview(authority ManagerAuthority, reason, requestKey string) (ManagerProposal, bool, error) {
	if !validText(reason, 500) || requestKey == "" || !validText(requestKey, 160) {
		return ManagerProposal{}, false, ErrConflict
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	bindingRev := state.HomeBindings.StateRevision
	roots := managerRootSetDigest(doc.Roots)
	digest := rootReviewProposalDigest(scope, authority, reason, roots, bindingRev)
	for _, receipt := range doc.Operations {
		if receipt.Key != requestKey {
			continue
		}
		if receipt.Action != "propose_root_review" || receipt.Digest != digest {
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
	proposal := ManagerProposal{ID: newID(), Kind: "root_review", BindingRevision: bindingRev,
		AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName, RootSetDigest: roots,
		Reason: reason, Digest: digest, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour)}
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: requestKey, action: "propose_root_review", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && boundManager(current, home, authority)
		}, func(current *Document) (string, error) {
			if managerRootSetDigest(current.Roots) != roots {
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

// Only IDs, revisions and revocation state are hashed. No root path, source
// file, picker reference or review receipt is copied into the proposal.
func managerRootSetDigest(roots []Root) string {
	rows := make([]struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		Revoked  bool   `json:"revoked"`
	}, 0, len(roots))
	for _, root := range roots {
		rows = append(rows, struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
			Revoked  bool   `json:"revoked"`
		}{root.ID, root.Revision, root.RevokedAt != nil})
	}
	// The Home document preserves insertion order; new grants and revocations
	// change this witness, while unrelated notes or sessions do not.
	data, _ := json.Marshal(rows)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func rootReviewProposalDigest(scope Scope, authority ManagerAuthority, reason, rootSet string, bindingRev int64) string {
	data, _ := json.Marshal(struct {
		Scope      Scope            `json:"scope"`
		Authority  ManagerAuthority `json:"authority"`
		Reason     string           `json:"reason"`
		RootSet    string           `json:"root_set"`
		BindingRev int64            `json:"binding_rev"`
	}{scope, authority, reason, rootSet, bindingRev})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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

// ProposeSessionRecap saves only a suggested draft for one accepted Home
// session. No structured date, decision, blocker, Ticket citation or next-action
// update is proposed. Text is untrusted, not DAW evidence or a completion claim;
// the owner still edits, reviews and confirms the canonical recap.
func (s *Store) ProposeSessionRecap(authority ManagerAuthority, entryID string, entryRevision, fieldsRevision int64,
	sessionID string, sessionRevision int64, recap, reason, requestKey string) (ManagerProposal, bool, error) {
	if entryID == "" || !validText(entryID, 160) || entryRevision < 1 || fieldsRevision < 0 ||
		sessionID == "" || !validText(sessionID, 160) || sessionRevision < 1 ||
		!(RecapInput{Recap: recap}).valid() || !validText(reason, 500) ||
		requestKey == "" || !validText(requestKey, 160) {
		return ManagerProposal{}, false, ErrConflict
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	if !matchesRecapProposal(doc, entryID, entryRevision, fieldsRevision, sessionID, sessionRevision) {
		return ManagerProposal{}, false, ErrConflict
	}
	bindingRev := state.HomeBindings.StateRevision
	digest := sessionRecapProposalDigest(scope, authority, entryID, sessionID, entryRevision,
		fieldsRevision, sessionRevision, recap, reason, bindingRev)
	for _, receipt := range doc.Operations {
		if receipt.Key != requestKey {
			continue
		}
		if receipt.Action != "propose_session_recap" || receipt.Digest != digest {
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
	proposal := ManagerProposal{ID: newID(), Kind: "session_recap", EntryID: entryID,
		EntryRevision: entryRevision, FieldsRevision: fieldsRevision, SessionID: sessionID,
		SessionRevision: sessionRevision, Recap: recap, BindingRevision: bindingRev,
		AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName,
		Reason: reason, Digest: digest, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour)}
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: requestKey, action: "propose_session_recap", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && boundManager(current, home, authority)
		}, func(current *Document) (string, error) {
			if !matchesRecapProposal(*current, entryID, entryRevision, fieldsRevision, sessionID, sessionRevision) {
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

func matchesRecapProposal(doc Document, entryID string, entryRev, fieldsRev int64, sessionID string, sessionRev int64) bool {
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Revision != entryRev || entry.Fields.Revision != fieldsRev {
		return false
	}
	for _, session := range doc.Sessions {
		if session.ID == sessionID && session.EntryID == entryID && session.Revision == sessionRev &&
			session.State == "accepted" && session.Recap == "" {
			return true
		}
	}
	return false
}

func sessionRecapProposalDigest(scope Scope, authority ManagerAuthority, entryID, sessionID string,
	entryRev, fieldsRev, sessionRev int64, recap, reason string, bindingRev int64) string {
	data, _ := json.Marshal(struct {
		Scope      Scope            `json:"scope"`
		Authority  ManagerAuthority `json:"authority"`
		EntryID    string           `json:"entry_id"`
		SessionID  string           `json:"session_id"`
		EntryRev   int64            `json:"entry_rev"`
		FieldsRev  int64            `json:"fields_rev"`
		SessionRev int64            `json:"session_rev"`
		Recap      string           `json:"recap"`
		Reason     string           `json:"reason"`
		BindingRev int64            `json:"binding_rev"`
	}{scope, authority, entryID, sessionID, entryRev, fieldsRev, sessionRev, recap, reason, bindingRev})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ProposeSessionGoal stores a bounded suggestion, not a goal review or session.
// Planned dates are owner-entered only. A new accepted Home goal invalidates
// this draft even when the catalog entry revision did not change.
func (s *Store) ProposeSessionGoal(authority ManagerAuthority, entryID string, entryRevision int64,
	goal GoalInput, reason, requestKey string) (ManagerProposal, bool, error) {
	if entryID == "" || !validText(entryID, 160) || entryRevision < 1 || !goal.valid() || goal.PlannedDate != "" ||
		!validText(reason, 500) || requestKey == "" || !validText(requestKey, 160) {
		return ManagerProposal{}, false, ErrConflict
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Revision != entryRevision {
		return ManagerProposal{}, false, ErrConflict
	}
	count := 0
	for _, session := range doc.Sessions {
		if session.EntryID == entryID {
			count++
		}
	}
	bindingRev := state.HomeBindings.StateRevision
	digest := sessionGoalProposalDigest(scope, authority, entryID, goal, reason, entryRevision, bindingRev, count)
	for _, receipt := range doc.Operations {
		if receipt.Key != requestKey {
			continue
		}
		if receipt.Action != "propose_session_goal" || receipt.Digest != digest {
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
	proposal := ManagerProposal{ID: newID(), Kind: "session_goal", EntryID: entryID, EntryRevision: entryRevision,
		BindingRevision: bindingRev, AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName,
		Goal: &goal, GoalSessionCount: count, Reason: reason, Digest: digest, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour)}
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: requestKey, action: "propose_session_goal", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && boundManager(current, home, authority)
		}, func(current *Document) (string, error) {
			selected := sessionEntry(*current, entryID)
			if selected == nil || selected.Revision != entryRevision {
				return "", ErrConflict
			}
			currentCount := 0
			for _, session := range current.Sessions {
				if session.EntryID == entryID {
					currentCount++
				}
			}
			if currentCount != count {
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

func sessionGoalProposalDigest(scope Scope, authority ManagerAuthority, entryID string, goal GoalInput, reason string,
	entryRev, bindingRev int64, count int) string {
	data, _ := json.Marshal(struct {
		Scope        Scope            `json:"scope"`
		Authority    ManagerAuthority `json:"authority"`
		EntryID      string           `json:"entry_id"`
		EntryRev     int64            `json:"entry_rev"`
		BindingRev   int64            `json:"binding_rev"`
		Goal         GoalInput        `json:"goal"`
		Reason       string           `json:"reason"`
		SessionCount int              `json:"session_count"`
	}{scope, authority, entryID, entryRev, bindingRev, goal, reason, count})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
	if proposal.Kind == "root_review" {
		if managerRootSetDigest(doc.Roots) != proposal.RootSetDigest {
			return "stale"
		}
	} else {
		entry := sessionEntry(doc, proposal.EntryID)
		if entry == nil {
			return "stale"
		}
		if proposal.Kind == "session_recap" {
			if !matchesRecapProposal(doc, proposal.EntryID, proposal.EntryRevision, proposal.FieldsRevision,
				proposal.SessionID, proposal.SessionRevision) {
				return "stale"
			}
		} else if proposal.Kind == "project_review" {
			if entry.Link != nil || entry.Revision != proposal.EntryRevision {
				return "stale"
			}
		} else if proposal.Kind == "session_goal" {
			if entry.Revision != proposal.EntryRevision {
				return "stale"
			}
			count := 0
			for _, session := range doc.Sessions {
				if session.EntryID == proposal.EntryID {
					count++
				}
			}
			if count != proposal.GoalSessionCount {
				return "stale"
			}
		} else if entry.Fields.Revision != proposal.FieldsRevision {
			return "stale"
		}
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
	if proposal.Kind == "root_review" {
		if proposal.Digest != rootReviewProposalDigest(scope, authority, proposal.Reason,
			proposal.RootSetDigest, proposal.BindingRevision) {
			return "stale"
		}
	} else if proposal.Kind == "project_review" {
		if proposal.Digest != projectReviewProposalDigest(scope, authority, proposal.EntryID,
			proposal.Reason, proposal.EntryRevision, proposal.BindingRevision) {
			return "stale"
		}
	} else if proposal.Kind == "session_recap" {
		if proposal.Digest != sessionRecapProposalDigest(scope, authority, proposal.EntryID, proposal.SessionID,
			proposal.EntryRevision, proposal.FieldsRevision, proposal.SessionRevision, proposal.Recap,
			proposal.Reason, proposal.BindingRevision) {
			return "stale"
		}
	} else if proposal.Kind == "session_goal" {
		if proposal.Goal == nil || proposal.Digest != sessionGoalProposalDigest(scope, authority, proposal.EntryID,
			*proposal.Goal, proposal.Reason, proposal.EntryRevision, proposal.BindingRevision, proposal.GoalSessionCount) {
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
	return ManagerProposalPage{Revision: doc.Revision, Rows: s.managerProposalRows(scope, doc, state),
		Total: len(doc.Proposals)}, nil
}

// managerProposalRows projects the newest 20 proposals from one snapshot.
// The shelf, the summary badge and the Action Center count the same rows.
func (s *Store) managerProposalRows(scope Scope, doc Document, state *workspace.AssistantProgramState) []ManagerProposalRow {
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
	rows := []ManagerProposalRow{}
	for i := len(doc.Proposals) - 1; i >= 0 && len(rows) < 20; i-- {
		proposal := doc.Proposals[i]
		name := "Saved project"
		if proposal.Kind == "root_review" {
			name = "Discovery folders"
		}
		if entry := sessionEntry(doc, proposal.EntryID); entry != nil {
			if projected := s.projectSearchRow(scope, *entry, roots, linked, inactive).Name; projected != "" {
				name = projected
			}
		}
		rows = append(rows, ManagerProposalRow{Proposal: proposal, Name: name,
			Status: s.managerProposalStatus(scope, doc, proposal)})
	}
	return rows
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
