package mapactivityhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mapactivity"
)

// CraftLookup reads what a hand-run task run was paid. The economy service
// implements it; nil means the economy is not wired and the card shows no
// Craft line (FR39, FR44).
type CraftLookup interface {
	RunCraft(ctx context.Context, taskID, runKey string) (int64, bool, error)
}

// SetCraftLookup wires the Craft lookup for result cards.
func (h *Handler) SetCraftLookup(lookup CraftLookup) {
	if h != nil {
		h.craft = lookup
	}
}

// CardParcel is the parcel as the result card needs it.
type CardParcel struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspace_id"`
	Kind        mapactivity.Kind    `json:"kind"`
	RefID       string              `json:"ref_id"`
	Title       string              `json:"title"`
	AgentName   string              `json:"agent_name"`
	Outcome     mapactivity.Outcome `json:"outcome"`
	StartedAt   *time.Time          `json:"started_at,omitempty"`
	ProducedAt  time.Time           `json:"produced_at"`
	OpenedAt    *time.Time          `json:"opened_at,omitempty"`
}

// CardRewards is what the run actually paid. A reward that was zero or is
// unknown is left out, and so is the whole block when nothing remains (FR44).
type CardRewards struct {
	XP    *mapactivity.XPReport `json:"xp,omitempty"`
	Craft *int64                `json:"craft,omitempty"`
}

// CardPayload is the body of both open endpoints' single-parcel answer: the
// result card (FR44). It is served only here, never on the stream (FR62).
type CardPayload struct {
	Parcel          CardParcel   `json:"parcel"`
	DurationSeconds *int64       `json:"duration_seconds,omitempty"`
	Summary         string       `json:"summary,omitempty"`
	FailureReason   string       `json:"failure_reason,omitempty"`
	Rewards         *CardRewards `json:"rewards,omitempty"`
}

// OpenByRefRequest names something the user saw somewhere other than the map.
type OpenByRefRequest struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id"`
	RefID       string `json:"ref_id"`
}

// OpenParcel marks one parcel opened and returns its result card. Opening an
// already-opened parcel returns the same card (FR37).
// POST /api/workspace-map/parcels/{id}/open
func (h *Handler) OpenParcel(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	parcel, err := h.tracker.OpenParcel(r.Context(), r.PathValue("id"))
	if errors.Is(err, mapactivity.ErrParcelNotFound) {
		_ = orihttp.RespondNotFound(w, "parcel not found")
		return
	}
	if errors.Is(err, mapactivity.ErrParcelStoreUnavailable) {
		_ = orihttp.RespondNotFound(w, "result parcels are not enabled")
		return
	}
	if err != nil {
		logger.Error("Failed to open a result parcel", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "failed to open the parcel")
		return
	}
	_ = orihttp.RespondSuccess(w, h.cardFor(r.Context(), parcel))
}

// OpenParcelsByRef opens every waiting parcel for one task, brief, or scan the
// user has just seen elsewhere (FR40). Nothing to open is a success.
// POST /api/workspace-map/parcels/open-by-ref
func (h *Handler) OpenParcelsByRef(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	var req OpenByRefRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	kind := mapactivity.Kind(strings.TrimSpace(req.Kind))
	switch kind {
	case mapactivity.KindTask, mapactivity.KindDailyBrief, mapactivity.KindFileJanitor:
	default:
		_ = orihttp.RespondBadRequest(w, "kind must be task, daily_brief, or file_janitor")
		return
	}
	if strings.TrimSpace(req.WorkspaceID) == "" {
		_ = orihttp.RespondBadRequest(w, "workspace_id is required")
		return
	}
	// The Daily Brief and the File Janitor console show everything that is
	// waiting, so they may leave ref_id out; a task result is always one task's.
	if kind == mapactivity.KindTask && strings.TrimSpace(req.RefID) == "" {
		_ = orihttp.RespondBadRequest(w, "ref_id is required for a task")
		return
	}
	opened, err := h.tracker.OpenParcelsByRef(r.Context(), kind, req.WorkspaceID, req.RefID)
	if errors.Is(err, mapactivity.ErrParcelStoreUnavailable) {
		_ = orihttp.RespondNotFound(w, "result parcels are not enabled")
		return
	}
	if err != nil {
		logger.Error("Failed to open result parcels by reference", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "failed to open parcels")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]int{"opened": len(opened)})
}

func (h *Handler) cardFor(ctx context.Context, parcel mapactivity.Parcel) CardPayload {
	card := CardPayload{
		Parcel: CardParcel{
			ID:          parcel.ID,
			WorkspaceID: parcel.WorkspaceID,
			Kind:        parcel.Kind,
			RefID:       parcel.RefID,
			Title:       parcel.Title,
			AgentName:   parcel.AgentName,
			Outcome:     parcel.Outcome,
			StartedAt:   parcel.StartedAt,
			ProducedAt:  parcel.ProducedAt,
			OpenedAt:    parcel.OpenedAt,
		},
	}
	if parcel.StartedAt != nil && !parcel.StartedAt.IsZero() && parcel.ProducedAt.After(*parcel.StartedAt) {
		seconds := int64(parcel.ProducedAt.Sub(*parcel.StartedAt).Round(time.Second) / time.Second)
		card.DurationSeconds = &seconds
	}
	switch parcel.Outcome {
	case mapactivity.OutcomeFailed, mapactivity.OutcomeTimeout:
		card.FailureReason = parcel.FailureReason
	default:
		card.Summary = parcel.Summary
	}

	rewards := CardRewards{}
	if parcel.XP.Awarded > 0 {
		xp := parcel.XP
		rewards.XP = &xp
	}
	if parcel.Kind == mapactivity.KindTask && h.craft != nil {
		amount, found, err := h.craft.RunCraft(ctx, parcel.RefID, parcel.RunKey)
		if err == nil && found && amount > 0 {
			rewards.Craft = &amount
		}
	}
	if rewards.XP != nil || rewards.Craft != nil {
		card.Rewards = &rewards
	}
	return card
}
