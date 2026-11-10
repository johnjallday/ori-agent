package filejanitor

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ReviewDraftEdit is a closed, bounded operation. Group and intent revisions
// refer to what the user saw, never a query for future matching files.
type ReviewDraftEdit struct {
	ExpectedRevision      string   `json:"expected_revision"`
	ExpectedGroupRevision string   `json:"expected_group_revision"`
	Action                string   `json:"action"`
	GroupID               string   `json:"group_id,omitempty"`
	CandidateIDs          []string `json:"candidate_ids,omitempty"`
	Category              string   `json:"category,omitempty"`
}

func (r ReviewDraftEdit) validate() error {
	if len(r.ExpectedRevision) != 64 || len(r.ExpectedGroupRevision) != 64 {
		return ErrInvalidAction
	}
	if len(r.CandidateIDs) > MaxReviewChoicesPerBatch {
		return ErrReviewBounds
	}
	seen := make(map[string]bool, len(r.CandidateIDs))
	for _, id := range r.CandidateIDs {
		if !managedRootGeneration.MatchString(id) || seen[id] {
			return ErrInvalidAction
		}
		seen[id] = true
	}
	group, files, category := r.GroupID != "", len(r.CandidateIDs) > 0, strings.TrimSpace(r.Category) != ""
	if group && !managedRootGeneration.MatchString(r.GroupID) {
		return ErrInvalidAction
	}
	valid := false
	switch r.Action {
	case "select_group":
		valid = group && !files
	case "select_filing_groups", "clear_all":
		valid = !group && !files && !category
	case "select_files":
		valid = files && !group
	case "exclude_files":
		valid = files && !group && !category
	case "set_category":
		valid = group != files && category
	case "clear_group", "deselect_group":
		valid = group && !files && !category
	case "mark_trash", "unmark_trash":
		valid = len(r.CandidateIDs) == 1 && !group && !category
	}
	if !valid {
		return ErrInvalidAction
	}
	if category {
		if _, err := LookupCategory(r.Category); err != nil {
			return err
		}
	}
	return nil
}

// reviewDraftSnapshot is called with a single canonical state snapshot. It
// performs only saved metadata reads, and is also safe inside UpdateScanState:
// none of its dependencies acquire the same scan-state mutex recursively.
func (s *Service) reviewDraftSnapshot(owner, workspaceID, batchID string, state ScanState) (ReviewGroups, reviewDraftBinding, ManagedRoot, error) {
	install, applies, err := s.reviewInstall(owner, workspaceID)
	if err != nil || !applies {
		return ReviewGroups{}, reviewDraftBinding{}, ManagedRoot{}, ErrReviewUnavailable
	}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return ReviewGroups{}, reviewDraftBinding{}, ManagedRoot{}, ErrReviewUnavailable
	}
	batch, found := state.Batch(batchID)
	if !found {
		return ReviewGroups{}, reviewDraftBinding{}, ManagedRoot{}, ErrBatchNotFound
	}
	candidates := state.CandidatesFor(batchID)
	if !reviewBatchScope(batch, candidates, workspaceID, root.Generation, install) {
		return ReviewGroups{}, reviewDraftBinding{}, ManagedRoot{}, ErrReviewUnavailable
	}
	binding := reviewDraftBinding{Owner: owner, RootID: root.Generation, InstalledAt: install.InstalledAt.UTC(), CapabilityVersion: install.Version, Digest: reviewScopeDigest(root, install)}
	groups, err := GroupReview(batch, candidates, binding.Digest)
	return groups, binding, root, err
}

// ReadReviewDraft restores only current, scoped explicit intent. Reconciliation
// is read-only and must never persist a draft or issue a preview on entry.
func (s *Service) ReadReviewDraft(ctx context.Context, owner, workspaceID, batchID string) (ReviewDraftView, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || batchID == "latest" {
		return ReviewDraftView{}, ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return ReviewDraftView{}, err
	}
	groups, binding, root, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil {
		return ReviewDraftView{}, err
	}
	view := s.revalidatedReviewDraft(state, batchID, binding, groups, root)
	_, freshBinding, freshRoot, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil || freshRoot != root || freshBinding != binding || ctx.Err() != nil {
		return ReviewDraftView{}, ErrReviewUnavailable
	}
	return view, nil
}

// EditReviewDraft serializes intent changes with execution before acquiring the
// scan-state transaction lock. All membership resolution, CAS, canonical
// category updates and approval invalidation happen in one atomic write.
func (s *Service) EditReviewDraft(owner, workspaceID, batchID string, request ReviewDraftEdit) (ReviewDraftView, error) {
	if err := request.validate(); err != nil {
		return ReviewDraftView{}, err
	}
	if s == nil || s.store == nil || batchID == "latest" || !managedRootGeneration.MatchString(batchID) {
		return ReviewDraftView{}, ErrReviewUnavailable
	}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return ReviewDraftView{}, ErrReviewUnavailable
	}
	unlock := lockRoot(root.Path)
	defer unlock()
	var view ReviewDraftView
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		groups, binding, currentRoot, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, *state)
		if err != nil {
			return err
		}
		if currentRoot != root {
			return ErrReviewConflict
		}
		before := s.revalidatedReviewDraft(*state, batchID, binding, groups, root)
		if before.Revision != request.ExpectedRevision || groups.Revision != request.ExpectedGroupRevision {
			return ErrReviewConflict
		}
		if (before.State == "invalid" || before.State == "stale") && request.Action != "clear_all" {
			return ErrReviewDraftInvalid
		}
		byID := make(map[string]int, len(state.Candidates))
		for i := range state.Candidates {
			if state.Candidates[i].BatchID == batchID {
				byID[state.Candidates[i].ID] = i
			}
		}
		choices := make(map[string]ReviewChoice, len(before.Choices))
		for _, item := range before.Choices {
			candidate := state.Candidates[byID[item.CandidateID]]
			choices[item.CandidateID] = ReviewChoice{CandidateID: item.CandidateID, FingerprintKey: candidate.Fingerprint.Key(), Operation: item.Operation, SourceGroupID: item.SourceGroupID}
		}
		category := Category("")
		if strings.TrimSpace(request.Category) != "" {
			definition, err := LookupCategory(request.Category)
			if err != nil {
				return err
			}
			category = definition.ID
		}
		setCategory := func(id string, group bool) error {
			index, found := byID[id]
			if !found || !proposalPending(state.Candidates[index]) {
				return ErrCandidateNotActionable
			}
			candidate := &state.Candidates[index]
			choice, selected := choices[id]
			// A group edit preserves unrelated prior per-file destination choices.
			if group && (!selected || choice.SourceGroupID != request.GroupID || (candidate.DecisionCategory != "" && candidate.DecisionGroupID != request.GroupID)) {
				return nil
			}
			candidate.Decision, candidate.DecisionCategory, candidate.DecidedAt = DecisionMove, category, s.clock()
			candidate.DecisionGroupID = ""
			if group {
				candidate.DecisionGroupID = request.GroupID
			}
			if selected && !group {
				choice.SourceGroupID = ""
				choices[id] = choice
			}
			return nil
		}
		validatedFileRoot := ""
		selectMove := func(id, source string) error {
			index, found := byID[id]
			if !found || !proposalPending(state.Candidates[index]) {
				return ErrCandidateNotActionable
			}
			candidate := state.Candidates[index]
			if validatedFileRoot == "" {
				settings, err := s.store.LoadSettings(workspaceID)
				if err != nil {
					return err
				}
				resolved, err := s.scannerFor().ResolveRoot(settings)
				if err != nil || resolved != root.Path || settings.RootID != root.Generation {
					return ErrReviewUnavailable
				}
				validatedFileRoot = resolved
			}
			current, err := currentFingerprint(validatedFileRoot, candidate.Name)
			if err != nil || !candidate.Fingerprint.Matches(current) {
				return ErrCandidateNotActionable
			}
			if !ValidCategory(candidate.EffectiveCategory()) || (candidate.NeedsReview && (candidate.DecisionCategory == "" || candidate.DecisionGroupID != "")) {
				return ErrCandidateNotActionable
			}
			choices[id] = ReviewChoice{CandidateID: id, FingerprintKey: candidate.Fingerprint.Key(), Operation: OperationMove, SourceGroupID: source}
			return nil
		}
		selectGroup := func(group ReviewGroup) error {
			if group.Kind != "filing" {
				return ErrCandidateNotActionable
			}
			members, _ := groups.Members(group.ID)
			for _, member := range members {
				id := member.Candidate.ID
				if _, exists := choices[id]; exists || member.Candidate.Decision == DecisionTrash {
					continue // never overwrite an explicit prior choice or Trash mark
				}
				if err := selectMove(id, group.ID); err != nil {
					return err
				}
				if category != "" {
					if err := setCategory(id, true); err != nil {
						return err
					}
				}
			}
			return nil
		}
		var targetGroup ReviewGroup
		if request.GroupID != "" {
			for _, group := range groups.Groups {
				if group.ID == request.GroupID {
					targetGroup = group
					break
				}
			}
			if targetGroup.ID == "" {
				return ErrReviewConflict
			}
		}
		for _, id := range request.CandidateIDs {
			if index, found := byID[id]; !found || !proposalPending(state.Candidates[index]) {
				return ErrCandidateNotFound
			}
		}
		switch request.Action {
		case "select_group":
			if err := selectGroup(targetGroup); err != nil {
				return err
			}
		case "select_filing_groups":
			for _, group := range groups.Groups {
				if group.Kind == "filing" {
					if err := selectGroup(group); err != nil {
						return err
					}
				}
			}
		case "select_files":
			for _, id := range request.CandidateIDs {
				if category != "" {
					if err := setCategory(id, false); err != nil {
						return err
					}
				}
				if err := selectMove(id, ""); err != nil {
					return err
				}
			}
		case "exclude_files":
			for _, id := range request.CandidateIDs {
				delete(choices, id)
			}
		case "set_category":
			ids := request.CandidateIDs
			if request.GroupID != "" {
				members, _ := groups.Members(request.GroupID)
				ids = make([]string, 0, len(members))
				for _, member := range members {
					ids = append(ids, member.Candidate.ID)
				}
			}
			for _, id := range ids {
				if err := setCategory(id, request.GroupID != ""); err != nil {
					return err
				}
			}
		case "clear_group":
			for id, choice := range choices {
				if choice.SourceGroupID == request.GroupID {
					delete(choices, id)
				}
			}
		case "deselect_group":
			members, _ := groups.Members(request.GroupID)
			for _, member := range members {
				if choices[member.Candidate.ID].Operation == OperationMove {
					delete(choices, member.Candidate.ID)
				}
			}
		case "clear_all":
			clear(choices)
		case "mark_trash":
			id := request.CandidateIDs[0]
			candidate := state.Candidates[byID[id]]
			choices[id] = ReviewChoice{CandidateID: id, FingerprintKey: candidate.Fingerprint.Key(), Operation: OperationTrash}
		case "unmark_trash":
			id := request.CandidateIDs[0]
			if choices[id].Operation == OperationTrash {
				delete(choices, id)
			}
		default:
			return ErrInvalidAction
		}
		if len(choices) > MaxReviewChoicesPerBatch {
			return ErrReviewBounds
		}
		draft := ReviewDraft{SchemaVersion: ReviewDraftSchemaVersion, BatchID: batchID, Owner: owner,
			RootID: binding.RootID, InstalledAt: binding.InstalledAt, CapabilityVersion: binding.CapabilityVersion,
			ScopeDigest: binding.Digest, Generation: 1, Choices: []ReviewChoice{}, RemovedStale: before.RemovedStale}
		kept := make([]ReviewDraft, 0, len(state.ReviewDrafts)+1)
		for _, old := range state.ReviewDrafts {
			if old.BatchID != batchID {
				kept = append(kept, old)
				continue
			}
			if len(old.invalid) > 0 && request.Action == "clear_all" {
				continue // explicit discard of an unknown schema, not a down-write
			}
			if old.Generation == math.MaxUint64 {
				return ErrReviewBounds
			}
			draft.Generation = max(draft.Generation, old.Generation+1)
		}
		if request.Action == "clear_all" {
			draft.RemovedStale = 0
		}
		for _, choice := range choices {
			draft.Choices = append(draft.Choices, choice)
		}
		sort.Slice(draft.Choices, func(i, j int) bool { return draft.Choices[i].CandidateID < draft.Choices[j].CandidateID })
		state.ReviewDrafts = append(kept, draft)
		resummarizeBatch(state, batchID)
		invalidateBatchApprovals(state, batchID)
		// Recheck saved lifecycle metadata immediately before persistence. A
		// separate install/root cannot inherit this transaction's old scope.
		freshGroups, freshBinding, freshRoot, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, *state)
		if err != nil || freshBinding != binding || freshRoot != root {
			return ErrReviewConflict
		}
		view = s.revalidatedReviewDraft(*state, batchID, binding, freshGroups, root)
		if view.State != "saved" || len(view.Choices) != len(choices) {
			return fmt.Errorf("%w: choices need review", ErrReviewConflict)
		}
		return nil
	})
	if err != nil {
		return ReviewDraftView{}, err
	}
	return view, nil
}
