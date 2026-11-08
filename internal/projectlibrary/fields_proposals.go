package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ProposalKindFields is the agent-proposed fields patch (PRD #792 FR17).
const ProposalKindFields = "fields"

// Who proposed a fields suggestion (FR18).
const (
	ProposerManager = "manager"
	ProposerKeeper  = "keeper"
)

// fieldsProposalExpiry is the same 24 hours every Home suggestion lives.
const fieldsProposalExpiry = 24 * time.Hour

// ErrUndeclaredField refuses a plugin field the Home's package does not
// declare (FR17, FR25).
var ErrUndeclaredField = errors.New("projectlibrary: field is not declared by the Home's package")

// ProposedFields is the sparse patch an agent may suggest: only next action,
// blockers, deliverables, milestones and the package's declared fields. Lists
// replace the whole list; Extra replaces only the ids it names. Stage,
// status, priority, purpose, dates and archive state are not in the type at
// all, so they cannot be proposed.
type ProposedFields struct {
	NextAction   *string             `json:"next_action,omitempty"`
	Blockers     *[]string           `json:"blockers,omitempty"`
	Deliverables *[]string           `json:"deliverables,omitempty"`
	Milestones   *[]Milestone        `json:"milestones,omitempty"`
	Extra        map[string][]string `json:"extra,omitempty"`
}

// valid enforces FR17's bounds without the declaration: at least one field,
// a non-empty next action of at most 240 characters, lists of at most 16
// lines of 1 to 240 characters, milestones as the stored ones, and plugin
// fields within the storage limits.
func (p ProposedFields) valid() bool {
	if p.NextAction == nil && p.Blockers == nil && p.Deliverables == nil && p.Milestones == nil && len(p.Extra) == 0 {
		return false
	}
	if p.NextAction != nil && (*p.NextAction == "" || !validText(*p.NextAction, 240)) {
		return false
	}
	for _, list := range []*[]string{p.Blockers, p.Deliverables} {
		if list == nil {
			continue
		}
		if len(*list) > 16 {
			return false
		}
		for _, item := range *list {
			if item == "" || !validText(item, 240) {
				return false
			}
		}
	}
	if p.Milestones != nil {
		if len(*p.Milestones) > 16 {
			return false
		}
		seen := map[string]bool{}
		for _, milestone := range *p.Milestones {
			if milestone.ID == "" || seen[milestone.ID] || !validText(milestone.ID, 160) ||
				milestone.Label == "" || !validText(milestone.Label, 240) || !validDate(milestone.DueDate) {
				return false
			}
			seen[milestone.ID] = true
		}
	}
	if p.Extra != nil && (len(p.Extra) == 0 || !validExtraFields(p.Extra)) {
		return false
	}
	return true
}

// patch is the owner's field patch the suggestion becomes against the
// entry's stored fields: Extra merges the proposed ids into the stored map,
// so other declared fields and any id the package no longer declares stay as
// they are. The merge is idempotent, so a review, its commit and a replayed
// commit digest the same patch.
func (p ProposedFields) patch(stored Fields) FieldsPatch {
	patch := FieldsPatch{}
	if p.NextAction != nil {
		value := *p.NextAction
		patch.NextAction = &value
	}
	if p.Blockers != nil {
		value := append([]string{}, (*p.Blockers)...)
		patch.Blockers = &value
	}
	if p.Deliverables != nil {
		value := append([]string{}, (*p.Deliverables)...)
		patch.Deliverables = &value
	}
	if p.Milestones != nil {
		value := append([]Milestone{}, (*p.Milestones)...)
		patch.Milestones = &value
	}
	if len(p.Extra) > 0 {
		merged := cloneExtraFields(stored.Extra)
		if merged == nil {
			merged = map[string][]string{}
		}
		for id, lines := range p.Extra {
			merged[id] = append([]string(nil), lines...)
		}
		patch.Extra = &merged
	}
	return patch
}

func (p ProposedFields) clone() ProposedFields {
	clone := ProposedFields{Extra: cloneExtraFields(p.Extra)}
	if p.NextAction != nil {
		value := *p.NextAction
		clone.NextAction = &value
	}
	if p.Blockers != nil {
		value := append([]string{}, (*p.Blockers)...)
		clone.Blockers = &value
	}
	if p.Deliverables != nil {
		value := append([]string{}, (*p.Deliverables)...)
		clone.Deliverables = &value
	}
	if p.Milestones != nil {
		value := append([]Milestone{}, (*p.Milestones)...)
		clone.Milestones = &value
	}
	return clone
}

// fieldsProposer is who a fields suggestion speaks for. It is part of the
// digest, so a suggestion cannot change hands.
type fieldsProposer struct {
	Kind            string `json:"kind"`
	WorkspaceID     string `json:"workspace_id"`
	AgentInstanceID string `json:"agent_instance_id"`
	AgentName       string `json:"agent_name"`
}

func proposerOf(proposal ManagerProposal) fieldsProposer {
	return fieldsProposer{Kind: proposal.ProposerKind, WorkspaceID: proposal.ProposerWorkspaceID,
		AgentInstanceID: proposal.AgentInstanceID, AgentName: proposal.AgentName}
}

func fieldsProposalDigest(scope Scope, proposer fieldsProposer, entryID string, fieldsRev, bindingRev int64,
	proposed ProposedFields, reason string) string {
	data, _ := json.Marshal(struct {
		Kind       string         `json:"kind"`
		Scope      Scope          `json:"scope"`
		Proposer   fieldsProposer `json:"proposer"`
		EntryID    string         `json:"entry_id"`
		FieldsRev  int64          `json:"fields_rev"`
		BindingRev int64          `json:"binding_rev"`
		Proposed   ProposedFields `json:"proposed"`
		Reason     string         `json:"reason"`
	}{ProposalKindFields, scope, proposer, entryID, fieldsRev, bindingRev, proposed, reason})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fieldsProposalShapeValid is the saved document's check of a fields row.
func fieldsProposalShapeValid(proposal ManagerProposal) bool {
	if proposal.NextAction != "" || proposal.SessionID != "" || proposal.SessionRevision != 0 || proposal.Recap != "" ||
		proposal.RootSetDigest != "" || proposal.EntryRevision != 0 || proposal.Goal != nil || proposal.GoalSessionCount != 0 ||
		proposal.ProposedFields == nil || !proposal.ProposedFields.valid() ||
		(proposal.ProposerKind != ProposerManager && proposal.ProposerKind != ProposerKeeper) ||
		proposal.ProposerWorkspaceID == "" || !validText(proposal.ProposerWorkspaceID, 160) ||
		(proposal.WithdrawnAt != nil && proposal.WithdrawnAt.Before(proposal.CreatedAt)) {
		return false
	}
	// A keeper never files under a scan turn.
	return proposal.ProposerKind == ProposerManager || proposal.ScanID == ""
}

// ProposeFieldsAsManager saves the Home's bound Manager's fields suggestion
// for one entry (FR19). A plugin field the Home's words snapshot does not
// declare is refused.
func (s *Store) ProposeFieldsAsManager(authority ManagerAuthority, entryID string, fieldsRevision int64,
	proposed ProposedFields, reason, requestKey string) (ManagerProposal, bool, error) {
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	proposer := fieldsProposer{Kind: ProposerManager, WorkspaceID: scope.HomeID,
		AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName}
	return s.proposeFields(scope, proposer, authority, entryID, fieldsRevision, proposed, reason, requestKey,
		func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return boundManager(state, home, authority)
		})
}

// proposeFields is the one fenced write behind both proposers: the request
// key permits exact retries, the entry's fields revision is compared inside
// the write, expired rows are pruned, the Home cap and a scan turn's cap and
// dedupe apply, and the same proposer kind's waiting suggestion for the entry
// is withdrawn in the same write (FR22). Plugin fields are checked against the
// Home's words snapshot, the pinned release's words (FR9).
func (s *Store) proposeFields(scope Scope, proposer fieldsProposer, authority ManagerAuthority, entryID string,
	fieldsRevision int64, proposed ProposedFields, reason, requestKey string,
	livePolicy func(*workspace.AssistantProgramState, *workspace.Workspace) bool) (ManagerProposal, bool, error) {
	if !validText(entryID, 160) || entryID == "" || fieldsRevision < 0 || !proposed.valid() ||
		!validText(reason, 500) || !validText(requestKey, 160) || requestKey == "" {
		return ManagerProposal{}, false, ErrConflict
	}
	proposed = proposed.clone()
	doc, state, err := s.readSnapshot(scope)
	if err != nil || state == nil {
		return ManagerProposal{}, false, ErrUnavailable
	}
	if len(proposed.Extra) > 0 && !DeclaredExtraFields(proposed.Extra, SnapshotWords(state)) {
		return ManagerProposal{}, false, ErrUndeclaredField
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Fields.Revision != fieldsRevision {
		return ManagerProposal{}, false, ErrConflict
	}
	bindingRev := state.HomeBindings.StateRevision
	if bindingRev < 1 {
		return ManagerProposal{}, false, ErrUnavailable // A Home with no staffed roles takes no suggestions.
	}
	digest := fieldsProposalDigest(scope, proposer, entryID, fieldsRevision, bindingRev, proposed, reason)
	for _, receipt := range doc.Operations {
		if receipt.Key == requestKey {
			if receipt.Action != "propose_fields" || receipt.Digest != digest {
				return ManagerProposal{}, false, ErrConflict
			}
			for _, saved := range doc.Proposals {
				if saved.ID == receipt.ConsequenceID && saved.Digest == digest && saved.ExpiresAt.After(s.now().UTC()) {
					return saved, true, nil
				}
			}
			return ManagerProposal{}, false, ErrConflict // Expired and pruned; never claim a saved replay.
		}
	}
	at := s.now().UTC()
	proposal := ManagerProposal{ID: newID(), Kind: ProposalKindFields, EntryID: entryID, FieldsRevision: fieldsRevision,
		BindingRevision: bindingRev, AgentInstanceID: proposer.AgentInstanceID, AgentName: proposer.AgentName,
		Reason: reason, Digest: digest, CreatedAt: at, ExpiresAt: at.Add(fieldsProposalExpiry),
		ProposedFields: &proposed, ProposerKind: proposer.Kind, ProposerWorkspaceID: proposer.WorkspaceID}
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: requestKey, action: "propose_fields", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && livePolicy(current, home)
		}, func(current *Document) (string, error) {
			selected := sessionEntry(*current, entryID)
			if selected == nil || selected.Fields.Revision != fieldsRevision {
				return "", ErrConflict
			}
			// A keeper's entry must still be its own song's (a relink in the
			// gap leaves nothing to save).
			if proposer.Kind == ProposerKeeper &&
				(selected.Link == nil || selected.Link.WorkspaceID != proposer.WorkspaceID) {
				return "", ErrUnavailable
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
			if proposer.Kind == ProposerKeeper {
				saves := 0
				for _, old := range current.Proposals {
					if old.Kind == ProposalKindFields && old.ProposerKind == ProposerKeeper &&
						old.ProposerWorkspaceID == proposer.WorkspaceID {
						saves++ // Live rows only: expired ones were pruned above.
					}
				}
				if saves >= keeperSavesPerDay {
					return "", ErrLimit
				}
			}
			if err := admitRunProposal(current, &proposal, authority, at); err != nil {
				return "", err
			}
			for i := range current.Proposals {
				old := &current.Proposals[i]
				if old.Kind == ProposalKindFields && old.EntryID == entryID && old.ProposerKind == proposer.Kind &&
					old.DismissedAt == nil && old.WithdrawnAt == nil {
					withdrawn := at
					old.WithdrawnAt = &withdrawn
				}
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

// proposerAvailable is the status rule's "can this proposer still be
// authorized": the bound Manager for the Manager's suggestions, the song's
// keeper for a keeper's (FR23).
func (s *Store) proposerAvailable(scope Scope, proposal ManagerProposal) bool {
	if proposal.Kind == ProposalKindFields && proposal.ProposerKind == ProposerKeeper {
		return s.keeperProposalAvailable(scope, proposal)
	}
	_, err := s.authorizeManager(ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: proposal.AgentInstanceID,
		AgentName: proposal.AgentName})
	return err == nil
}

// proposerLivePolicy is the commit-time recheck for a fields suggestion: the
// Home binding revision it was made under and its proposer still authorized.
func (s *Store) proposerLivePolicy(scope Scope, proposal ManagerProposal) func(*workspace.AssistantProgramState, *workspace.Workspace) bool {
	return func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
		if state.HomeBindings.StateRevision != proposal.BindingRevision {
			return false
		}
		if proposal.ProposerKind == ProposerKeeper {
			// The keeper's full authority was checked by actionableProposal
			// just before; inside the fence only the Home record is read.
			return keeperLivePolicy(proposal.ProposerWorkspaceID, scope.OwnerUserID)(state, home)
		}
		return boundManager(state, home, ManagerAuthority{HomeID: scope.HomeID,
			AgentInstanceID: proposal.AgentInstanceID, AgentName: proposal.AgentName})
	}
}

// savedProposal finds a proposal by ID in a snapshot.
func savedProposal(doc Document, proposalID string) (ManagerProposal, bool) {
	for _, proposal := range doc.Proposals {
		if proposal.ID == proposalID {
			return proposal, true
		}
	}
	return ManagerProposal{}, false
}

// ReviewProposedFields derives the owner's field patch from the *saved*
// suggestion, never the browser (FR25), after rechecking that every plugin
// field it names is still declared: one that is not is a stale suggestion.
func (s *Store) ReviewProposedFields(scope Scope, proposalID, author string, words Words) (FieldReview, error) {
	proposal, err := s.actionableProposal(scope, proposalID)
	if err != nil {
		return FieldReview{}, err
	}
	if proposal.Kind != ProposalKindFields || proposal.ProposedFields == nil {
		return FieldReview{}, ErrConflict
	}
	if len(proposal.ProposedFields.Extra) > 0 && !DeclaredExtraFields(proposal.ProposedFields.Extra, words) {
		return FieldReview{}, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return FieldReview{}, err
	}
	entry := sessionEntry(doc, proposal.EntryID)
	if entry == nil {
		return FieldReview{}, ErrConflict
	}
	return s.ReviewFields(scope, proposal.EntryID, proposal.FieldsRevision, proposal.ProposedFields.patch(entry.Fields), author)
}

// CommitProposedFields writes the reviewed suggestion on the owner's gesture.
// The written fields are the owner's: source reviewed_user, the owner as
// author. A lost reply may be retried with the same key; a new write rechecks
// the suggestion, its declaration and its proposer.
func (s *Store) CommitProposedFields(scope Scope, proposalID, reviewToken, key, author string, words Words) (Entry, bool, error) {
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return Entry{}, false, err
	}
	proposal, ok := savedProposal(doc, proposalID)
	if !ok || proposal.Kind != ProposalKindFields || proposal.ProposedFields == nil {
		return Entry{}, false, ErrConflict
	}
	entry := sessionEntry(doc, proposal.EntryID)
	if entry == nil {
		return Entry{}, false, ErrConflict
	}
	patch := proposal.ProposedFields.patch(entry.Fields)
	for _, op := range doc.Operations {
		if op.Key != key {
			continue
		}
		if op.Action != "edit_fields" || op.ConsequenceID != proposal.EntryID {
			return Entry{}, false, ErrConflict
		}
		return s.commitFields(scope, proposal.EntryID, reviewToken, key, proposal.FieldsRevision, patch, author, nil)
	}
	if _, err := s.actionableProposal(scope, proposalID); err != nil {
		return Entry{}, false, err
	}
	if len(proposal.ProposedFields.Extra) > 0 && !DeclaredExtraFields(proposal.ProposedFields.Extra, words) {
		return Entry{}, false, ErrConflict
	}
	return s.commitFields(scope, proposal.EntryID, reviewToken, key, proposal.FieldsRevision, patch, author,
		s.proposerLivePolicy(scope, proposal))
}

// ReviewProposal and CommitProposal route an owner's Accept by the saved
// suggestion's kind: a next action or a fields patch. Navigation and draft
// kinds never yield a field review. The kind is returned for feedback.
func (s *Store) ReviewProposal(scope Scope, proposalID, author string, words Words) (FieldReview, string, error) {
	kind, err := s.proposalKind(scope, proposalID)
	if err != nil {
		return FieldReview{}, "", err
	}
	switch kind {
	case "":
		review, err := s.ReviewProposedNextAction(scope, proposalID, author)
		return review, kind, err
	case ProposalKindFields:
		review, err := s.ReviewProposedFields(scope, proposalID, author, words)
		return review, kind, err
	}
	return FieldReview{}, kind, ErrConflict
}

func (s *Store) CommitProposal(scope Scope, proposalID, reviewToken, key, author string, words Words) (Entry, bool, string, error) {
	kind, err := s.proposalKind(scope, proposalID)
	if err != nil {
		return Entry{}, false, "", err
	}
	switch kind {
	case "":
		entry, replay, err := s.CommitProposedNextAction(scope, proposalID, reviewToken, key, author)
		return entry, replay, kind, err
	case ProposalKindFields:
		entry, replay, err := s.CommitProposedFields(scope, proposalID, reviewToken, key, author, words)
		return entry, replay, kind, err
	}
	return Entry{}, false, kind, ErrConflict
}

func (s *Store) proposalKind(scope Scope, proposalID string) (string, error) {
	if !validText(proposalID, 160) || proposalID == "" {
		return "", ErrConflict
	}
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return "", err
	}
	proposal, ok := savedProposal(doc, proposalID)
	if !ok {
		return "", ErrConflict
	}
	return proposal.Kind, nil
}
