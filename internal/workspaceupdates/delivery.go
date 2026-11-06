// Package workspaceupdates owns host-authored welcome bookkeeping and safe
// update projections. It has no model, setup, scan or work-execution interface.
package workspaceupdates

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/session"
)

var (
	ErrNotFound    = errors.New("workspace update not found")
	ErrUnavailable = errors.New("workspace updates unavailable")
)

const (
	StateReady        = "ready"
	StateWaitingSetup = "waiting_setup"
	StateUnavailable  = "unavailable"
)

type Composer interface {
	ComposeLeadWelcome(context.Context, string, string) (session.LeadConversationInput, string, error)
}

type DeliveryStore interface {
	EnsureLeadConversation(context.Context, session.LeadConversationInput) (session.LeadConversationResult, error)
}

type Delivery struct {
	session.LeadConversationResult
	State string `json:"state,omitempty"`
}

// Deliver is the single host boundary shared by the explicit legacy POST and
// enrolled creation/ready/recovery. Neither composition nor commit needs an HTTP
// request or an open browser. A failed publication cannot undo committed truth.
func Deliver(ctx context.Context, composer Composer, store DeliveryStore, owner, workspaceID string) (Delivery, error) {
	if composer == nil || store == nil || owner == "" || workspaceID == "" {
		return Delivery{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Delivery{}, err
	}
	input, state, err := composer.ComposeLeadWelcome(ctx, owner, workspaceID)
	if err != nil {
		return Delivery{}, err
	}
	if state != StateReady {
		return Delivery{State: state}, nil
	}
	// Composition cannot redirect the caller's authenticated scope. The store
	// checks its version fence again in the transaction that writes the message.
	if input.WorkspaceID != workspaceID || input.OwnerUserID != owner {
		return Delivery{}, ErrNotFound
	}
	out, err := store.EnsureLeadConversation(ctx, input)
	if err != nil {
		return Delivery{}, err
	}
	return Delivery{LeadConversationResult: out}, nil
}
