package agenthttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// A saved PA arrangement is applied one arranged group at a time, through the
// existing conversation-bound folder setup review: its plan, its digest-checked
// Set up, its structure run and its canonical receipt. Nothing here confirms an
// effect. These handlers compile and bind that review to the exact record,
// revision and operation, and read back what the bound review became.

// Arrangement effect states as the canonical correlation records them.
const (
	arrangementEffectAwaiting  = "awaiting_user_review"
	arrangementEffectApplying  = "applying"
	arrangementEffectPartial   = "partially_applied"
	arrangementEffectCompleted = "completed"
	arrangementEffectClosed    = "closed"
)

// PersonalAssistantArrangementEffect mirrors one canonical correlation row.
type PersonalAssistantArrangementEffect struct {
	Operation      int
	RecordRevision int64
	OfferID        string
	MessageID      string
	State          string
	WorkspaceID    string
	Route          string
	Workspaces     int
	ReceiptDigest  string
}

// PersonalAssistantSavedArrangement is a canonical record with the owning
// store's pins and its group-review correlations. It is history, not authority.
type PersonalAssistantSavedArrangement struct {
	Record         workspacearrangement.Record
	MessageID      string
	StateVersion   int64
	Imported       bool
	ContextCurrent bool
	Effects        []PersonalAssistantArrangementEffect
}

// PersonalAssistantArrangementReview binds one opened group review.
type PersonalAssistantArrangementReview struct {
	RecordID       string
	RecordRevision int64
	Operation      int
	FolderRevision string
	Event          foldercontext.Event
}

type personalAssistantArrangementExecutionStore interface {
	ReadArrangementExecution(context.Context, string, assistantcontext.SaveOwner, string) (*PersonalAssistantSavedArrangement, error)
	BindArrangementReview(context.Context, string, assistantcontext.SaveOwner, PersonalAssistantArrangementReview) ([]PersonalAssistantConversationMessage, error)
	ReconcileArrangementEffect(context.Context, string, assistantcontext.SaveOwner, string, PersonalAssistantArrangementEffect, PersonalAssistantArrangementEffect) error
	DeclineArrangement(context.Context, string, assistantcontext.SaveOwner, string, int64) (workspacearrangement.Record, error)
}

type personalAssistantArrangementSetups interface {
	ReviewArrangementGroup(context.Context, foldercontext.Target, string, personalassistant.FolderArrangementGroup) (personalassistant.FolderOfferView, bool, error)
	ArrangementOutcome(context.Context, foldercontext.Target, string) (personalassistant.FolderArrangementOutcome, error)
	ConversationOfferStatus(context.Context, foldercontext.Target, string) (personalassistant.FolderOfferStatus, error)
}

// PersonalAssistantArrangementExecution is the history card's group list. Each
// state is read from that group's own bound review; Reviewable and Declinable
// mean the request may be made, which then rechecks every authority, never
// that it will succeed.
type PersonalAssistantArrangementExecution struct {
	State      string                                   `json:"state"`
	Reviewable bool                                     `json:"reviewable"`
	Declinable bool                                     `json:"declinable"`
	Groups     []PersonalAssistantArrangementGroupState `json:"groups"`
}

// PersonalAssistantArrangementGroupState is one create_group intention.
type PersonalAssistantArrangementGroupState struct {
	Operation  int    `json:"operation"`
	Name       string `json:"name"`
	Members    int    `json:"members"`
	State      string `json:"state"`
	OfferID    string `json:"offer_id,omitempty"`
	Route      string `json:"route,omitempty"`
	Workspaces int    `json:"workspaces,omitempty"`
}

// arrangementFingerprint pins the configured provider/model and the exact
// registered provider instance, without credentials. A changed registration,
// even under the same name, is a different runtime.
func arrangementFingerprint(configuredProvider, configuredModel, model string, pointer uintptr) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", configuredProvider, configuredModel, model, pointer)))
	return hex.EncodeToString(sum[:])
}

// checkArrangementRuntime refuses model-authored intent once the runtime that
// wrote it is no longer the configured one. Reviewing calls no model.
func (h *HomeAssistantAskHandler) checkArrangementRuntime(record workspacearrangement.Record) error {
	if record.Origin != "model" {
		return nil
	}
	if h.SystemModel == nil {
		return workspacearrangement.ErrStale
	}
	provider, model, err := h.resolveProvider()
	if err != nil || provider == nil || strings.TrimSpace(model) == "" {
		return workspacearrangement.ErrStale
	}
	value := reflect.ValueOf(provider)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return workspacearrangement.ErrStale
	}
	configuredProvider, configuredModel := h.SystemModel.GetSystemModel()
	if provider.Name() != record.Provider || model != record.Model ||
		arrangementFingerprint(configuredProvider, configuredModel, model, value.Pointer()) != record.ProviderFingerprint {
		return workspacearrangement.ErrStale
	}
	return nil
}

func findArrangementEffect(saved *PersonalAssistantSavedArrangement, operation int) *PersonalAssistantArrangementEffect {
	for i := range saved.Effects {
		if saved.Effects[i].Operation == operation {
			return &saved.Effects[i]
		}
	}
	return nil
}

// reconcileArrangementEffects records what each bound review's own canonical
// offer already shows: monotonic correlation only. It never creates, confirms,
// resumes or retries anything, and an unreadable offer stays as recorded.
func (h *HomeAssistantAskHandler) reconcileArrangementEffects(ctx context.Context, target foldercontext.Target, owner assistantcontext.SaveOwner, saved *PersonalAssistantSavedArrangement) {
	setups, ok := h.FolderSetups.(personalAssistantArrangementSetups)
	store, canStore := h.Conversations.(personalAssistantArrangementExecutionStore)
	if !ok || !canStore || saved == nil {
		return
	}
	for i, effect := range saved.Effects {
		if effect.State == arrangementEffectCompleted || effect.State == arrangementEffectClosed {
			continue
		}
		outcome, err := setups.ArrangementOutcome(ctx, target, effect.OfferID)
		if err != nil || outcome.Binding.RecordID != saved.Record.ID || outcome.Binding.Operation != effect.Operation {
			continue
		}
		next := effect
		switch outcome.Status {
		case personalassistant.FolderOfferResolved:
			if outcome.WorkspaceID == "" || outcome.Workspaces < 1 {
				continue
			}
			next.State, next.WorkspaceID, next.Route, next.Workspaces, next.ReceiptDigest = arrangementEffectCompleted, outcome.WorkspaceID, outcome.Route, outcome.Workspaces, outcome.ReceiptDigest
		case personalassistant.FolderOfferClosed, personalassistant.FolderOfferDeclined:
			if effect.State != arrangementEffectAwaiting {
				continue
			}
			next.State = arrangementEffectClosed
		case personalassistant.FolderOfferAwaitingOutcome:
			next.State = arrangementEffectApplying
			if outcome.SetupStatus == personalassistant.FolderSetupStopped || (outcome.SetupStatus == personalassistant.FolderSetupRunning && !outcome.Running) {
				next.State = arrangementEffectPartial
			}
		default:
			continue
		}
		if next.State == effect.State {
			continue
		}
		if store.ReconcileArrangementEffect(ctx, target.ConversationID, owner, saved.Record.ID, effect, next) == nil {
			saved.Effects[i] = next
		}
	}
}

// arrangementExecution projects the group list from canonical state only. It
// reads the conversation's current folder event but never the folder itself.
func (h *HomeAssistantAskHandler) arrangementExecution(ctx context.Context, target foldercontext.Target, owner assistantcontext.SaveOwner, saved *PersonalAssistantSavedArrangement) *PersonalAssistantArrangementExecution {
	record := saved.Record
	if record.SourceKind != workspacearrangement.Attachment {
		return nil
	}
	out := &PersonalAssistantArrangementExecution{State: record.State, Groups: []PersonalAssistantArrangementGroupState{}}
	open := (record.State == "draft" || record.State == "needs_more_evidence") && !saved.Imported && saved.StateVersion == owner.StateVersion
	completed, total, partial, applying, awaiting := 0, 0, false, false, false
	for index, op := range record.Draft.Operations {
		if op.Kind != workspacearrangement.CreateGroup {
			continue
		}
		total++
		group := PersonalAssistantArrangementGroupState{Operation: index, Name: op.Name, Members: len(op.Members.IDs), State: "not_reviewed"}
		if effect := findArrangementEffect(saved, index); effect != nil {
			group.State, group.OfferID = effect.State, effect.OfferID
			switch effect.State {
			case arrangementEffectCompleted:
				completed++
				group.Route, group.Workspaces = effect.Route, effect.Workspaces
			case arrangementEffectPartial:
				partial = true
			case arrangementEffectApplying:
				applying = true
			case arrangementEffectAwaiting:
				awaiting = true
			}
		}
		out.Groups = append(out.Groups, group)
	}
	switch {
	case total == 0:
	case completed == total:
		out.State = arrangementEffectCompleted
	case completed > 0 || partial:
		out.State = arrangementEffectPartial
	case applying:
		out.State = arrangementEffectApplying
	case awaiting:
		out.State = arrangementEffectAwaiting
	}
	out.Declinable = open && completed == 0 && !partial && !applying
	if open && total > completed {
		if state, err := h.folderState(ctx, target); err == nil && state.Observation != nil && state.Observation.ID == record.SourceID {
			out.Reviewable = true
		}
	}
	return out
}

func writeArrangementReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalassistant.ErrFolderArrangementReview):
		detail := strings.TrimPrefix(err.Error(), personalassistant.ErrFolderArrangementReview.Error()+": ")
		if detail == err.Error() || detail == "" {
			detail = "this arranged group cannot be reviewed as it stands"
		}
		writeConversationError(w, http.StatusConflict, "arrangement_group_unavailable", strings.ToUpper(detail[:1])+detail[1:]+". Nothing was created.")
	case errors.Is(err, personalassistant.ErrFolderReviewPending):
		writeConversationError(w, http.StatusConflict, "folder_review_pending", "Another setup review is open. Finish it, or choose Keep chatting on it, before reviewing this group. Nothing was created.")
	case errors.Is(err, personalassistant.ErrFolderOfferDecided):
		writeConversationError(w, http.StatusConflict, "folder_review_declined", "Setup suggestions for this folder were declined earlier. Nothing was created.")
	case errors.Is(err, personalassistant.ErrFolderPathLost), errors.Is(err, personalassistant.ErrFolderSelection), errors.Is(err, workspacearrangement.ErrStale), errors.Is(err, workspacearrangement.ErrUnavailable):
		writeConversationError(w, http.StatusConflict, "arrangement_source_unavailable", "This folder is no longer held for review here (for example after a restart, a change to the folder, or an hour away). Attach it again and ask for a new arrangement. Nothing was created.")
	case errors.Is(err, personalassistant.ErrFolderScanBusy):
		writeConversationError(w, http.StatusConflict, "arrangement_busy", "Another folder request is still running in this conversation. Wait for it to finish.")
	default:
		writeConversationError(w, http.StatusConflict, "arrangement_review_failed", "This group could not be reviewed right now. Nothing was created.")
	}
}

// arrangementExecutionContext resolves the authenticated owner, the
// conversation target and the arrangement path values shared by both POSTs.
func (h *HomeAssistantAskHandler) arrangementExecutionContext(w http.ResponseWriter, r *http.Request) (assistantcontext.SaveOwner, foldercontext.Target, string, personalAssistantArrangementExecutionStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	scope, ok := h.readScope(w, r)
	if !ok {
		return assistantcontext.SaveOwner{}, foldercontext.Target{}, "", nil, false
	}
	store, canStore := h.Conversations.(personalAssistantArrangementExecutionStore)
	if !canStore || h.FolderSetups == nil || h.FolderObservations == nil || h.folderStore() == nil {
		writeConversationError(w, http.StatusServiceUnavailable, "arrangement_review_unavailable", "Reviewing saved arrangements is unavailable right now. Nothing was created.")
		return assistantcontext.SaveOwner{}, foldercontext.Target{}, "", nil, false
	}
	owner, err := h.canonicalConversationOwner(r.Context())
	if err != nil || owner.WorkspaceID != scope.workspaceID || !strings.EqualFold(owner.AgentName, scope.agentName) {
		writeConversationError(w, http.StatusConflict, PersonalAssistantConversationOutOfScope, "The conversation owner changed. Nothing was created.")
		return assistantcontext.SaveOwner{}, foldercontext.Target{}, "", nil, false
	}
	conversationID, recordID := strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("arrangement_id"))
	if r.URL.RawQuery != "" || conversationID == "" || len(conversationID) > 96 || recordID == "" || len(recordID) > 160 {
		writeConversationError(w, http.StatusBadRequest, "invalid_arrangement_reference", "Use a saved arrangement from this conversation.")
		return assistantcontext.SaveOwner{}, foldercontext.Target{}, "", nil, false
	}
	target, err := h.folderTarget(scope, conversationID, "")
	if err != nil {
		writeConversationError(w, http.StatusBadRequest, "invalid_arrangement_reference", "Use a saved arrangement from this conversation.")
		return assistantcontext.SaveOwner{}, foldercontext.Target{}, "", nil, false
	}
	return owner, target, recordID, store, true
}

// ArrangementGroupReviewHandler opens the existing setup review for one
// arranged group: POST .../conversations/{id}/arrangements/{arrangement_id}/group-review.
// It calls no model and confirms nothing; Set up on the review is the only
// confirmation, and it is checked against this exact binding again.
func (h *HomeAssistantAskHandler) ArrangementGroupReviewHandler(w http.ResponseWriter, r *http.Request) {
	owner, target, recordID, store, ok := h.arrangementExecutionContext(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision               string `json:"revision"`
		ExpectedRecordRevision int64  `json:"expected_record_revision"`
		Operation              *int   `json:"operation"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	if req.Operation == nil || *req.Operation < 0 || *req.Operation >= workspacearrangement.MaxOperations || req.ExpectedRecordRevision < 1 || req.Revision == "" || len(req.Revision) > 96 {
		writeConversationError(w, http.StatusBadRequest, "invalid_arrangement_review", "Choose a group from a saved arrangement in this conversation.")
		return
	}
	setups, canReview := h.FolderSetups.(personalAssistantArrangementSetups)
	source, canRead := h.FolderObservations.(arrangementObservationSource)
	if !canReview || !canRead {
		writeConversationError(w, http.StatusServiceUnavailable, "arrangement_review_unavailable", "Reviewing saved arrangements is unavailable right now. Nothing was created.")
		return
	}
	release, entered := h.folderRequests.enter(target)
	if !entered {
		writeArrangementReviewError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	ctx := r.Context()
	saved, err := store.ReadArrangementExecution(ctx, target.ConversationID, owner, recordID)
	if err != nil || saved == nil {
		writeConversationError(w, http.StatusNotFound, "arrangement_unavailable", "That saved arrangement could not be read in this conversation.")
		return
	}
	h.reconcileArrangementEffects(ctx, target, owner, saved)
	record, operation := saved.Record, *req.Operation
	if saved.Imported || saved.StateVersion != owner.StateVersion || record.SourceKind != workspacearrangement.Attachment ||
		record.Revision != req.ExpectedRecordRevision || (record.State != "draft" && record.State != "needs_more_evidence") {
		writeConversationError(w, http.StatusConflict, "arrangement_stale", "This saved arrangement changed or is history only. Reopen it, or ask for a new arrangement. Nothing was created.")
		return
	}
	if operation >= len(record.Draft.Operations) || record.Draft.Operations[operation].Kind != workspacearrangement.CreateGroup {
		writeConversationError(w, http.StatusBadRequest, "invalid_arrangement_review", "That intention is not a group this arrangement proposes.")
		return
	}
	effect := findArrangementEffect(saved, operation)
	if effect != nil && effect.State != arrangementEffectAwaiting && effect.State != arrangementEffectClosed {
		writeConversationError(w, http.StatusConflict, "arrangement_group_confirmed", "This group was already confirmed. Its setup card in this conversation shows its progress and receipt; nothing new was prepared.")
		return
	}
	state, err := h.folderExpected(ctx, target, req.Revision)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	if state.Observation == nil || state.Observation.ID != record.SourceID {
		writeConversationError(w, http.StatusConflict, "arrangement_source_changed", "The folder this arrangement was made from is no longer the one attached here. Attach it again and ask for a new arrangement. Nothing was created.")
		return
	}
	// Only an open review in this conversation is protected here. A finished,
	// closed or pruned one is history, so a second group can follow a first.
	// The review below still refuses while any other review is open.
	if state.OfferID != "" && (effect == nil || effect.OfferID != state.OfferID) {
		status, err := setups.ConversationOfferStatus(ctx, target, state.OfferID)
		if err != nil && !errors.Is(err, personalassistant.ErrFolderOfferNotFound) {
			writeArrangementReviewError(w, err)
			return
		}
		if status == personalassistant.FolderOfferPending {
			writeArrangementReviewError(w, personalassistant.ErrFolderReviewPending)
			return
		}
	}
	if err := h.checkArrangementRuntime(record); err != nil {
		writeConversationError(w, http.StatusConflict, "arrangement_runtime_changed", "The assistant's model changed since this arrangement was proposed. Ask for a new arrangement to review it with the current model. Nothing was created.")
		return
	}
	// The record's exact items must still be the live held evidence. This
	// reads the held scan only; the review below rechecks every directory.
	evidence, err := workspacearrangement.NewEvidence(ctx, func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return source.ArrangementEvidence(ctx, target, record.SourceID)
	})
	if err != nil || evidence.ValidateRecord(ctx, record) != nil {
		writeArrangementReviewError(w, workspacearrangement.ErrStale)
		return
	}
	op := record.Draft.Operations[operation]
	view, created, err := setups.ReviewArrangementGroup(ctx, target, record.SourceID, personalassistant.FolderArrangementGroup{
		Binding: personalassistant.FolderStructureArrangement{RecordID: record.ID, RecordRevision: record.Revision, Operation: operation},
		Name:    op.Name, CandidateIDs: append([]string(nil), op.Members.IDs...),
	})
	if err != nil {
		writeArrangementReviewError(w, err)
		return
	}
	if view.Status != personalassistant.FolderOfferPending {
		// A confirmed run of this intention already exists; it is recovered.
		writeConversationError(w, http.StatusConflict, "arrangement_group_confirmed", "This group was already confirmed. Its setup card in this conversation shows its progress and receipt; nothing new was prepared.")
		return
	}
	rows, err := store.BindArrangementReview(ctx, target.ConversationID, owner, PersonalAssistantArrangementReview{
		RecordID: record.ID, RecordRevision: record.Revision, Operation: operation, FolderRevision: req.Revision,
		Event: foldercontext.Event{Version: 1, Observation: state.Observation, OfferID: view.ID},
	})
	if err != nil || len(rows) != 1 {
		if created {
			// No control is live until the canonical binding exists.
			_ = h.FolderSetups.CloseReview(context.WithoutCancel(ctx), target, view.ID)
		}
		if errors.Is(err, ErrPersonalAssistantFolderConflict) {
			writeFolderError(w, err)
			return
		}
		writeConversationError(w, http.StatusConflict, "arrangement_stale", "This saved arrangement or conversation changed while the review was prepared. Reopen it and try again. Nothing was created.")
		return
	}
	orihttp.WriteJSON(w, map[string]any{
		"conversation":       HomeAssistantConversationState{ID: target.ConversationID, Stored: true},
		"folder_context":     PersonalAssistantFolderState{Revision: rows[0].ID, Observation: state.Observation, OfferID: view.ID},
		"arrangement_review": map[string]any{"arrangement_id": record.ID, "operation": operation, "offer_id": view.ID},
	})
}

// ArrangementDeclineHandler closes a current unapplied PA proposal:
// POST .../conversations/{id}/arrangements/{arrangement_id}/decline. Intent stays
// as cancelled history; an open bound review is closed; nothing is undone.
func (h *HomeAssistantAskHandler) ArrangementDeclineHandler(w http.ResponseWriter, r *http.Request) {
	owner, target, recordID, store, ok := h.arrangementExecutionContext(w, r)
	if !ok {
		return
	}
	var req struct {
		ExpectedRecordRevision int64 `json:"expected_record_revision"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	if req.ExpectedRecordRevision < 1 {
		writeConversationError(w, http.StatusBadRequest, "invalid_arrangement_decline", "Choose a saved arrangement from this conversation.")
		return
	}
	release, entered := h.folderRequests.enter(target)
	if !entered {
		writeArrangementReviewError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	ctx := r.Context()
	saved, err := store.ReadArrangementExecution(ctx, target.ConversationID, owner, recordID)
	if err != nil || saved == nil {
		writeConversationError(w, http.StatusNotFound, "arrangement_unavailable", "That saved arrangement could not be read in this conversation.")
		return
	}
	h.reconcileArrangementEffects(ctx, target, owner, saved)
	setups, _ := h.FolderSetups.(personalAssistantArrangementSetups)
	open, refusal := arrangementDeclineReviews(ctx, setups, target, saved)
	switch refusal {
	case "":
	case "arrangement_effects_applied":
		writeConversationError(w, http.StatusConflict, refusal, "A group from this arrangement was already confirmed. Declining cannot undo it; nothing was changed.")
		return
	default:
		writeConversationError(w, http.StatusConflict, refusal, "Ori could not read back a group review from this arrangement, so it cannot tell whether that group was set up. Nothing was changed.")
		return
	}
	record, err := store.DeclineArrangement(ctx, target.ConversationID, owner, recordID, req.ExpectedRecordRevision)
	if err != nil {
		writeConversationError(w, http.StatusConflict, "arrangement_stale", "This saved arrangement changed or is already history. Reopen it before declining. Nothing was changed.")
		return
	}
	// Canonical retirement came first: even if closing a review sidecar fails,
	// its guard now refuses the cancelled record.
	for _, offerID := range open {
		_ = h.FolderSetups.CloseReview(context.WithoutCancel(ctx), target, offerID)
	}
	summary, err := record.Summarize()
	if err != nil {
		writeConversationError(w, http.StatusConflict, "arrangement_unavailable", "The arrangement was declined, but its history could not be shown. Reopen this conversation.")
		return
	}
	orihttp.WriteJSON(w, PersonalAssistantArrangementHistory{Arrangement: summary, ConversationID: target.ConversationID, MessageID: saved.MessageID})
}

// arrangementDeclineReviews lists the open group reviews a decline closes, or
// names why it must refuse. Set up records nothing in the arrangement journal,
// so an awaiting review is read back from its own offer: one that cannot be
// read, or that shows a confirmation reconciliation could not record, is
// never declined.
func arrangementDeclineReviews(ctx context.Context, setups personalAssistantArrangementSetups, target foldercontext.Target, saved *PersonalAssistantSavedArrangement) ([]string, string) {
	for _, effect := range saved.Effects {
		if effect.State == arrangementEffectApplying || effect.State == arrangementEffectPartial || effect.State == arrangementEffectCompleted {
			return nil, "arrangement_effects_applied"
		}
	}
	open := []string{}
	for _, effect := range saved.Effects {
		if effect.State != arrangementEffectAwaiting {
			continue
		}
		if setups == nil {
			return nil, "arrangement_effects_unverified"
		}
		outcome, err := setups.ArrangementOutcome(ctx, target, effect.OfferID)
		if err != nil || outcome.Binding.RecordID != saved.Record.ID || outcome.Binding.Operation != effect.Operation {
			return nil, "arrangement_effects_unverified"
		}
		switch outcome.Status {
		case personalassistant.FolderOfferPending:
			open = append(open, effect.OfferID)
		case personalassistant.FolderOfferClosed, personalassistant.FolderOfferDeclined:
		default:
			return nil, "arrangement_effects_applied"
		}
	}
	return open, ""
}

// validateArrangementOffer is the arranged part of the folder review guard.
// Before Set up the record must be open at the bound revision, the review must
// be the operation's current binding, and the authoring runtime unchanged. A
// confirmed run may continue only while that binding still names it.
func (h *HomeAssistantAskHandler) validateArrangementOffer(ctx context.Context, offer personalassistant.FolderOffer) error {
	store, ok := h.Conversations.(personalAssistantArrangementExecutionStore)
	review := offer.ConversationReview
	if !ok || review == nil || !offer.Structure.Arranged() {
		return foldercontext.ErrInvalid
	}
	binding := offer.Structure.Arrangement
	owner, err := h.canonicalConversationOwner(ctx)
	if err != nil {
		return foldercontext.ErrInvalid
	}
	saved, err := store.ReadArrangementExecution(ctx, review.Target.ConversationID, owner, binding.RecordID)
	if err != nil || saved == nil || saved.Imported || saved.StateVersion != owner.StateVersion || saved.Record.Revision != binding.RecordRevision {
		return foldercontext.ErrInvalid
	}
	effect := findArrangementEffect(saved, binding.Operation)
	if effect == nil || effect.OfferID != offer.ID || effect.RecordRevision != binding.RecordRevision || effect.State == arrangementEffectClosed {
		return foldercontext.ErrInvalid
	}
	if offer.DecidedAt != nil {
		// A confirmed run may continue after its folder is attached again (a
		// restart drops a held folder). The generic guard holds it to its own
		// local canonical event; the run rechecks every member's identity.
		return nil
	}
	if saved.Record.SourceID != review.ObservationID || (saved.Record.State != "draft" && saved.Record.State != "needs_more_evidence") ||
		effect.State != arrangementEffectAwaiting {
		return foldercontext.ErrInvalid
	}
	return h.checkArrangementRuntime(saved.Record)
}
