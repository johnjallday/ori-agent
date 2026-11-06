package learningmissions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid learning record")
	ErrUnavailable = errors.New("learning record unavailable")
	ErrConflict    = errors.New("learning state changed; reload before trying again")
	ErrUnsupported = errors.New("learning definition version unavailable")
	ErrLimit       = errors.New("learning storage or request limit reached")
)

const (
	MaxWorkspaces     = 5
	MaxCandidates     = 8
	MaxInputBytes     = 16 * 1024
	MaxResponseBytes  = 8 * 1024
	MaxDraftBytes     = 4 * 1024
	MaxResultBytes    = 64 * 1024
	MaxOutputTokens   = 1200
	GenerationTimeout = 30 * time.Second
	ExplicitSpacing   = 30 * time.Second
	AutomaticSpacing  = 24 * time.Hour
	DailyCalls        = 10
	OfferLifetime     = 24 * time.Hour
	ReviewLifetime    = 10 * time.Minute
	DeferCooldown     = 7 * 24 * time.Hour
	MaxRecordBytes    = 16 * 1024
	MaxLearningBytes  = 16 * 1024 * 1024
	MaxEvidence       = 10000
)

type State string

const (
	Offered     State = "offered"
	Active      State = "active"
	Blocked     State = "blocked"
	Completed   State = "completed"
	Deferred    State = "deferred"
	Declined    State = "declined"
	Stopped     State = "stopped"
	Invalidated State = "invalidated"
)

func (s State) Valid() bool {
	return slices.Contains([]State{Offered, Active, Blocked, Completed, Deferred, Declined, Stopped, Invalidated}, s)
}

func CanTransition(from, to State) bool {
	switch from {
	case Offered:
		return slices.Contains([]State{Active, Deferred, Declined, Invalidated}, to)
	case Active:
		return slices.Contains([]State{Blocked, Completed, Stopped, Invalidated}, to)
	case Blocked:
		return slices.Contains([]State{Active, Stopped, Invalidated}, to)
	default:
		return false
	}
}

type Guidance string

const (
	GuidanceUnknown Guidance = "unknown"
	GuidanceBasic   Guidance = "basic"
	GuidanceGuided  Guidance = "guided"
)

type Preferences struct {
	Mode             string   `json:"mode"`
	WorkspaceIDs     []string `json:"workspace_ids"`
	Observe          bool     `json:"observe"`
	ObservationSince int64    `json:"observation_since_ms,omitempty"`
	Guidance         Guidance `json:"guidance"`
	Focus            string   `json:"focus,omitempty"`
	Effort           string   `json:"effort"`
	ModelFingerprint string   `json:"model_fingerprint,omitempty"`
	ConsentVersion   int      `json:"consent_version"`
}

func DefaultPreferences() Preferences {
	return Preferences{Mode: "on_request", WorkspaceIDs: []string{}, Guidance: GuidanceGuided, Effort: "small"}
}

func (p Preferences) Validate() error {
	if !slices.Contains([]string{"off", "on_request", "automatic"}, p.Mode) ||
		!slices.Contains([]Guidance{GuidanceBasic, GuidanceGuided}, p.Guidance) ||
		!slices.Contains([]string{"small", "standard"}, p.Effort) || len(p.WorkspaceIDs) > MaxWorkspaces ||
		!boundedText(p.Focus, 300, false) || p.ConsentVersion < 0 || p.ConsentVersion > 1 ||
		(p.ModelFingerprint != "" && !validDigest(p.ModelFingerprint)) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range p.WorkspaceIDs {
		if !validID(id) || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	if (p.Observe || p.Mode == "automatic") && (len(p.WorkspaceIDs) == 0 || p.ConsentVersion != 1) {
		return ErrInvalid
	}
	if p.Observe && p.ObservationSince <= 0 {
		return ErrInvalid
	}
	if p.Mode == "automatic" && !validDigest(p.ModelFingerprint) {
		return ErrInvalid
	}
	return nil
}

type Owner struct {
	ID          string      `json:"-"`
	Revision    int64       `json:"revision"`
	Epoch       int64       `json:"-"`
	Preferences Preferences `json:"preferences"`
}

// Definition freezes the reviewed proposal; no routes, permissions or actions
// are accepted from generated prose. CandidateRef is resolved by the host.
type Definition struct {
	Structure        StructureID `json:"structure"`
	Version          int         `json:"version"`
	WorkspaceID      string      `json:"studio_id"`
	Incarnation      string      `json:"incarnation"`
	CandidateRef     string      `json:"candidate_ref"`
	ScopeDigest      string      `json:"scope_digest"`
	Goal             string      `json:"goal"`
	Scope            string      `json:"scope"`
	Why              string      `json:"why"`
	Steps            []string    `json:"steps"`
	Guidance         Guidance    `json:"guidance"`
	Hints            []string    `json:"hints,omitempty"`
	Personalized     bool        `json:"personalized"`
	ModelFingerprint string      `json:"model_fingerprint,omitempty"`
	OriginalReceipt  string      `json:"original_receipt,omitempty"`
	ExpiresAt        int64       `json:"expires_at_ms"`
}

func (d Definition) Validate() error {
	if _, err := Lookup(d.Structure, d.Version); err != nil {
		return err
	}
	if !validID(d.WorkspaceID) || !validID(d.Incarnation) || !validID(d.CandidateRef) ||
		!validDigest(d.ScopeDigest) || !boundedText(d.Goal, 300, true) ||
		!boundedText(d.Scope, 1000, true) || !boundedText(d.Why, 500, true) ||
		!validSteps(d.Structure, d.Version, d.Steps) || len(d.Hints) > len(d.Steps) ||
		!slices.Contains([]Guidance{GuidanceBasic, GuidanceGuided}, d.Guidance) || d.ExpiresAt <= 0 {
		return ErrInvalid
	}
	for _, hint := range d.Hints {
		if !boundedText(hint, 500, true) {
			return ErrInvalid
		}
	}
	if d.Personalized && !validDigest(d.ModelFingerprint) {
		return ErrInvalid
	}
	if d.Structure != DelegateInspect && !validID(d.OriginalReceipt) {
		return ErrInvalid
	}
	return nil
}

// ResultRef is identity, not result text or permission to open a file.
type ResultRef struct {
	WorkspaceID string `json:"studio_id"`
	Incarnation string `json:"incarnation"`
	TicketID    string `json:"ticket_id"`
	RunID       string `json:"run_id"`
	ArtifactID  string `json:"artifact_id"`
	Digest      string `json:"digest"`
}

func (r ResultRef) Validate() error {
	if !validID(r.WorkspaceID) || !validID(r.Incarnation) || !validID(r.TicketID) ||
		!validID(r.RunID) || !validID(r.ArtifactID) || !validDigest(r.Digest) {
		return ErrInvalid
	}
	return nil
}

type Progress struct {
	Blocker     string     `json:"blocker,omitempty"`
	TicketID    string     `json:"ticket_id,omitempty"`
	OperationID string     `json:"operation_id,omitempty"`
	AttemptID   string     `json:"attempt_id,omitempty"`
	RunID       string     `json:"run_id,omitempty"`
	Input       string     `json:"input,omitempty"`
	Assignee    string     `json:"assignee,omitempty"`
	InputDigest string     `json:"input_digest,omitempty"`
	InputChange string     `json:"input_change,omitempty"`
	Correction  string     `json:"correction,omitempty"`
	Result      *ResultRef `json:"result,omitempty"`
	AcceptedAt  int64      `json:"accepted_at_ms,omitempty"`
	HintsShown  bool       `json:"hints_shown"`
	HintsUsed   bool       `json:"hints_used"`
	ReviewID    string     `json:"review_id,omitempty"`
}

func (p Progress) Validate() error {
	for _, id := range []string{p.Blocker, p.TicketID, p.OperationID, p.AttemptID, p.RunID, p.ReviewID} {
		if id != "" && !validID(id) {
			return ErrInvalid
		}
	}
	if !boundedText(p.Assignee, 128, false) || len(p.Input) > MaxDraftBytes || len(p.Correction) > MaxDraftBytes ||
		!boundedText(p.InputChange, 500, false) || !utf8.ValidString(p.Input) || !utf8.ValidString(p.Correction) ||
		(p.InputDigest != "" && !validDigest(p.InputDigest)) {
		return ErrInvalid
	}
	if p.Result != nil {
		return p.Result.Validate()
	}
	return nil
}

type Instance struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"-"`
	Epoch      int64      `json:"-"`
	Revision   int64      `json:"revision"`
	State      State      `json:"state"`
	Definition Definition `json:"definition"`
	Progress   Progress   `json:"progress"`
	TicketHref string     `json:"ticket_href,omitempty"` // read-only canonical host projection
	CreatedAt  int64      `json:"created_at_ms"`
	UpdatedAt  int64      `json:"updated_at_ms"`
}

func (i Instance) Validate() error {
	if !validID(i.ID) || !validID(i.OwnerID) || i.Revision < 1 || i.Epoch < 1 || !i.State.Valid() ||
		i.CreatedAt <= 0 || i.UpdatedAt < i.CreatedAt {
		return ErrInvalid
	}
	if err := i.Definition.Validate(); err != nil {
		return err
	}
	return i.Progress.Validate()
}

type Operation struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Epoch          int64  `json:"-"`
	RequestDigest  string `json:"-"`
	InstanceID     string `json:"instance_id,omitempty"`
	TicketID       string `json:"ticket_id,omitempty"`
	ReviewID       string `json:"review_id,omitempty"`
	AttemptID      string `json:"attempt_id,omitempty"`
	Failure        string `json:"failure,omitempty"`
	CreatedAt      int64  `json:"created_at_ms"`
	ReservedTokens int    `json:"reserved_tokens,omitempty"`
	ActualTokens   *int   `json:"actual_tokens,omitempty"`
}

func (o Operation) Validate() error {
	if !validID(o.ID) || !validID(o.Kind) || len(o.Kind) > 32 || !validDigest(o.RequestDigest) || o.Epoch < 1 || o.CreatedAt <= 0 ||
		!slices.Contains([]string{"pending", "succeeded", "failed", "unknown", "invalidated"}, o.Status) || o.ReservedTokens < 0 ||
		(o.ActualTokens != nil && *o.ActualTokens < 0) {
		return ErrInvalid
	}
	for _, id := range []string{o.InstanceID, o.TicketID, o.ReviewID, o.AttemptID, o.Failure} {
		if id != "" && !validID(id) {
			return ErrInvalid
		}
	}
	return nil
}

type Review struct {
	ID              string      `json:"id"`
	InstanceID      string      `json:"instance_id,omitempty"`
	Revision        int64       `json:"revision"`
	Epoch           int64       `json:"epoch"`
	Structure       StructureID `json:"structure"`
	Version         int         `json:"version"`
	Result          ResultRef   `json:"result"`
	Original        *ResultRef  `json:"original,omitempty"`
	Decision        string      `json:"decision,omitempty"`
	Reason          string      `json:"reason,omitempty"`
	Note            string      `json:"note,omitempty"`
	Change          string      `json:"change,omitempty"`
	Guidance        Guidance    `json:"guidance"`
	HintsShown      bool        `json:"hints_shown"`
	HintsUsed       bool        `json:"hints_used"`
	InputDigest     string      `json:"input_digest"`
	OriginalReceipt string      `json:"original_receipt,omitempty"`
	CreatedAt       int64       `json:"created_at_ms"`
	ExpiresAt       int64       `json:"expires_at_ms"`
	DecidedAt       int64       `json:"decided_at_ms,omitempty"`
}

func (r Review) Validate() error {
	if !validID(r.ID) || (r.InstanceID != "" && !validID(r.InstanceID)) || r.Revision < 1 || r.Epoch < 1 ||
		r.CreatedAt <= 0 || r.ExpiresAt <= r.CreatedAt || !validDigest(r.InputDigest) ||
		!slices.Contains([]Guidance{GuidanceUnknown, GuidanceBasic, GuidanceGuided}, r.Guidance) {
		return ErrInvalid
	}
	if _, err := Lookup(r.Structure, r.Version); err != nil {
		return err
	}
	if err := r.Result.Validate(); err != nil {
		return err
	}
	if r.Original != nil {
		if err := r.Original.Validate(); err != nil {
			return err
		}
	}
	if !boundedText(r.Note, 500, false) || !boundedText(r.Change, 500, false) ||
		(r.OriginalReceipt != "" && !validID(r.OriginalReceipt)) {
		return ErrInvalid
	}
	if r.Decision == "" {
		if r.DecidedAt != 0 || r.Reason != "" || r.Note != "" || r.Change != "" {
			return ErrInvalid
		}
		return nil
	}
	if !slices.Contains([]string{"use", "revise", "reject"}, r.Decision) || r.DecidedAt < r.CreatedAt || r.DecidedAt > r.ExpiresAt ||
		!slices.Contains([]string{"meets_scope", "missing_detail", "incorrect", "not_useful", "other"}, r.Reason) ||
		(r.Reason == "other" && strings.TrimSpace(r.Note) == "") {
		return ErrInvalid
	}
	return nil
}

type Evidence struct {
	Key         string      `json:"key"`
	ReviewID    string      `json:"review_id"`
	Structure   StructureID `json:"structure"`
	Version     int         `json:"version"`
	Competency  string      `json:"competency"`
	Guidance    Guidance    `json:"guidance"`
	Reused      bool        `json:"reused"`
	Unavailable bool        `json:"unavailable"`
	CreatedAt   int64       `json:"created_at_ms"`
}

func (e Evidence) Validate() error {
	if !validDigest(e.Key) || !validID(e.ReviewID) || e.CreatedAt <= 0 ||
		!slices.Contains([]string{"delegate", "evaluate", "improve", "reuse"}, e.Competency) ||
		!slices.Contains([]Guidance{GuidanceUnknown, GuidanceBasic, GuidanceGuided}, e.Guidance) {
		return ErrInvalid
	}
	_, err := Lookup(e.Structure, e.Version)
	return err
}

func validID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return s != "." && s != ".."
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

func boundedText(s string, max int, required bool) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || (required && strings.TrimSpace(s) == "") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func encode(value any, max int) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", ErrInvalid
	}
	if len(data) > max {
		return "", ErrLimit
	}
	return string(data), nil
}
