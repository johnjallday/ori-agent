package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// ErrFolderPlanChanged means the plan digest the browser sent no longer matches
// what Set up would do. The caller answers with the fresh offer (409).
var ErrFolderPlanChanged = errors.New("personal assistant: the setup plan changed")

// folderSetupTimeout bounds one run, which may include a plugin download.
const folderSetupTimeout = 20 * time.Minute

// eligibleForOneCard is true for what a one-card setup covers: a single
// recognized project with a reviewed capability row, or a whole collection of
// projects (a portfolio) — never one whose capability was suppressed.
func eligibleForOneCard(offer FolderOffer) bool {
	return !offer.CapabilitySuppressed && isProjectCapabilityOffer(offer)
}

// attachSetupRun shows the stored run. A run recorded as running with no live
// run in this process (the server restarted) is shown as interrupted.
func (s *FolderDigestService) attachSetupRun(offer FolderOffer, v *FolderOfferView) {
	if offer.Setup == nil || offer.Status != FolderOfferAwaitingOutcome {
		return
	}
	run := *offer.Setup
	view := &FolderSetupView{
		PlanDigest: run.PlanDigest, Status: run.Status, StopReason: run.StopReason, EntryCandidates: append([]string(nil), run.EntryCandidates...),
		Lines: append([]FolderPlanLine(nil), run.Lines...),
	}
	if run.Intent.Destination != nil {
		destination := *run.Intent.Destination
		view.Destination = &destination
	}
	if run.Status == FolderSetupRunning && !s.isRunning(offer.ID) {
		view.Status, view.StopReason = FolderSetupStopped, FolderStopInterrupted
		for i := range view.Lines {
			if view.Lines[i].State == FolderLineWorking {
				view.Lines[i].State = FolderLineFailed
			}
		}
	}
	v.Setup = view
}

// planPending reports whether an offer's setup is still to be planned: a pending
// offer, or one the user sent to the step-by-step journey (Adjust…) that has no
// one-card run yet, so the one-click plan stays available beside Continue setup.
func planPending(offer FolderOffer) bool {
	return offer.Status == FolderOfferPending ||
		(offer.Status == FolderOfferAwaitingOutcome && offer.Choice == FolderChoiceProject && offer.Setup == nil)
}

// attachSetupPlan reads the plan for an offer that has none running. A host that
// cannot plan leaves the card on the step-by-step journey rather than showing a
// guess.
func (s *FolderDigestService) attachSetupPlan(ctx context.Context, userID string, offer FolderOffer, v *FolderOfferView) {
	if s.deps.Setup == nil || !planPending(offer) || !eligibleForOneCard(offer) {
		return
	}
	plan, err := s.deps.Setup.Plan(ctx, s.setupRequest(ctx, userID, offer))
	if err != nil || len(plan.Lines) == 0 {
		v.SetupUnavailableReason = "setup_unavailable"
		if reason, ok := err.(interface{ SetupPreviewReason() string }); ok {
			switch reason.SetupPreviewReason() {
			case "integration_unavailable", "home_provider_unavailable", "destination_unavailable":
				v.SetupUnavailableReason = reason.SetupPreviewReason()
			}
		}
		return
	}
	plan = plan.Stamped()
	v.Plan = &plan
}

// setupRequest is what the host needs to plan an offer's setup.
func (s *FolderDigestService) setupRequest(ctx context.Context, userID string, offer FolderOffer) FolderSetupRequest {
	req := FolderSetupRequest{UserID: userID, Offer: offer}
	if row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension); ok &&
		row.Offer != nil && s.deps.AppInstalled != nil {
		req.AppInstalled = s.deps.AppInstalled(ctx, row.Offer.IntegrationName)
	}
	return req
}

// withFirstTask returns the receipt of a verified project with its first task
// seeded and described. Every other row is kept as the host reported it.
func (s *FolderDigestService) withFirstTask(ctx context.Context, userID, shape string, verified FolderCreateResult) []FolderReceiptRow {
	if s.deps.FirstTask == nil {
		return verified.Receipt
	}
	task, err := s.deps.FirstTask.SeedFirstTask(ctx, FolderFirstTaskRequest{
		UserID: userID, WorkspaceID: verified.WorkspaceID, Shape: folderdigest.Shape(shape),
	})
	rows := make([]FolderReceiptRow, 0, len(verified.Receipt)+1)
	for _, row := range verified.Receipt {
		if row.Kind != "task" { // the seeder's row replaces any earlier one
			rows = append(rows, row)
		}
	}
	if err != nil {
		logger.Warn("The setup's first task could not be added", logger.Fields{"workspace_id": verified.WorkspaceID, "error": err.Error()})
		return rows
	}
	return append(rows, task)
}

func (s *FolderDigestService) isRunning(offerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[offerID]
}

// claimRun marks an offer's setup as running in this process. It reports false
// when one already is, so a second click (or tab) starts nothing.
func (s *FolderDigestService) claimRun(offerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[offerID] {
		return false
	}
	s.running[offerID] = true
	return true
}

func (s *FolderDigestService) releaseRun(offerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, offerID)
}

// StartSetup is the click on Set up. It records the yes while the offer is
// pending, refuses a click made on a plan the server would no longer produce,
// and starts (or resumes) the run in the background. A second click while a run
// is live returns the current view and starts nothing.
func (s *FolderDigestService) StartSetup(ctx context.Context, userID, offerID string, input FolderSetupInput) (FolderOfferView, error) {
	if s == nil || s.store == nil || s.deps.Setup == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.PlanDigest = strings.TrimSpace(input.PlanDigest)
	input.EntryName = strings.TrimSpace(input.EntryName)
	if input.RequestID == "" || len(input.RequestID) > folderRequestIDMax || input.PlanDigest == "" || len(input.PlanDigest) > 64 {
		return FolderOfferView{}, fmt.Errorf("%w: setup request", ErrValidation)
	}
	if input.EntryName != "" && (len(input.EntryName) > folderDigestMaxName || filepath.Base(input.EntryName) != input.EntryName ||
		validateFolderName(input.EntryName) != nil) {
		return FolderOfferView{}, fmt.Errorf("%w: project file", ErrValidation)
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
		if receipt.OfferID != offerID || receipt.Action != "setup" {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		return s.viewFor(ctx, userID, *offer, binding.Paused), nil
	}
	if !eligibleForOneCard(*offer) {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	switch {
	case planPending(*offer):
	case offer.Status == FolderOfferAwaitingOutcome && offer.Choice == FolderChoiceProject && offer.Setup != nil:
	default:
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	if s.isRunning(offerID) {
		return s.viewFor(ctx, userID, *offer, binding.Paused), nil
	}
	release, guardErr := s.acquireConversationReview(ctx, *offer)
	if guardErr != nil {
		return FolderOfferView{}, guardErr
	}
	defer release()
	// A project file the user chooses must be one the server offered. The journey
	// checks the name against the folder again; this keeps a made-up name from
	// ever reaching it.
	if input.EntryName != "" && offer.Setup != nil && len(offer.Setup.EntryCandidates) > 0 {
		offered := false
		for _, candidate := range offer.Setup.EntryCandidates {
			offered = offered || candidate == input.EntryName
		}
		if !offered {
			return FolderOfferView{}, fmt.Errorf("%w: project file", ErrValidation)
		}
	}
	// The project's own folder: the scan root, or the subfolder the offer is about.
	path, err := s.subjectPathOf(*offer)
	if err != nil {
		return FolderOfferView{}, err
	}

	// The click consents to the plan on screen. An offer with no run yet is
	// checked against the plan the server would produce now; a resumed run, whose
	// plan legitimately shrinks as steps finish, against the plan it was
	// confirmed on.
	var plan FolderSetupPlan
	if offer.Setup == nil {
		fresh, planErr := s.deps.Setup.Plan(ctx, s.setupRequest(ctx, userID, *offer))
		if planErr != nil {
			return FolderOfferView{}, planErr
		}
		plan = fresh.Stamped()
		if plan.Digest == "" || plan.Digest != input.PlanDigest {
			return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
		}
	} else {
		if offer.Setup.PlanDigest != input.PlanDigest {
			return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
		}
		plan = FolderSetupPlan{
			Lines: append([]FolderPlanLine(nil), offer.Setup.Lines...), Digest: offer.Setup.PlanDigest,
			Intent: offer.Setup.Intent, Destination: offer.Setup.Intent.Destination,
		}
		if plan.Destination != nil {
			guard, ok := s.deps.Setup.(FolderSetupDestinationGuard)
			request := s.setupRequest(ctx, userID, *offer)
			request.Plan = plan
			if !ok || guard.ValidateSetupDestination(ctx, request) != nil {
				return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
			}
		}
	}

	now := s.now()
	entryName := input.EntryName
	var started FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil {
			return ErrFolderOfferNotFound
		}
		if item.Status == FolderOfferPending {
			decided := now
			item.Decision, item.RequestID, item.DecidedAt = FolderDecisionYes, input.RequestID, &decided
			item.Status, item.Choice = FolderOfferAwaitingOutcome, FolderChoiceProject
			item.Outcome = &FolderOutcome{Kind: FolderChoiceProject}
			d.Decisions = append(d.Decisions, FolderDecision{
				OfferID: item.ID, FolderKey: item.FolderKey, CandidateKey: item.Subject.Key,
				Name: item.Subject.Name, Verdict: item.Verdict, Decision: FolderDecisionYes, Choice: FolderChoiceProject, At: now,
			})
		}
		// A chosen file wins; then the one an earlier run was given; then the file
		// the user picked when they showed the folder.
		if entryName == "" && item.Setup != nil {
			entryName = item.Setup.EntryName
		}
		if entryName == "" {
			entryName = item.EntryName
		}
		lines := append([]FolderPlanLine(nil), plan.Lines...)
		for i := range lines {
			if lines[i].State == "" {
				lines[i].State = FolderLineWaiting
			}
		}
		runID := ""
		if item.Setup != nil {
			runID = item.Setup.RunID
		}
		item.Setup = &FolderSetupRun{
			PlanDigest: plan.Digest, Lines: lines, Status: FolderSetupRunning, EntryName: entryName,
			RunID: runID, StartedAt: now, UpdatedAt: now, Intent: plan.Intent,
		}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: item.ID, Action: "setup", At: now})
		pruneFolderDigest(d)
		started = *item
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	if !s.claimRun(offerID) {
		return s.viewFor(ctx, userID, started, binding.Paused), nil
	}
	// The run outlives the request: keep its values, drop its cancellation.
	go s.runSetup(context.WithoutCancel(ctx), userID, started, path, plan, entryName)
	return s.viewFor(ctx, userID, started, binding.Paused), nil
}

// runSetup drives one run on a context that outlives the request, then lets the
// existing journey verification settle the offer. A run that cannot be proved
// to have made a workspace for this exact folder never resolves the offer.
func (s *FolderDigestService) runSetup(parent context.Context, userID string, offer FolderOffer, path string, plan FolderSetupPlan, entryName string) {
	defer s.releaseRun(offer.ID)
	ctx, cancel := context.WithTimeout(parent, folderSetupTimeout)
	defer cancel()
	update := func(ctx context.Context, u FolderSetupUpdate) error { return s.recordSetup(ctx, userID, offer.ID, u) }
	err := s.deps.Setup.Run(ctx, FolderSetupRequest{
		UserID: userID, Offer: offer, Path: path, EntryName: entryName, Plan: plan, Update: update,
	})
	if err != nil {
		logger.Warn("One-card folder setup ended unexpectedly", logger.Fields{"offer_id": offer.ID, "error": err.Error()})
		reason := FolderStopFailed
		if errors.Is(err, ErrFolderPlanChanged) {
			reason = FolderStopPlanChanged
		}
		_ = s.recordSetup(context.WithoutCancel(ctx), userID, offer.ID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: reason, keepLines: true})
		return
	}
	s.settleSetup(context.WithoutCancel(ctx), userID, offer.ID)
}

// settleSetup resolves the offer through the same verification the journey's own
// completion uses, when the run reported that it finished.
func (s *FolderDigestService) settleSetup(ctx context.Context, userID, offerID string) {
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Setup == nil || offer.Setup.Status != FolderSetupDone || offer.Status != FolderOfferAwaitingOutcome {
		return
	}
	if offer.Portfolio != nil {
		// A collection resolves to its Home through the same proofs the
		// step-by-step path uses: a Home the reviewed template made after Set up
		// was pressed, or the owner's existing Home re-read from canonical state.
		requestID := "setup-" + offerID + "-home"
		if offer.Portfolio.ExistingHome {
			_, err = s.ResolveExistingHome(ctx, userID, offerID, requestID)
		} else {
			_, err = s.ResolvePortfolio(ctx, userID, offerID, FolderResolveInput{HomeID: offer.Setup.HomeID, RequestID: requestID})
		}
		if err != nil {
			logger.Warn("One-card collection setup finished but could not be verified", logger.Fields{"offer_id": offerID, "error": err.Error()})
			_ = s.recordSetup(ctx, userID, offerID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, keepLines: true})
		}
		return
	}
	_, err = s.ResolveJourney(ctx, userID, offerID, FolderJourneyInput{
		RunID: offer.Setup.RunID, RequestID: "setup-" + offerID + "-" + offer.Setup.RunID,
	})
	if err != nil {
		logger.Warn("One-card folder setup finished but could not be verified", logger.Fields{"offer_id": offerID, "error": err.Error()})
		_ = s.recordSetup(ctx, userID, offerID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, keepLines: true})
	}
}

// recordSetup writes a run snapshot onto the offer. It never touches an offer
// that is no longer awaiting its outcome, so a late update cannot undo a resolve.
func (s *FolderDigestService) recordSetup(ctx context.Context, userID, offerID string, u FolderSetupUpdate) error {
	now := s.now()
	_, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		offer := d.Offer(offerID)
		if offer == nil || offer.Setup == nil || offer.Status != FolderOfferAwaitingOutcome {
			return errFolderReplay
		}
		run := offer.Setup
		if !u.keepLines {
			run.Lines = append([]FolderPlanLine(nil), u.Lines...)
		}
		run.Status, run.StopReason, run.UpdatedAt = u.Status, u.StopReason, now
		if u.RunID != "" {
			run.RunID = u.RunID
		}
		if u.EntryName != "" {
			run.EntryName = u.EntryName
		}
		if u.HomeID != "" {
			run.HomeID = u.HomeID
		}
		if len(u.Receipt) > 0 {
			run.Receipt = append([]FolderReceiptRow(nil), u.Receipt...)
		}
		run.EntryCandidates = append([]string(nil), u.EntryCandidates...)
		return nil
	})
	if errors.Is(err, errFolderReplay) {
		return nil
	}
	return err
}
