package workspaceupdates

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
)

type composerFunc func(context.Context, string, string) (session.LeadConversationInput, string, error)

func (f composerFunc) ComposeLeadWelcome(ctx context.Context, owner, id string) (session.LeadConversationInput, string, error) {
	return f(ctx, owner, id)
}

type deliveryStoreFunc func(context.Context, session.LeadConversationInput) (session.LeadConversationResult, error)

func (f deliveryStoreFunc) EnsureLeadConversation(ctx context.Context, in session.LeadConversationInput) (session.LeadConversationResult, error) {
	return f(ctx, in)
}

func TestDeliverUsesHostCompositionAndNeverRedirectsScope(t *testing.T) {
	for _, mode := range []string{"ready", "setup", "unavailable", "owner", "workspace", "compose-failure", "commit-failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			composed, committed := 0, 0
			composer := composerFunc(func(_ context.Context, owner, id string) (session.LeadConversationInput, string, error) {
				composed++
				if owner != "local" || id != "ws" {
					t.Fatal("caller scope lost")
				}
				in := session.LeadConversationInput{WorkspaceID: id, OwnerUserID: owner, AgentInstanceID: "lead", AgentName: "Lead", WorkspaceVersion: 12, Introduction: "Hello"}
				switch mode {
				case "owner":
					in.OwnerUserID = "victim"
				case "workspace":
					in.WorkspaceID = "victim"
				case "setup":
					return in, StateWaitingSetup, nil
				case "unavailable":
					return in, StateUnavailable, nil
				case "compose-failure":
					return in, "", ErrUnavailable
				}
				return in, StateReady, nil
			})
			store := deliveryStoreFunc(func(_ context.Context, in session.LeadConversationInput) (session.LeadConversationResult, error) {
				committed++
				if in.WorkspaceVersion != 12 || in.AgentInstanceID != "lead" {
					t.Fatal("version/identity fence lost")
				}
				if mode == "commit-failure" {
					return session.LeadConversationResult{}, session.ErrWorkspaceVersionConflict
				}
				return session.LeadConversationResult{SessionID: "saved", Changed: true}, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			out, err := Deliver(ctx, composer, store, "local", "ws")
			switch mode {
			case "ready":
				if err != nil || !out.Changed || out.SessionID != "saved" || composed != 1 || committed != 1 {
					t.Fatalf("delivery %+v %v", out, err)
				}
			case "commit-failure":
				if !errors.Is(err, session.ErrWorkspaceVersionConflict) || out.Changed || committed != 1 {
					t.Fatalf("failed commit reported as delivery: %+v %v", out, err)
				}
			case "setup", "unavailable":
				if err != nil || out.State == "" || committed != 0 {
					t.Fatalf("ineligible delivery: %+v %v", out, err)
				}
			default:
				if err == nil || committed != 0 || out.Changed {
					t.Fatalf("invalid scope/context committed: %+v %v", out, err)
				}
			}
			if mode == "cancelled" && composed != 0 {
				t.Fatal("composed after cancellation")
			}
		})
	}
}
