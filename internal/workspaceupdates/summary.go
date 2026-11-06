package workspaceupdates

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ResolveOwner is shared by read/seen HTTP and the legacy delivery fallback.
// Only the host's current-user provider can select an owner; absence retains
// Ori's local-user mode. A broken provider never falls back to local authority.
func ResolveOwner(ctx context.Context, current func(context.Context) (string, error)) (string, error) {
	if current == nil {
		return userprofile.LocalUserID, nil
	}
	owner, err := current(ctx)
	if err != nil || strings.TrimSpace(owner) == "" {
		return "", ErrUnavailable
	}
	return strings.TrimSpace(owner), nil
}

type WelcomeSource struct {
	*session.WelcomeDelivery
	State           string `json:"state"`
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	AgentName       string `json:"agent_name,omitempty"`
}

type Summary struct {
	WorkspaceID string                   `json:"workspace_id"`
	FolderSlug  string                   `json:"folder_slug"`
	Name        string                   `json:"name"`
	ObservedAt  time.Time                `json:"observed_at"`
	Welcome     WelcomeSource            `json:"welcome"`
	Review      filejanitor.FilingReview `json:"review"`
}

func safeLabel(value string, limit int) string {
	runes := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func (s *Service) ownedWorkspace(owner, id string) (*workspace.Workspace, error) {
	// Projection authority is the primary workspace identity. A missing setup
	// snapshot or conversation must not hide independently valid filing work.
	ws, err := s.workspaces.Get(id)
	if err != nil || ws == nil {
		return nil, ErrNotFound
	}
	if owner == "" || workspaceOwner(ws) != owner || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return nil, ErrNotFound
	}
	return ws, nil
}

// Summary reads canonical saved truth without enrollment, delivery or seen
// writes. A source failure is not an authoritative zero. The caller may retain
// qualified last-known information, but never turn that into action authority.
func (s *Service) Summary(ctx context.Context, owner, id string) (Summary, error) {
	return s.summary(ctx, owner, id, false)
}

func (s *Service) summary(ctx context.Context, owner, id string, retried bool) (Summary, error) {
	ws, err := s.ownedWorkspace(owner, id)
	if err != nil {
		return Summary{}, err
	}
	version := ws.Version
	out := Summary{WorkspaceID: ws.ID, FolderSlug: ws.FolderSlug, Name: safeLabel(ws.Name, 120), ObservedAt: time.Now().UTC(),
		Welcome: WelcomeSource{State: "not_applicable"}, Review: filejanitor.FilingReview{State: "not_applicable"}}
	if scope, eligible := Scope(ws, owner); eligible {
		out.Welcome.AgentInstanceID, out.Welcome.AgentName = scope.AgentInstanceID, safeLabel(scope.AgentName, 100)
		delivery, err := s.store.ReadWelcome(ctx, scope)
		if err != nil {
			out.Welcome.State = StateUnavailable
		} else {
			out.Welcome.State = StateReady
			out.Welcome.WelcomeDelivery = &delivery
		}
	}
	if s.reviews != nil {
		review, err := s.reviews.ReadFilingReview(ctx, owner, id)
		if err != nil {
			out.Review = filejanitor.FilingReview{State: StateUnavailable}
		} else {
			out.Review = review
		}
	} else if ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		out.Review.State = StateUnavailable
	}
	// A transfer/deletion while another source is read cannot lend the first
	// snapshot's labels or messages to the now-foreign workspace.
	latest, err := s.ownedWorkspace(owner, id)
	if err != nil {
		return Summary{}, err
	}
	if latest.Version != version {
		// Page activation and unrelated workspace metadata can advance the
		// version while sources are read. Retry once from current authority;
		// never relax the fence or loop indefinitely during active changes.
		if !retried && ctx.Err() == nil {
			return s.summary(ctx, owner, id, true)
		}
		return Summary{}, ErrUnavailable
	}
	return out, nil
}

// Acknowledge revalidates current owner and exact lead identity, then lets the
// database atomically fence version/session/message membership. It acknowledges
// only the named exposure; the returned summary can include a newer unseen one.
func (s *Service) Acknowledge(ctx context.Context, owner, id string, ack session.WelcomeAcknowledgement) (Summary, error) {
	ws, err := s.ownedWorkspace(owner, id)
	if err != nil {
		return Summary{}, err
	}
	scope, eligible := Scope(ws, owner)
	if !eligible {
		return Summary{}, ErrNotFound
	}
	if err := s.store.AcknowledgeWelcome(ctx, scope, ack); err != nil {
		if errors.Is(err, session.ErrWelcomeUnavailable) || errors.Is(err, session.ErrWorkspaceVersionConflict) {
			return Summary{}, session.ErrWelcomeUnavailable
		}
		return Summary{}, err
	}
	return s.Summary(ctx, owner, id)
}
