package personalassistant

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

// FolderHomeStaffingHost extends the existing setup host with read-only draft
// operations. No method may execute setup, assign, create or grant anything.
type FolderHomeStaffingHost interface {
	RecommendFolderHomeStaffing(context.Context, FolderSetupRequest) (*rosterstaffing.Advice, error)
	SelectFolderHomeStaffing(context.Context, FolderSetupRequest, rosterstaffing.RoleChoice, *rosterstaffing.Advice) (*rosterstaffing.TeamIntent, error)
}

type FolderHomeStaffingInput struct {
	RequestID     string                     `json:"request_id"`
	Action        string                     `json:"action"`
	ContextDigest string                     `json:"context_digest,omitempty"`
	Revision      int64                      `json:"staffing_revision"`
	Choice        *rosterstaffing.RoleChoice `json:"choice,omitempty"`
	RoleID        string                     `json:"role_id,omitempty"`
}

func (i FolderHomeStaffingInput) validate() error {
	if i.RequestID == "" || len(i.RequestID) > folderRequestIDMax || i.Revision < 0 || i.Revision >= 1<<53-1 {
		return ErrValidation
	}
	switch i.Action {
	case "recommend_staffing":
		if i.Choice != nil || i.RoleID != "" {
			return ErrValidation
		}
	case "choose_staffing":
		if i.Choice == nil || i.RoleID != "" {
			return ErrValidation
		}
	case "clear_staffing":
		if i.Choice != nil || len(i.RoleID) > 128 {
			return ErrValidation
		}
		return nil
	default:
		return ErrValidation
	}
	if _, err := hex.DecodeString(i.ContextDigest); err != nil || len(i.ContextDigest) != 64 {
		return ErrValidation
	}
	return nil
}

// ReviewHomeStaffing changes only the existing offer's bounded draft. Inference
// runs without store/profile/conversation mutation locks. A short review lease
// and CAS protect the result, so a clear/edit/accepted setup always wins.
func (s *FolderDigestService) ReviewHomeStaffing(ctx context.Context, userID, offerID string, input FolderHomeStaffingInput) (FolderOfferView, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	if err := input.validate(); err != nil {
		return FolderOfferView{}, err
	}
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return FolderOfferView{}, ErrFolderOfferNotFound
	}
	if receipt := doc.Receipt(input.RequestID); receipt != nil {
		if receipt.OfferID != offerID || receipt.Action != input.Action {
			return FolderOfferView{}, ErrConflict
		}
		return s.viewFor(ctx, userID, *offer, binding.Paused), nil
	}
	if offer.Status != FolderOfferPending || offer.Setup != nil || offer.PlacementHandoff != nil || offer.Declined != nil {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	original := offer.HomeStaffingReview.Clone()
	if original == nil && input.Action == "clear_staffing" && offer.Portfolio == nil {
		row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
		if !ok || row.Offer == nil || row.Offer.HomeProviderKey == "" {
			return FolderOfferView{}, ErrFolderOutcomeUnavailable
		}
	}
	var revision int64
	if original != nil {
		revision = original.Revision
	}
	if revision != input.Revision {
		return FolderOfferView{}, ErrConflict
	}
	release, err := s.acquireHomeStaffingReview(ctx, *offer, input.Action == "clear_staffing")
	if err != nil {
		return FolderOfferView{}, err
	}
	release() // No lease spans host planning, profile reads or inference.
	next := original.Clone()
	if next == nil {
		next = &FolderHomeStaffingReview{Version: 1, ContextDigest: strings.Repeat("0", 64)}
	}
	if input.Action == "clear_staffing" {
		if input.RoleID == "" {
			next.Team, next.Advice = nil, nil
		} else if next.Team != nil {
			kept := next.Team.Roles[:0]
			for _, role := range next.Team.Roles {
				if role.RoleID != input.RoleID {
					kept = append(kept, role)
				}
			}
			next.Team.Roles = kept
			if len(kept) == 0 {
				next.Team = nil
			}
		}
	} else {
		host, ok := s.deps.Setup.(FolderHomeStaffingHost)
		if !ok {
			return FolderOfferView{}, ErrFolderOutcomeUnavailable
		}
		req := s.planRequest(ctx, userID, *offer)
		plan, err := s.deps.Setup.Plan(ctx, req)
		if err != nil {
			return FolderOfferView{}, err
		}
		if plan.HomeStaffing == nil || plan.HomeStaffing.ContextDigest != input.ContextDigest {
			return FolderOfferView{}, ErrFolderPlanChanged
		}
		if original != nil && original.Team != nil && original.ContextDigest != input.ContextDigest {
			return FolderOfferView{}, ErrFolderPlanChanged
		}
		if next.ContextDigest != input.ContextDigest {
			next.ContextDigest, next.Advice = input.ContextDigest, nil
		}
		req.Plan = plan
		if input.Action == "recommend_staffing" {
			if !s.claimHomeStaffingRecommendation(userID, offerID) {
				return FolderOfferView{}, ErrFolderScanBusy
			}
			defer s.releaseHomeStaffingRecommendation(userID, offerID)
			next.Advice, err = host.RecommendFolderHomeStaffing(ctx, req)
			if err != nil {
				return FolderOfferView{}, err
			}
			if next.Advice == nil {
				return FolderOfferView{}, ErrFolderOutcomeUnavailable
			}
		} else {
			if next.Team != nil {
				for _, role := range next.Team.Roles {
					if role.RoleID == input.Choice.RoleID {
						return FolderOfferView{}, ErrConflict
					}
				}
			}
			selected, err := host.SelectFolderHomeStaffing(ctx, req, *input.Choice, next.Advice.Clone())
			if err != nil {
				return FolderOfferView{}, err
			}
			if selected == nil || selected.Validate() != nil || len(selected.Roles) != 1 || selected.Roles[0].RoleID != input.Choice.RoleID || selected.Roles[0].Name != input.Choice.Name || selected.Roles[0].Mode != input.Choice.Mode {
				return FolderOfferView{}, ErrFolderPlanChanged
			}
			if next.Team == nil {
				next.Team = &rosterstaffing.TeamIntent{Version: 1}
			}
			next.Team.Roles = append(next.Team.Roles, selected.Clone().Roles[0])
		}
		// The provider/declaration/Home may have changed while inference was
		// running without a lease. Re-read material metadata, never re-rank.
		fresh, err := s.deps.Setup.Plan(ctx, req)
		if err != nil {
			return FolderOfferView{}, err
		}
		if fresh.HomeStaffing == nil || fresh.HomeStaffing.ContextDigest != input.ContextDigest {
			return FolderOfferView{}, ErrFolderPlanChanged
		}
	}
	if err := ctx.Err(); err != nil {
		return FolderOfferView{}, err
	}
	next.Revision = revision + 1
	if err := next.validate(); err != nil {
		return FolderOfferView{}, err
	}
	release, err = s.acquireHomeStaffingReview(ctx, *offer, input.Action == "clear_staffing")
	if err != nil {
		return FolderOfferView{}, err
	}
	defer release()
	var updated FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(current *FolderDigestDocument) error {
		item := current.Offer(offerID)
		if item == nil {
			return ErrFolderOfferNotFound
		}
		if current.Receipt(input.RequestID) != nil || item.Status != offer.Status || item.Setup != nil || item.PlacementHandoff != nil || item.Declined != nil ||
			item.FolderKey != offer.FolderKey || item.RootIdentity != offer.RootIdentity || item.SubjectIdentity != offer.SubjectIdentity || item.EntryName != offer.EntryName ||
			!reflect.DeepEqual(item.Portfolio, offer.Portfolio) || !reflect.DeepEqual(item.Structure, offer.Structure) || !reflect.DeepEqual(item.Subject, offer.Subject) || !reflect.DeepEqual(item.ConversationReview, offer.ConversationReview) || !reflect.DeepEqual(item.HomeStaffingReview, original) {
			return ErrConflict
		}
		item.HomeStaffingReview = next.Clone()
		current.Receipts = append(current.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: offerID, Action: input.Action, At: s.deps.Now()})
		pruneFolderDigest(current)
		updated = *item
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	return s.viewFor(ctx, userID, updated, binding.Paused), nil
}

// A bounded clear needs the original review owner/held-selection lease, not
// a working destination provider. It cannot select a source or authorize work.
func (s *FolderDigestService) acquireHomeStaffingReview(ctx context.Context, offer FolderOffer, clear bool) (func(), error) {
	if !clear {
		return s.acquireConversationReview(ctx, offer)
	}
	if offer.ConversationReview == nil {
		return func() {}, nil
	}
	if s.conversationLease == nil {
		return nil, ErrFolderSelection
	}
	return s.conversationLease(ctx, offer)
}

func (s *FolderDigestService) claimHomeStaffingRecommendation(userID, offerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staffingRecommendations == nil {
		s.staffingRecommendations = map[string]bool{}
	}
	key := userID + "\x00" + offerID
	if s.staffingRecommendations[key] {
		return false
	}
	s.staffingRecommendations[key] = true
	return true
}

func (s *FolderDigestService) releaseHomeStaffingRecommendation(userID, offerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.staffingRecommendations, userID+"\x00"+offerID)
}
