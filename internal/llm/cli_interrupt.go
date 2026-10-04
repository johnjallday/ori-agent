package llm

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Interrupted CLI runs.
//
// The Codex and Claude Code providers run a CLI as a child process, and a child
// process can be stopped from outside. The common case is Ctrl-C in the terminal
// running Ori: the signal goes to the whole process group, so the CLI receives
// it at the same moment the server does. The server shuts down gracefully and so
// lives long enough to record what its child reported.
//
// Neither CLI reports that as a cancellation Ori already understands. Codex
// traps the signal and exits 1 like any other failure; Claude Code exits 0 with
// an error result. Left unclassified, the task was blocked with "an error Ori
// couldn't classify. Retrying may not help" — for a run that only needs to be
// started again.

// maxCLIFailureDetail bounds how much CLI output an error may carry. The error
// is stored on the task and shown to the user; it is a reason, not a transcript.
const maxCLIFailureDetail = 2000

// cliStoppedBySignal reports whether a CLI run ended because an interrupt,
// terminate, or hangup signal killed the process. SIGKILL is deliberately not
// here: it is what a canceled or expired context sends, which is the caller's
// own decision rather than an interruption from outside.
func cliStoppedBySignal(runErr error) bool {
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}
	switch status.Signal() {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP:
		return true
	default:
		return false
	}
}

// codexFailureDetail returns the part of Codex's output that says why a run
// failed. Codex prints a banner and then echoes the whole prompt before anything
// else, so the reason is whatever follows the prompt. Using the output whole put
// the system prompt and every tool definition into the task's error, with the
// cause on its last line.
func codexFailureDetail(output, prompt string) string {
	detail := strings.TrimSpace(output)
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		if i := strings.LastIndex(detail, prompt); i >= 0 {
			detail = strings.TrimSpace(detail[i+len(prompt):])
		}
	}
	return tailOfCLIOutput(detail, maxCLIFailureDetail)
}

// codexTurnInterrupted reports whether Codex ended the run because it was
// interrupted. Codex records the turn as aborted and exits 1, exactly as it does
// for a failed request, so the last line of its output is the only place the
// difference shows (codex-cli 0.159.3 and 0.160.0).
func codexTurnInterrupted(detail string) bool {
	last := strings.TrimSpace(detail)
	if i := strings.LastIndexByte(last, '\n'); i >= 0 {
		last = last[i+1:]
	}
	return strings.EqualFold(strings.TrimSpace(last), "turn interrupted")
}

// claudeRunAborted reports whether Claude Code ended the run because it was
// interrupted. It says so in a field of its own: an interrupted run's result
// carries terminal_reason "aborted_streaming" (Claude Code 2.1.289).
func claudeRunAborted(resp claudeCLIResponse) bool {
	return resp.IsError && strings.HasPrefix(strings.TrimSpace(resp.TerminalReason), "aborted")
}

// tailOfCLIOutput keeps the end of s, where a CLI reports why it stopped.
func tailOfCLIOutput(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := len(s) - limit
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return "…" + strings.TrimSpace(s[cut:])
}
