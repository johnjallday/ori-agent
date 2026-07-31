package wakeservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParsePMSetScheduleFixtures(t *testing.T) {
	t.Parallel()
	location := time.FixedZone("fixture", -4*60*60)
	tests := []struct {
		name       string
		file       string
		eventCount int
		wantError  bool
	}{
		{name: "empty", file: "pmset-empty.txt"},
		{name: "unrelated and repeating", file: "pmset-unrelated.txt", eventCount: 2},
		{name: "matching", file: "pmset-matching.txt", eventCount: 2},
		{name: "same-time foreign", file: "pmset-same-time-foreign.txt", eventCount: 2},
		{name: "conflicting owned", file: "pmset-conflicting-owned.txt", eventCount: 2},
		{name: "malformed", file: "pmset-malformed.txt", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload, err := os.ReadFile(filepath.Join("testdata", test.file))
			if err != nil {
				t.Fatal(err)
			}
			events, err := parsePMSetSchedule(payload, location)
			if test.wantError {
				if err == nil {
					t.Fatalf("parsePMSetSchedule() events = %+v, want error", events)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != test.eventCount {
				t.Fatalf("event count = %d, want %d: %+v", len(events), test.eventCount, events)
			}
		})
	}
}

func TestPMSetUsesOnlyFixedArgumentVectors(t *testing.T) {
	t.Parallel()
	location := time.FixedZone("fixture", -4*60*60)
	wakeAt := time.Date(2026, 7, 31, 16, 5, 6, 999, time.UTC)
	var calls [][]string
	runner := func(_ context.Context, arguments []string) ([]byte, error) {
		calls = append(calls, append([]string(nil), arguments...))
		return []byte("Scheduled power events:\n"), nil
	}
	scheduler, err := NewPMSet(runner, time.Second, location)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Events(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Schedule(context.Background(), wakeAt); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Cancel(context.Background(), wakeAt); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-g", "sched"},
		{"schedule", "wakeorpoweron", "07/31/26 12:05:06", "com.ori.herdr-wake"},
		{"schedule", "cancel", "wakeorpoweron", "07/31/26 12:05:06", "com.ori.herdr-wake"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("pmset calls = %#v, want %#v", calls, want)
	}
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), "cancelall") {
			t.Fatalf("unsafe broad cancellation call: %v", call)
		}
	}
}

func TestPMSetBoundsOutputAndSurfacesSafeFailure(t *testing.T) {
	t.Parallel()
	oversized, err := NewPMSet(func(context.Context, []string) ([]byte, error) {
		return make([]byte, maxPMSetOutput+1), nil
	}, time.Second, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oversized.Events(context.Background()); err == nil {
		t.Fatal("Events() succeeded with oversized output")
	}

	failed, err := NewPMSet(func(context.Context, []string) ([]byte, error) {
		return []byte("permission denied\n"), errors.New("exit status 1")
	}, time.Second, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Schedule(context.Background(), time.Now()); err == nil ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Schedule() error = %v", err)
	}
}
