package wakeclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/wakecoord"
)

// These tests exercise the helper against a real shared store in a temporary
// directory. Nothing here programs a wake: the only thing that can do that is
// the owner, and in these tests the owner is whatever the test writes.

var now = time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)

func newClient(t *testing.T) (*Client, *wakecoord.Store) {
	t.Helper()
	dir := t.TempDir()
	client := New(dir)
	client.Now = func() time.Time { return now }
	client.VerifyTimeout = 10 * time.Millisecond
	client.VerifyInterval = time.Millisecond
	return client, wakecoord.New(dir)
}

// TestVerifyFailsUntilTheOwnerProgramsTheWake is the property the sleep
// sequence depends on: registering is a request, not a wake.
func TestVerifyFailsUntilTheOwnerProgramsTheWake(t *testing.T) {
	client, store := newClient(t)
	wakeAt := now.Add(time.Hour)

	if err := client.Register("ovr-1", wakeAt, "Claude reset"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := client.Verify(context.Background(), "ovr-1", wakeAt); !errors.Is(err, ErrNotProgrammed) {
		t.Fatalf("Verify before the owner acted = %v, want ErrNotProgrammed", err)
	}

	// Now the owner does its job.
	if err := store.RecordProgrammed(wakecoord.Programmed{
		CandidateID: "ovr-1", Source: wakecoord.SourceOvernightRun, WakeAt: wakeAt,
	}, now); err != nil {
		t.Fatal(err)
	}
	programmed, err := client.Verify(context.Background(), "ovr-1", wakeAt)
	if err != nil {
		t.Fatalf("Verify after the owner acted: %v", err)
	}
	if !programmed.Equal(wakeAt.UTC()) {
		t.Fatalf("programmed = %v, want %v", programmed, wakeAt)
	}
}

// TestVerifyRejectsAWakeForSomebodyElse stops one run from mistaking another
// subsystem's wake for its own.
func TestVerifyRejectsAWakeForSomebodyElse(t *testing.T) {
	client, store := newClient(t)
	wakeAt := now.Add(time.Hour)

	for _, programmed := range []wakecoord.Programmed{
		{CandidateID: "ovr-2", Source: wakecoord.SourceOvernightRun, WakeAt: wakeAt},
		{CandidateID: "ovr-1", Source: wakecoord.SourceWorkspaceTask, WakeAt: wakeAt},
	} {
		if err := store.RecordProgrammed(programmed, now); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Verify(context.Background(), "ovr-1", wakeAt); err == nil {
			t.Fatalf("verified against a wake belonging to %+v", programmed)
		}
	}
}

// TestVerifyRejectsAWakeProgrammedTooLate covers the case that would leave the
// machine asleep through the reset.
func TestVerifyRejectsAWakeProgrammedTooLate(t *testing.T) {
	client, store := newClient(t)
	wakeAt := now.Add(time.Hour)

	if err := store.RecordProgrammed(wakecoord.Programmed{
		CandidateID: "ovr-1", Source: wakecoord.SourceOvernightRun, WakeAt: wakeAt.Add(time.Minute),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Verify(context.Background(), "ovr-1", wakeAt); err == nil {
		t.Fatal("a wake programmed after the requested time was accepted")
	}
}

// TestVerifyAcceptsAnEarlierWake covers the normal case: the owner applies its
// own lead time, so the machine wakes a little sooner than asked.
func TestVerifyAcceptsAnEarlierWake(t *testing.T) {
	client, store := newClient(t)
	wakeAt := now.Add(time.Hour)

	if err := store.RecordProgrammed(wakecoord.Programmed{
		CandidateID: "ovr-1", Source: wakecoord.SourceOvernightRun, WakeAt: wakeAt.Add(-5 * time.Minute),
	}, now); err != nil {
		t.Fatal(err)
	}
	programmed, err := client.Verify(context.Background(), "ovr-1", wakeAt)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !programmed.Before(wakeAt) {
		t.Fatalf("programmed = %v, want the owner's earlier wake", programmed)
	}
}

func TestCancelWithdrawsOnlyThisRunsCandidate(t *testing.T) {
	client, store := newClient(t)
	if err := client.Register("ovr-1", now.Add(time.Hour), "Claude reset"); err != nil {
		t.Fatal(err)
	}
	if err := store.Register(wakecoord.Candidate{
		ID: "task-1", Source: wakecoord.SourceWorkspaceTask, WakeAt: now.Add(2 * time.Hour),
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel("ovr-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	candidates, err := store.Candidates(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Source != wakecoord.SourceWorkspaceTask {
		t.Fatalf("candidates = %+v, want the workspace task untouched", candidates)
	}
}

// TestASilentOwnerIsNotReady is the case where Ori simply is not running.
func TestASilentOwnerIsNotReady(t *testing.T) {
	client, _ := newClient(t)
	readiness := client.Owner()
	if readiness.Running || readiness.Ready {
		t.Fatalf("readiness = %+v, want an owner that has said nothing to be treated as absent", readiness)
	}
	if readiness.Detail == "" {
		t.Fatal("the refusal was not explained")
	}
}

func TestAStaleOwnerReportIsNotBelieved(t *testing.T) {
	client, store := newClient(t)
	if err := store.PublishOwner(wakecoord.Owner{
		Supported: true, Enabled: true, ApprovalGranted: true,
	}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if readiness := client.Owner(); readiness.Ready {
		t.Fatalf("readiness = %+v, want a stale report disbelieved", readiness)
	}
}

func TestOwnerReadinessExplainsEachRefusal(t *testing.T) {
	cases := []struct {
		name     string
		owner    wakecoord.Owner
		ready    bool
		contains string
	}{
		{"unsupported platform", wakecoord.Owner{Enabled: true, ApprovalGranted: true}, false, "cannot program"},
		{"turned off", wakecoord.Owner{Supported: true, ApprovalGranted: true}, false, "turned off"},
		{"not approved", wakecoord.Owner{Supported: true, Enabled: true}, false, "approval"},
		{"ready", wakecoord.Owner{Supported: true, Enabled: true, ApprovalGranted: true}, true, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, store := newClient(t)
			if err := store.PublishOwner(testCase.owner, now); err != nil {
				t.Fatal(err)
			}
			readiness := client.Owner()
			if readiness.Ready != testCase.ready {
				t.Fatalf("ready = %v, want %v (%s)", readiness.Ready, testCase.ready, readiness.Detail)
			}
			if !testCase.ready && !strings.Contains(readiness.Detail, testCase.contains) {
				t.Fatalf("detail = %q, want it to mention %q", readiness.Detail, testCase.contains)
			}
		})
	}
}
