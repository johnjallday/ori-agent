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
// recognized project with a reviewed capability row, an inbox folder, or a
// whole collection of projects (a portfolio) — never one whose capability was
// suppressed.
func eligibleForOneCard(offer FolderOffer) bool {
	// A proposed group is the host's own setup and needs no capability: a
	// domain the user declined only changes what the group is made of.
	if offer.Structure.ProposesWorkspaces() {
		return true
	}
	return !offer.CapabilitySuppressed && (isProjectCapabilityOffer(offer) || isSoftwareOffer(offer) || isInboxOffer(offer))
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
		Lines: append([]FolderPlanLine(nil), run.Lines...), Brief: run.Brief,
		Visual: CloneFolderSetupVisual(run.Visual),
	}
	if run.Hire != nil {
		hire := *run.Hire
		view.Hire = &hire
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
	if isSoftwareOffer(offer) && !softwarePlannable(offer, v) {
		return
	}
	plan, err := s.deps.Setup.Plan(ctx, s.planRequest(ctx, userID, offer))
	if err != nil || len(plan.Lines) == 0 {
		v.SetupUnavailableReason = "setup_unavailable"
		v.Availability = setupAvailability(err)
		if reason, ok := err.(interface{ SetupPreviewReason() string }); ok {
			switch reason.SetupPreviewReason() {
			case "integration_unavailable", "home_provider_unavailable", "destination_unavailable",
				"project_exists", "assistant_unavailable", "name_taken", "folder_tidied", "root_refused":
				v.SetupUnavailableReason = reason.SetupPreviewReason()
			}
		}
		if refusal, ok := err.(interface {
			SetupRefusal() (message, ownerName, ownerRoute string)
		}); ok {
			message, name, route := refusal.SetupRefusal()
			v.SetupUnavailableMessage = truncateRunes(strings.TrimSpace(message), 300)
			if name != "" && strings.HasPrefix(route, "/workspaces/") && !strings.HasPrefix(route, "//") {
				v.Tidied = &FolderTidiedView{Name: truncateRunes(name, 120), Route: route}
			}
		}
		return
	}
	plan = plan.Stamped()
	v.Plan = &plan
	v.Needs = folderPlanNeeds(offer.Subject, plan.Intent)
	// The names the plan uses, so the card's fields start from them rather than
	// from a reading of the plan's wording.
	if v.Software != nil && plan.Intent.Software != nil {
		v.Software.WorkspaceName, v.Software.Assistant = plan.Intent.Software.WorkspaceName, plan.Intent.Software.Assistant
	}
}

// attachSetupReadiness is deliberately separate from attachSetupPlan: the
// latter also runs during attachment eligibility. Partial prerequisite reads
// belong to a canonical review (including a single project's automatic review),
// never raw selection, ambiguous attachment or an eligibility check.
func (s *FolderDigestService) attachSetupReadiness(ctx context.Context, userID string, offer FolderOffer, v *FolderOfferView) {
	_, pluginControls := s.deps.Setup.(FolderPrerequisiteSetup)
	v.PrerequisitesAvailable = pluginControls && offer.Status == FolderOfferPending && offer.Setup == nil && offer.Declined == nil && !v.NeedsPick &&
		offer.ConversationReview != nil && !IsBuildReview(offer.ConversationReview.Target) && offer.ConversationReview.Operation == FolderOperationCreate &&
		v.Capability != nil && (v.Capability.SetupSource == "host" || v.Capability.SetupSource == "plugin" || libraryReview(offer)) && v.DestinationStatus != "changed"
	if v.Plan != nil || v.Setup != nil || v.NeedsPick || !planPending(offer) || !eligibleForOneCard(offer) {
		return
	}
	if reader, ok := s.deps.Setup.(FolderSetupReadinessReader); ok {
		v.Readiness = CloneFolderSetupVisual(reader.ReadSetupReadiness(ctx, s.setupRequest(ctx, userID, offer)))
	}
}

// setupRequest is what the host needs to plan an offer's setup.
func (s *FolderDigestService) setupRequest(ctx context.Context, userID string, offer FolderOffer) FolderSetupRequest {
	req := FolderSetupRequest{UserID: userID, Offer: offer}
	if review := offer.ConversationReview; review != nil {
		req.WorkspaceName = review.WorkspaceName
	}
	if isInboxOffer(offer) {
		return req
	}
	if isSoftwareOffer(offer) {
		// A request the service cannot fill in (the folder is no longer held, or
		// its workspaces cannot be read) carries no software part, and the host
		// plans nothing for it.
		if software, err := s.softwareRequest(ctx, userID, offer); err == nil {
			req.Software = software
		}
		return req
	}
	if row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension); ok &&
		row.Offer != nil && s.deps.AppInstalled != nil {
		req.AppInstalled = s.deps.AppInstalled(ctx, row.Offer.IntegrationName)
	}
	return req
}

// withFirstTask returns the receipt of a verified project with its first task
// seeded and described. Every other row is kept as the host reported it. brief
// is what the user asked the hire to do, given to the look as their intent.
func (s *FolderDigestService) withFirstTask(ctx context.Context, userID, shape, brief string, verified FolderCreateResult) []FolderReceiptRow {
	if s.deps.FirstTask == nil {
		return verified.Receipt
	}
	task, err := s.deps.FirstTask.SeedFirstTask(ctx, FolderFirstTaskRequest{
		UserID: userID, WorkspaceID: verified.WorkspaceID, Shape: folderdigest.Shape(shape), Brief: brief,
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
	brief, err := NormalizeFolderBrief(input.Brief)
	if err != nil {
		return FolderOfferView{}, err
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
	if offer.Status == FolderOfferPending && doc.Tombstoned(offer.Subject.Key) {
		return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderReviewRequired
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
		if errors.Is(guardErr, ErrFolderPlanChanged) {
			return s.viewFor(ctx, userID, *offer, binding.Paused), guardErr
		}
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
	if isSoftwareOffer(*offer) {
		if offer.NeedsCandidate {
			return FolderOfferView{}, ErrFolderCandidateRequired
		}
		if !s.softwareSetupAvailable() {
			return FolderOfferView{}, ErrFolderOutcomeUnavailable
		}
		// The directory must still be the one the offer was made about.
		if path, _, err = s.softwareSubject(*offer); err != nil {
			return FolderOfferView{}, err
		}
		if offer.Setup == nil {
			// Set up is an explicit click, so the project's files are read once
			// more: a review made on files that have since changed is not the
			// review being confirmed.
			refreshed, changed, checkErr := s.recheckSoftwareEvidence(ctx, userID, *offer, path)
			if checkErr != nil {
				return FolderOfferView{}, checkErr
			}
			if changed {
				return s.viewFor(ctx, userID, refreshed, binding.Paused), ErrFolderPlanChanged
			}
		}
	}

	// The click consents to the plan on screen. An offer with no run yet is
	// checked against the plan the server would produce now; a resumed run, whose
	// plan legitimately shrinks as steps finish, against the plan it was
	// confirmed on.
	var plan FolderSetupPlan
	if offer.Setup == nil {
		fresh, planErr := s.deps.Setup.Plan(ctx, s.planRequest(withFolderSubjectPath(ctx, path), userID, *offer))
		if planErr != nil {
			return FolderOfferView{}, planErr
		}
		plan = fresh.Stamped()
		if plan.Digest == "" || plan.Digest != input.PlanDigest {
			return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
		}
		// A plan that hires for a new workspace takes the user's brief, or its
		// default when the field was cleared: a workspace is never created with
		// an empty goal. A plan with no question takes no brief.
		switch {
		case plan.BriefDefault == "":
			brief = ""
		case brief == "":
			brief = plan.BriefDefault
		}
	} else {
		if offer.Setup.PlanDigest != input.PlanDigest {
			return s.viewFor(ctx, userID, *offer, binding.Paused), ErrFolderPlanChanged
		}
		plan = FolderSetupPlan{
			Lines: append([]FolderPlanLine(nil), offer.Setup.Lines...), Digest: offer.Setup.PlanDigest,
			Intent: offer.Setup.Intent, Destination: offer.Setup.Intent.Destination, Hire: offer.Setup.Hire,
			Visual: CloneFolderSetupVisual(offer.Setup.Visual),
		}
		// A resume carries on with the brief the run was started with.
		brief = offer.Setup.Brief
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
		// A ticked watching checkbox is the user's own choice, outside the
		// digest: the run carries it as a fifth line, and a resume keeps it.
		monitoring := plan.Intent.Inbox != nil && input.Monitoring && plan.Monitoring != nil
		if item.Setup != nil {
			monitoring = plan.Intent.Inbox != nil && item.Setup.Monitoring
		}
		if monitoring {
			if !hasPlanLineKind(lines, FolderPlanMode) {
				lines = append(lines, FolderPlanLine{Kind: FolderPlanMode, Name: "Watching turned on",
					Detail: "Ori notices new files after a short settle time and proposes them for review. Nothing is filed by itself."})
			}
			plan.Lines = append([]FolderPlanLine(nil), lines...)
		}
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
			RunID: runID, StartedAt: now, UpdatedAt: now, Intent: plan.Intent, Brief: brief,
			Visual: CloneFolderSetupVisual(plan.Visual), Monitoring: monitoring,
		}
		if plan.Hire != nil {
			hire := *plan.Hire
			item.Setup.Hire = &hire
		}
		if structure := plan.Intent.Structure; structure != nil && structure.GroupType == FolderGroupPlain {
			// The user confirmed a plain group. The folder is no longer a
			// collection waiting for a Home, and nothing later treats it as one.
			item.Portfolio = nil
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

func hasPlanLineKind(lines []FolderPlanLine, kind string) bool {
	for _, line := range lines {
		if line.Kind == kind {
			return true
		}
	}
	return false
}

// runSetup drives one run on a context that outlives the request, then lets the
// existing journey verification settle the offer. A run that cannot be proved
// to have made a workspace for this exact folder never resolves the offer.
func (s *FolderDigestService) runSetup(parent context.Context, userID string, offer FolderOffer, path string, plan FolderSetupPlan, entryName string) {
	defer s.releaseRun(offer.ID)
	ctx, cancel := context.WithTimeout(parent, folderSetupTimeout)
	defer cancel()
	update := func(ctx context.Context, u FolderSetupUpdate) error { return s.recordSetup(ctx, userID, offer.ID, u) }
	request := FolderSetupRequest{
		UserID: userID, Offer: offer, Path: path, EntryName: entryName, Plan: plan, Update: update,
	}
	if offer.Setup != nil {
		request.Brief = offer.Setup.Brief
		request.Monitoring = offer.Setup.Monitoring
	}
	if review := offer.ConversationReview; review != nil {
		request.WorkspaceName = review.WorkspaceName
	}
	if isSoftwareOffer(offer) {
		software, softwareErr := s.softwareRequest(ctx, userID, offer)
		if softwareErr != nil {
			// The folder was lost or replaced between the click and the run.
			reason := FolderStopFailed
			if errors.Is(softwareErr, ErrFolderPathLost) {
				reason = FolderStopNeedsPick
			}
			_ = s.recordSetup(context.WithoutCancel(ctx), userID, offer.ID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: reason, keepLines: true})
			return
		}
		request.Software = software
	}
	err := s.deps.Setup.Run(ctx, request)
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
	if offer.Setup.Intent.Recipe == FolderRecipeStructure {
		// A structure has no plugin quest to read back. Its proof is the group
		// and its workspaces themselves, re-read from canonical state.
		if err := s.resolveStructure(ctx, userID, offerID); err != nil {
			logger.Warn("Folder structure setup finished but could not be verified", logger.Fields{"offer_id": offerID, "error": err.Error()})
			_ = s.recordSetup(ctx, userID, offerID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, keepLines: true})
		}
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
	if offer.Setup.Intent.Recipe == folderdigest.BuiltinInboxFolder {
		// Its proof is the File Janitor workspace, its grant and its first
		// batch, re-read from canonical state. The batch is the first look:
		// nothing else is started.
		if err := s.resolveInbox(ctx, userID, offerID); err != nil {
			logger.Warn("Inbox folder setup finished but could not be verified", logger.Fields{"offer_id": offerID, "error": err.Error()})
			_ = s.recordSetup(ctx, userID, offerID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, keepLines: true})
		}
		return
	}
	if isSoftwareOffer(*offer) {
		// A built-in setup has no plugin quest to read back. Its proof is the
		// workspace itself, re-read from canonical state for this exact folder.
		if err := s.resolveSoftware(ctx, userID, offerID); err != nil {
			logger.Warn("Software project setup finished but could not be verified", logger.Fields{"offer_id": offerID, "error": err.Error()})
			_ = s.recordSetup(ctx, userID, offerID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, keepLines: true})
			return
		}
		s.linkOptionalLook(ctx, userID, offerID)
		return // Setup consent never starts the optional project task.
	}
	_, err = s.ResolveJourney(ctx, userID, offerID, FolderJourneyInput{
		RunID: offer.Setup.RunID, RequestID: "setup-" + offerID + "-" + offer.Setup.RunID,
	})
	if err != nil {
		logger.Warn("One-card folder setup finished but could not be verified", logger.Fields{"offer_id": offerID, "error": err.Error()})
		_ = s.recordSetup(ctx, userID, offerID, FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, keepLines: true})
		return
	}
	s.linkOptionalLook(ctx, userID, offerID)
	// The workspace is verified and its first task seeded. A separate user
	// action owns its start, including when this was an old run being resumed.
}

// recordSetup writes a run snapshot onto the offer. It never touches an offer
// that is no longer awaiting its outcome, so a late update cannot undo a resolve.
func (s *FolderDigestService) recordSetup(ctx context.Context, userID, offerID string, u FolderSetupUpdate) error {
	now := s.now()
	updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
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
	// A run that stopped is settled; one that finished is settled by its
	// resolve (afterOutcome), or stopped again when it cannot be verified.
	if err == nil && u.Status == FolderSetupStopped {
		if stored := updated.Offer(offerID); stored != nil {
			s.notifySetup(ctx, userID, *stored)
		}
	}
	return err
}
