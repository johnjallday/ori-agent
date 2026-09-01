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
	"github.com/johnjallday/ori-agent/internal/types"
)

const (
	// CurrentRolloutVersion is persisted only for installations explicitly
	// enrolled at first state-file creation.
	CurrentRolloutVersion = 1
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

// NormalizeRolloutVersion accepts only the explicit ineligible marker (zero)
// and the rollout version understood by this build.
func NormalizeRolloutVersion(value int) (int, error) {
	switch value {
	case 0, CurrentRolloutVersion:
		return value, nil
	default:
		return 0, fmt.Errorf("personal assistant: unsupported rollout version %d", value)
	}
}

var (
	// ErrNotFound means the user-owned relationship or preview does not exist.
	ErrNotFound = errors.New("personal assistant: not found")
	// ErrConflict means a compare-and-swap version or uniqueness check failed.
	ErrConflict = errors.New("personal assistant: state conflict")
	// ErrIneligible means the installation is not enrolled in this rollout.
	ErrIneligible = errors.New("personal assistant: installation is ineligible")
	// ErrRepairNeeded means a durable relationship has an invalid canonical link.
	ErrRepairNeeded    = errors.New("personal assistant: repair needed")
	htmlLikeTagPattern = regexp.MustCompile(`<\s*/?\s*[A-Za-z][^>]*>`)
)

// RelationshipStatus is the durable hire lifecycle.
type RelationshipStatus string

const (
	StatusNotHired     RelationshipStatus = "not_hired"
	StatusHiring       RelationshipStatus = "hiring"
	StatusActive       RelationshipStatus = "active"
	StatusPaused       RelationshipStatus = "paused"
	StatusRepairNeeded RelationshipStatus = "repair_needed"
)

// NormalizeRelationshipStatus returns a canonical closed-enum value.
func NormalizeRelationshipStatus(raw string) (RelationshipStatus, error) {
	status := RelationshipStatus(strings.ToLower(strings.TrimSpace(raw)))
	switch status {
	case StatusNotHired, StatusHiring, StatusActive, StatusPaused, StatusRepairNeeded:
		return status, nil
	default:
		return "", fmt.Errorf("personal assistant: invalid relationship status %q", raw)
	}
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

// RepairStep is a closed, safe provisioning step code. It never contains a
// provider/database error or user-authored text.
type RepairStep string

const (
	RepairNone             RepairStep = ""
	RepairDesignation      RepairStep = "designation"
	RepairDailyBriefConfig RepairStep = "daily_brief_config"
	RepairFinalization     RepairStep = "relationship_finalization"
)

// NormalizeRepairStep validates a persisted repair step.
func NormalizeRepairStep(raw string) (RepairStep, error) {
	step := RepairStep(strings.TrimSpace(raw))
	switch step {
	case RepairNone, RepairDesignation, RepairDailyBriefConfig, RepairFinalization:
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
	FirstAssignmentStatus  FirstAssignmentStatus
	LastHireRequestID      string
	HirePayloadHash        string
	HirePayloadJSON        string
	RepairStep             RepairStep
	RenameFromName         string
	RenameToName           string
	RenameStep             RenameStep
	StateVersion           int64
	HiredAt                *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
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
