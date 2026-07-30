package app

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
)

type agentPickerRunner struct {
	primary string
	feature string
	calls   []string
	focused []string
}

func (r *agentPickerRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	key := strings.Join(command.Args, " ")
	r.calls = append(r.calls, key)
	switch key {
	case "agent list":
		return herdr.CommandResult{Stdout: []byte(fmt.Sprintf(`{"result":{"agents":[
			{"agent":"claude","name":"ori-bridge-builder","agent_status":"idle","cwd":%q,"pane_id":"w1:p1"},
			{"agent":"codex","agent_status":"working","cwd":%q,"pane_id":"w-dev:p1"}
		]}}`, r.feature, r.primary))}, nil
	case "agent focus w-dev:p1":
		r.focused = append(r.focused, "w-dev:p1")
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"agent_focused","pane_id":"w-dev:p1"}}`)}, nil
	case "agent focus ori-bridge-builder":
		r.focused = append(r.focused, "ori-bridge-builder")
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"agent_focused","name":"ori-bridge-builder"}}`)}, nil
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected Herdr command: %s", key)
	}
}

func TestGoAgentPromptsUntilAValidChoiceAndFocusesTheExactLiveAgent(t *testing.T) {
	primary, feature := createPrimaryCheckoutWithFeature(t)
	runner := &agentPickerRunner{primary: primary, feature: feature}
	codexChoice := 1
	if feature < primary {
		codexChoice = 2
	}
	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:        &output,
		Stderr:        &stderr,
		Stdin:         strings.NewReader(fmt.Sprintf("9\n%d\n", codexChoice)),
		Getwd:         func() (string, error) { return primary, nil },
		LookupEnv:     func(string) (string, bool) { return "", false },
		Runner:        runner,
		IsInteractive: func() bool { return true },
	})

	exit := application.Run(context.Background(), []string{
		"--repo-root", primary,
		"--home", filepath.Join(t.TempDir(), "runtime"),
		"--herdr-bin", "fake-herdr",
		"go",
	})
	if exit != 0 {
		t.Fatalf("go exit=%d stderr=%s", exit, stderr.String())
	}
	if len(runner.focused) != 1 || runner.focused[0] != "w-dev:p1" {
		t.Fatalf("focused targets = %v, want the unnamed Codex pane", runner.focused)
	}
	for _, want := range []string{
		"Select an open agent:",
		"ori-bridge-builder",
		"codex@ori-agent-dev",
		"Focused codex@ori-agent-dev (codex) in ori-agent-dev.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("picker output did not contain %q:\n%s", want, output.String())
		}
	}
	if !strings.Contains(stderr.String(), `Invalid choice "9"`) {
		t.Fatalf("invalid choice was not explained: %s", stderr.String())
	}
}

func TestGoAgentCanBeCancelledWithoutChangingFocus(t *testing.T) {
	primary, feature := createPrimaryCheckoutWithFeature(t)
	runner := &agentPickerRunner{primary: primary, feature: feature}
	application := New(Dependencies{
		Stdout:        &bytes.Buffer{},
		Stderr:        &bytes.Buffer{},
		Stdin:         strings.NewReader("q\n"),
		Getwd:         func() (string, error) { return primary, nil },
		LookupEnv:     func(string) (string, bool) { return "", false },
		Runner:        runner,
		IsInteractive: func() bool { return true },
	})

	exit := application.Run(context.Background(), []string{
		"--repo-root", primary,
		"--home", filepath.Join(t.TempDir(), "runtime"),
		"--herdr-bin", "fake-herdr",
		"go",
	})
	if exit != 0 || len(runner.focused) != 0 {
		t.Fatalf("cancel exit=%d focused=%v", exit, runner.focused)
	}
}

func TestGoAgentRefusesANonInteractiveShellBeforeCallingHerdr(t *testing.T) {
	runner := &agentPickerRunner{}
	var stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:        &bytes.Buffer{},
		Stderr:        &stderr,
		Stdin:         strings.NewReader("1\n"),
		LookupEnv:     func(string) (string, bool) { return "", false },
		Runner:        runner,
		IsInteractive: func() bool { return false },
	})

	if exit := application.Run(context.Background(), []string{"go"}); exit != 1 {
		t.Fatalf("non-interactive go exit=%d, want 1", exit)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("non-interactive picker called Herdr: %v", runner.calls)
	}
	if !strings.Contains(stderr.String(), "requires an interactive terminal") {
		t.Fatalf("non-interactive error = %q", stderr.String())
	}
}
