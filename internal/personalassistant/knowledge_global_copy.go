package personalassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CopyGlobalFact appends an approved global fact to another workspace's
// MEMORY.md as an ordinary entry, "[fact, <date>, global:<item-id>] <text>",
// so that workspace's own agents know it too. The copy diverges on purpose:
// it is never updated or removed when the global fact changes. The text is
// read from the canonical HQ line, never from sidecar revisions, and must be
// the version the user is looking at. Repeating a copy changes nothing.
func (s *KnowledgeLearningService) CopyGlobalFact(ctx context.Context, userID, itemID string, expectedVersion int64, workspaceID, date string) (bool, error) {
	if s == nil || s.store == nil || s.memory == nil {
		return false, ErrGlobalMemoryUnavailable
	}
	workspaceID = strings.TrimSpace(workspaceID)
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return false, globalMemoryResolveError(err)
	}
	if workspaceID == "" || workspaceID == binding.HQWorkspaceID {
		return false, fmt.Errorf("%w: choose a workspace other than the Personal HQ", ErrValidation)
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return false, err
	}
	item, ok := knowledgeItemByID(doc, itemID)
	if !ok {
		return false, ErrNotFound
	}
	if item.State != KnowledgeApproved || item.Prepared != nil || item.Version != expectedVersion {
		return false, ErrConflict
	}
	snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
	if err != nil {
		return false, err
	}
	text, unavailable := s.canonicalReviewText(ctx, binding, snapshot, item)
	if unavailable != "" || item.Target == nil || item.Target.Kind != "memory" {
		return false, ErrConflict
	}
	return s.memory.AppendUnique(workspaceID, workspace.MemoryEntry{
		Type: workspace.MemoryTypeFact, Date: date,
		Provenance: workspace.GlobalCopyProvenance(item.ID), Text: text,
	})
}

// GlobalFactCopies returns which of the given workspaces hold a copy of a
// global fact. It only reads, and it works for a forgotten fact too, so the
// user can see where a copy lives on after Forget. A workspace whose memory
// cannot be read is skipped; the scan stops when ctx is done.
func (s *KnowledgeLearningService) GlobalFactCopies(ctx context.Context, userID, itemID string, workspaceIDs []string) ([]string, error) {
	if err := s.knownGlobalFact(ctx, userID, itemID); err != nil {
		return nil, err
	}
	provenance := workspace.GlobalCopyProvenance(itemID)
	var holders []string
	for _, workspaceID := range workspaceIDs {
		if err := ctx.Err(); err != nil {
			return holders, err
		}
		entries, err := s.memory.EntriesWithProvenance(workspaceID, provenance)
		if err != nil || len(entries) == 0 {
			continue
		}
		holders = append(holders, workspaceID)
	}
	return holders, nil
}

// RemoveGlobalFactCopy deletes one copied line of a global fact from one
// workspace. Every other line in that workspace is untouched.
func (s *KnowledgeLearningService) RemoveGlobalFactCopy(ctx context.Context, userID, itemID, workspaceID string) error {
	if err := s.knownGlobalFact(ctx, userID, itemID); err != nil {
		return err
	}
	_, err := s.memory.RemoveByProvenance(workspaceID, workspace.GlobalCopyProvenance(itemID))
	return err
}

// knownGlobalFact checks that itemID is a fact in this user's global memory,
// in any state: copies of a forgotten fact must still be findable.
func (s *KnowledgeLearningService) knownGlobalFact(ctx context.Context, userID, itemID string) error {
	if s == nil || s.store == nil || s.memory == nil {
		return ErrGlobalMemoryUnavailable
	}
	if _, err := s.store.resolve(ctx, userID); err != nil {
		return globalMemoryResolveError(err)
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	if _, ok := knowledgeItemByID(doc, itemID); !ok {
		return ErrNotFound
	}
	return nil
}
