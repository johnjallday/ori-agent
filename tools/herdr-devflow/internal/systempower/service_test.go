package systempower

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Every test here uses an injected runner. Nothing in this package's test suite
// may execute pmset or osascript, and the fake is how that is guaranteed rather
// than hoped for.

func fakeRunner(output string, err error) (Runner, *[]string) {
	var calls []string
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte(output), err
	}, &calls
}

func TestPowerSourceReadsExternalPower(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("power inspection is implemented on macOS only")
	}
	cases := []struct {
		name   string
		output string
		want   Source
	}{
		{"plugged in", "Now drawing from 'AC Power'\n -InternalBattery-0 100%; charged", SourceAC},
		{"on battery", "Now drawing from 'Battery Power'\n -InternalBattery-0 82%; discharging", SourceBattery},
		{"something else entirely", "unexpected output", SourceUnknown},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			run, _ := fakeRunner(testCase.output, nil)
			service := &Service{GOOS: "darwin", Run: run}
			if got := service.PowerSource(context.Background()); got != testCase.want {
				t.Fatalf("PowerSource = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestAnUnreadablePowerSourceIsUnknownNotAssumed is the gate that matters: a
// Mac whose power source cannot be read is never treated as plugged in.
func TestAnUnreadablePowerSourceIsUnknownNotAssumed(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("power inspection is implemented on macOS only")
	}
	run, _ := fakeRunner("", errors.New("pmset failed"))
	service := &Service{GOOS: "darwin", Run: run}
	source := service.PowerSource(context.Background())
	if source != SourceUnknown || source.External() {
		t.Fatalf("PowerSource = %q, external = %v; want an unusable answer", source, source.External())
	}
}

func TestSleepRefusesOffMacOS(t *testing.T) {
	service := &Service{GOOS: "linux"}
	if err := service.Sleep(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Sleep off macOS = %v, want ErrUnsupported", err)
	}
	if service.SupportsSleep() {
		t.Fatal("a non-macOS build reported that it can sleep the machine")
	}
	if service.PowerSource(context.Background()).External() {
		t.Fatal("a non-macOS build claimed external power")
	}
}

// TestSleepInvokesExactlyOneBoundedCommand pins what sleeping actually does, so
// a change to it has to be deliberate.
func TestSleepInvokesExactlyOneBoundedCommand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sleep is implemented on macOS only")
	}
	run, calls := fakeRunner("", nil)
	service := &Service{GOOS: "darwin", Run: run, Timeout: time.Second}
	if err := service.Sleep(context.Background()); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %v, want exactly one", *calls)
	}
	if !strings.Contains((*calls)[0], "osascript") || !strings.Contains((*calls)[0], "sleep") {
		t.Fatalf("call = %q", (*calls)[0])
	}
}

func TestSleepReportsAFailureRatherThanSwallowingIt(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sleep is implemented on macOS only")
	}
	run, _ := fakeRunner("", errors.New("refused"))
	service := &Service{GOOS: "darwin", Run: run}
	if err := service.Sleep(context.Background()); err == nil {
		t.Fatal("a failed sleep was reported as success")
	}
}
