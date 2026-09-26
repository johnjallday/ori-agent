package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func events(items ...event) string {
	var b strings.Builder
	for _, e := range items {
		data, err := json.Marshal(e)
		if err != nil {
			panic(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestInterleavedAndNestedTimingsAreDeterministicAndNotAdded(t *testing.T) {
	input := events(
		event{Action: "start", Package: "z"}, event{Action: "start", Package: "a"},
		event{Action: "run", Package: "z", Test: "TestParent"},
		event{Action: "run", Package: "a", Test: "TestSkipped"},
		event{Action: "skip", Package: "a", Test: "TestSkipped"},
		event{Action: "run", Package: "z", Test: "TestParent/child"},
		event{Action: "pause", Package: "z", Test: "TestParent/child"},
		event{Action: "cont", Package: "z", Test: "TestParent/child"},
		event{Action: "pass", Package: "z", Test: "TestParent/child", Elapsed: 2},
		event{Action: "pass", Package: "z", Test: "TestParent", Elapsed: 3},
		event{Action: "pass", Package: "a", Elapsed: 4},
		event{Action: "pass", Package: "z", Elapsed: 4},
	)
	var output, diagnostic bytes.Buffer
	if code := run(strings.NewReader(input), &output, &diagnostic); code != 0 {
		t.Fatalf("code=%d: %s", code, output.String())
	}
	for _, want := range []string{"executed/pass **2**", "Fresh top-level completions: pass 1 / fail 0 / skip 1. Nested: pass 1", "do not sum", "not additive"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output.String())
		}
	}
	if strings.Index(output.String(), escaped("a")) > strings.Index(output.String(), escaped("z")) {
		t.Fatal("ties not sorted by name")
	}
	for range 5 {
		if got := summarize(strings.NewReader(input)).markdown(); got != output.String() {
			t.Fatal("nondeterministic report")
		}
	}
	if diagnostic.Len() != 0 {
		t.Fatalf("success dumped test chatter: %s", diagnostic.String())
	}
}

func TestCachedReplaysAreNotFreshExecutionOrSlowTestEvidence(t *testing.T) {
	input := events(
		event{Action: "start", Package: "cached"},
		event{Action: "run", Package: "cached", Test: "TestReplay"},
		event{Action: "pass", Package: "cached", Test: "TestReplay", Elapsed: 900},
		event{Action: "output", Package: "cached", Output: "ok  \tcached\t(cached)\tcoverage: 50%\n"},
		event{Action: "pass", Package: "cached"},
		event{Action: "start", Package: "empty"}, event{Action: "skip", Package: "empty"},
	)
	r := summarize(strings.NewReader(input))
	text := r.markdown()
	if r.failed() || !strings.Contains(text, "cached **1**, skipped **1**") || !strings.Contains(text, "Fresh top-level completions: pass 0") || strings.Contains(text, "900.000") {
		t.Fatal(text)
	}
}

func TestFailureAndUnsafeLongOutputRemainReadableButCannotIssueCommands(t *testing.T) {
	name := "TestUnsafe|<img>\n::error::\r\x1b[31m##[warning]"
	input := events(
		event{Action: "start", Package: "p"}, event{Action: "run", Package: "p", Test: name},
		event{Action: "output", Package: "p", Test: name, Output: strings.Repeat("x", 200000) + "\n::error::untrusted\r::warning::still untrusted\nassertion failed\n"},
		event{Action: "fail", Package: "p", Test: name, Elapsed: 1}, event{Action: "fail", Package: "p", Elapsed: 1},
	)
	var output, diagnostic bytes.Buffer
	if run(strings.NewReader(input), &output, &diagnostic) == 0 {
		t.Fatal("test failure lost")
	}
	if !strings.Contains(output.String(), "failed **1**") || strings.Contains(output.String(), "<img>") || strings.Contains(output.String(), "::") || strings.Contains(output.String(), "##[") {
		t.Fatal(output.String())
	}
	if !strings.Contains(diagnostic.String(), "assertion failed") || strings.Contains(diagnostic.String(), "\r") || strings.Contains(diagnostic.String(), "::") || strings.Contains(diagnostic.String(), "##[") || strings.Contains(diagnostic.String(), "\x1b") {
		t.Fatal("unsafe or missing diagnostics")
	}
	if diagnostic.Len() > 3*tailLimit {
		t.Fatal("unbounded failure output")
	}
}

func TestBadAndIncompleteStreamsFailClosed(t *testing.T) {
	for name, input := range map[string]string{
		"empty":            "",
		"malformed":        "not JSON\n",
		"truncated":        `{"Action":"start","Package":"p"`,
		"unfinished":       events(event{Action: "start", Package: "p"}, event{Action: "run", Package: "p", Test: "TestHang"}),
		"missing start":    events(event{Action: "pass", Package: "p"}),
		"missing test run": events(event{Action: "start", Package: "p"}, event{Action: "pass", Package: "p", Test: "TestMissing"}, event{Action: "pass", Package: "p"}),
		"pending test":     events(event{Action: "start", Package: "p"}, event{Action: "run", Package: "p", Test: "TestHang"}, event{Action: "pass", Package: "p"}),
		"duplicate":        events(event{Action: "start", Package: "p"}, event{Action: "pass", Package: "p"}, event{Action: "pass", Package: "p"}),
		"negative elapsed": events(event{Action: "start", Package: "p"}, event{Action: "pass", Package: "p", Elapsed: -1}),
		"build failure":    events(event{Action: "build-output", ImportPath: "dependency", Output: "compile error\n"}, event{Action: "build-fail", ImportPath: "dependency"}),
		"timeout":          events(event{Action: "start", Package: "p"}, event{Action: "run", Package: "p", Test: "TestHang"}, event{Action: "output", Package: "p", Output: "panic: test timed out\n"}, event{Action: "fail", Package: "p"}),
	} {
		t.Run(name, func(t *testing.T) {
			var output, diagnostic bytes.Buffer
			if run(strings.NewReader(input), &output, &diagnostic) == 0 {
				t.Fatal("reported broken stream as passing")
			}
			if output.Len() == 0 {
				t.Fatal("no failure summary")
			}
		})
	}
}

func TestUnknownFieldsAndRepeatedTestRuns(t *testing.T) {
	input := "{\"Action\":\"start\",\"Package\":\"p\",\"Future\":{\"nested\":true}}\n"
	for range 3 {
		input += events(event{Action: "run", Package: "p", Test: "TestRepeat"}, event{Action: "pass", Package: "p", Test: "TestRepeat", Elapsed: 1})
	}
	input += events(event{Action: "pass", Package: "p", Elapsed: 3})
	r := summarize(strings.NewReader(input))
	if r.failed() || !strings.Contains(r.markdown(), "Fresh top-level completions: pass 3") {
		t.Fatal(r.markdown())
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestReadAndWriteFailuresAreNotSuccess(t *testing.T) {
	valid := events(event{Action: "start", Package: "p"}, event{Action: "pass", Package: "p"})
	if run(strings.NewReader(valid), brokenWriter{}, io.Discard) == 0 {
		t.Fatal("summary write failure lost")
	}
	if !summarize(io.MultiReader(strings.NewReader(valid), brokenReader{})).failed() {
		t.Fatal("reader failure lost")
	}
	if diagnostics(brokenWriter{}, "failure") == nil {
		t.Fatal("diagnostic failure lost")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
