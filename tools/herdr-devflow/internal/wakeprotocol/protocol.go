// Package wakeprotocol defines the complete local IPC contract shared by the
// unprivileged Herdr helper and the privileged Herdr wake daemon.
//
// The protocol deliberately contains no executable paths, shell fragments,
// environment values, credentials, or arbitrary pmset arguments. The daemon
// derives every privileged path, event type, and event owner internally.
package wakeprotocol

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// Version is the only wire version accepted by v1 clients and daemons.
	Version = 1
	// StateVersion is the root-owned wake-state schema version.
	StateVersion = 1

	// MaxRequestBytes and MaxResponseBytes are checked before JSON decoding.
	MaxRequestBytes  = 32 * 1024
	MaxResponseBytes = 64 * 1024
	// MaxRequestsPerConnection makes request-count exhaustion impossible in v1:
	// every local socket connection carries exactly one request and response.
	MaxRequestsPerConnection = 1

	MaxRequestIDLength      = 96
	MaxIdempotencyKeyLength = 128
	MaxCandidateIDLength    = 96
	MaxBuildVersionLength   = 96
	MaxReasonLength         = 160
	MaxMessageLength        = 240
	MaxCandidates           = 64

	// MaxWakeHorizon is fixed by the accepted v1 engineering contract.
	MaxWakeHorizon = 7 * 24 * time.Hour
	// MaxExpiryAfterWake bounds how long stale candidate state can survive
	// after the event it existed to serve.
	MaxExpiryAfterWake = 2 * time.Hour
)

// Operation is one typed action the privileged daemon understands.
type Operation string

const (
	OperationHealth            Operation = "health"
	OperationRegisterOrReplace Operation = "register_or_replace"
	OperationCancel            Operation = "cancel"
	OperationList              Operation = "list"
	OperationVerify            Operation = "verify"
)

// Source identifies the unprivileged Herdr subsystem that owns a candidate.
type Source string

const (
	SourceOvernight    Source = "herdr-overnight"
	SourceContinuation Source = "herdr-continuation"
)

// Purpose explains the narrow reason a candidate may wake the Mac.
type Purpose string

const (
	PurposeOvernightStart Purpose = "overnight_scheduled_start"
	PurposeClaudeReset    Purpose = "overnight_claude_reset"
	PurposeContinuation   Purpose = "one_time_continuation"
)

// Result is the top-level outcome of a request.
type Result string

const (
	ResultSuccess   Result = "success"
	ResultRefusal   Result = "refusal"
	ResultUncertain Result = "uncertain"
)

// Code is a stable machine-readable result or refusal reason.
type Code string

const (
	CodeOK                   Code = "ok"
	CodeInvalidRequest       Code = "invalid_request"
	CodePayloadTooLarge      Code = "payload_too_large"
	CodeIncompatibleProtocol Code = "incompatible_protocol"
	CodeUnauthorized         Code = "unauthorized"
	CodeUnsupported          Code = "unsupported"
	CodeNotInstalled         Code = "not_installed"
	CodeNotFound             Code = "not_found"
	CodeConflict             Code = "conflict"
	CodeUnsafeHostSchedule   Code = "unsafe_host_schedule"
	CodePMSetFailed          Code = "pmset_failed"
	CodeVerificationFailed   Code = "verification_failed"
	CodeStateFailed          Code = "state_failed"
	CodeTimeout              Code = "timeout"
	CodeUncertain            Code = "uncertain"
)

// Request is the only top-level client-to-daemon envelope.
//
// Exactly one operation payload is accepted. RequestID correlates the reply;
// IdempotencyKey identifies one logical state-changing action across retries.
type Request struct {
	ProtocolVersion int        `json:"protocol_version"`
	RequestID       string     `json:"request_id"`
	HelperBuild     string     `json:"helper_build"`
	Operation       Operation  `json:"operation"`
	IdempotencyKey  string     `json:"idempotency_key,omitempty"`
	Candidate       *Candidate `json:"candidate,omitempty"`
	Target          *Target    `json:"target,omitempty"`
}

// Candidate is one validated request for a Herdr-owned wake.
type Candidate struct {
	ID        string    `json:"id"`
	Source    Source    `json:"source"`
	Purpose   Purpose   `json:"purpose"`
	WakeAt    time.Time `json:"wake_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Reason    string    `json:"reason,omitempty"`
}

// Target identifies exactly one previously registered candidate.
type Target struct {
	ID      string  `json:"id"`
	Source  Source  `json:"source"`
	Purpose Purpose `json:"purpose"`
}

// Response is the only daemon-to-client envelope.
type Response struct {
	ProtocolVersion int           `json:"protocol_version"`
	RequestID       string        `json:"request_id"`
	DaemonBuild     string        `json:"daemon_build"`
	Operation       Operation     `json:"operation"`
	Result          Result        `json:"result"`
	Code            Code          `json:"code"`
	Message         string        `json:"message,omitempty"`
	Health          *Health       `json:"health,omitempty"`
	State           *State        `json:"state,omitempty"`
	Verification    *Verification `json:"verification,omitempty"`
}

// Health is the bounded service identity returned by the health operation.
type Health struct {
	Installed       bool      `json:"installed"`
	Running         bool      `json:"running"`
	ProtocolVersion int       `json:"protocol_version"`
	StateVersion    int       `json:"state_version"`
	DaemonBuild     string    `json:"daemon_build"`
	AllowedUID      int       `json:"allowed_uid"`
	CheckedAt       time.Time `json:"checked_at"`
	LastSelfTestAt  time.Time `json:"last_self_test_at,omitempty"`
}

// State is the bounded public view of root-owned daemon state.
type State struct {
	StateVersion int         `json:"state_version"`
	AllowedUID   int         `json:"allowed_uid"`
	Candidates   []Candidate `json:"candidates"`
	Programmed   *Programmed `json:"programmed,omitempty"`
	ReconciledAt time.Time   `json:"reconciled_at,omitempty"`
}

// Programmed identifies the one Herdr event currently represented in pmset.
type Programmed struct {
	Target
	WakeAt       time.Time `json:"wake_at"`
	ProgrammedAt time.Time `json:"programmed_at"`
	Owner        string    `json:"owner"`
	EventType    string    `json:"event_type"`
}

// Verification describes direct state-and-pmset read-back evidence.
type Verification struct {
	Target
	RequestedWakeAt  time.Time `json:"requested_wake_at"`
	ProgrammedWakeAt time.Time `json:"programmed_wake_at"`
	VerifiedAt       time.Time `json:"verified_at"`
	Matched          bool      `json:"matched"`
}

// ValidationError is safe to return across the privilege boundary.
type ValidationError struct {
	Code    Code
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	buildPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)
)

// Validate checks a request against the daemon's clock before privileged code
// can inspect state or invoke pmset.
func (r Request) Validate(now time.Time) error {
	now = now.UTC()
	if r.ProtocolVersion != Version {
		return invalid(CodeIncompatibleProtocol, "protocol version must be 1")
	}
	if err := boundedIdentifier("request_id", r.RequestID, MaxRequestIDLength); err != nil {
		return err
	}
	if r.HelperBuild == "" || len(r.HelperBuild) > MaxBuildVersionLength || !buildPattern.MatchString(r.HelperBuild) {
		return invalid(CodeInvalidRequest, "helper_build is missing or malformed")
	}

	switch r.Operation {
	case OperationHealth, OperationList:
		if r.IdempotencyKey != "" || r.Candidate != nil || r.Target != nil {
			return invalid(CodeInvalidRequest, string(r.Operation)+" does not accept an operation payload")
		}
	case OperationRegisterOrReplace:
		if err := validateIdempotencyKey(r.IdempotencyKey); err != nil {
			return err
		}
		if r.Candidate == nil || r.Target != nil {
			return invalid(CodeInvalidRequest, "register_or_replace requires exactly one candidate")
		}
		if err := r.Candidate.Validate(now); err != nil {
			return err
		}
	case OperationCancel:
		if err := validateIdempotencyKey(r.IdempotencyKey); err != nil {
			return err
		}
		if r.Target == nil || r.Candidate != nil {
			return invalid(CodeInvalidRequest, "cancel requires exactly one target")
		}
		if err := r.Target.Validate(); err != nil {
			return err
		}
	case OperationVerify:
		if r.IdempotencyKey != "" {
			return invalid(CodeInvalidRequest, "verify does not accept an idempotency key")
		}
		if r.Target == nil || r.Candidate != nil {
			return invalid(CodeInvalidRequest, "verify requires exactly one target")
		}
		if err := r.Target.Validate(); err != nil {
			return err
		}
	default:
		return invalid(CodeInvalidRequest, "operation is missing or unsupported")
	}
	return nil
}

// Validate checks candidate identity, purpose, absolute times, and safe reason.
func (c Candidate) Validate(now time.Time) error {
	if err := boundedIdentifier("candidate id", c.ID, MaxCandidateIDLength); err != nil {
		return err
	}
	if err := validateSourcePurpose(c.Source, c.Purpose); err != nil {
		return err
	}
	if c.WakeAt.IsZero() || !c.WakeAt.After(now) {
		return invalid(CodeInvalidRequest, "wake_at must be an absolute future timestamp")
	}
	if c.WakeAt.After(now.Add(MaxWakeHorizon)) {
		return invalid(CodeInvalidRequest, "wake_at exceeds the 168-hour horizon")
	}
	if c.ExpiresAt.IsZero() || !c.ExpiresAt.After(c.WakeAt) {
		return invalid(CodeInvalidRequest, "expires_at must be after wake_at")
	}
	if c.ExpiresAt.After(c.WakeAt.Add(MaxExpiryAfterWake)) {
		return invalid(CodeInvalidRequest, "expires_at is too far after wake_at")
	}
	if err := validateSafeText("reason", c.Reason, MaxReasonLength); err != nil {
		return err
	}
	return nil
}

// Validate checks an exact candidate reference.
func (t Target) Validate() error {
	if err := boundedIdentifier("candidate id", t.ID, MaxCandidateIDLength); err != nil {
		return err
	}
	return validateSourcePurpose(t.Source, t.Purpose)
}

// Validate checks that a response itself stays inside the v1 contract.
func (r Response) Validate() error {
	if r.ProtocolVersion != Version {
		return invalid(CodeIncompatibleProtocol, "response protocol version must be 1")
	}
	if err := boundedIdentifier("request_id", r.RequestID, MaxRequestIDLength); err != nil {
		return err
	}
	if r.DaemonBuild == "" || len(r.DaemonBuild) > MaxBuildVersionLength || !buildPattern.MatchString(r.DaemonBuild) {
		return invalid(CodeInvalidRequest, "daemon_build is missing or malformed")
	}
	switch r.Operation {
	case OperationHealth, OperationRegisterOrReplace, OperationCancel, OperationList, OperationVerify:
	default:
		return invalid(CodeInvalidRequest, "response operation is missing or unsupported")
	}
	switch r.Result {
	case ResultSuccess, ResultRefusal, ResultUncertain:
	default:
		return invalid(CodeInvalidRequest, "response result is missing or unsupported")
	}
	if r.Code == "" {
		return invalid(CodeInvalidRequest, "response code is required")
	}
	return validateSafeText("message", r.Message, MaxMessageLength)
}

func validateIdempotencyKey(value string) error {
	if err := boundedIdentifier("idempotency_key", value, MaxIdempotencyKeyLength); err != nil {
		return err
	}
	return nil
}

func boundedIdentifier(name, value string, maximum int) error {
	if value == "" || len(value) > maximum || !identifierPattern.MatchString(value) {
		return invalid(CodeInvalidRequest, name+" is missing or malformed")
	}
	return nil
}

func validateSourcePurpose(source Source, purpose Purpose) error {
	switch source {
	case SourceOvernight:
		if purpose == PurposeOvernightStart || purpose == PurposeClaudeReset {
			return nil
		}
	case SourceContinuation:
		if purpose == PurposeContinuation {
			return nil
		}
	}
	return invalid(CodeInvalidRequest, "source and purpose are unsupported or incompatible")
}

func validateSafeText(name, value string, maximum int) error {
	if len(value) > maximum || !utf8.ValidString(value) {
		return invalid(CodeInvalidRequest, name+" is malformed or too long")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return invalid(CodeInvalidRequest, name+" contains control characters")
		}
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"password", "authorization:", "bearer ", "api_key", "apikey", "secret=", "token="} {
		if strings.Contains(lower, forbidden) {
			return invalid(CodeInvalidRequest, name+" contains forbidden sensitive material")
		}
	}
	return nil
}

func invalid(code Code, message string) error {
	return &ValidationError{Code: code, Message: message}
}

// ErrorCode extracts a stable protocol code from a validation failure.
func ErrorCode(err error) Code {
	var validation *ValidationError
	if errors.As(err, &validation) {
		return validation.Code
	}
	return CodeInvalidRequest
}

// MutationDigest returns a stable digest of the privileged meaning of one
// state-changing request. Request IDs and build strings are intentionally
// excluded so a lost response can be retried with the same idempotency key.
func MutationDigest(request Request) (string, error) {
	switch request.Operation {
	case OperationRegisterOrReplace, OperationCancel:
	default:
		return "", invalid(CodeInvalidRequest, "only state-changing requests have a mutation digest")
	}
	meaning := struct {
		Operation Operation  `json:"operation"`
		Candidate *Candidate `json:"candidate,omitempty"`
		Target    *Target    `json:"target,omitempty"`
	}{
		Operation: request.Operation,
		Candidate: request.Candidate,
		Target:    request.Target,
	}
	payload, err := json.Marshal(meaning)
	if err != nil {
		return "", fmt.Errorf("encode mutation meaning: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// ConflictingMutation reports whether two requests reuse one idempotency key
// for different privileged meanings.
func ConflictingMutation(previous, current Request) (bool, error) {
	if previous.IdempotencyKey == "" || current.IdempotencyKey == "" ||
		previous.IdempotencyKey != current.IdempotencyKey {
		return false, nil
	}
	previousDigest, err := MutationDigest(previous)
	if err != nil {
		return false, err
	}
	currentDigest, err := MutationDigest(current)
	if err != nil {
		return false, err
	}
	return previousDigest != currentDigest, nil
}

// ReadRequest reads one length-prefixed request and rejects oversize,
// malformed, unknown-field, or trailing data before returning it.
func ReadRequest(reader io.Reader) (Request, error) {
	var request Request
	if err := readFrame(reader, MaxRequestBytes, &request); err != nil {
		return Request{}, err
	}
	return request, nil
}

// WriteRequest writes one bounded length-prefixed request.
func WriteRequest(writer io.Writer, request Request) error {
	return writeFrame(writer, MaxRequestBytes, request)
}

// ReadResponse reads one bounded length-prefixed response.
func ReadResponse(reader io.Reader) (Response, error) {
	var response Response
	if err := readFrame(reader, MaxResponseBytes, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

// WriteResponse writes one bounded length-prefixed response.
func WriteResponse(writer io.Writer, response Response) error {
	return writeFrame(writer, MaxResponseBytes, response)
}

func readFrame(reader io.Reader, maximum int, destination any) error {
	buffered := bufio.NewReader(reader)
	var header [4]byte
	if _, err := io.ReadFull(buffered, header[:]); err != nil {
		return invalid(CodeInvalidRequest, "request frame header is incomplete")
	}
	size := int(binary.BigEndian.Uint32(header[:]))
	if size <= 0 {
		return invalid(CodeInvalidRequest, "request frame is empty")
	}
	if size > maximum {
		return invalid(CodePayloadTooLarge, fmt.Sprintf("payload exceeds %d bytes", maximum))
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(buffered, payload); err != nil {
		return invalid(CodeInvalidRequest, "request frame payload is incomplete")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid(CodeInvalidRequest, "request JSON is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return invalid(CodeInvalidRequest, "request JSON contains trailing data")
	}
	return nil
}

func writeFrame(writer io.Writer, maximum int, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode protocol frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > maximum {
		return invalid(CodePayloadTooLarge, fmt.Sprintf("payload exceeds %d bytes", maximum))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return fmt.Errorf("write protocol frame header: %w", err)
	}
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("write protocol frame payload: %w", err)
	}
	return nil
}
