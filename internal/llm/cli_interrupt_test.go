package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeCLI writes an executable shell script standing in for a provider CLI.
func fakeCLI(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatalf("write fake %s cli: %v", name, err)
	}
	return path
}

// What Codex prints to stderr before it does anything else: a banner, then the
// whole prompt it was given.
const fakeCodexPreamble = `prompt=$(cat)
printf 'OpenAI Codex v0.160.0\n--------\nmodel: gpt-test\n--------\nuser\n%s\nhook: SessionStart\nhook: SessionStart Completed\n' "$prompt" >&2
`

// A prompt the size of a real task's: system prompt plus tool definitions.
func longCodexPrompt() string {
	return "System:\nYou are a helpful AI assistant.\n\nUser:\nSet up your Personal HQ\n\n" +
		strings.Repeat("tool definition ", 2000)
}

// The failure this file exists for: Ctrl-C on the server reached the Codex
// child, which printed "turn interrupted" and exited 1 like any other failure.
func TestRunCodexExec_InterruptedTurnIsTypedAndBrief(t *testing.T) {
	cli := fakeCLI(t, "codex", fakeCodexPreamble+"printf 'turn interrupted\\n' >&2\nexit 1\n")

	_, err := (&CodexProvider{cliPath: cli}).runCodexExec(context.Background(), "gpt-test", longCodexPrompt(), "medium", nil, nil)
	if err == nil {
		t.Fatal("expected the interrupted run to fail")
	}

	typed, ok := AsProviderError(err)
	if !ok || typed.Category != CategoryInterrupted {
		t.Fatalf("error = %v, want a typed interruption", err)
	}
	if typed.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", typed.Provider)
	}
	if strings.Contains(err.Error(), "tool definition") || strings.Contains(err.Error(), "OpenAI Codex v") {
		t.Fatalf("the error must not carry the banner or the prompt, got %d chars", len(err.Error()))
	}
	if !strings.HasSuffix(err.Error(), "turn interrupted: exit status 1") {
		t.Fatalf("error = %q, want it to end with the reason", err.Error())
	}
}

// An ordinary failure keeps its reason, loses the echoed prompt, and is not
// mistaken for an interruption.
func TestRunCodexExec_FailureKeepsTheReasonNotThePrompt(t *testing.T) {
	cli := fakeCLI(t, "codex", fakeCodexPreamble+"printf 'ERROR: stream disconnected before completion\\n' >&2\nexit 1\n")

	_, err := (&CodexProvider{cliPath: cli}).runCodexExec(context.Background(), "gpt-test", longCodexPrompt(), "medium", nil, nil)
	if err == nil {
		t.Fatal("expected the run to fail")
	}
	if _, typed := AsProviderError(err); typed {
		t.Fatalf("an ordinary failure must not be reported as an interruption: %v", err)
	}
	want := "hook: SessionStart\nhook: SessionStart Completed\nERROR: stream disconnected before completion: exit status 1"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// A CLI that does not trap the signal simply dies from it. SIGTERM rather than
// SIGINT: a test run started in the background inherits an ignored SIGINT.
func TestCLIProviders_SignalDeathIsInterrupted(t *testing.T) {
	const dieFromSignal = "kill -TERM $$\nsleep 5\n"

	t.Run("codex", func(t *testing.T) {
		cli := fakeCLI(t, "codex", "cat >/dev/null\n"+dieFromSignal)
		_, err := (&CodexProvider{cliPath: cli}).runCodexExec(context.Background(), "gpt-test", "hi", "medium", nil, nil)
		if typed, ok := AsProviderError(err); !ok || typed.Category != CategoryInterrupted {
			t.Fatalf("error = %v, want a typed interruption", err)
		}
	})

	t.Run("claude_code", func(t *testing.T) {
		cli := fakeCLI(t, "claude", dieFromSignal)
		_, err := (&ClaudeCodeProvider{cliPath: cli}).runClaudeExec(context.Background(), "haiku", "", "hi", nil, nil)
		typed, ok := AsProviderError(err)
		if !ok || typed.Category != CategoryInterrupted {
			t.Fatalf("error = %v, want a typed interruption", err)
		}
		if typed.Provider != "claude_code" {
			t.Fatalf("provider = %q, want claude_code", typed.Provider)
		}
	})
}

// Claude Code traps the signal, exits 0, and reports the interruption in its
// result (captured from Claude Code 2.1.289).
func TestRunClaudeExec_AbortedRunIsInterrupted(t *testing.T) {
	cli := fakeCLI(t, "claude", `cat <<'JSON'
{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null"]}
JSON
`)

	_, err := (&ClaudeCodeProvider{cliPath: cli}).runClaudeExec(context.Background(), "haiku", "", "hi", nil, nil)
	typed, ok := AsProviderError(err)
	if !ok || typed.Category != CategoryInterrupted {
		t.Fatalf("error = %v, want a typed interruption", err)
	}
	if !strings.Contains(err.Error(), "aborted_streaming") {
		t.Fatalf("error = %q, want it to keep Claude's own reason", err.Error())
	}
}

func TestRunClaudeExec_OrdinaryErrorResultIsNotAnInterruption(t *testing.T) {
	cli := fakeCLI(t, "claude", `cat <<'JSON'
{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Credit balance is too low"}
JSON
`)

	_, err := (&ClaudeCodeProvider{cliPath: cli}).runClaudeExec(context.Background(), "haiku", "", "hi", nil, nil)
	if err == nil || err.Error() != "Credit balance is too low" {
		t.Fatalf("error = %v, want Claude's own message unchanged", err)
	}
	if _, typed := AsProviderError(err); typed {
		t.Fatalf("an ordinary error result must not be reported as an interruption: %v", err)
	}
}

func TestCodexFailureDetail(t *testing.T) {
	// A failure before the prompt is echoed (a bad flag) is short: keep it whole.
	usage := "error: unexpected argument '--frobnicate' found"
	if got := codexFailureDetail(usage+"\n", "a prompt that was never echoed"); got != usage {
		t.Fatalf("detail = %q, want %q", got, usage)
	}

	// Nothing after the echoed prompt means Codex gave no reason at all.
	if got := codexFailureDetail("banner\nuser\nthe prompt\n", "the prompt"); got != "" {
		t.Fatalf("detail = %q, want empty", got)
	}

	// Output that does not echo the prompt is still bounded, keeps its end, and
	// is never cut in the middle of a character.
	long := strings.Repeat("é", 4000) + "\nERROR: the real reason"
	got := codexFailureDetail(long, "a prompt that was never echoed")
	if len(got) > maxCLIFailureDetail+len("…") {
		t.Fatalf("detail is %d bytes, want at most %d", len(got), maxCLIFailureDetail+len("…"))
	}
	if !strings.HasSuffix(got, "ERROR: the real reason") {
		t.Fatalf("detail must keep the end of the output, got %q", got[len(got)-40:])
	}
	if !utf8.ValidString(got) {
		t.Fatal("detail was cut in the middle of a character")
	}
}

func TestCodexTurnInterrupted(t *testing.T) {
	for detail, want := range map[string]bool{
		"turn interrupted": true,
		"hook: SessionStart\nhook: SessionStart Completed\nturn interrupted\n": true,
		"ERROR: stream disconnected before completion":                         false,
		// Only Codex's own closing line counts, not the phrase in passing.
		"turn interrupted\nERROR: usage limit reached": false,
		"": false,
	} {
		if got := codexTurnInterrupted(detail); got != want {
			t.Fatalf("codexTurnInterrupted(%q) = %v, want %v", detail, got, want)
		}
	}
}
