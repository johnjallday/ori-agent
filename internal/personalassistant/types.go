// Package personalassistant owns the durable user-to-assistant relationship and
// the idempotent first-assignment journal. It stores links to canonical Ori
// records; it does not replace Personal HQ, Tickets, Follow-Ups, Daily Brief,
// user profile, workspace Memory, or agent runtime stores.
package personalassistant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/types"
)

const (
	// MaxDisplayNameLen bounds the user-visible assistant name.
	MaxDisplayNameLen = 100
	// MaxMandateLen bounds the free-text working agreement.
	MaxMandateLen = 1000
	// MaxAssignmentTextLen bounds one user-authored first-assignment item.
	MaxAssignmentTextLen = 2000
	// MaxAppearanceJSONBytes bounds the duplicate appearance snapshot.
	MaxAppearanceJSONBytes = 4096
	// MaxAssignmentJSONBytes bounds one normalized preview payload.
	MaxAssignmentJSONBytes = 32 * 1024
	// MaxCanonicalRefs is the maximum number of records one first assignment may create.
	MaxCanonicalRefs = 64
)

var (
	// ErrNotFound means the user-owned relationship or preview does not exist.
	ErrNotFound = errors.New("personal assistant: not found")
	// ErrConflict means a compare-and-swap version or uniqueness check failed.
	ErrConflict = errors.New("personal assistant: state conflict")
	// ErrRepairNeeded means a durable relationship has an invalid canonical link.
	ErrRepairNeeded = errors.New("personal assistant: repair needed")
	// ErrNeedsHQ means a genuinely hired relationship has no Personal HQ yet.
	// This is an expected setup stage, not corruption — callers must never map
	// it onto repair language or a generic "hire" prompt.
	ErrNeedsHQ         = errors.New("personal assistant: personal hq is not built yet")
	htmlLikeTagPattern = regexp.MustCompile(`<\s*/?\s*[A-Za-z][^>]*>`)
)

// RelationshipStatus is the durable hire lifecycle.
//
// The setup sequence is:
//
//	not_hired -> hiring -> awaiting_hq -> provisioning_hq -> active
//
// Per-status invariants, enforced by ValidateStateInvariants:
//
//   - not_hired: no owned profile, no HQ, no entry instance, no HiredAt.
//   - hiring: a confirmed hire is creating or finalizing the assistant profile
//     and the relationship row. It never means "creating a workspace".
//   - awaiting_hq: the owned global profile exists and HiredAt is set. HQ
//     workspace and entry-instance IDs are still empty — Build My HQ has not
//     been confirmed. This is an expected setup stage, not corruption.
//   - provisioning_hq: a confirmed HQ setup is partially applied. It is entered
//     only after the HQ operation is claimed, so the profile still exists and
//     the canonical IDs fill in as each checkpoint returns them.
//   - active/paused: the complete validated linkage (profile -> entry instance
//     -> HQ workspace) plus a Daily Brief source resolves.
//   - repair_needed: a durable result exists whose known safe continuation
//     failed. It is never used for the ordinary pre-HQ stage.
type RelationshipStatus string

const (
	StatusNotHired RelationshipStatus = "not_hired"
	StatusHiring   RelationshipStatus = "hiring"
	// StatusAwaitingHQ means a real assistant profile and relationship exist and
	// the user has not yet confirmed Build My HQ on the guided Map quest.
	StatusAwaitingHQ RelationshipStatus = "awaiting_hq"
	// StatusProvisioningHQ means a confirmed HQ setup request is claimed and
	// partially applied, so a restart projects a resumable operation rather than
	// inviting a second create.
	StatusProvisioningHQ RelationshipStatus = "provisioning_hq"
	StatusActive         RelationshipStatus = "active"
	StatusPaused         RelationshipStatus = "paused"
	StatusRepairNeeded   RelationshipStatus = "repair_needed"
)

// NormalizeRelationshipStatus returns a canonical closed-enum value.
func NormalizeRelationshipStatus(raw string) (RelationshipStatus, error) {
	status := RelationshipStatus(strings.ToLower(strings.TrimSpace(raw)))
	switch status {
	case StatusNotHired, StatusHiring, StatusAwaitingHQ, StatusProvisioningHQ,
		StatusActive, StatusPaused, StatusRepairNeeded:
		return status, nil
	default:
		return "", fmt.Errorf("personal assistant: invalid relationship status %q", raw)
	}
}

// HasOwnedProfile reports whether the status guarantees that a durable global
// agent profile is owned by this relationship.
func (s RelationshipStatus) HasOwnedProfile() bool {
	switch s {
	case StatusAwaitingHQ, StatusProvisioningHQ, StatusActive, StatusPaused:
		return true
	default:
		return false
	}
}

// RequiresHQ reports whether the status requires a complete validated HQ
// linkage. Pre-HQ stages deliberately do not.
func (s RelationshipStatus) RequiresHQ() bool {
	return s == StatusActive || s == StatusPaused
}

// FirstAssignmentStatus is the durable first-value lifecycle.
type FirstAssignmentStatus string

const (
	FirstAssignmentNotStarted FirstAssignmentStatus = "not_started"
	FirstAssignmentPreviewed  FirstAssignmentStatus = "previewed"
	FirstAssignmentApplying   FirstAssignmentStatus = "applying"
	FirstAssignmentCompleted  FirstAssignmentStatus = "completed"
	FirstAssignmentFailed     FirstAssignmentStatus = "failed"
)

// NormalizeFirstAssignmentStatus returns a canonical closed-enum value.
func NormalizeFirstAssignmentStatus(raw string) (FirstAssignmentStatus, error) {
	status := FirstAssignmentStatus(strings.ToLower(strings.TrimSpace(raw)))
	switch status {
	case FirstAssignmentNotStarted, FirstAssignmentPreviewed, FirstAssignmentApplying,
		FirstAssignmentCompleted, FirstAssignmentFailed:
		return status, nil
	default:
		return "", fmt.Errorf("personal assistant: invalid first-assignment status %q", raw)
	}
}

// AssignmentStatus is one preview/apply operation's lifecycle.
type AssignmentStatus string

const (
	AssignmentPreviewed  AssignmentStatus = "previewed"
	AssignmentApplying   AssignmentStatus = "applying"
	AssignmentCompleted  AssignmentStatus = "completed"
	AssignmentFailed     AssignmentStatus = "failed"
	AssignmentSuperseded AssignmentStatus = "superseded"
)

// NormalizeAssignmentStatus returns a canonical closed-enum value.
func NormalizeAssignmentStatus(raw string) (AssignmentStatus, error) {
	status := AssignmentStatus(strings.ToLower(strings.TrimSpace(raw)))
	switch status {
	case AssignmentPreviewed, AssignmentApplying, AssignmentCompleted, AssignmentFailed, AssignmentSuperseded:
		return status, nil
	default:
		return "", fmt.Errorf("personal assistant: invalid assignment status %q", raw)
	}
}

// FocusArea is one supported initial working-agreement focus.
type FocusArea string

const (
	FocusPlanMyDay          FocusArea = "plan_my_day"
	FocusTrackCommitments   FocusArea = "track_commitments_and_follow_ups"
	FocusPrepareForMeetings FocusArea = "prepare_for_meetings"
	FocusKeepProjectsMoving FocusArea = "keep_projects_moving"
	FocusHelpWithEmail      FocusArea = "help_with_email"
	FocusSomethingElse      FocusArea = "something_else"

	// Domain focus areas. A specialist mapping offers these in place of the
	// generic set above; the enum stays closed and server-validated either way.
	FocusTrackSongsInProgress      FocusArea = "track_songs_in_progress"
	FocusChaseCollaboratorHandoffs FocusArea = "chase_collaborator_handoffs"
	FocusKeepReleaseDatesVisible   FocusArea = "keep_release_dates_visible"
	FocusOrganizeProjectFiles      FocusArea = "organize_project_files"
)

var focusAliases = map[string]FocusArea{
	"plan_my_day": FocusPlanMyDay, "plan my day": FocusPlanMyDay,
	"track_commitments_and_follow_ups": FocusTrackCommitments,
	"track commitments and follow-ups": FocusTrackCommitments,
	"track commitments and follow ups": FocusTrackCommitments,
	"prepare_for_meetings":             FocusPrepareForMeetings, "prepare for meetings": FocusPrepareForMeetings,
	"keep_projects_moving": FocusKeepProjectsMoving, "keep projects moving": FocusKeepProjectsMoving,
	"help_with_email": FocusHelpWithEmail, "help with email": FocusHelpWithEmail,
	"something_else": FocusSomethingElse, "something else": FocusSomethingElse,

	"track_songs_in_progress":     FocusTrackSongsInProgress,
	"track songs in progress":     FocusTrackSongsInProgress,
	"chase_collaborator_handoffs": FocusChaseCollaboratorHandoffs,
	"chase collaborator handoffs": FocusChaseCollaboratorHandoffs,
	"keep_release_dates_visible":  FocusKeepReleaseDatesVisible,
	"keep release dates visible":  FocusKeepReleaseDatesVisible,
	"organize_project_files":      FocusOrganizeProjectFiles,
	"organize project files":      FocusOrganizeProjectFiles,
}

// NormalizeFocusAreas validates, canonicalizes, and de-duplicates focus areas.
func NormalizeFocusAreas(raw []string) ([]FocusArea, error) {
	if len(raw) > 6 {
		return nil, fmt.Errorf("personal assistant: too many focus areas")
	}
	seen := make(map[FocusArea]struct{}, 6)
	out := make([]FocusArea, 0, len(raw))
	for _, value := range raw {
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		key = strings.ReplaceAll(key, "-", "_")
		area, ok := focusAliases[key]
		if !ok {
			// Try the canonical underscore form after whitespace normalization.
			area, ok = focusAliases[strings.ReplaceAll(key, " ", "_")]
		}
		if !ok {
			return nil, fmt.Errorf("personal assistant: invalid focus area %q", value)
		}
		if _, duplicate := seen[area]; duplicate {
			continue
		}
		seen[area] = struct{}{}
		out = append(out, area)
	}
	if len(out) > 6 {
		return nil, fmt.Errorf("personal assistant: too many focus areas")
	}
	return out, nil
}

// NormalizeSpecialistSlug validates an accepted domain specialist against the
// built-in mapping. An empty slug is valid and means the generic relationship.
//
// This is the input boundary: an unknown slug is rejected here rather than
// persisted. Persisted rows are deliberately not re-checked against the
// registry on read, so a future mapping change can never make an existing
// relationship unreadable — an unrecognised persisted slug simply reads as no
// specialist.
func NormalizeSpecialistSlug(raw string) (string, error) {
	slug := strings.TrimSpace(raw)
	if slug == "" {
		return "", nil
	}
	if _, ok := specialist.Get(slug); !ok {
		return "", fmt.Errorf("personal assistant: unknown specialist %q", raw)
	}
	return slug, nil
}

// RepairStep is a closed, safe provisioning step code. It never contains a
// provider/database error or user-authored text.
type RepairStep string

const (
	RepairNone RepairStep = ""
	// RepairProfileCreation means the hire could not create or take ownership of
	// the global assistant profile.
	RepairProfileCreation RepairStep = "profile_creation"
	// RepairHQCreation means a claimed HQ setup could not create the workspace
	// through the canonical template path.
	RepairHQCreation       RepairStep = "hq_creation"
	RepairDesignation      RepairStep = "designation"
	RepairDailyBriefConfig RepairStep = "daily_brief_config"
	RepairFinalization     RepairStep = "relationship_finalization"
)

// NormalizeRepairStep validates a persisted repair step.
func NormalizeRepairStep(raw string) (RepairStep, error) {
	step := RepairStep(strings.TrimSpace(raw))
	switch step {
	case RepairNone, RepairProfileCreation, RepairHQCreation,
		RepairDesignation, RepairDailyBriefConfig, RepairFinalization:
		return step, nil
	default:
		return "", fmt.Errorf("personal assistant: invalid repair step %q", raw)
	}
}

// RenameStep is the durable, restart-safe rename operation boundary.
type RenameStep string

const (
	RenameNone            RenameStep = ""
	RenameProfilePending  RenameStep = "profile_pending"
	RenameHQPending       RenameStep = "hq_pending"
	RenameSessionsPending RenameStep = "sessions_pending"
	RenameStatePending    RenameStep = "state_pending"
)

func NormalizeRenameStep(raw string) (RenameStep, error) {
	step := RenameStep(strings.TrimSpace(raw))
	switch step {
	case RenameNone, RenameProfilePending, RenameHQPending, RenameSessionsPending, RenameStatePending:
		return step, nil
	default:
		return "", fmt.Errorf("personal assistant: invalid rename step %q", raw)
	}
}

// State is one user's durable personal-assistant relationship.
type State struct {
	UserID                 string
	AssistantID            string
	Status                 RelationshipStatus
	DisplayName            string
	Appearance             *types.AgentAppearance
	HQWorkspaceID          string
	HQEntryAgentInstanceID string
	GlobalAgentProfileName string
	Mandate                string
	FocusAreas             []FocusArea
	// SpecialistSlug is the domain specialist the user accepted, or "" for the
	// generic relationship. It is a stable machine identity from the built-in
	// mapping, never user-authored text.
	SpecialistSlug        string
	FirstAssignmentStatus FirstAssignmentStatus
	LastHireRequestID     string
	HirePayloadHash       string
	HirePayloadJSON       string
	// HQ setup operation journal. These are provisional recovery fields for one
	// confirmed Build My HQ request: enough to make a replay idempotent and a
	// restart resumable, and nothing more. The payload is reduced to its receipt
	// (request ID plus hash) once the canonical workspace and Daily Brief config
	// exist, so PAF never holds a permanent duplicate of the brief schedule.
	LastHQRequestID string
	HQPayloadHash   string
	HQPayloadJSON   string
	RepairStep      RepairStep
	RenameFromName  string
	RenameToName    string
	RenameStep      RenameStep
	StateVersion    int64
	HiredAt         *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NewState constructs a fresh relationship with a generated stable ID.
func NewState(userID string) *State {
	return &State{
		UserID: strings.TrimSpace(userID), AssistantID: uuid.NewString(),
		Status: StatusNotHired, Appearance: types.NewAgentAppearance(),
		FirstAssignmentStatus: FirstAssignmentNotStarted, StateVersion: 1,
	}
}

// Clone returns a defensive deep copy.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	out := *s
	out.Appearance = s.Appearance.Clone()
	out.FocusAreas = append([]FocusArea(nil), s.FocusAreas...)
	if s.HiredAt != nil {
		hiredAt := *s.HiredAt
		out.HiredAt = &hiredAt
	}
	return &out
}

// ValidateStateInvariants rejects persisted or in-flight combinations that no
// legal transition can produce. It is deliberately structural: it never infers
// a profile, HQ, or entry instance from a display name, and it never repairs a
// malformed row into a plausible-looking one.
func (s *State) ValidateStateInvariants() error {
	if s == nil {
		return errors.New("personal assistant: nil state")
	}
	profile := strings.TrimSpace(s.GlobalAgentProfileName)
	workspace := strings.TrimSpace(s.HQWorkspaceID)
	instance := strings.TrimSpace(s.HQEntryAgentInstanceID)

	if s.Status.HasOwnedProfile() && profile == "" {
		return fmt.Errorf("personal assistant: %s requires an owned global profile", s.Status)
	}
	if s.Status.HasOwnedProfile() && s.HiredAt == nil {
		return fmt.Errorf("personal assistant: %s requires a hire timestamp", s.Status)
	}
	switch s.Status {
	case StatusNotHired:
		if profile != "" || workspace != "" || instance != "" || s.HiredAt != nil {
			return errors.New("personal assistant: not_hired must carry no durable result")
		}
	case StatusAwaitingHQ:
		// Build My HQ has not been confirmed, so no canonical HQ ID can exist.
		if workspace != "" || instance != "" {
			return errors.New("personal assistant: awaiting_hq must not carry hq identifiers")
		}
	case StatusProvisioningHQ:
		// The operation claim is written before any creation, so the request ID
		// is the one thing that must already be present.
		if strings.TrimSpace(s.LastHQRequestID) == "" {
			return errors.New("personal assistant: provisioning_hq requires an hq request id")
		}
		if instance != "" && workspace == "" {
			return errors.New("personal assistant: entry instance without an hq workspace")
		}
	case StatusActive, StatusPaused:
		if workspace == "" || instance == "" {
			return fmt.Errorf("personal assistant: %s requires complete hq linkage", s.Status)
		}
	case StatusHiring, StatusRepairNeeded:
		// Bounded partial results are the point of these states; the remaining
		// checks below still apply.
	}
	if instance != "" && workspace == "" {
		return errors.New("personal assistant: entry instance without an hq workspace")
	}
	if workspace != "" && profile == "" {
		return errors.New("personal assistant: hq workspace without an owned global profile")
	}
	return nil
}

// CanonicalRef points to a record created by an assignment apply.
type CanonicalRef struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ID          string `json:"id"`
}

// Assignment is one idempotent preview/apply journal record.
type Assignment struct {
	PreviewID             string
	UserID                string
	AssistantID           string
	AssignmentVersion     int64
	NormalizedPayload     json.RawMessage
	NormalizedPayloadHash string
	ApplyRequestID        string
	BriefRequestID        string
	BriefRevisionID       string
	BriefStatus           string
	BriefTrigger          string
	Status                AssignmentStatus
	CreatedCanonicalRefs  []CanonicalRef
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Clone returns a defensive deep copy.
func (a *Assignment) Clone() *Assignment {
	if a == nil {
		return nil
	}
	out := *a
	out.NormalizedPayload = append(json.RawMessage(nil), a.NormalizedPayload...)
	out.CreatedCanonicalRefs = append([]CanonicalRef(nil), a.CreatedCanonicalRefs...)
	return &out
}

// PayloadHash returns the SHA-256 hash used to bind apply to a preview payload.
func PayloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validateText(label, value string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("personal assistant: %s is required", label)
	}
	if utf8.RuneCountInString(value) > max {
		return "", fmt.Errorf("personal assistant: %s is capped at %d characters", label, max)
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return "", fmt.Errorf("personal assistant: %s contains a control character", label)
		}
	}
	if err := sensitive.RejectSecretLikeText(value); err != nil {
		return "", fmt.Errorf("personal assistant: %s: %w", label, err)
	}
	return value, nil
}

func validateMandate(value string) (string, error) {
	value, err := validateText("mandate", value, MaxMandateLen, false)
	if err != nil {
		return "", err
	}
	if htmlLikeTagPattern.MatchString(value) {
		return "", errors.New("personal assistant: mandate must be plain text")
	}
	return value, nil
}

func validateOpaqueID(label, value string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("personal assistant: %s is required", label)
	}
	if len(value) > 200 {
		return "", fmt.Errorf("personal assistant: %s is too long", label)
	}
	return value, nil
}
