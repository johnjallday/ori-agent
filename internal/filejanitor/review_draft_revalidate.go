package filejanitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// revalidatedReviewDraft restores exact intent only while its selected source
// states still match. Unlike pure proposal GETs, this reads metadata for saved
// selected names only: no enumeration, content opens, classifier or writes.
// The same revalidation runs inside edit/approval transactions. It cannot grant
// mutation authority; the executor still revalidates immediately before moving.
func (s *Service) revalidatedReviewDraft(state ScanState, batchID string, binding reviewDraftBinding, groups ReviewGroups, root ManagedRoot) ReviewDraftView {
	view := projectReviewDraft(state, batchID, binding, groups.Revision)
	finish := func(view ReviewDraftView) ReviewDraftView {
		view.Groups = reviewDraftGroupViews(groups, view.Choices)
		return view
	}
	groupByCandidate := make(map[string]string, groups.Counts.Filing+groups.Counts.Questions)
	for groupID, members := range groups.members {
		for _, member := range members {
			groupByCandidate[member.Candidate.ID] = groupID
		}
	}
	for i := range view.Choices {
		view.Choices[i].GroupID = groupByCandidate[view.Choices[i].CandidateID]
	}
	if len(view.Choices) == 0 {
		return finish(view)
	}
	settings, err := s.store.LoadSettings(state.WorkspaceID)
	rootAvailable := err == nil && settings.RootID == binding.RootID
	if rootAvailable {
		resolved, resolveErr := s.scannerFor().ResolveRoot(settings)
		rootAvailable = resolveErr == nil && resolved == root.Path
	}
	byID := make(map[string]JanitorCandidate, len(state.Candidates))
	for _, candidate := range state.Candidates {
		byID[candidate.ID] = candidate
	}
	kept := make([]ReviewChoiceView, 0, len(view.Choices))
	var invalid []any
	for _, choice := range view.Choices {
		candidate := byID[choice.CandidateID]
		var current Fingerprint
		valid := rootAvailable
		if valid {
			current, err = currentFingerprint(root.Path, candidate.Name)
			valid = err == nil && candidate.Fingerprint.Matches(current)
		}
		if valid {
			kept = append(kept, choice)
		} else {
			view.RemovedStale++
			invalid = append(invalid, []string{choice.CandidateID, current.Key()})
		}
	}
	if len(invalid) == 0 {
		return finish(view)
	}
	view.Choices = kept
	if !rootAvailable {
		view.State = "stale"
	}
	// Hash bounded structured evidence, never return paths or fingerprints.
	// All values below are strings/integers/slices, so encoding cannot fail.
	data, err := json.Marshal([]any{view.Revision, view.State, view.Choices, view.RemovedStale, invalid})
	if err != nil {
		view.State, view.Choices = "invalid", []ReviewChoiceView{}
		data = []byte("invalid:" + view.Revision)
	}
	sum := sha256.Sum256(data)
	view.Revision = hex.EncodeToString(sum[:])
	return finish(view)
}

func reviewDraftGroupViews(groups ReviewGroups, choices []ReviewChoiceView) []ReviewDraftGroupView {
	byID := make(map[string]ReviewChoiceView, len(choices))
	for _, choice := range choices {
		byID[choice.CandidateID] = choice
	}
	out := make([]ReviewDraftGroupView, 0, len(groups.Groups))
	for _, group := range groups.Groups {
		view := ReviewDraftGroupView{ID: group.ID}
		categories := make(map[Category]bool)
		for _, member := range groups.members[group.ID] {
			choice := byID[member.Candidate.ID]
			if choice.Operation == OperationTrash {
				view.Trash++
			}
			if choice.Operation == OperationMove {
				view.Selected++
				if choice.SourceGroupID == group.ID {
					view.GroupMarked++
				}
			}
			if group.Kind == "filing" && choice.Operation != OperationTrash && member.Candidate.Decision != DecisionTrash {
				view.Eligible++
			}
			if choice.Operation != OperationTrash {
				if category, err := LookupCategory(string(member.Candidate.EffectiveCategory())); err == nil {
					categories[category.ID] = true
					view.Category = category.ID
				}
			}
		}
		view.MixedDestinations = len(categories) > 1
		if view.MixedDestinations {
			view.Category = ""
		}
		out = append(out, view)
	}
	return out
}
