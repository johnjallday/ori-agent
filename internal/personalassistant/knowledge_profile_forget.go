package personalassistant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func canonicalProfileValue(profile *userprofile.UserProfile, field string) (string, error) {
	if profile == nil || !allowedKnowledgeProfileField(field) {
		return "", ErrConflict
	}
	if strings.HasPrefix(field, "preferences.") {
		return profile.Preferences[strings.TrimPrefix(field, "preferences.")], nil
	}
	switch field {
	case "display_name":
		return profile.DisplayName, nil
	case "email":
		return profile.Email, nil
	case "timezone":
		return profile.Timezone, nil
	case "locale":
		return profile.Locale, nil
	case "role_category":
		return profile.RoleCategory, nil
	case "about":
		return profile.About, nil
	default:
		return "", ErrConflict
	}
}

// forgetApprovedProfile follows the same exclusion-first protocol as memory
// Forget, but clears only one allowlisted SQL field via an atomic field/value
// and profile-version compare-and-swap. No old value is needed from the scrubbed
// sidecar: recovery reads and hashes the current canonical field instead.
func (s *KnowledgeLearningService) forgetApprovedProfile(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string) (KnowledgeItem, error) {
	if s.profiles == nil {
		return KnowledgeItem{}, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		item, found := knowledgeItemByID(doc, itemID)
		if !found {
			return KnowledgeItem{}, ErrNotFound
		}
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			if receipt.ItemID == itemID && receipt.Result == "forgotten" && item.State == KnowledgeForgotten {
				return item, nil
			}
			return KnowledgeItem{}, ErrConflict
		}
		if item.Target == nil || item.Target.Kind != "profile" || !allowedKnowledgeProfileField(item.Target.Field) || item.Version != expectedVersion {
			return KnowledgeItem{}, ErrConflict
		}
		if item.Prepared == nil {
			if item.State != KnowledgeApproved {
				return KnowledgeItem{}, ErrConflict
			}
			_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
				for i := range latest.Items {
					current := &latest.Items[i]
					if current.ID != itemID || current.Version != expectedVersion || current.State != KnowledgeApproved || current.Prepared != nil || current.Target == nil {
						continue
					}
					keys := knowledgeSuppressionKeys(*current)
					if !hasKnowledgeTombstoneCapacity(*latest, keys) {
						return ErrKnowledgeLimit
					}
					at := s.now().UTC()
					for _, key := range keys {
						if !keySuppressed(*latest, key) {
							latest.Tombstones = append(latest.Tombstones, KnowledgeTombstone{SemanticKey: key, ItemID: itemID, CreatedAt: at})
						}
					}
					current.State = KnowledgeNeedsReview
					current.Revisions = nil
					current.CurrentRevisionID = ""
					current.Prepared = &KnowledgeOperation{Kind: "forget_profile", RequestID: requestID, OldHash: current.Target.CanonicalHash}
					current.UpdatedAt = at
					return nil
				}
				return ErrConflict
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return KnowledgeItem{}, err
			}
			continue
		}
		if item.State != KnowledgeNeedsReview || item.Prepared.Kind != "forget_profile" || item.Prepared.RequestID != requestID ||
			item.Prepared.OldHash != item.Target.CanonicalHash {
			return KnowledgeItem{}, ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		current, err := s.profiles.Get(ctx, binding.UserID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		value, err := canonicalProfileValue(current, item.Target.Field)
		if err != nil {
			return KnowledgeItem{}, err
		}
		if value != "" {
			if hashKnowledgeLine(value) != item.Target.CanonicalHash {
				return KnowledgeItem{}, s.markForgetNeedsReview(ctx, userID, doc, itemID, requestID)
			}
			expectedAt, err := time.Parse(time.RFC3339Nano, item.Target.Version)
			if err != nil || !current.UpdatedAt.Equal(expectedAt) {
				return KnowledgeItem{}, s.markForgetNeedsReview(ctx, userID, doc, itemID, requestID)
			}
			if _, err := s.profiles.UpdateFieldCAS(ctx, binding.UserID, item.Target.Field, current.UpdatedAt, value, ""); err != nil {
				return KnowledgeItem{}, err // the prepared exclusion survives a transient SQL failure
			}
		}
		verified, err := s.profiles.Get(ctx, binding.UserID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		verifiedValue, err := canonicalProfileValue(verified, item.Target.Field)
		if err != nil || verifiedValue != "" {
			return KnowledgeItem{}, ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		var forgotten KnowledgeItem
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			for i := range latest.Items {
				current := &latest.Items[i]
				if current.ID != itemID || current.Version != expectedVersion || current.State != KnowledgeNeedsReview || current.Prepared == nil ||
					current.Prepared.RequestID != requestID || current.Prepared.Kind != "forget_profile" {
					continue
				}
				at := s.now().UTC()
				current.State = KnowledgeForgotten
				current.Prepared = nil
				current.Target = nil
				current.Version++
				current.UpdatedAt = at
				if len(latest.Receipts) >= knowledgeMaxReceipts {
					latest.Receipts = latest.Receipts[1:]
				}
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: "forgotten", CreatedAt: at})
				forgotten = *current
				return nil
			}
			return ErrConflict
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return forgotten, err
	}
	return KnowledgeItem{}, ErrConflict
}
