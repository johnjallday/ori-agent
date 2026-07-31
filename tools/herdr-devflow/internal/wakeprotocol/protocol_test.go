package wakeprotocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRequestValidationAcceptsEveryTypedOperation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	target := Target{ID: "ovr-123", Source: SourceOvernight, Purpose: PurposeClaudeReset}
	candidate := Candidate{
		ID:        target.ID,
		Source:    target.Source,
		Purpose:   target.Purpose,
		WakeAt:    now.Add(5 * time.Hour),
		ExpiresAt: now.Add(6 * time.Hour),
		Reason:    "verified Claude reset",
	}
	requests := []Request{
		{ProtocolVersion: Version, RequestID: "req-health", HelperBuild: "dev", Operation: OperationHealth},
		{ProtocolVersion: Version, RequestID: "req-list", HelperBuild: "1.0.0", Operation: OperationList},
		{ProtocolVersion: Version, RequestID: "req-register", HelperBuild: "dev+abc", Operation: OperationRegisterOrReplace, IdempotencyKey: "idem-register", Candidate: &candidate},
		{ProtocolVersion: Version, RequestID: "req-cancel", HelperBuild: "dev", Operation: OperationCancel, IdempotencyKey: "idem-cancel", Target: &target},
		{ProtocolVersion: Version, RequestID: "req-verify", HelperBuild: "dev", Operation: OperationVerify, Target: &target},
	}
	for _, request := range requests {
		request := request
		t.Run(string(request.Operation), func(t *testing.T) {
			t.Parallel()
			if err := request.Validate(now); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestRequestValidationRejectsBeforePrivilegedExecution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	valid := validRegister(now)
	cases := []struct {
		name   string
		mutate func(*Request)
		code   Code
	}{
		{"missing version", func(r *Request) { r.ProtocolVersion = 0 }, CodeIncompatibleProtocol},
		{"newer version", func(r *Request) { r.ProtocolVersion = 2 }, CodeIncompatibleProtocol},
		{"missing request id", func(r *Request) { r.RequestID = "" }, CodeInvalidRequest},
		{"malformed request id", func(r *Request) { r.RequestID = "../request" }, CodeInvalidRequest},
		{"oversized request id", func(r *Request) { r.RequestID = strings.Repeat("a", MaxRequestIDLength+1) }, CodeInvalidRequest},
		{"missing build", func(r *Request) { r.HelperBuild = "" }, CodeInvalidRequest},
		{"malformed build", func(r *Request) { r.HelperBuild = "dev build" }, CodeInvalidRequest},
		{"unsupported operation", func(r *Request) { r.Operation = "run_command" }, CodeInvalidRequest},
		{"missing idempotency key", func(r *Request) { r.IdempotencyKey = "" }, CodeInvalidRequest},
		{"malformed idempotency key", func(r *Request) { r.IdempotencyKey = "key with spaces" }, CodeInvalidRequest},
		{"missing candidate", func(r *Request) { r.Candidate = nil }, CodeInvalidRequest},
		{"candidate and target", func(r *Request) {
			target := Target{ID: "ovr-123", Source: SourceOvernight, Purpose: PurposeClaudeReset}
			r.Target = &target
		}, CodeInvalidRequest},
		{"missing candidate id", func(r *Request) { r.Candidate.ID = "" }, CodeInvalidRequest},
		{"malformed candidate id", func(r *Request) { r.Candidate.ID = "../../etc" }, CodeInvalidRequest},
		{"past wake", func(r *Request) {
			r.Candidate.WakeAt = now.Add(-time.Second)
			r.Candidate.ExpiresAt = now.Add(time.Hour)
		}, CodeInvalidRequest},
		{"wake at current instant", func(r *Request) { r.Candidate.WakeAt = now; r.Candidate.ExpiresAt = now.Add(time.Hour) }, CodeInvalidRequest},
		{"excessively distant wake", func(r *Request) {
			r.Candidate.WakeAt = now.Add(MaxWakeHorizon + time.Nanosecond)
			r.Candidate.ExpiresAt = r.Candidate.WakeAt.Add(time.Hour)
		}, CodeInvalidRequest},
		{"missing expiration", func(r *Request) { r.Candidate.ExpiresAt = time.Time{} }, CodeInvalidRequest},
		{"expired before wake", func(r *Request) { r.Candidate.ExpiresAt = r.Candidate.WakeAt }, CodeInvalidRequest},
		{"expiration too distant", func(r *Request) { r.Candidate.ExpiresAt = r.Candidate.WakeAt.Add(MaxExpiryAfterWake + time.Nanosecond) }, CodeInvalidRequest},
		{"unsupported source", func(r *Request) { r.Candidate.Source = "workspace-task" }, CodeInvalidRequest},
		{"unsupported purpose", func(r *Request) { r.Candidate.Purpose = "arbitrary" }, CodeInvalidRequest},
		{"mismatched source and purpose", func(r *Request) { r.Candidate.Source = SourceContinuation }, CodeInvalidRequest},
		{"oversized reason", func(r *Request) { r.Candidate.Reason = strings.Repeat("x", MaxReasonLength+1) }, CodeInvalidRequest},
		{"multiline reason", func(r *Request) { r.Candidate.Reason = "safe\ncommand" }, CodeInvalidRequest},
		{"sensitive reason", func(r *Request) { r.Candidate.Reason = "token=do-not-store" }, CodeInvalidRequest},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			request := cloneRequest(t, valid)
			testCase.mutate(&request)
			err := request.Validate(now)
			if err == nil {
				t.Fatal("Validate() accepted an unsafe request")
			}
			if got := ErrorCode(err); got != testCase.code {
				t.Fatalf("ErrorCode(%v) = %q, want %q", err, got, testCase.code)
			}
		})
	}
}

func TestMaximumWakeHorizonBoundaryIsExact(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	request := validRegister(now)
	request.Candidate.WakeAt = now.Add(MaxWakeHorizon)
	request.Candidate.ExpiresAt = request.Candidate.WakeAt.Add(time.Hour)
	if err := request.Validate(now); err != nil {
		t.Fatalf("the exact maximum horizon was rejected: %v", err)
	}
}

func TestFramingRejectsOversizeMalformedUnknownAndTrailingPayloads(t *testing.T) {
	t.Parallel()

	var oversize bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxRequestBytes+1)
	oversize.Write(header[:])
	if _, err := ReadRequest(&oversize); ErrorCode(err) != CodePayloadTooLarge {
		t.Fatalf("oversize error = %v, want payload_too_large", err)
	}

	for name, payload := range map[string][]byte{
		"malformed":     []byte(`{"protocol_version":`),
		"unknown field": []byte(`{"protocol_version":1,"request_id":"req","helper_build":"dev","operation":"health","command":"whoami"}`),
		"trailing JSON": []byte(`{"protocol_version":1,"request_id":"req","helper_build":"dev","operation":"health"} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			var frame bytes.Buffer
			binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
			frame.Write(header[:])
			frame.Write(payload)
			if _, err := ReadRequest(&frame); err == nil || ErrorCode(err) != CodeInvalidRequest {
				t.Fatalf("ReadRequest() error = %v, want invalid_request", err)
			}
		})
	}
}

func TestRequestAndResponseFramesRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	request := validRegister(now)
	var requestFrame bytes.Buffer
	if err := WriteRequest(&requestFrame, request); err != nil {
		t.Fatal(err)
	}
	decodedRequest, err := ReadRequest(&requestFrame)
	if err != nil {
		t.Fatal(err)
	}
	if decodedRequest.RequestID != request.RequestID || !decodedRequest.Candidate.WakeAt.Equal(request.Candidate.WakeAt) {
		t.Fatalf("decoded request = %+v, want %+v", decodedRequest, request)
	}

	response := Response{
		ProtocolVersion: Version,
		RequestID:       request.RequestID,
		DaemonBuild:     "dev",
		Operation:       request.Operation,
		Result:          ResultSuccess,
		Code:            CodeOK,
		Message:         "candidate accepted",
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("response Validate() error = %v", err)
	}
	var responseFrame bytes.Buffer
	if err := WriteResponse(&responseFrame, response); err != nil {
		t.Fatal(err)
	}
	decodedResponse, err := ReadResponse(&responseFrame)
	if err != nil {
		t.Fatal(err)
	}
	if decodedResponse != response {
		t.Fatalf("decoded response = %+v, want %+v", decodedResponse, response)
	}
}

func TestDuplicateIdempotencyKeysRejectConflictingMeanings(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	first := validRegister(now)

	retry := cloneRequest(t, first)
	retry.RequestID = "req-retry"
	conflict, err := ConflictingMutation(first, retry)
	if err != nil || conflict {
		t.Fatalf("identical retry conflict = %v, %v; want false", conflict, err)
	}

	changed := cloneRequest(t, first)
	changed.RequestID = "req-changed"
	changed.Candidate.WakeAt = changed.Candidate.WakeAt.Add(time.Minute)
	conflict, err = ConflictingMutation(first, changed)
	if err != nil || !conflict {
		t.Fatalf("changed retry conflict = %v, %v; want true", conflict, err)
	}

	newAction := cloneRequest(t, changed)
	newAction.IdempotencyKey = "idem-other"
	conflict, err = ConflictingMutation(first, newAction)
	if err != nil || conflict {
		t.Fatalf("different key conflict = %v, %v; want false", conflict, err)
	}
}

func validRegister(now time.Time) Request {
	return Request{
		ProtocolVersion: Version,
		RequestID:       "req-register",
		HelperBuild:     "dev",
		Operation:       OperationRegisterOrReplace,
		IdempotencyKey:  "idem-register",
		Candidate: &Candidate{
			ID:        "ovr-123",
			Source:    SourceOvernight,
			Purpose:   PurposeClaudeReset,
			WakeAt:    now.Add(5 * time.Hour),
			ExpiresAt: now.Add(6 * time.Hour),
			Reason:    "verified Claude reset",
		},
	}
}

func cloneRequest(t *testing.T, request Request) Request {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var cloned Request
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
