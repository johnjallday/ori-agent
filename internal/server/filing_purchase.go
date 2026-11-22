package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func (b *ServerBuilder) filingClaimStore() *economy.SQLiteStore {
	if b.sessionStore == nil || b.sessionStore.DB() == nil {
		return nil
	}
	return economy.NewSQLiteStore(b.sessionStore.DB())
}

type filingConfirmation struct {
	RequestID string `json:"request_id"`
	Cost      *int64 `json:"cost"`
	Creative  *bool  `json:"creative"`
	CheckOnly bool   `json:"check_only"`
}

type filingPurchaseResult struct {
	Outcome string                 `json:"outcome"`
	Quote   economy.ExpansionQuote `json:"filing_expansion"`
}

const filingPurchaseTimeout = 30 * time.Second

var filingPurchaseOrigin = connectionshttp.NewOriginGuard()

func (b *ServerBuilder) purchaseFiling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if err := filingPurchaseOrigin.Check(r); err != nil {
		_ = orihttp.RespondForbidden(w, "Confirm the build from Ori's local page")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Filing purchase requires JSON confirmation")
		return
	}
	var input filingConfirmation
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || !input.valid() {
		_ = orihttp.RespondBadRequest(w, "Confirm the displayed price or check an existing purchase")
		return
	}
	if b.userProvider == nil || b.sessionHandler == nil || b.filingClaimStore() == nil {
		_ = orihttp.RespondServiceUnavailable(w, "Filing purchase is unavailable")
		return
	}
	owner, err := b.userProvider.CurrentUserID(r.Context())
	if err != nil || owner != userprofile.LocalUserID {
		_ = orihttp.RespondForbidden(w, "Only the installation owner can confirm this build")
		return
	}
	// Once accepted, finish/reconcile under a bounded server context even if
	// the browser loses its response. The installation reset lease still owns
	// the request and blocks reset while the route is active.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), filingPurchaseTimeout)
	defer cancel()
	ctx, release := b.sessionHandler.LockFilingCreation(ctx)
	defer release()
	if err := r.Context().Err(); err != nil {
		return // nothing was accepted while this client was queued
	}
	result, status := b.confirmFiling(ctx, owner, input)
	_ = orihttp.RespondJSON(w, status, result)
}

func (in filingConfirmation) valid() bool {
	if in.CheckOnly {
		return in.RequestID == "" && in.Cost == nil && in.Creative == nil
	}
	id, err := uuid.Parse(in.RequestID)
	return err == nil && id != uuid.Nil && id.String() == in.RequestID && in.Cost != nil && *in.Cost >= 0 && in.Creative != nil
}

func (b *ServerBuilder) confirmFiling(ctx context.Context, owner string, input filingConfirmation) (filingPurchaseResult, int) {
	store := b.filingClaimStore()
	unavailable := filingPurchaseResult{"unavailable", emptyFilingQuote(economy.ExpansionUnavailable)}
	claim, err := store.FindFilingClaim(ctx, owner, input.RequestID)
	if err != nil {
		return unavailable, http.StatusServiceUnavailable
	}
	if claim != nil {
		return b.resolveFiling(ctx, claim)
	}
	quote := b.quoteNewFiling(ctx)
	if input.CheckOnly || quote.State != economy.ExpansionAffordable {
		return filingPurchaseResult{string(quote.State), quote}, http.StatusConflict
	}
	if quote.EffectiveCost == nil || *quote.EffectiveCost != *input.Cost || quote.Creative != *input.Creative {
		return filingPurchaseResult{"stale_price", quote}, http.StatusConflict
	}
	plan, err := b.sessionHandler.ReviewFileJanitorCreation(ctx)
	if err != nil {
		logger.Warn("Filing purchase team could not be reviewed", logger.Fields{"error": err})
		return unavailable, http.StatusServiceUnavailable
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return unavailable, http.StatusServiceUnavailable
	}
	claim = &economy.FilingClaim{
		ID: uuid.NewString(), WorkspaceID: uuid.NewString(), Owner: owner, RequestID: input.RequestID,
		Cost: *quote.EffectiveCost, BaseCost: quote.BaseCost, PlanJSON: string(planJSON), State: economy.FilingBuilding,
	}
	// Settings and ownership can change while the team plan is read. Recheck
	// them as late as possible; the ledger's own atomic check arbitrates spend.
	fresh := b.quoteNewFiling(ctx)
	if fresh.State != economy.ExpansionAffordable {
		return filingPurchaseResult{string(fresh.State), fresh}, http.StatusConflict
	}
	if fresh.EffectiveCost == nil || *fresh.EffectiveCost != claim.Cost || fresh.Creative != *input.Creative {
		return filingPurchaseResult{"stale_price", fresh}, http.StatusConflict
	}
	if err := store.ReserveFiling(ctx, *claim); err != nil {
		if errors.Is(err, economy.ErrInsufficientBalance) {
			return filingPurchaseResult{"insufficient", b.quoteNewFiling(ctx)}, http.StatusConflict
		}
		// Never create after an uncertain reservation result. If it committed,
		// the original claim is recoverable without creating anything new.
		logger.Warn("Filing purchase could not be reserved", logger.Fields{"error": err})
		return unavailable, http.StatusServiceUnavailable
	}
	if _, err := b.sessionHandler.CreateReviewedFileJanitor(ctx, filingCreateRequest(claim, plan)); err != nil {
		logger.Warn("Filing purchase creation returned without success", logger.Fields{"operation_id": claim.ID, "error": err})
	}
	return b.resolveFiling(ctx, claim)
}

func emptyFilingQuote(state economy.ExpansionState) economy.ExpansionQuote {
	return economy.ExpansionQuote{State: state, BaseCost: economy.FilingExpansionCost}
}

func filingCreateRequest(claim *economy.FilingClaim, plan sessionhttp.ReviewedTemplateCreationPlan) sessionhttp.ReviewedTemplateCreationRequest {
	return sessionhttp.ReviewedTemplateCreationRequest{
		Name: "Filing", FolderSlug: "filing-" + claim.WorkspaceID, Plan: plan,
		Descriptor: sessionhttp.AssistantSetupCreationDescriptor{
			OwnerUserID: claim.Owner, WorkspaceID: claim.WorkspaceID,
			RunID: filejanitor.ExpansionRunPrefix + claim.ID, OperationID: claim.ID,
			ReviewDigest: plan.BlueprintDigest, ProfileProvenanceID: claim.ID, ConfigDigest: plan.Roles[0].ConfigDigest,
		},
	}
}

// resolveFiling never enters creation. This is shared by immediate results,
// client retries, explicit checks and server startup under the installation
// lease. Read-only quote polling does not use it.
func (b *ServerBuilder) resolveFiling(ctx context.Context, claim *economy.FilingClaim) (filingPurchaseResult, int) {
	if claim.State == economy.FilingBuilding {
		if err := b.reconcileFiling(ctx, claim); err != nil {
			logger.Warn("Filing purchase needs recovery", logger.Fields{"operation_id": claim.ID, "error": err})
		}
	}
	if claim.State == economy.FilingRefunded {
		return filingPurchaseResult{"refunded", b.quoteNewFiling(ctx)}, http.StatusConflict
	}
	quote := b.quoteFilingClaim(ctx, claim)
	if claim.State == economy.FilingDelivered {
		return filingPurchaseResult{"delivered", quote}, http.StatusOK
	}
	return filingPurchaseResult{"pending", quote}, http.StatusAccepted
}

func (b *ServerBuilder) reconcileFiling(ctx context.Context, claim *economy.FilingClaim) error {
	store := b.filingClaimStore()
	var plan sessionhttp.ReviewedTemplateCreationPlan
	if err := json.Unmarshal([]byte(claim.PlanJSON), &plan); err != nil || len(plan.Roles) != 1 {
		return errors.New("the recorded Filing plan is unreadable")
	}
	request := filingCreateRequest(claim, plan)
	observation, err := b.sessionHandler.ObserveReviewedFileJanitor(request)
	if err != nil {
		return err
	}
	if observation.WorkspaceProven && observation.AgentInstanceID != "" &&
		(plan.Roles[0].Action == "reuse" || observation.ProfileCreated) && b.filingClaimDestination(ctx, claim) != nil {
		if err := store.CompleteFiling(ctx, *claim); err != nil {
			return err
		}
		claim.State = economy.FilingDelivered
		return nil
	}
	if !observation.WorkspacePresent && !claim.Registered {
		_, rowErr := b.sessionStore.GetWorkspace(ctx, claim.WorkspaceID)
		if errors.Is(rowErr, session.ErrWorkspaceNotFound) && b.workspaceFileStore != nil {
			if _, err := b.workspaceFileStore.GetFolderPath(claim.WorkspaceID); err == nil {
				return errors.New("a folder exists without the purchase workspace registration")
			}
			// The atomic registration witness rules out any write by this
			// creator to a workspace even after its runtime cache is gone.
			if err := store.RefundFiling(ctx, *claim); err != nil {
				return err
			}
			claim.State = economy.FilingRefunded
			return nil
		}
	}
	return errors.New("delivery cannot yet be proved; retain the original claim")
}

func (b *ServerBuilder) filingClaimDestination(ctx context.Context, claim *economy.FilingClaim) *economy.ExpansionDestination {
	if b.workspaceFileStore == nil {
		return nil
	}
	row, err := b.sessionStore.GetWorkspace(ctx, claim.WorkspaceID)
	if err != nil || row == nil || row.OwnerUserID != claim.Owner || row.IsGroup() || row.Status != session.WorkspaceStatusActive ||
		!workspace.IsCanonicalWorkspaceSlug(row.FolderSlug) {
		return nil
	}
	ws, err := b.workspaceFileStore.Get(row.ID)
	if err != nil || !visibleFilingRecord(ws, *row, claim.Owner) || !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return nil
	}
	p := ws.GetTemplateProvenance()
	if p == nil || !p.Builtin || p.TemplateID != fileJanitorBlueprintID || p.AssistantSetup == nil ||
		p.AssistantSetup.RunID != filejanitor.ExpansionRunPrefix+claim.ID || p.AssistantSetup.OperationID != claim.ID {
		return nil
	}
	return &economy.ExpansionDestination{Name: row.Name, Route: "/workspaces/" + row.FolderSlug + "?panel=file-janitor"}
}

func (b *ServerBuilder) quoteFilingClaim(ctx context.Context, claim *economy.FilingClaim) economy.ExpansionQuote {
	quote := emptyFilingQuote(economy.ExpansionPending)
	quote.BaseCost = claim.BaseCost
	if balances, err := b.filingClaimStore().Balances(ctx); err == nil {
		quote.Balance = &balances.Craft
	}
	if claim.State == economy.FilingDelivered {
		quote.State = economy.ExpansionRemoved
		waived := claim.BaseCost - claim.Cost
		quote.Charged, quote.Waived = &claim.Cost, &waived
		if destination := b.filingClaimDestination(ctx, claim); destination != nil {
			quote.State = economy.ExpansionBuilt
			quote.Destinations = []economy.ExpansionDestination{*destination}
		}
	}
	return quote
}

func (b *ServerBuilder) recoverFilingPurchase() {
	if b.sessionHandler == nil || b.filingClaimStore() == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	claim, err := b.filingClaimStore().FindFilingClaim(ctx, userprofile.LocalUserID, "")
	if err != nil {
		logger.Warn("Filing startup recovery could not read the saved claim", logger.Fields{"error": err})
		return
	}
	if claim != nil && claim.State == economy.FilingBuilding {
		_, _ = b.resolveFiling(ctx, claim)
	}
}
