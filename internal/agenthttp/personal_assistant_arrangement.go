package agenthttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Arrangement mode is explicit expanded metadata sharing, never attachment or
// mutation consent. It cannot be combined with another approval/draft action.
type HomeAssistantArrangementRequest struct {
	ShareHeldMetadata bool   `json:"share_held_metadata"`
	ExpectedProvider  string `json:"expected_provider,omitempty"`
	ExpectedModel     string `json:"expected_model,omitempty"`
}

// UnmarshalJSON accepts explicit sharing and optional paired runtime pins;
// aliases, duplicate keys and unrelated approvals never stand in for consent.
func (r *HomeAssistantArrangementRequest) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return workspacearrangement.ErrInvalid
	}
	var out HomeAssistantArrangementRequest
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return workspacearrangement.ErrInvalid
		}
		seen[name] = true
		value, err := decoder.Token()
		if err != nil {
			return workspacearrangement.ErrInvalid
		}
		switch name {
		case "share_held_metadata":
			out.ShareHeldMetadata, ok = value.(bool)
		case "expected_provider", "expected_model":
			var text string
			text, ok = value.(string)
			ok = ok && workspacearrangement.ValidGoal(text) && len(text) <= 160
			if name == "expected_provider" {
				out.ExpectedProvider = text
			} else {
				out.ExpectedModel = text
			}
		default:
			ok = false
		}
		if !ok {
			return workspacearrangement.ErrInvalid
		}
	}
	if seen["expected_provider"] != seen["expected_model"] {
		return workspacearrangement.ErrInvalid
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return workspacearrangement.ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return workspacearrangement.ErrInvalid
	}
	*r = out
	return nil
}

type HomeAssistantArrangementSummary = workspacearrangement.Summary

type arrangementObservationSource interface {
	ArrangementEvidence(context.Context, foldercontext.Target, string) (workspacearrangement.Snapshot, error)
	HeldArrangementObservation(context.Context, foldercontext.Target, string) (*foldercontext.Observation, error)
}

// These bounded references are history, never review/apply authority.
type PersonalAssistantArrangementRef struct {
	ID              string `json:"id"`
	MessageID       string `json:"message_id"`
	Revision        int64  `json:"revision"`
	State           string `json:"state"`
	ContextCurrent  bool   `json:"context_current"`
	ReviewAvailable bool   `json:"review_available"`
}

type personalAssistantArrangementReader interface {
	ListArrangementRefs(context.Context, string, assistantcontext.SaveOwner) ([]PersonalAssistantArrangementRef, error)
}

type personalAssistantArrangementCleanup interface {
	DiscardUnansweredArrangementConversation(context.Context, string, assistantcontext.SaveOwner) error
}

type personalAssistantArrangementStore interface {
	SupportsArrangementStorage() bool
	AppendArrangementTurn(context.Context, string, assistantcontext.SaveOwner, *foldercontext.Event, string, string, string, *assistantcontext.Attribution, workspacearrangement.Record) ([]PersonalAssistantConversationMessage, error)
}

func (h *HomeAssistantAskHandler) answerArrangementTurn(ctx context.Context, req HomeAssistantAskRequest, prompt string, identity *HomeAssistantIdentity, work *PersonalAssistantWorkContext, conversation *openConversation) HomeAssistantAskResponse {
	refusal := func(err error, reason string) HomeAssistantAskResponse {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		failure := arrangementFailure(err, reason)
		state := unstoredConversation(conversation)
		if state != nil {
			state.Error = "arrangement_" + failure.Code
		}
		return HomeAssistantAskResponse{Response: failure.Message, Intent: homeAssistantConversationIntent.Key, Identity: identity, Conversation: state, ModelUnavailable: true, ArrangementError: failure}
	}
	if conversation == nil || conversation.turn == nil || work == nil || req.Arrangement == nil || req.ConfirmedAction != nil || req.ResearchApproval != nil || req.Draft != nil || req.Operation != nil {
		return refusal(nil, "invalid_request")
	}
	if !req.Arrangement.ShareHeldMetadata {
		return refusal(nil, "sharing_required")
	}
	if !workspacearrangement.ValidGoal(prompt) {
		return refusal(nil, "invalid_goal")
	}
	if req.FolderContext == nil {
		return refusal(nil, "source_required")
	}
	if req.FolderContext.Historical {
		return refusal(nil, "historical_source")
	}
	if len(req.FolderContext.FocusIDs) != 0 {
		return refusal(nil, "focused_source")
	}
	if h.ArrangementPlanner == nil {
		return refusal(nil, "storage_unavailable")
	}
	source, ok := h.FolderObservations.(arrangementObservationSource)
	store, canSave := h.Conversations.(personalAssistantArrangementStore)
	if !ok {
		return refusal(nil, "source_unavailable")
	}
	if !canSave || !store.SupportsArrangementStorage() {
		return refusal(nil, "storage_unavailable")
	}
	ref := *req.FolderContext
	target, err := h.folderTarget(conversation.scope, conversation.id, ref.DraftID)
	if err != nil {
		return refusal(err, "invalid_request")
	}
	release, entered := h.folderRequests.enter(target)
	if !entered {
		return refusal(nil, "busy")
	}
	defer release()
	provider, model, err := h.resolveProvider()
	if err != nil || strings.TrimSpace(model) == "" {
		return refusal(err, "provider_unavailable")
	}
	// API providers are pointer-backed. Pin this registration for the whole
	// turn; replacing a provider under the same configured name must abort it.
	value := reflect.ValueOf(provider)
	if value.Kind() != reflect.Pointer || value.IsNil() || !workspacearrangement.SupportsMetadataPlanning(provider) {
		return refusal(nil, "unsupported_transport")
	}
	providerPointer := value.Pointer()
	configuredProvider, configuredModel := h.SystemModel.GetSystemModel()
	if (req.Arrangement.ExpectedProvider != "" && req.Arrangement.ExpectedProvider != configuredProvider) ||
		(req.Arrangement.ExpectedModel != "" && req.Arrangement.ExpectedModel != configuredModel) {
		return refusal(workspacearrangement.ErrStale, "stale_context")
	}
	fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", configuredProvider, configuredModel, model, providerPointer)))
	owner := conversation.turn.saveOwner()
	expectedConversation := owner.ExpectedConversationRevision
	check := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fresh, err := h.resolvePersonalAssistantContext(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		scope, ready := conversationScope(fresh)
		if err != nil || !ready || scope != conversation.scope || fresh.StateVersion != work.StateVersion {
			return workspacearrangement.ErrStale
		}
		if err := h.revalidateWorkspaceTurn(ctx, conversation.turn); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		currentProvider, currentModel, err := h.resolveProvider()
		if err != nil || currentModel != model {
			return workspacearrangement.ErrStale
		}
		current := reflect.ValueOf(currentProvider)
		key, choice := h.SystemModel.GetSystemModel()
		if current.Kind() != reflect.Pointer || current.IsNil() || current.Pointer() != providerPointer || key != configuredProvider || choice != configuredModel {
			return workspacearrangement.ErrStale
		}
		state, err := h.folderExpected(ctx, target, ref.Revision)
		if err != nil {
			return err
		}
		if state.Observation == nil || state.Observation.ID != ref.SelectionID {
			if h.FolderObservations.WasSaved(target, ref.SelectionID) {
				return workspacearrangement.ErrStale
			}
			if target.ConversationID != "" {
				_, messages, err := h.readCanonicalFolderEvents(ctx, target.ConversationID, "", ref.SelectionID)
				if err != nil {
					return err
				}
				for _, message := range messages {
					if !message.Imported && message.FolderContext != nil && message.FolderContext.Observation != nil && message.FolderContext.Observation.ID == ref.SelectionID {
						return workspacearrangement.ErrStale
					}
				}
			}
		}
		if target.ConversationID != "" {
			record, _, err := h.readCanonicalFolderEvents(ctx, target.ConversationID, "", "")
			if err != nil || !conversation.scope.owns(record) || expectedConversation == "" || expectedConversation != assistantcontext.ConversationRevision(record.UpdatedAt, record.MessageCount, record.ContextEpoch) {
				return workspacearrangement.ErrStale
			}
		}
		return ctx.Err()
	}
	evidence, err := workspacearrangement.NewEvidence(ctx, func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		if err := check(ctx); err != nil {
			return workspacearrangement.Snapshot{}, err
		}
		return source.ArrangementEvidence(ctx, target, ref.SelectionID)
	})
	if err != nil {
		return refusal(err, "source_unavailable")
	}
	observation, err := source.HeldArrangementObservation(ctx, target, ref.SelectionID)
	if err != nil {
		return refusal(err, "source_unavailable")
	}
	initialState, err := h.folderExpected(ctx, target, ref.Revision)
	if err != nil {
		return refusal(err, "source_unavailable")
	}
	offerID := ""
	if initialState.Observation != nil && initialState.Observation.ID == ref.SelectionID {
		offerID = initialState.OfferID
	}
	contextKey := "conversation-" + target.ConversationID
	if target.ConversationID == "" {
		contextKey = "draft-" + target.DraftID
	}
	var savedState *HomeAssistantConversationState
	var folderState *PersonalAssistantFolderState
	var summary *HomeAssistantArrangementSummary
	failureStage := "planning_failed"
	result, err := h.ArrangementPlanner.Run(ctx, workspacearrangement.PlanInput{Owner: owner.UserID, ContextKey: contextKey, Provider: provider, Model: model, Goal: prompt, History: conversation.history, Evidence: evidence, Revalidate: check}, func(ctx context.Context, result workspacearrangement.PlanResult) error {
		failureStage = "save_failed"
		var record *workspacearrangement.Record
		if result.Draft != nil {
			prepared, err := evidence.NewRecord(ctx, *result.Draft, workspacearrangement.Provenance{Origin: "model", SourceID: ref.SelectionID, Provider: provider.Name(), Model: model, ProviderFingerprint: hex.EncodeToString(fingerprint[:])})
			if err != nil {
				return err
			}
			preview, err := prepared.Summarize()
			if err != nil {
				return err
			}
			summary = &preview
			record = &prepared
		}
		if err := check(ctx); err != nil {
			return err
		}
		created := false
		if conversation.id == "" {
			createdRecord, err := h.Conversations.Create(ctx, conversation.scope.workspaceID, conversation.scope.agentName, conversationTitle(prompt))
			if err != nil {
				return err
			}
			conversation.id, conversation.title, created = createdRecord.ID, createdRecord.Title, true
			owner.ExpectedConversationRevision = assistantcontext.ConversationRevision(createdRecord.UpdatedAt, createdRecord.MessageCount, createdRecord.ContextEpoch)
		}
		event := foldercontext.Event{Version: 1, Observation: observation, OfferID: offerID}
		var rows []PersonalAssistantConversationMessage
		saveErr := evidence.Revalidate(ctx)
		if saveErr == nil {
			if record != nil {
				rows, saveErr = store.AppendArrangementTurn(ctx, conversation.id, owner, &event, ref.Revision, prompt, result.Answer, conversation.turn.attribution(), *record)
			} else if writer, ok := h.Conversations.(personalAssistantAttributedStore); ok {
				rows, saveErr = writer.AppendAttributedTurn(ctx, conversation.id, owner, &event, ref.Revision, prompt, result.Answer, conversation.turn.attribution())
			} else {
				saveErr = workspacearrangement.ErrUnavailable
			}
		}
		if saveErr != nil {
			if cleanup, ok := h.Conversations.(personalAssistantArrangementCleanup); created && ok {
				// Cancellation must not disable cleanup, but cleanup must not erase
				// a committed answer or concurrent edit after an uncertain save.
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				if cleanup.DiscardUnansweredArrangementConversation(cleanupCtx, conversation.id, owner) == nil {
					conversation.id, conversation.title = "", ""
				}
				cancel()
			}
			return saveErr
		}
		savedState = &HomeAssistantConversationState{ID: conversation.id, Title: conversation.title, Started: created, Stored: true}
		for _, row := range rows {
			switch row.Role {
			case "user":
				savedState.UserMessageID = row.ID
			case "assistant":
				savedState.AssistantMessageID = row.ID
			case "system":
				folderState = &PersonalAssistantFolderState{Revision: row.ID, Observation: observation, OfferID: offerID}
			}
		}
		h.FolderObservations.BindSaved(target, ref.SelectionID, conversation.id)
		return nil
	})
	if err != nil {
		return refusal(err, failureStage)
	}
	return HomeAssistantAskResponse{Response: result.Answer, Intent: homeAssistantConversationIntent.Key, Identity: identity, Conversation: savedState, FolderContext: folderState, Arrangement: summary}
}
