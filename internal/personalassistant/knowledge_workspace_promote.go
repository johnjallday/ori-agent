package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspacePromoteSourceKind tags the evidence of a fact the user promoted
// from a workspace's memory. The fact itself is the user's explicit statement;
// the evidence only records where it came from.
const WorkspacePromoteSourceKind = "workspace_promote"

// WorkspacePromoteRequest is a user's Promote to global action on one ordinary
// workspace memory entry. Text is the wording the user confirmed; Original is
// the entry as it stood in the workspace.
type WorkspacePromoteRequest struct {
	WorkspaceID string
	Category    string
	Text        string
	Original    string
}

// WorkspacePromoteResult says what a promote did. Refusals are errors:
// ErrGlobalMemoryUnavailable and ErrValidation.
type WorkspacePromoteResult string

const (
	WorkspacePromoted              WorkspacePromoteResult = "promoted"
	WorkspacePromoteAlreadyKnown   WorkspacePromoteResult = "already_known"
	WorkspacePromoteAlreadyPending WorkspacePromoteResult = "already_pending"
)

// GlobalMemoryWorkspace returns the Personal HQ workspace that holds the
// user's global memory, or ErrGlobalMemoryUnavailable when there is none.
func (s *KnowledgeLearningService) GlobalMemoryWorkspace(ctx context.Context, userID string) (string, error) {
	if s == nil || s.store == nil {
		return "", ErrGlobalMemoryUnavailable
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return "", globalMemoryResolveError(err)
	}
	return binding.HQWorkspaceID, nil
}

// PromoteWorkspaceEntry copies a workspace memory entry into global memory as
// an approved fact. The user performed the action, so it goes through the
// explicit-save path and never consumes proposal quota. The workspace entry
// stays where it is. Promoting a fact global memory already holds, by
// normalized text, creates nothing.
func (s *KnowledgeLearningService) PromoteWorkspaceEntry(ctx context.Context, userID string, req WorkspacePromoteRequest) (WorkspacePromoteResult, KnowledgeItem, error) {
	if s == nil || s.store == nil || s.memory == nil || s.now == nil {
		return "", KnowledgeItem{}, ErrGlobalMemoryUnavailable
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return "", KnowledgeItem{}, fmt.Errorf("%w: workspace is required", ErrValidation)
	}
	text, err := workspace.ValidateMemoryText(req.Text)
	if err != nil {
		return "", KnowledgeItem{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	switch req.Category {
	case "how_you_work", "people", "projects", "routines":
	default:
		return "", KnowledgeItem{}, fmt.Errorf("%w: unsupported category", ErrValidation)
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return "", KnowledgeItem{}, globalMemoryResolveError(err)
	}
	if workspaceID == binding.HQWorkspaceID {
		return WorkspacePromoteAlreadyKnown, KnowledgeItem{}, nil // HQ memory is global memory
	}
	snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
	if err != nil {
		return "", KnowledgeItem{}, err
	}
	key := knowledgeTextKey(text)
	for _, entry := range snapshot.Entries {
		if knowledgeTextKey(entry.Entry.Text) == key {
			return WorkspacePromoteAlreadyKnown, KnowledgeItem{}, nil
		}
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return "", KnowledgeItem{}, err
	}
	// One user action, one retry key: a lost response resumes the same save.
	// After Forget the same wording may be promoted again under a fresh key.
	requestID := workspacePromoteRequestID(workspaceID, text, "")
	resuming := ""
	if receipt, found := knowledgeReceiptByID(doc, requestID); found {
		if item, ok := knowledgeItemByID(doc, receipt.ItemID); ok && item.State != KnowledgeForgotten && item.State != KnowledgeRejected {
			resuming = item.ID
		} else {
			requestID = workspacePromoteRequestID(workspaceID, text, uuid.NewString())
		}
	}
	for _, item := range doc.Items {
		if item.ID == resuming || item.State == KnowledgeRejected || item.State == KnowledgeForgotten || !knowledgeItemMatches(item, key) {
			continue
		}
		if item.State == KnowledgeCandidate {
			return WorkspacePromoteAlreadyPending, item, nil
		}
		return WorkspacePromoteAlreadyKnown, item, nil
	}
	evidence := KnowledgeEvidence{SourceKind: WorkspacePromoteSourceKind, SourceID: workspaceID, ObservedAt: s.now().UTC()}
	if original, err := workspace.ValidateMemoryText(req.Original); err == nil && original == req.Original {
		evidence.Summary = original
	}
	item, err := s.saveExplicit(ctx, userID, binding.StateVersion, requestID, req.Category, text, []KnowledgeEvidence{evidence})
	if err != nil {
		return "", KnowledgeItem{}, err
	}
	return WorkspacePromoted, item, nil
}

func workspacePromoteRequestID(workspaceID, text, salt string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\n" + knowledgeTextKey(text) + "\n" + salt))
	return "promote-" + hex.EncodeToString(sum[:16])
}

// globalMemoryResolveError maps "no hired assistant with a usable Personal
// HQ" to the one error every global-memory control explains the same way.
func globalMemoryResolveError(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNeedsHQ) || errors.Is(err, ErrRepairNeeded) {
		return ErrGlobalMemoryUnavailable
	}
	return err
}
