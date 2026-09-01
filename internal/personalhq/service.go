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

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
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

// DesignationSyncer projects a designation transition onto the target
// workspace's folder-store record (workspace.json), the canonical store for
// the workspace-side Designation field (no SQLite column). designation is ""
// (clear) or "personal_hq". It is optional and wired post-construction via
// SetDesignationSyncer, because the folder store is created after this service
// during server startup (Phase 18 vs 17) — the same wiring-order constraint
// SetOnDesignated works around. Implemented by sessionhttp.Handler.
//
// The personalhq designation record remains the source of truth; a sync
// failure is best-effort and self-heals on the next startup backfill, so
// callers must not fail a designation transition when the projection fails.
type DesignationSyncer interface {
	SetWorkspaceDesignation(ctx context.Context, workspaceID, designation string) error
}

// DesignationReader reads the canonical folder-backed workspace record. The
// workspace-side Designation field has no SQLite column, so callers that gate
// behavior on that field must use this reader instead of a SQLite-primary
// workspace Get.
type DesignationReader interface {
	GetFolderWorkspace(id string) (*workspace.Workspace, error)
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

	// EntryAgentInstanceID and EntryAgentName project the canonical workspace
	// entry point without asking clients to infer identity from display order.
	EntryAgentInstanceID string `json:"entry_agent_instance_id,omitempty"`
	EntryAgentName       string `json:"entry_agent_name,omitempty"`

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

	// onDesignated fires (outside any lock) after a workspace successfully
	// becomes the user's designated HQ, whether via Designate or Replace —
	// which covers both "Build My HQ" (a brand new workspace) and
	// "designate an existing workspace" in one place. Set post-construction
	// via SetOnDesignated once the progression engine exists (mirrors
	// smartOnboardingHandler.SetOnPersonalized in internal/server), since
	// the engine is built after this service during server startup.
	onDesignated func(ctx context.Context, userID, workspaceID string)

	// designationSync projects designate/replace/clear transitions onto the
	// workspace-side Designation field (workspace.json). Optional; wired
	// post-construction via SetDesignationSyncer (see DesignationSyncer).
	designationSync DesignationSyncer

	// designationReader reads that canonical folder projection for callers that
	// need to decide whether a workspace is the Personal HQ at request time.
	// It is wired after the folder store exists during server startup.
	designationReader DesignationReader
}

// NewService constructs a Personal HQ service. Both dependencies are
// required; callers should nil-check before wiring if either store failed
// to initialize (mirrors other optional-dependency handlers in this repo).
func NewService(profiles ProfileStore, workspaces WorkspaceReader) *Service {
	return &Service{profiles: profiles, workspaces: workspaces}
}

// SetOnDesignated registers a callback fired after a workspace becomes the
// user's designated Personal HQ (Designate or Replace). Used to complete the
// optional t2-build-hq progression quest without this package importing
// internal/progression.
func (s *Service) SetOnDesignated(fn func(ctx context.Context, userID, workspaceID string)) {
	s.onDesignated = fn
}

// SetDesignationSyncer registers the sink that mirrors designation transitions
// onto the workspace-side Designation field. Wired post-construction because
// the folder store is built after this service during server startup.
func (s *Service) SetDesignationSyncer(syncer DesignationSyncer) {
	s.designationSync = syncer
}

// SetDesignationReader wires the canonical folder-store reader used for
// workspace-side Personal HQ checks. It is separate from DesignationSyncer so
// read-only callers never need write access to the projection.
func (s *Service) SetDesignationReader(reader DesignationReader) {
	if s == nil {
		return
	}
	s.designationReader = reader
}

// IsWorkspaceDesignatedPersonalHQ resolves a workspace-side Personal HQ
// designation through the canonical folder record. The SQLite-primary
// workspace store intentionally has no Designation column, so it must not be
// used for this decision. When no folder reader has been wired (for example,
// in a minimal test harness), the authoritative profile status is a safe
// fallback.
func (s *Service) IsWorkspaceDesignatedPersonalHQ(ctx context.Context, userID, workspaceID string) (bool, error) {
	if s == nil {
		return false, errors.New("personal hq service is not configured")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return false, nil
	}
	if s.designationReader != nil {
		ws, err := s.designationReader.GetFolderWorkspace(workspaceID)
		if err != nil {
			return false, err
		}
		return ws != nil && session.NormalizeWorkspaceDesignation(ws.Designation) == session.WorkspaceDesignationPersonalHQ, nil
	}

	status, err := s.Status(ctx, userID)
	if err != nil || status == nil {
		return false, err
	}
	return status.Valid && status.WorkspaceID == workspaceID, nil
}

// syncDesignation projects a designation value onto a workspace's folder-store
// record, best-effort. The personalhq record is the source of truth; a failed
// projection is logged and left for the startup backfill to heal, never
// surfaced as an error to the designation transition.
func (s *Service) syncDesignation(ctx context.Context, workspaceID, designation string) {
	if s == nil || s.designationSync == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	if err := s.designationSync.SetWorkspaceDesignation(ctx, workspaceID, designation); err != nil {
		logger.Warn("personal hq: failed to project designation onto workspace", logger.Fields{
			"workspace_id": workspaceID, "designation": designation, "error": err,
		})
	}
}

// DesignatedWorkspaceIDs returns the set of workspace IDs currently pointed at
// by a personal-hq designation record, keyed for O(1) membership. The startup
// backfill reconciles the workspace-side Designation projection against these
// authoritative records.
//
// Local-first: userprofile exposes no multi-user enumeration, so only the
// local user's record is consulted. The Designation field is global (a
// workspace either is or isn't an HQ), so on a single-user install this is the
// complete designated set.
func (s *Service) DesignatedWorkspaceIDs(ctx context.Context) (map[string]bool, error) {
	if s == nil || s.profiles == nil {
		return nil, errors.New("personal hq service is not configured")
	}
	state, err := s.profiles.GetPersonalHQState(ctx, normalizeUserID(""))
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, 1)
	if state != nil {
		if id := strings.TrimSpace(state.PersonalWorkspaceID); id != "" {
			out[id] = true
		}
	}
	return out, nil
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
			for _, instance := range ws.AgentInstances {
				if instance.EntryPoint {
					out.EntryAgentInstanceID = instance.ID
					out.EntryAgentName = instance.Name
					break
				}
			}
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
	status, err := s.setDesignation(ctx, userID, workspaceID)
	if err == nil {
		// Bounded, field-only observable event (PRD FR137) — no prompt,
		// prose, or workspace content, just stable IDs.
		logger.Info("personal hq: existing workspace designated", logger.Fields{"user_id": userID, "workspace_id": workspaceID})
	}
	return status, err
}

// Replace atomically switches the Personal HQ designation to a different
// workspace, regardless of whether one is currently designated. The switch
// is a single-column update, so no intermediate zero-HQ state is observable.
func (s *Service) Replace(ctx context.Context, userID, workspaceID string) (*Status, error) {
	userID = normalizeUserID(userID)
	previous, _ := s.Status(ctx, userID)
	status, err := s.setDesignation(ctx, userID, workspaceID)
	if err == nil {
		fields := logger.Fields{"user_id": userID, "workspace_id": workspaceID}
		if previous != nil && previous.HasDesignation() {
			fields["previous_workspace_id"] = previous.WorkspaceID
		}
		logger.Info("personal hq: designation replaced", fields)
	}
	return status, err
}

// Clear removes the Personal HQ designation without touching the workspace
// itself or the user's onboarding history.
func (s *Service) Clear(ctx context.Context, userID string) (*Status, error) {
	if s == nil || s.profiles == nil {
		return nil, errors.New("personal hq service is not configured")
	}
	userID = normalizeUserID(userID)
	previous, _ := s.Status(ctx, userID)
	if err := s.profiles.SetPersonalWorkspaceID(ctx, userID, ""); err != nil {
		return nil, err
	}
	if previous != nil && previous.HasDesignation() {
		s.syncDesignation(ctx, previous.WorkspaceID, "")
		logger.Info("personal hq: designation cleared", logger.Fields{"user_id": userID, "workspace_id": previous.WorkspaceID})
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
	previous, _ := s.Status(ctx, userID)
	if err := s.profiles.SetHQOnboardingState(ctx, userID, state); err != nil {
		return nil, err
	}
	if previous != nil {
		logger.Info("personal hq: onboarding state changed", logger.Fields{
			"user_id": userID, "from": string(previous.OnboardingState), "to": string(state),
		})
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
	// Capture the workspace previously pointed at (stale or valid) before the
	// record moves, so its workspace-side projection can be cleared when the
	// HQ changes workspaces.
	var previousWorkspaceID string
	if prev, prevErr := s.profiles.GetPersonalHQState(ctx, userID); prevErr == nil && prev != nil {
		previousWorkspaceID = strings.TrimSpace(prev.PersonalWorkspaceID)
	}
	if err := s.profiles.SetPersonalWorkspaceID(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	// Project the transition onto the workspace records. The record just
	// written is the source of truth; these mirrors are best-effort. The
	// target is guaranteed non-group here (ineligibleReason gate above).
	s.syncDesignation(ctx, workspaceID, string(session.WorkspaceDesignationPersonalHQ))
	if previousWorkspaceID != "" && previousWorkspaceID != workspaceID {
		s.syncDesignation(ctx, previousWorkspaceID, "")
	}
	if s.onDesignated != nil {
		s.onDesignated(ctx, userID, workspaceID)
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
