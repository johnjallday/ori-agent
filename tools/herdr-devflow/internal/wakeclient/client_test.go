package wakeclient

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

var clientNow = time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)

type scriptedDaemon struct {
	requests []wakeprotocol.Request
	handle   func(wakeprotocol.Request) wakeprotocol.Response
}

func (d *scriptedDaemon) dial(context.Context, string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		request, err := wakeprotocol.ReadRequest(server)
		if err != nil {
			return
		}
		d.requests = append(d.requests, request)
		response := d.handle(request)
		response.ProtocolVersion = wakeprotocol.Version
		response.RequestID = request.RequestID
		response.DaemonBuild = "daemon-2"
		response.Operation = request.Operation
		_ = wakeprotocol.WriteResponse(server, response)
	}()
	return client, nil
}

func newClientForTest(daemon *scriptedDaemon) *Client {
	client := NewForPurpose(
		"/test/wake.sock",
		wakeprotocol.SourceContinuation,
		wakeprotocol.PurposeContinuation,
	)
	client.BuildVersion = "helper-1"
	client.Now = func() time.Time { return clientNow }
	client.Timeout = time.Second
	client.Dial = daemon.dial
	return client
}

func TestClientHealthRegisterVerifyListAndExactCancel(t *testing.T) {
	t.Parallel()
	wakeAt := clientNow.Add(time.Hour)
	target := wakeprotocol.Target{
		ID: "sch-1", Source: wakeprotocol.SourceContinuation,
		Purpose: wakeprotocol.PurposeContinuation,
	}
	daemon := &scriptedDaemon{}
	daemon.handle = func(request wakeprotocol.Request) wakeprotocol.Response {
		response := wakeprotocol.Response{Result: wakeprotocol.ResultSuccess, Code: wakeprotocol.CodeOK}
		switch request.Operation {
		case wakeprotocol.OperationHealth:
			response.Health = &wakeprotocol.Health{
				Installed: true, Running: true, ProtocolVersion: wakeprotocol.Version,
				StateVersion: wakeprotocol.StateVersion, DaemonBuild: "daemon-2", AllowedUID: 501,
			}
		case wakeprotocol.OperationRegisterOrReplace:
			if request.Candidate == nil || request.Candidate.ID != target.ID {
				t.Fatalf("register request = %+v", request)
			}
		case wakeprotocol.OperationVerify:
			response.Verification = &wakeprotocol.Verification{
				Target: target, RequestedWakeAt: wakeAt, ProgrammedWakeAt: wakeAt,
				VerifiedAt: clientNow.Add(time.Second), Matched: true,
			}
		case wakeprotocol.OperationList:
			response.State = &wakeprotocol.State{
				StateVersion: wakeprotocol.StateVersion, AllowedUID: 501,
				Candidates: []wakeprotocol.Candidate{},
			}
		}
		return response
	}
	client := newClientForTest(daemon)
	health, err := client.Health(context.Background())
	if err != nil || health.AllowedUID != 501 {
		t.Fatalf("Health() = %+v, %v", health, err)
	}
	registered, err := client.RegisterCandidate(context.Background(), target.ID, wakeAt, "continuation")
	if err != nil || registered.DaemonBuild != "daemon-2" ||
		registered.ProtocolVersion != wakeprotocol.Version {
		t.Fatalf("RegisterCandidate() = %+v, %v", registered, err)
	}
	verified, err := client.VerifyCandidate(context.Background(), target.ID, wakeAt)
	if err != nil || !verified.ProgrammedAt.Equal(wakeAt) ||
		!verified.VerifiedAt.Equal(clientNow.Add(time.Second)) {
		t.Fatalf("VerifyCandidate() = %+v, %v", verified, err)
	}
	if _, err := client.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, err := client.CancelCandidate(context.Background(), target.ID)
	if err != nil || canceled.Result != wakeprotocol.ResultSuccess {
		t.Fatalf("CancelCandidate() = %+v, %v", canceled, err)
	}
	want := []wakeprotocol.Operation{
		wakeprotocol.OperationHealth,
		wakeprotocol.OperationRegisterOrReplace,
		wakeprotocol.OperationVerify,
		wakeprotocol.OperationList,
		wakeprotocol.OperationCancel,
	}
	var got []wakeprotocol.Operation
	for _, request := range daemon.requests {
		got = append(got, request.Operation)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
	if daemon.requests[1].IdempotencyKey == "" ||
		daemon.requests[4].IdempotencyKey == "" ||
		daemon.requests[1].Candidate.Source != wakeprotocol.SourceContinuation ||
		daemon.requests[1].Candidate.Purpose != wakeprotocol.PurposeContinuation {
		t.Fatalf("mutation identity = %+v / %+v", daemon.requests[1], daemon.requests[4])
	}
}

func TestVerifyRejectsWrongLateAndExcessivelyEarlyEvidence(t *testing.T) {
	t.Parallel()
	wakeAt := clientNow.Add(time.Hour)
	tests := []struct {
		name       string
		programmed time.Time
		targetID   string
	}{
		{name: "later", programmed: wakeAt.Add(time.Second), targetID: "sch-1"},
		{name: "too early", programmed: wakeAt.Add(-MaxSkew - time.Second), targetID: "sch-1"},
		{name: "wrong target", programmed: wakeAt, targetID: "sch-other"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			daemon := &scriptedDaemon{handle: func(request wakeprotocol.Request) wakeprotocol.Response {
				return wakeprotocol.Response{
					Result: wakeprotocol.ResultSuccess, Code: wakeprotocol.CodeOK,
					Verification: &wakeprotocol.Verification{
						Target: wakeprotocol.Target{
							ID: test.targetID, Source: wakeprotocol.SourceContinuation,
							Purpose: wakeprotocol.PurposeContinuation,
						},
						RequestedWakeAt: wakeAt, ProgrammedWakeAt: test.programmed,
						VerifiedAt: clientNow, Matched: test.targetID == "sch-1",
					},
				}
			}}
			client := newClientForTest(daemon)
			if _, err := client.VerifyCandidate(context.Background(), "sch-1", wakeAt); err == nil {
				t.Fatal("unsafe verification was accepted")
			}
		})
	}
}

func TestLostResponseIsUncertainAndRetryUsesSameIdempotencyKey(t *testing.T) {
	t.Parallel()
	wakeAt := clientNow.Add(time.Hour)
	var requests []wakeprotocol.Request
	client := NewForSource("/test/wake.sock", wakeprotocol.SourceContinuation)
	client.BuildVersion = "test"
	client.Now = func() time.Time { return clientNow }
	client.Timeout = 50 * time.Millisecond
	client.Dial = func(context.Context, string) (net.Conn, error) {
		caller, daemon := net.Pipe()
		go func() {
			defer daemon.Close()
			request, _ := wakeprotocol.ReadRequest(daemon)
			requests = append(requests, request)
			// Deliberately close without a response.
		}()
		return caller, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		evidence, err := client.RegisterCandidate(context.Background(), "sch-1", wakeAt, "continuation")
		if !errors.Is(err, ErrUncertain) || evidence.CandidateID != "sch-1" {
			t.Fatalf("attempt %d evidence/error = %+v, %v", attempt, evidence, err)
		}
	}
	if len(requests) != 2 || requests[0].IdempotencyKey != requests[1].IdempotencyKey {
		t.Fatalf("retry keys = %+v", requests)
	}
}

func TestRefusalAndProtocolMismatchRemainMachineReadable(t *testing.T) {
	t.Parallel()
	daemon := &scriptedDaemon{handle: func(request wakeprotocol.Request) wakeprotocol.Response {
		return wakeprotocol.Response{
			Result:  wakeprotocol.ResultRefusal,
			Code:    wakeprotocol.CodeConflict,
			Message: "an untracked Herdr-owned event exists",
		}
	}}
	client := newClientForTest(daemon)
	_, err := client.RegisterCandidate(
		context.Background(), "sch-1", clientNow.Add(time.Hour), "continuation",
	)
	var operationError *OperationError
	if !errors.As(err, &operationError) ||
		operationError.Code != wakeprotocol.CodeConflict ||
		operationError.Result != wakeprotocol.ResultRefusal {
		t.Fatalf("error = %#v", err)
	}
}

func TestUnavailableClientHasNoOriFallback(t *testing.T) {
	t.Parallel()
	client := NewForSource("/missing/standalone.sock", wakeprotocol.SourceContinuation)
	client.BuildVersion = "test"
	client.Now = func() time.Time { return clientNow }
	client.Dial = func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("missing")
	}
	_, err := client.RegisterCandidate(
		context.Background(), "sch-1", clientNow.Add(time.Hour), "continuation",
	)
	if !errors.Is(err, ErrUnavailable) ||
		!stringsContains(err.Error(), "standalone") {
		t.Fatalf("error = %v", err)
	}
}

func stringsContains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
