package grouprequirements

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func validateHomeStaffingIntent(input Input, current Evaluation) error {
	if input.HomeStaffing == nil && input.HomeAdvice == nil {
		return nil
	}
	if input.OperationKind != OperationPrepareHome || input.GroupTemplateID == "" || input.GroupTemplateRevision == "" ||
		current.SelectedComposition != CompositionGrouped || current.ProgramKey == nil || current.HomeDeclaration == nil ||
		(input.HomeStaffing != nil && input.HomeAdvice != nil) {
		return ErrReviewRequired
	}
	declared := make(map[string]workspace.AssistantRoleScope, len(current.HomeDeclaration.Roles))
	for _, role := range current.HomeDeclaration.Roles {
		declared[role.ID] = role.Scope
	}
	if team := input.HomeStaffing; team != nil {
		if team.Validate() != nil {
			return ErrReviewRequired
		}
		for _, role := range team.Roles {
			if role.Scope != string(workspace.AssistantRoleScopeHome) || declared[role.RoleID] != workspace.AssistantRoleScopeHome {
				return ErrReviewRequired
			}
		}
	}
	if advice := input.HomeAdvice; advice != nil {
		if advice.Version != 2 || advice.Validate(0) != nil || advice.TemplateID != input.GroupTemplateID || advice.TemplateRevision != input.GroupTemplateRevision {
			return ErrReviewRequired
		}
		for _, role := range advice.Roles {
			if role.Scope != string(workspace.AssistantRoleScopeHome) || declared[role.RoleID] != workspace.AssistantRoleScopeHome {
				return ErrReviewRequired
			}
		}
	}
	return nil
}

// ReviewedHomeTeam retrieves only private choice material for a compiled caller
// revalidating the original Home review. It does not refresh witnesses, consume
// the receipt, staff a role, or authorize a different target. Claim retains its
// own expiry/replay checks; the role owner must also check the resulting Home
// operation and exact source before applying any remaining role.
func (s *Service) ReviewedHomeTeam(ctx context.Context, input Input, token string) (*rosterstaffing.TeamIntent, error) {
	review, err := s.ReviewedHomeStaffing(ctx, input, token)
	return review.Team, err
}

// HomeStaffingReview is private compiled material from the original receipt.
// An empty HomeWorkspaceID promises a newly prepared Home, not whichever Home
// another operation happens to create before this choice is applied.
type HomeStaffingReview struct {
	Team            *rosterstaffing.TeamIntent `json:"-"`
	HomeWorkspaceID string                     `json:"-"`
	ReviewDigest    string                     `json:"-"`
}

func (s *Service) ReviewedHomeStaffing(ctx context.Context, input Input, token string) (HomeStaffingReview, error) {
	receipt, err := s.homeReviewReceipt(ctx, input, token)
	if err != nil {
		return HomeStaffingReview{}, err
	}
	if receipt.HomeStaffing != nil && receipt.HomeStaffing.Validate() != nil {
		return HomeStaffingReview{}, ErrReviewStale
	}
	return HomeStaffingReview{Team: receipt.HomeStaffing.Clone(), HomeWorkspaceID: receipt.HomeWorkspaceID, ReviewDigest: receipt.ReviewDigest}, nil
}

// ReviewedHomeAdvice reads an original still-live recommendation. Its choices
// must be explicitly selected and revalidated by a separate exact-team review;
// this getter never turns advice into assignment authority.
func (s *Service) ReviewedHomeAdvice(ctx context.Context, input Input, token string) (*rosterstaffing.Advice, error) {
	receipt, err := s.homeReviewReceipt(ctx, input, token)
	if err != nil {
		return nil, err
	}
	if receipt.HomeAdvice == nil || receipt.HomeAdvice.Version != 2 || receipt.HomeAdvice.Validate(0) != nil ||
		receipt.ConsumedAt != nil || !s.now().Before(receipt.ExpiresAt) {
		return nil, ErrReviewStale
	}
	return receipt.HomeAdvice.Clone(), nil
}

// ObservedHomeTeam reads original completed-operation material for a compiled
// metadata observer. Expiry is deliberately irrelevant to past provenance, but
// this result is never pending-role execution authority. The observer must
// independently match the current canonical role receipt, binding and instance.
func (s *Service) ObservedHomeTeam(ctx context.Context, owner, token, key, homeID string, current *workspace.AssistantProgramState) (HomeStaffingReview, error) {
	if s == nil || s.receipts == nil || strings.TrimSpace(owner) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(key) == "" || current == nil || current.GroupTemplate == nil {
		return HomeStaffingReview{}, ErrUnavailable
	}
	receipt, err := s.receipts.GetReceipt(ctx, token)
	if err != nil || receipt.OwnerUserID != owner || receipt.OperationKind != OperationPrepareHome ||
		receipt.HomeStaffing == nil || receipt.HomeStaffing.Validate() != nil || receipt.ReviewDigest != receiptDigest(receipt) ||
		receipt.GroupTemplateID == "" || receipt.GroupTemplateRev == "" || receipt.ProgramKey == nil ||
		current.Key != *receipt.ProgramKey || current.Key.OwnerUserID != owner ||
		current.GroupTemplate.GroupTemplateID != receipt.GroupTemplateID || current.GroupTemplate.GroupTemplateRevision != receipt.GroupTemplateRev {
		return HomeStaffingReview{}, ErrReviewStale
	}
	op, err := s.receipts.GetOperation(ctx, owner, OperationPrepareHome, key)
	if err != nil || op.OwnerUserID != owner || op.OperationKind != OperationPrepareHome || op.Status != OperationSucceeded ||
		op.ReviewDigest != receipt.ReviewDigest || op.InputDigest != receipt.InputDigest || op.HomeWorkspaceID == "" || op.HomeWorkspaceID != homeID ||
		op.OperationDigest != operationDigest(op) {
		return HomeStaffingReview{}, ErrReviewStale
	}
	return HomeStaffingReview{Team: receipt.HomeStaffing.Clone(), HomeWorkspaceID: op.HomeWorkspaceID, ReviewDigest: receipt.ReviewDigest}, nil
}

func (s *Service) homeReviewReceipt(ctx context.Context, input Input, token string) (Receipt, error) {
	if s == nil || s.receipts == nil {
		return Receipt{}, ErrUnavailable
	}
	receipt, err := s.receipts.GetReceipt(ctx, strings.TrimSpace(token))
	if err != nil {
		return Receipt{}, ErrReviewRequired
	}
	if receipt.ReviewDigest != receiptDigest(receipt) || receipt.OwnerUserID != strings.TrimSpace(input.OwnerUserID) ||
		receipt.OperationKind != OperationPrepareHome || input.OperationKind != OperationPrepareHome ||
		receipt.InputDigest != strings.ToLower(strings.TrimSpace(input.InputDigest)) ||
		receipt.GroupTemplateID != input.GroupTemplateID || receipt.GroupTemplateRev != input.GroupTemplateRevision {
		return Receipt{}, ErrReviewStale
	}
	return receipt, nil
}
