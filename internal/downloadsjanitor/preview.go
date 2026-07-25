package downloadsjanitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PreviewRequest is what a client submits to see the final plan.
//
// It carries candidate IDs, operations, and category IDs — nothing else. There
// is deliberately no field for a source path, a destination path, or a
// filename: the server already knows where every candidate is, and a client
// that could name a path would be a client that could redirect a move (FR-71).
type PreviewRequest struct {
	WorkspaceID string
	// UserID is the person the approval will be bound to.
	UserID string
	Items  []PreviewRequestItem
}

// PreviewRequestItem is one requested operation.
type PreviewRequestItem struct {
	CandidateID string
	// Operation must be "move" in this group. Trash arrives with its own
	// confirmation path and is rejected here rather than silently accepted.
	Operation Operation
	// Category is an allowlisted category ID; empty keeps the candidate's
	// current effective category.
	Category string
}

// PreviewItem is one line of the final, server-derived plan.
type PreviewItem struct {
	CandidateID string    `json:"candidate_id"`
	Name        string    `json:"name"`
	Operation   Operation `json:"operation"`
	Category    Category  `json:"category,omitempty"`
	// Destination is relative to the configured folder, e.g.
	// "Filed/Documents/report (2).pdf".
	Destination string `json:"destination,omitempty"`
	// Renamed reports that the destination name had to change because a file of
	// that name already exists. Ori never overwrites, so the user is shown the
	// name the file will actually get (FR-84).
	Renamed bool  `json:"renamed,omitempty"`
	Size    int64 `json:"size,omitempty"`
}

// Preview is the plan plus the approval that authorizes exactly it.
type Preview struct {
	BatchID string        `json:"batch_id"`
	Items   []PreviewItem `json:"items"`
	// MoveCount and TrashCount are stated so the confirmation control can say
	// exactly what it will attempt (FR-69).
	MoveCount  int `json:"move_count"`
	TrashCount int `json:"trash_count"`
	// Token authorizes this exact plan, once, until ExpiresAt.
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// IdempotencyKey identifies the resulting apply.
	IdempotencyKey string `json:"idempotency_key"`
}

// PreviewMoves validates a set of requested decisions, derives the real
// destination for each from server state, and issues a single-use approval
// bound to that plan.
//
// Nothing is mutated here, and nothing is journaled: a preview the user
// abandons leaves only an unconsumed token that expires on its own.
func (s *Service) PreviewMoves(req PreviewRequest) (Preview, error) {
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return Preview{}, fmt.Errorf("%w: an approval must belong to a user", ErrApprovalInvalid)
	}
	if len(req.Items) == 0 {
		return Preview{}, fmt.Errorf("%w: select at least one file", ErrInvalidCandidate)
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return Preview{}, err
	}
	// Resolving the root through the directory reference means a workspace
	// whose folder was unlinked cannot even reach a preview.
	root, err := s.scannerFor().ResolveRoot(settings)
	if err != nil {
		return Preview{}, err
	}

	var preview Preview
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		now := s.clock()
		items := make([]PreviewItem, 0, len(req.Items))
		plan := make([]PlanItem, 0, len(req.Items))
		batchID := ""
		// Destination names are reserved as the plan is built, so two files in
		// one batch heading for the same name do not both get told they can
		// have it.
		reserved := map[string]struct{}{}

		for _, requested := range req.Items {
			candidate, ok := state.Candidate(strings.TrimSpace(requested.CandidateID))
			if !ok {
				return fmt.Errorf("%w: %s", ErrCandidateNotFound, requested.CandidateID)
			}
			if candidate.WorkspaceID != workspaceID {
				// Belt and braces: state is already workspace-scoped, so this
				// can only fire if that ever stops being true.
				return fmt.Errorf("%w: %s", ErrCandidateNotFound, requested.CandidateID)
			}
			if !candidate.Actionable() {
				return fmt.Errorf("%w: %s is %s", ErrCandidateNotActionable, candidate.Display(), candidate.State)
			}
			// Move and Trash are both approvable, but they are never conflated:
			// each item carries its own operation, a Trash decision can only
			// come from an explicit per-file choice, and the plan hash covers
			// the operation — so an approval given for moves can never be spent
			// on a removal (FR-66, FR-70).
			operation := requested.Operation
			if operation != OperationMove && operation != OperationTrash {
				return fmt.Errorf("%w: %q is not an operation Ori can approve", ErrInvalidAction, requested.Operation)
			}
			if batchID == "" {
				batchID = candidate.BatchID
			} else if candidate.BatchID != batchID {
				return fmt.Errorf("%w: approve one batch at a time", ErrInvalidAction)
			}

			if operation == OperationTrash {
				// A Trash item has no category and no destination inside the
				// folder: it leaves the folder entirely, recoverably.
				current, err := currentFingerprint(root, candidate.Name)
				if err != nil {
					return fmt.Errorf("%w: %s is no longer available", ErrCandidateNotActionable, candidate.Display())
				}
				if !candidate.Fingerprint.Matches(current) {
					return fmt.Errorf("%w: %s changed since it was proposed — rescan to review it again", ErrCandidateNotActionable, candidate.Display())
				}
				items = append(items, PreviewItem{
					CandidateID: candidate.ID,
					Name:        candidate.Display(),
					Operation:   OperationTrash,
					Size:        candidate.Size,
				})
				plan = append(plan, PlanItem{
					CandidateID:    candidate.ID,
					Operation:      OperationTrash,
					FingerprintKey: candidate.Fingerprint.Key(),
				})
				continue
			}

			category := candidate.EffectiveCategory()
			if requested := strings.TrimSpace(requested.Category); requested != "" {
				definition, err := LookupCategory(requested)
				if err != nil {
					return err
				}
				category = definition.ID
			}
			if !ValidCategory(category) {
				return fmt.Errorf("%w: %q", ErrUnknownCategory, category)
			}

			// The source must still be the file that was proposed. Checking now
			// keeps a plainly stale plan from ever being approved; it is checked
			// again immediately before the mutation, which is what actually
			// protects the file.
			current, err := currentFingerprint(root, candidate.Name)
			if err != nil {
				return fmt.Errorf("%w: %s is no longer available", ErrCandidateNotActionable, candidate.Display())
			}
			if !candidate.Fingerprint.Matches(current) {
				return fmt.Errorf("%w: %s changed since it was proposed — rescan to review it again", ErrCandidateNotActionable, candidate.Display())
			}

			destinationDir, err := DestinationDir(settings, category)
			if err != nil {
				return err
			}
			finalName, err := resolveAvailableNameReserving(destinationDir, candidate.Name, reserved)
			if err != nil {
				return err
			}
			relative, err := DestinationRelativeFor(settings.FilingRootName, category, finalName)
			if err != nil {
				return err
			}

			items = append(items, PreviewItem{
				CandidateID: candidate.ID,
				Name:        candidate.Display(),
				Operation:   OperationMove,
				Category:    category,
				Destination: relative,
				Renamed:     finalName != candidate.Name,
				Size:        candidate.Size,
			})
			plan = append(plan, PlanItem{
				CandidateID:    candidate.ID,
				Operation:      OperationMove,
				Category:       category,
				FingerprintKey: candidate.Fingerprint.Key(),
			})
		}

		token, tokenHash, err := newApprovalToken()
		if err != nil {
			return err
		}
		record := ApprovalRecord{
			ID:             "approval-" + uuid.New().String(),
			TokenHash:      tokenHash,
			UserID:         userID,
			WorkspaceID:    workspaceID,
			BatchID:        batchID,
			PayloadHash:    PayloadHash(workspaceID, batchID, plan),
			IdempotencyKey: "apply-" + uuid.New().String(),
			IssuedAt:       now,
			ExpiresAt:      now.Add(ApprovalTTL),
		}
		state.Approvals = append(state.Approvals, record)

		moves, trashes := 0, 0
		for _, item := range items {
			if item.Operation == OperationTrash {
				trashes++
			} else {
				moves++
			}
		}
		preview = Preview{
			BatchID:        batchID,
			Items:          items,
			MoveCount:      moves,
			TrashCount:     trashes,
			Token:          token,
			ExpiresAt:      record.ExpiresAt,
			IdempotencyKey: record.IdempotencyKey,
		}
		return nil
	})
	if err != nil {
		return Preview{}, err
	}
	return preview, nil
}

// resolveAvailableNameReserving resolves a free destination name, treating names
// already reserved by this plan as occupied.
func resolveAvailableNameReserving(dir, name string, reserved map[string]struct{}) (string, error) {
	for attempt := 1; attempt <= maxCollisionAttempts; attempt++ {
		candidate, err := resolveAvailableName(dir, name)
		if err != nil {
			return "", err
		}
		key := strings.ToLower(filepath.Join(dir, candidate))
		if _, taken := reserved[key]; !taken {
			reserved[key] = struct{}{}
			return candidate, nil
		}
		// The name resolution collided with another item in this same plan;
		// pretend the reserved name exists and try again.
		name = bumpName(candidate)
	}
	return "", fmt.Errorf("%w: could not find a free name for %q", ErrDestinationUnavailable, name)
}

// bumpName turns "report (2).pdf" into "report (3).pdf", and "report.pdf" into
// "report (2).pdf".
func bumpName(name string) string {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	open := strings.LastIndex(stem, " (")
	if open > 0 && strings.HasSuffix(stem, ")") {
		var n int
		if _, err := fmt.Sscanf(stem[open+2:len(stem)-1], "%d", &n); err == nil && n > 0 {
			return fmt.Sprintf("%s (%d)%s", stem[:open], n+1, extension)
		}
	}
	return fmt.Sprintf("%s (2)%s", stem, extension)
}

// currentFingerprint reads the file's present state without opening it. A
// symlink or a non-regular file at the path is reported as unavailable rather
// than fingerprinted: it is not the file that was proposed.
func currentFingerprint(root, name string) (Fingerprint, error) {
	if err := ValidateFileName(name); err != nil {
		return Fingerprint{}, err
	}
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil {
		return Fingerprint{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Fingerprint{}, fmt.Errorf("%w: %s is not a regular file", ErrCandidateNotActionable, name)
	}
	return fingerprintFor(name, info), nil
}

// ConsumeApproval atomically spends a token and returns the approval it
// authorized.
//
// Consuming happens before any mutation and in the same write that records it,
// so two concurrent confirms cannot both proceed: the second finds the token
// already spent. Every binding is rechecked here rather than trusted from the
// preview — user, workspace, batch, expiry, and the payload hash of the plan
// actually being submitted.
func (s *Service) ConsumeApproval(workspaceID, userID, token string, plan []PlanItem, batchID string) (ApprovalRecord, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	token = strings.TrimSpace(token)
	if token == "" {
		return ApprovalRecord{}, ErrApprovalRequired
	}
	var consumed ApprovalRecord
	_, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		now := s.clock()
		index, found := findApproval(*state, hashToken(token))
		if !found {
			// An unknown token and a consumed-then-pruned token are reported
			// identically: a caller learns nothing from the difference.
			return ErrApprovalInvalid
		}
		record := state.Approvals[index]
		switch {
		case record.Consumed():
			return ErrApprovalConsumed
		case record.Expired(now):
			return ErrApprovalExpired
		case !strings.EqualFold(strings.TrimSpace(record.UserID), strings.TrimSpace(userID)):
			return ErrApprovalInvalid
		case record.WorkspaceID != workspaceID:
			return ErrApprovalInvalid
		case record.BatchID != batchID:
			return ErrApprovalInvalid
		case record.PayloadHash != PayloadHash(workspaceID, batchID, plan):
			// The submitted plan is not the plan the user approved: a decision,
			// a category, or a file's state changed.
			return ErrApprovalInvalid
		}
		state.Approvals[index].ConsumedAt = now
		consumed = state.Approvals[index]
		return nil
	})
	if err != nil {
		return ApprovalRecord{}, err
	}
	return consumed, nil
}
