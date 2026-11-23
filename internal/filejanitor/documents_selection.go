package filejanitor

import (
	"context"
	"os"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DocumentsSelection is a read-only handoff, not a ReviewDraft or an
// approval. Only the caller's exact top-level saved-view members participate.
// No default ticks or generic category plan can be run with this response.
type DocumentsSelection struct {
	State        string   `json:"state"`
	Names        []string `json:"names,omitempty"`
	Batch        string   `json:"-"`
	CandidateIDs []string `json:"-"`
	// RootDirID is the managed folder's identity as the caller bound it to the
	// review; the conditional mover compares it with the disk at every move.
	RootDirID string `json:"-"`
	// Key is opaque to this package: the caller (the Documents review) sets it to
	// bind a preview to its view revision, kept group and chosen sources.
	Key string `json:"-"`
}
type ReviewedLooseFile struct {
	Name    string
	Version workspace.SourceVersion
}

func (s *Service) MatchDocumentsSelection(ctx context.Context, owner, id, documents, witness string, files []ReviewedLooseFile) (DocumentsSelection, error) {
	unavailable := DocumentsSelection{State: "unavailable"}
	if s == nil || s.store == nil || len(files) == 0 || len(files) > 8 || ctx.Err() != nil {
		return unavailable, ErrReviewUnavailable
	}
	_, installed, err := s.reviewInstall(owner, id)
	if err != nil {
		return unavailable, err
	}
	if !installed {
		return DocumentsSelection{State: "install"}, nil
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return DocumentsSelection{State: "access"}, nil
	}
	settings, err := s.requireConfigured(id)
	if err != nil {
		return unavailable, err
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != documents || root.Path != documents || settings.RootID != root.Generation {
		return unavailable, ErrReviewUnavailable
	}
	rootMatches := func() bool {
		st, err := os.Lstat(documents)
		return err == nil && st.IsDir() && witness != "" && directoryid.Witness(st) == witness
	}
	if !rootMatches() {
		return unavailable, ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(id, true)
	if err != nil {
		return unavailable, err
	}
	batch, ok := latestCandidateBatch(state)
	if !ok {
		return DocumentsSelection{State: "scan"}, nil
	}
	groups, binding, current, err := s.reviewDraftSnapshot(owner, id, batch.ID, state)
	if err != nil || current != root || batch.RootID != root.Generation {
		return unavailable, ErrReviewUnavailable
	}
	draft := projectReviewDraft(state, batch.ID, binding, groups)
	if draft.State == "invalid" || draft.State == "stale" {
		return unavailable, ErrReviewUnavailable
	}
	trash := map[string]bool{}
	for _, choice := range draft.Choices {
		if choice.Operation == OperationTrash {
			trash[choice.CandidateID] = true
		}
	}
	list, err := workspace.ListContainedEntries(documents, "", 1)
	if err != nil {
		return unavailable, err
	}
	versions := map[string]workspace.SourceVersion{}
	for _, e := range list.Entries {
		if e.Kind == "file" {
			versions[e.Name] = e.Version
		}
	}
	result := DocumentsSelection{State: "matched", Batch: batch.ID, RootDirID: witness}
	seen := map[string]bool{}
	candidates := state.CandidatesFor(batch.ID)
	for _, f := range files {
		v := f.Version
		if ValidateFileName(f.Name) != nil || seen[f.Name] || v.Identity == "" || v.Placeholder || v.Links != 1 || versions[f.Name] != v {
			return DocumentsSelection{State: "changed"}, nil
		}
		seen[f.Name] = true
		match := ""
		for _, c := range candidates {
			if c.Name != f.Name {
				continue
			}
			fp := c.Fingerprint
			if match != "" || !proposalPending(c) || !moveAnswered(c) || c.Decision == DecisionTrash || trash[c.ID] || fp.FileID != v.Identity || fp.ModTime.UnixNano() != v.Modified || fp.Size != v.Bytes {
				return DocumentsSelection{State: "changed"}, nil
			}
			match = c.ID
		}
		if match == "" {
			return DocumentsSelection{State: "scan"}, nil
		}
		result.CandidateIDs = append(result.CandidateIDs, match)
		result.Names = append(result.Names, f.Name)
	}
	if again, err := s.StoredManagedRoot(owner, id); err != nil || again != root || !rootMatches() || ctx.Err() != nil {
		return unavailable, ErrReviewUnavailable
	}
	return result, nil
}
