package personalassistant

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// FolderSetupEntryInput changes one pending review's file choice, not its
// confirmation or run. PlanDigest fences the plan the chooser was shown on.
type FolderSetupEntryInput struct {
	PlanDigest string
	EntryName  string
}

func setupEntryOffered(plan FolderSetupPlan, name string) bool {
	if len(plan.EntryCandidates) == 0 {
		return name == plan.EntryName
	}
	for _, candidate := range plan.EntryCandidates {
		if candidate == name {
			return true
		}
	}
	return false
}

func resumedSetupEntry(plan FolderSetupPlan, run FolderSetupRun, requested string) (FolderSetupPlan, string, error) {
	if run.StopReason == FolderStopNeedsChoice {
		offered := false
		for _, candidate := range run.EntryCandidates {
			offered = offered || candidate == requested
		}
		if requested == "" || !offered {
			return FolderSetupPlan{}, "", fmt.Errorf("%w: project file", ErrValidation)
		}
		return WithFolderSetupEntry(plan, requested, nil), requested, nil
	}
	if requested != "" && requested != run.EntryName {
		return FolderSetupPlan{}, "", fmt.Errorf("%w: project file", ErrValidation)
	}
	return plan, run.EntryName, nil
}

// WithFolderSetupEntry names the file consequence in the folder step and binds
// it into confirmation. A stopped run can use it after an explicit answer to
// its runtime file question, without changing the other reviewed effects.
func WithFolderSetupEntry(plan FolderSetupPlan, name string, candidates []string) FolderSetupPlan {
	if !validSetupPlanEntries(name, candidates) {
		return FolderSetupPlan{}
	}
	lines := make([]FolderPlanLine, 0, len(plan.Lines)+1)
	added := false
	for _, line := range plan.Lines {
		if plan.EntryName != "" && line.Kind == FolderPlanFolder && line.Name == "Opens "+plan.EntryName {
			continue
		}
		lines = append(lines, line)
		if name != "" && line.Kind == FolderPlanFolder && !added {
			lines = append(lines, FolderPlanLine{Kind: FolderPlanFolder, Name: "Opens " + name})
			added = true
		}
	}
	if name != "" && !added {
		lines = append(lines, FolderPlanLine{Kind: FolderPlanFolder, Name: "Opens " + name})
	}
	plan.Lines, plan.EntryName = lines, name
	plan.EntryCandidates = append([]string(nil), candidates...)
	return plan.Stamped()
}

// folderSetupEntryReceipt names only a finished run's committed file. It is
// called after the workspace proof; a pending choice alone earns no receipt.
func folderSetupEntryReceipt(offer *FolderOffer, rows []FolderReceiptRow) []FolderReceiptRow {
	if offer == nil || offer.Setup == nil || offer.Setup.Status != FolderSetupDone || !ValidFolderEntryName(offer.Setup.EntryName) {
		return rows
	}
	name := "Opens " + offer.Setup.EntryName
	for _, row := range rows {
		if row.Kind == FolderPlanFolder && row.Name == name {
			return rows
		}
	}
	return append(append([]FolderReceiptRow(nil), rows...), FolderReceiptRow{Kind: FolderPlanFolder, Name: name})
}

// ChooseSetupEntry saves a choice only for an undecided plan of the same
// canonical review. It starts no setup, model turn, journey or file operation.
func (s *FolderDigestService) ChooseSetupEntry(ctx context.Context, userID, offerID string, input FolderSetupEntryInput) (FolderOfferView, error) {
	if s == nil || s.store == nil || s.deps.Setup == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	input.PlanDigest = strings.TrimSpace(input.PlanDigest)
	if !ValidFolderEntryName(input.EntryName) || input.PlanDigest == "" || len(input.PlanDigest) > 64 {
		return FolderOfferView{}, fmt.Errorf("%w: project file choice", ErrValidation)
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return FolderOfferView{}, ErrFolderOfferNotFound
	}
	if !planPending(*offer) || offer.Setup != nil || offer.PlacementHandoff != nil || !eligibleForOneCard(*offer) {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	release, err := s.acquireConversationReview(ctx, *offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	defer release()
	path, err := s.subjectPathOf(userID, *offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	ctx = withFolderSubjectPath(ctx, path)
	plan, err := s.deps.Setup.Plan(ctx, s.planRequest(ctx, userID, *offer))
	if err != nil {
		return FolderOfferView{}, err
	}
	plan = plan.Stamped()
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	if plan.Digest == "" || plan.Digest != input.PlanDigest {
		return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
	}
	if !setupEntryOffered(plan, input.EntryName) {
		return FolderOfferView{}, fmt.Errorf("%w: project file", ErrValidation)
	}
	chosen := *offer
	chosen.EntryName = input.EntryName
	fresh, err := s.deps.Setup.Plan(ctx, s.planRequest(ctx, userID, chosen))
	if err != nil {
		return FolderOfferView{}, err
	}
	if fresh.Stamped().Digest == "" || fresh.EntryName != input.EntryName {
		return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
	}
	updated, err := s.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
		current := doc.Offer(offerID)
		if current == nil || !reflect.DeepEqual(*current, *offer) {
			return ErrConflict
		}
		current.EntryName = input.EntryName
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	return s.viewFor(ctx, userID, *updated.Offer(offerID), binding.Paused), nil
}
