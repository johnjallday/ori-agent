package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// fakeGHEnv makes this test binary impersonate `gh`. TestMain checks it
	// before any test runs, so the impersonating process never re-enters the
	// suite that spawned it.
	fakeGHEnv = "HERDR_DEVFLOW_TEST_FAKE_GH"
	// fakeGHLogEnv names the file the impersonator records its invocation in.
	fakeGHLogEnv = "HERDR_DEVFLOW_TEST_FAKE_GH_LOG"
	// fakeGHRecordSeparator separates the recorded working directory from the
	// recorded argument vector, and each argument from the next. NUL is the one
	// byte an argument cannot contain, so an argument carrying newlines, quotes,
	// or shell metacharacters still round-trips exactly.
	fakeGHRecordSeparator = "\x00"
)

// TestMain lets this binary stand in for `gh` when the runner invokes it. The
// package forbids building shell command strings, and that prohibition applies
// to its own fixtures — so the fake CLI is this binary re-executed, not a
// script a shell would have to parse.
func TestMain(m *testing.M) {
	if os.Getenv(fakeGHEnv) == "1" {
		recordFakeGHInvocation()
		return
	}
	os.Exit(m.Run())
}

func recordFakeGHInvocation() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	record := append([]string{cwd}, os.Args[1:]...)
	if path := os.Getenv(fakeGHLogEnv); path != "" {
		// Errors are unrecoverable here and the assertion below reports the
		// missing record far more usefully than a partial write would.
		_ = os.WriteFile(path, []byte(strings.Join(record, fakeGHRecordSeparator)), 0600)
	}
	// An empty JSON array is what every read-only listing this package performs
	// expects on success.
	_, _ = os.Stdout.WriteString("[]")
}

// installFakeGH puts an executable named `gh` on PATH that records exactly how
// it was invoked, and returns the path of the record it will write.
func installFakeGH(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("this test binary cannot locate itself: %v", err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(self, filepath.Join(binDir, "gh")); err != nil {
		t.Skipf("a fake gh could not be installed: %v", err)
	}
	log := filepath.Join(t.TempDir(), "invocation")
	t.Setenv("PATH", binDir)
	t.Setenv(fakeGHEnv, "1")
	t.Setenv(fakeGHLogEnv, log)
	return log
}

func readFakeGHInvocation(t *testing.T, log string) (string, []string) {
	t.Helper()
	// #nosec G304 -- log is a path this test created under its own temp dir.
	recorded, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the fake gh recorded no invocation: %v", err)
	}
	fields := strings.Split(string(recorded), fakeGHRecordSeparator)
	if len(fields) == 0 {
		t.Fatalf("the fake gh recorded an empty invocation")
	}
	return fields[0], fields[1:]
}

// TestExecRunnerPassesArgumentsAsDataToAFixedBinary characterizes the property
// every GitHub operation in this package is built on: the real runner executes
// one fixed binary with an argument vector, and no shell ever sees the values.
//
// It matters more once arguments stop being fixed literals. Issue titles,
// bodies, and search terms are user text; if the boundary interpreted them,
// a backtick in an idea would run a command. The hostile arguments below are
// asserted to arrive byte for byte, exactly as they were passed.
func TestExecRunnerPassesArgumentsAsDataToAFixedBinary(t *testing.T) {
	log := installFakeGH(t)
	workDir := t.TempDir()

	hostile := []string{
		"issue", "list",
		"--search", "author:@me state:open",
		"; rm -rf /",
		"$(id)",
		"`whoami`",
		"a | b & c > d",
		"first line\nsecond line",
		"quote'and\"quote",
		"\x1b[31mred\x1b[0m",
	}
	output, err := ExecRunner(workDir)(context.Background(), hostile...)
	if err != nil {
		t.Fatalf("ExecRunner: %v", err)
	}
	if string(output) != "[]" {
		t.Fatalf("stdout = %q, want the fake CLI's payload", string(output))
	}

	cwd, args := readFakeGHInvocation(t, log)
	if len(args) != len(hostile) {
		t.Fatalf("received %d arguments, want %d: %q", len(args), len(hostile), args)
	}
	for index, want := range hostile {
		if args[index] != want {
			t.Fatalf("argument %d = %q, want it passed through verbatim as %q", index, args[index], want)
		}
	}

	wantDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != wantDir {
		t.Fatalf("gh ran in %q, want the configured repository directory %q", gotDir, wantDir)
	}
}

// TestExecRunnerReportsAMissingBinaryThroughTheClassifier proves the missing-CLI
// path end to end rather than only through an injected error: with no `gh` on
// PATH the real runner's failure still becomes a sanitized, classified error
// carrying a recovery action.
func TestExecRunnerReportsAMissingBinaryThroughTheClassifier(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	client := New(Options{Dir: t.TempDir()})

	_, err := client.ListPullRequests(context.Background(), "dev")

	var remoteErr *Error
	if !errors.As(err, &remoteErr) {
		t.Fatalf("err = %v, want a classified error", err)
	}
	if remoteErr.Kind != ErrorMissing {
		t.Fatalf("kind = %q, want %q", remoteErr.Kind, ErrorMissing)
	}
	if !strings.Contains(remoteErr.Recovery(), "cli.github.com") {
		t.Fatalf("recovery = %q, want the install instruction", remoteErr.Recovery())
	}
}
