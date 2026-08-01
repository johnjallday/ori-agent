// Package wakeclient is the unprivileged helper side of the standalone Herdr
// Wake Service protocol. It has no Ori process, settings, heartbeat, API, or
// shared-state fallback.
package wakeclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
)

const (
	DefaultTimeout = 5 * time.Second
	MaxSkew        = 30 * time.Minute
)

var (
	ErrUnavailable   = errors.New("standalone Herdr wake service is unavailable")
	ErrNotProgrammed = errors.New("the requested Herdr wake is not programmed")
	ErrUncertain     = errors.New("the standalone wake result is uncertain")
)

// OperationError retains the daemon's machine-readable result without
// exposing arbitrary process or environment output.
type OperationError struct {
	Operation wakeprotocol.Operation
	Result    wakeprotocol.Result
	Code      wakeprotocol.Code
	Message   string
	Cause     error
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return string(e.Operation) + ": " + e.Message
	}
	return string(e.Operation) + ": standalone wake operation failed"
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Cause != nil {
		return e.Cause
	}
	if e.Result == wakeprotocol.ResultUncertain {
		return ErrUncertain
	}
	return ErrUnavailable
}

// Evidence is the durable unprivileged proof returned to schedule/run state.
type Evidence struct {
	Source          wakeprotocol.Source
	Purpose         wakeprotocol.Purpose
	CandidateID     string
	RequestedAt     time.Time
	ProgrammedAt    time.Time
	VerifiedAt      time.Time
	ProtocolVersion int
	DaemonBuild     string
	HelperBuild     string
	Result          wakeprotocol.Result
	Code            wakeprotocol.Code
	Message         string
}

// OwnerReadiness describes the standalone daemon, not an Ori heartbeat.
type OwnerReadiness struct {
	Installed       bool
	Running         bool
	Ready           bool
	Compatible      bool
	AllowedUID      int
	ProtocolVersion int
	StateVersion    int
	DaemonBuild     string
	LastSelfTestAt  time.Time
	Detail          string
}

// Client sends one bounded request per authenticated Unix-socket connection.
type Client struct {
	SocketPath   string
	BuildVersion string
	Source       wakeprotocol.Source
	Purpose      wakeprotocol.Purpose
	Now          func() time.Time
	Timeout      time.Duration
	Dial         func(context.Context, string) (net.Conn, error)
	defaultsOnce sync.Once
	sequence     atomic.Uint64
}

// New builds an Overnight reset client over an explicit socket path.
func New(socketPath string) *Client {
	return NewForPurpose(
		socketPath,
		wakeprotocol.SourceOvernight,
		wakeprotocol.PurposeClaudeReset,
	)
}

// NewForSource preserves the source-scoped constructor while deriving the only
// valid v1 purpose for that source.
func NewForSource(socketPath string, source wakeprotocol.Source) *Client {
	purpose := wakeprotocol.PurposeClaudeReset
	if source == wakeprotocol.SourceContinuation {
		purpose = wakeprotocol.PurposeContinuation
	}
	return NewForPurpose(socketPath, source, purpose)
}

// NewForPurpose builds a client for one fixed source/purpose pair.
func NewForPurpose(
	socketPath string,
	source wakeprotocol.Source,
	purpose wakeprotocol.Purpose,
) *Client {
	return &Client{
		SocketPath: socketPath,
		Source:     source,
		Purpose:    purpose,
	}
}

func Default() (*Client, error) {
	return DefaultForPurpose(
		wakeprotocol.SourceOvernight,
		wakeprotocol.PurposeClaudeReset,
	)
}

func DefaultForSource(source wakeprotocol.Source) (*Client, error) {
	purpose := wakeprotocol.PurposeClaudeReset
	if source == wakeprotocol.SourceContinuation {
		purpose = wakeprotocol.PurposeContinuation
	}
	return DefaultForPurpose(source, purpose)
}

func DefaultForPurpose(
	source wakeprotocol.Source,
	purpose wakeprotocol.Purpose,
) (*Client, error) {
	return NewForPurpose(wakeservice.SocketPath, source, purpose), nil
}

func (c *Client) defaults() {
	c.defaultsOnce.Do(func() {
		if c.SocketPath == "" {
			c.SocketPath = wakeservice.SocketPath
		}
		if c.BuildVersion == "" {
			c.BuildVersion = "dev"
		}
		if c.Source == "" {
			c.Source = wakeprotocol.SourceOvernight
		}
		if c.Purpose == "" {
			c.Purpose = wakeprotocol.PurposeClaudeReset
		}
		if c.Now == nil {
			c.Now = func() time.Time { return time.Now().UTC() }
		}
		if c.Timeout <= 0 {
			c.Timeout = DefaultTimeout
		}
		if c.Dial == nil {
			c.Dial = func(ctx context.Context, path string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: c.Timeout}
				return dialer.DialContext(ctx, "unix", path)
			}
		}
	})
}

func (c *Client) Health(ctx context.Context) (wakeprotocol.Health, error) {
	response, err := c.request(ctx, wakeprotocol.Request{
		Operation: wakeprotocol.OperationHealth,
	})
	if err != nil {
		return wakeprotocol.Health{}, err
	}
	if response.Health == nil {
		return wakeprotocol.Health{}, c.responseError(response, "health response omitted service identity")
	}
	if response.Health.ProtocolVersion != wakeprotocol.Version ||
		response.Health.StateVersion != wakeprotocol.StateVersion {
		return wakeprotocol.Health{}, &OperationError{
			Operation: wakeprotocol.OperationHealth,
			Result:    wakeprotocol.ResultRefusal,
			Code:      wakeprotocol.CodeIncompatibleProtocol,
			Message:   "daemon protocol or state version is incompatible; run wt herd wake install",
		}
	}
	return *response.Health, nil
}

// RegisterCandidate persists and programs the exact typed candidate.
func (c *Client) RegisterCandidate(
	ctx context.Context,
	id string,
	wakeAt time.Time,
	detail string,
) (Evidence, error) {
	c.defaults()
	wakeAt = wakeAt.UTC()
	candidate := wakeprotocol.Candidate{
		ID:        id,
		Source:    c.Source,
		Purpose:   c.Purpose,
		WakeAt:    wakeAt,
		ExpiresAt: wakeAt.Add(time.Hour),
		Reason:    detail,
	}
	request := wakeprotocol.Request{
		Operation:      wakeprotocol.OperationRegisterOrReplace,
		IdempotencyKey: mutationKey("register", candidateIdentity(candidate), wakeAt),
		Candidate:      &candidate,
	}
	response, err := c.request(ctx, request)
	evidence := c.evidence(id, wakeAt, response)
	if err != nil {
		return evidence, err
	}
	if response.Result != wakeprotocol.ResultSuccess {
		return evidence, c.responseError(response, "wake candidate was not accepted")
	}
	return evidence, nil
}

// VerifyCandidate requires direct daemon pmset read-back evidence.
func (c *Client) VerifyCandidate(
	ctx context.Context,
	id string,
	wakeAt time.Time,
) (Evidence, error) {
	c.defaults()
	target := &wakeprotocol.Target{
		ID: id, Source: c.Source, Purpose: c.Purpose,
	}
	response, err := c.request(ctx, wakeprotocol.Request{
		Operation: wakeprotocol.OperationVerify,
		Target:    target,
	})
	evidence := c.evidence(id, wakeAt.UTC(), response)
	if err != nil {
		return evidence, err
	}
	if response.Result != wakeprotocol.ResultSuccess ||
		response.Verification == nil ||
		!response.Verification.Matched ||
		response.Verification.ID != id ||
		response.Verification.Source != c.Source ||
		response.Verification.Purpose != c.Purpose {
		return evidence, c.responseError(response, "matching fixed-owner pmset event was not verified")
	}
	programmed := response.Verification.ProgrammedWakeAt.UTC()
	if programmed.After(wakeAt.UTC()) ||
		programmed.Before(wakeAt.UTC().Add(-MaxSkew)) {
		return evidence, &OperationError{
			Operation: wakeprotocol.OperationVerify,
			Result:    wakeprotocol.ResultRefusal,
			Code:      wakeprotocol.CodeVerificationFailed,
			Message:   "programmed wake is outside the safe lead-time window",
			Cause:     ErrNotProgrammed,
		}
	}
	evidence.RequestedAt = response.Verification.RequestedWakeAt.UTC()
	evidence.ProgrammedAt = programmed
	evidence.VerifiedAt = response.Verification.VerifiedAt.UTC()
	return evidence, nil
}

// CancelCandidate withdraws only the exact typed candidate.
func (c *Client) CancelCandidate(ctx context.Context, id string) (Evidence, error) {
	c.defaults()
	target := &wakeprotocol.Target{
		ID: id, Source: c.Source, Purpose: c.Purpose,
	}
	response, err := c.request(ctx, wakeprotocol.Request{
		Operation:      wakeprotocol.OperationCancel,
		IdempotencyKey: mutationKey("cancel", candidateIdentityTarget(*target), time.Time{}),
		Target:         target,
	})
	evidence := c.evidence(id, time.Time{}, response)
	if err != nil {
		return evidence, err
	}
	if response.Result != wakeprotocol.ResultSuccess {
		return evidence, c.responseError(response, "wake candidate cancellation was not confirmed")
	}
	evidence.VerifiedAt = c.Now().UTC()
	return evidence, nil
}

func (c *Client) List(ctx context.Context) (wakeprotocol.State, error) {
	response, err := c.request(ctx, wakeprotocol.Request{Operation: wakeprotocol.OperationList})
	if err != nil {
		return wakeprotocol.State{}, err
	}
	if response.Result != wakeprotocol.ResultSuccess || response.State == nil {
		return wakeprotocol.State{}, c.responseError(response, "wake state was not returned")
	}
	return *response.State, nil
}

// Legacy-shaped methods keep Overnight call sites buildable while routing all
// production operations through the standalone daemon.
func (c *Client) Register(id string, wakeAt time.Time, detail string) error {
	_, err := c.RegisterCandidate(context.Background(), id, wakeAt, detail)
	return err
}

func (c *Client) Verify(ctx context.Context, id string, wakeAt time.Time) (time.Time, error) {
	evidence, err := c.VerifyCandidate(ctx, id, wakeAt)
	return evidence.ProgrammedAt, err
}

func (c *Client) Cancel(id string) error {
	_, err := c.CancelCandidate(context.Background(), id)
	return err
}

func (c *Client) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
	defer cancel()
	_, err := c.List(ctx)
	return err == nil
}

func (c *Client) Owner() OwnerReadiness {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
	defer cancel()
	health, err := c.Health(ctx)
	if err != nil {
		return OwnerReadiness{
			Detail: "standalone Herdr wake service is not healthy; run wt herd wake doctor",
		}
	}
	return OwnerReadiness{
		Installed:       health.Installed,
		Running:         health.Running,
		Ready:           health.Installed && health.Running,
		Compatible:      true,
		AllowedUID:      health.AllowedUID,
		ProtocolVersion: health.ProtocolVersion,
		StateVersion:    health.StateVersion,
		DaemonBuild:     health.DaemonBuild,
		LastSelfTestAt:  health.LastSelfTestAt,
		Detail:          "standalone Herdr wake service is healthy",
	}
}

func (c *Client) request(
	ctx context.Context,
	partial wakeprotocol.Request,
) (wakeprotocol.Response, error) {
	c.defaults()
	callContext, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	requestID := "wake-" + strconv.FormatUint(c.sequence.Add(1), 10)
	partial.ProtocolVersion = wakeprotocol.Version
	partial.RequestID = requestID
	partial.HelperBuild = c.BuildVersion
	if err := partial.Validate(c.Now().UTC()); err != nil {
		return wakeprotocol.Response{}, err
	}
	connection, err := c.Dial(callContext, c.SocketPath)
	if err != nil {
		return wakeprotocol.Response{}, &OperationError{
			Operation: partial.Operation,
			Result:    wakeprotocol.ResultUncertain,
			Code:      wakeprotocol.CodeNotInstalled,
			Message:   "standalone Herdr wake service could not be reached; run wt herd wake doctor",
			Cause:     ErrUnavailable,
		}
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(c.Timeout))
	if err := wakeprotocol.WriteRequest(connection, partial); err != nil {
		return wakeprotocol.Response{}, &OperationError{
			Operation: partial.Operation,
			Result:    wakeprotocol.ResultUncertain,
			Code:      wakeprotocol.CodeUncertain,
			Message:   "wake request could not be sent completely",
			Cause:     ErrUncertain,
		}
	}
	response, err := wakeprotocol.ReadResponse(connection)
	if err != nil {
		return wakeprotocol.Response{}, &OperationError{
			Operation: partial.Operation,
			Result:    wakeprotocol.ResultUncertain,
			Code:      wakeprotocol.CodeUncertain,
			Message:   "wake daemon response was lost; inspect with wt herd wake doctor",
			Cause:     ErrUncertain,
		}
	}
	if err := response.Validate(); err != nil {
		return response, &OperationError{
			Operation: partial.Operation,
			Result:    wakeprotocol.ResultUncertain,
			Code:      wakeprotocol.CodeIncompatibleProtocol,
			Message:   "wake daemon returned an invalid or incompatible response",
			Cause:     ErrUncertain,
		}
	}
	if response.RequestID != requestID || response.Operation != partial.Operation {
		return response, &OperationError{
			Operation: partial.Operation,
			Result:    wakeprotocol.ResultUncertain,
			Code:      wakeprotocol.CodeVerificationFailed,
			Message:   "wake daemon response identity did not match the request",
			Cause:     ErrUncertain,
		}
	}
	if response.Result == wakeprotocol.ResultUncertain {
		return response, c.responseError(response, "wake daemon reported an uncertain result")
	}
	return response, nil
}

func (c *Client) responseError(
	response wakeprotocol.Response,
	fallback string,
) error {
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = fallback
	}
	cause := ErrUnavailable
	if response.Result == wakeprotocol.ResultUncertain {
		cause = ErrUncertain
	} else if response.Code == wakeprotocol.CodeNotFound ||
		response.Code == wakeprotocol.CodeVerificationFailed {
		cause = ErrNotProgrammed
	}
	return &OperationError{
		Operation: response.Operation,
		Result:    response.Result,
		Code:      response.Code,
		Message:   message,
		Cause:     cause,
	}
}

func (c *Client) evidence(
	id string,
	requested time.Time,
	response wakeprotocol.Response,
) Evidence {
	return Evidence{
		Source:          c.Source,
		Purpose:         c.Purpose,
		CandidateID:     id,
		RequestedAt:     requested.UTC(),
		ProtocolVersion: response.ProtocolVersion,
		DaemonBuild:     response.DaemonBuild,
		HelperBuild:     c.BuildVersion,
		Result:          response.Result,
		Code:            response.Code,
		Message:         response.Message,
	}
}

func (c *Client) timeout() time.Duration {
	c.defaults()
	return c.Timeout
}

func candidateIdentity(candidate wakeprotocol.Candidate) string {
	return candidateIdentityTarget(wakeprotocol.Target{
		ID: candidate.ID, Source: candidate.Source, Purpose: candidate.Purpose,
	})
}

func candidateIdentityTarget(target wakeprotocol.Target) string {
	return string(target.Source) + ":" + string(target.Purpose) + ":" + target.ID
}

func mutationKey(operation, identity string, at time.Time) string {
	material := operation + "\x00" + identity + "\x00" + at.UTC().Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(material))
	return operation + "-" + fmt.Sprintf("%x", digest[:12])
}
