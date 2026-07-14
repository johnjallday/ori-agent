// Package personalhq implements the Personal HQ domain service: the
// user-scoped, zero-or-one designation of an ordinary workspace as the
// user's personal command center, plus the durable onboarding status that
// tracks whether the user has seen, started, finished, or skipped the guided
// HQ setup experience.
//
// Personal HQ is deliberately not a new workspace kind. The designation is a
// single field on the user's profile row (see internal/userprofile), and
// this service is the only place that mutates or validates it.
package personalhq

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

var (
	// ErrWorkspaceIDRequired is returned when Designate or Replace is called
	// with an empty workspace ID. Use Clear to remove a designation.
	ErrWorkspaceIDRequired = errors.New("personal hq: workspace id is required")
	// ErrWorkspaceNotFound is returned when the target workspace does not exist.
	ErrWorkspaceNotFound = errors.New("personal hq: workspace not found")
	// ErrGroupNotEligible is returned when the target workspace is a group.
	// Groups can contain member workspaces and are not eligible to be an HQ.
	ErrGroupNotEligible = errors.New("personal hq: group workspaces cannot be a personal hq")
	// ErrWorkspaceUnavailable is returned when the target workspace is
	// trashed or missing.
	ErrWorkspaceUnavailable = errors.New("personal hq: workspace is trashed or missing")
	// ErrUnauthorized is returned when the target workspace belongs to a
	// different user.
	ErrUnauthorized = errors.New("personal hq: workspace is not accessible to this user")
	// ErrAlreadyDesignated is returned by Designate (not Replace) when the
	// user already has a valid HQ. Callers should use Replace with explicit
	// confirmation instead.
	ErrAlreadyDesignated = errors.New("personal hq: user already has a designated personal hq; use replace")
	// ErrInvalidOnboardingState is returned when SetOnboardingState receives
	// an unrecognized state value.
	ErrInvalidOnboardingState = errors.New("personal hq: invalid onboarding state")
)

// Invalid-designation reasons surfaced on Status.InvalidReason. These are
// stable strings consumed by the HTTP layer and, eventually, repair UI.
const (
	InvalidReasonMissing    = "missing"
	InvalidReasonTrashed    = "trashed"
	InvalidReasonGroup      = "group"
	InvalidReasonWrongOwner = "wrong_owner"
)

// WorkspaceReader is the narrow workspace-lookup contract this service
// needs. session.HybridStore and session.SQLiteStore both satisfy it.
type WorkspaceReader interface {
	GetWorkspace(ctx context.Context, id string) (*session.Workspace, error)
}

// ProfileStore is the focused Personal HQ persistence contract. It is
// intentionally narrower than userprofile.UserStore so existing fakes/tests
// built against UserStore are not forced to grow HQ-specific methods.
// userprofile.SQLiteStore satisfies this interface.
type ProfileStore interface {
	GetPersonalHQState(ctx context.Context, userID string) (*userprofile.PersonalHQState, error)
	SetPersonalWorkspaceID(ctx context.Context, userID, workspaceID string) error
	SetHQOnboardingState(ctx context.Context, userID string, state userprofile.HQOnboardingState) error
}

// Status is the resolved, read-time view of a user's Personal HQ.
type Status struct {
	UserID string `json:"user_id"`

	// WorkspaceID is the raw stored designation, which may be stale or
	// invalid. It is empty when the user has no HQ designated.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// Workspace is populated only when the designation resolves to an
	// eligible, accessible workspace (Valid is true).
	Workspace *session.Workspace `json:"workspace,omitempty"`

	// Valid is true only when WorkspaceID is set and resolves to an
	// eligible workspace. A user with no designation at all is not "valid"
	// but is also not "invalid" — see HasDesignation/NeedsRepair.
	Valid bool `json:"valid"`

	// InvalidReason explains why a non-empty WorkspaceID failed to resolve:
	// one of InvalidReasonMissing, InvalidReasonTrashed, InvalidReasonGroup,
	// or InvalidReasonWrongOwner. Empty when Valid is true or there is no
	// designation at all.
	InvalidReason string `json:"invalid_reason,omitempty"`

	OnboardingState userprofile.HQOnboardingState `json:"hq_onboarding_state"`
}

// HasDesignation reports whether the user has ever pointed at a workspace,
// regardless of whether it currently resolves.
func (s *Status) HasDesignation() bool {
	return s != nil && strings.TrimSpace(s.WorkspaceID) != ""
}

// NeedsRepair reports whether the stored designation is stale (points at a
// workspace that no longer resolves) and should surface repair actions
// (Clear, Choose Existing, Build New) rather than a normal no-HQ state.
func (s *Status) NeedsRepair() bool {
	return s.HasDesignation() && !s.Valid
}

// Service is the Personal HQ domain service.
type Service struct {
	profiles   ProfileStore
	workspaces WorkspaceReader
}

// NewService constructs a Personal HQ service. Both dependencies are
// required; callers should nil-check before wiring if either store failed
// to initialize (mirrors other optional-dependency handlers in this repo).
func NewService(profiles ProfileStore, workspaces WorkspaceReader) *Service {
	return &Service{profiles: profiles, workspaces: workspaces}
}

// Status resolves the current Personal HQ designation and onboarding state
// for a user. It never errors merely because the stored designation is
// stale (trashed/missing/deleted/group/wrong-owner) — that is reported via
// InvalidReason so read paths like Home/Map rendering stay resilient.
func (s *Service) Status(ctx context.Context, userID string) (*Status, error) {
	if s == nil || s.profiles == nil {
		return nil, errors.New("personal hq service is not configured")
	}
	userID = normalizeUserID(userID)
	state, err := s.profiles.GetPersonalHQState(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &Status{
		UserID:          userID,
		WorkspaceID:     state.PersonalWorkspaceID,
		OnboardingState: userprofile.NormalizeHQOnboardingState(string(state.OnboardingState)),
	}
	if !out.HasDesignation() {
		return out, nil
	}
	if s.workspaces == nil {
		// Degrade rather than fail: the caller can still show onboarding
		// state and a generic "HQ unavailable" message.
		out.InvalidReason = InvalidReasonMissing
		return out, nil
	}
	ws, wsErr := s.workspaces.GetWorkspace(ctx, out.WorkspaceID)
	switch {
	case errors.Is(wsErr, session.ErrWorkspaceNotFound):
		out.InvalidReason = InvalidReasonMissing
	case wsErr != nil:
		return nil, wsErr
	default:
		if reason := ineligibleReason(ws, userID); reason != "" {
			out.InvalidReason = reason
		} else {
			out.Valid = true
			out.Workspace = ws
		}
	}
	return out, nil
}

// Designate sets the Personal HQ for a user who does not currently have a
// valid one. Callers with an existing valid HQ must use Replace, which
// requires explicit confirmation naming both workspaces (FR37/FR38).
func (s *Service) Designate(ctx context.Context, userID, workspaceID string) (*Status, error) {
	userID = normalizeUserID(userID)
	current, err := s.Status(ctx, userID)
	if err != nil {
		return nil, err
	}
	if current.Valid {
		return nil, ErrAlreadyDesignated
	}
	return s.setDesignation(ctx, userID, workspaceID)
}

// Replace atomically switches the Personal HQ designation to a different
// workspace, regardless of whether one is currently designated. The switch
// is a single-column update, so no intermediate zero-HQ state is observable.
func (s *Service) Replace(ctx context.Context, userID, workspaceID string) (*Status, error) {
	return s.setDesignation(ctx, normalizeUserID(userID), workspaceID)
}

// Clear removes the Personal HQ designation without touching the workspace
// itself or the user's onboarding history.
func (s *Service) Clear(ctx context.Context, userID string) (*Status, error) {
	if s == nil || s.profiles == nil {
		return nil, errors.New("personal hq service is not configured")
	}
	userID = normalizeUserID(userID)
	if err := s.profiles.SetPersonalWorkspaceID(ctx, userID, ""); err != nil {
		return nil, err
	}
	return s.Status(ctx, userID)
}

// SetOnboardingState records a durable Personal HQ onboarding status
// transition (unseen/in_progress/completed/skipped), independent of the
// current designation.
func (s *Service) SetOnboardingState(ctx context.Context, userID string, state userprofile.HQOnboardingState) (*Status, error) {
	if s == nil || s.profiles == nil {
		return nil, errors.New("personal hq service is not configured")
	}
	userID = normalizeUserID(userID)
	if _, ok := userprofile.ParseHQOnboardingState(string(state)); !ok {
		return nil, fmt.Errorf("%w: %q", ErrInvalidOnboardingState, state)
	}
	if err := s.profiles.SetHQOnboardingState(ctx, userID, state); err != nil {
		return nil, err
	}
	return s.Status(ctx, userID)
}

func (s *Service) setDesignation(ctx context.Context, userID, workspaceID string) (*Status, error) {
	if s == nil || s.profiles == nil {
		return nil, errors.New("personal hq service is not configured")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrWorkspaceIDRequired
	}
	if s.workspaces == nil {
		return nil, errors.New("personal hq service has no workspace reader configured")
	}
	ws, err := s.workspaces.GetWorkspace(ctx, workspaceID)
	if errors.Is(err, session.ErrWorkspaceNotFound) {
		return nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, err
	}
	switch ineligibleReason(ws, userID) {
	case InvalidReasonGroup:
		return nil, ErrGroupNotEligible
	case InvalidReasonTrashed, InvalidReasonMissing:
		return nil, ErrWorkspaceUnavailable
	case InvalidReasonWrongOwner:
		return nil, ErrUnauthorized
	}
	if err := s.profiles.SetPersonalWorkspaceID(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	return s.Status(ctx, userID)
}

// ineligibleReason returns the stable InvalidReason* string explaining why a
// resolved workspace cannot be (or remain) a user's Personal HQ, or "" when
// it is eligible.
func ineligibleReason(ws *session.Workspace, userID string) string {
	if ws.IsGroup() {
		return InvalidReasonGroup
	}
	switch ws.Status {
	case session.WorkspaceStatusTrashed:
		return InvalidReasonTrashed
	case session.WorkspaceStatusMissing:
		return InvalidReasonMissing
	}
	if ws.OwnerUserID != "" && ws.OwnerUserID != userID {
		return InvalidReasonWrongOwner
	}
	return ""
}

func normalizeUserID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return userprofile.LocalUserID
	}
	return id
}
