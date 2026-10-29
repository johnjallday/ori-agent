package grouprequirements

import (
	"context"
	"strings"
)

// ObserveHomeOperation reads only the exact existing Home-only journal and
// review. It never evaluates a provider, claims/marks an operation, ensures a
// Home, consumes consent or resumes a partly completed consequence. In
// particular ErrNotFound is not proof that an unknown delivery did no work.
func (s *Service) ObserveHomeOperation(ctx context.Context, owner, token, key, inputDigest string) (Operation, Receipt, error) {
	operation, receipt, err := s.observeOperation(ctx, owner, token, key, inputDigest, OperationPrepareHome)
	if err != nil {
		return Operation{}, Receipt{}, err
	}
	if operation.ChildWorkspaceID != "" || operation.ProjectLinkID != "" {
		return Operation{}, Receipt{}, ErrOperationConflict
	}
	return operation, receipt, nil
}

// observeOperation is a journal getter, not an evaluation or execution path.
// Only the bounded typed readers select its operation kind.
func (s *Service) observeOperation(ctx context.Context, owner, token, key, inputDigest string, kind OperationKind) (Operation, Receipt, error) {
	if s == nil || s.receipts == nil {
		return Operation{}, Receipt{}, ErrUnavailable
	}
	owner, token, key = strings.TrimSpace(owner), strings.TrimSpace(token), strings.TrimSpace(key)
	inputDigest = strings.ToLower(strings.TrimSpace(inputDigest))
	if owner == "" || token == "" || len(token) > 200 || key == "" || len(key) > 200 || !validDigest(inputDigest) {
		return Operation{}, Receipt{}, ErrReviewRequired
	}
	receipt, err := s.receipts.GetReceipt(ctx, token)
	if err != nil {
		return Operation{}, Receipt{}, err
	}
	if receipt.OwnerUserID != owner || receipt.OperationKind != kind || receipt.InputDigest != inputDigest {
		return Operation{}, Receipt{}, ErrReviewStale
	}
	operation, err := s.receipts.GetOperation(ctx, owner, kind, key)
	if err != nil {
		return Operation{}, Receipt{}, err
	}
	if operation.OwnerUserID != owner || operation.OperationKind != kind || operation.IdempotencyKey != key ||
		operation.InputDigest != inputDigest || operation.ReviewDigest != receipt.ReviewDigest {
		return Operation{}, Receipt{}, ErrOperationConflict
	}
	// An expired accepted review stays observable. It does not become fresh
	// consent, nor does a missing/unknown status become successful completion.
	return operation, receipt, nil
}
