package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []Event
}

func (p *recordingPublisher) Publish(event Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *recordingPublisher) snapshot() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event(nil), p.events...)
}

func newTestScriptedHandler(delay time.Duration) (*ScriptedTaskHandler, *recordingPublisher) {
	pub := &recordingPublisher{}
	return &ScriptedTaskHandler{bus: pub, stepDelay: delay}, pub
}

func scriptedTestTask(description string) Task {
	return Task{ID: "task-1", WorkspaceID: "ws-1", To: "Theo", Description: description}
}

func TestScriptedTaskHandler_PublishesTheFixedSequenceAndReturnsTheResult(t *testing.T) {
	handler, pub := newTestScriptedHandler(0)

	result, err := handler.ExecuteTask(context.Background(), "Theo", scriptedTestTask("Look into the release notes"))
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if result != ScriptedTaskResult {
		t.Fatalf("result = %q, want %q", result, ScriptedTaskResult)
	}

	want := []struct {
		eventType EventType
		toolName  string
	}{
		{EventTaskThinking, ""},
		{EventTaskToolCall, "read_file"},
		{EventTaskToolResult, "read_file"},
		{EventTaskToolCall, "web_search"},
		{EventTaskToolResult, "web_search"},
		{EventTaskThinking, ""},
	}
	events := pub.snapshot()
	if len(events) != len(want) {
		t.Fatalf("published %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, w := range want {
		ev := events[i]
		if ev.Type != w.eventType {
			t.Errorf("event %d type = %s, want %s", i, ev.Type, w.eventType)
		}
		if ev.WorkspaceID != "ws-1" || ev.Data["task_id"] != "task-1" || ev.Data["agent"] != "Theo" {
			t.Errorf("event %d identity = ws %q task %v agent %v, want ws-1/task-1/Theo",
				i, ev.WorkspaceID, ev.Data["task_id"], ev.Data["agent"])
		}
		if w.toolName != "" && ev.Data["tool_name"] != w.toolName {
			t.Errorf("event %d tool_name = %v, want %s", i, ev.Data["tool_name"], w.toolName)
		}
		if ev.Type == EventTaskToolResult && ev.Data["success"] != true {
			t.Errorf("event %d success = %v, want true", i, ev.Data["success"])
		}
	}
}

func TestScriptedTaskHandler_FailMarkerFailsAfterTheFirstToolCall(t *testing.T) {
	handler, pub := newTestScriptedHandler(0)

	_, err := handler.ExecuteTask(context.Background(), "Theo", scriptedTestTask("Please [FAIL] this one"))
	if !errors.Is(err, ErrScriptedTaskFailed) {
		t.Fatalf("error = %v, want ErrScriptedTaskFailed", err)
	}

	events := pub.snapshot()
	if len(events) != 2 {
		t.Fatalf("published %d events, want 2 (thinking + first tool call): %+v", len(events), events)
	}
	if events[1].Type != EventTaskToolCall || events[1].Data["tool_name"] != "read_file" {
		t.Fatalf("last event = %s %v, want the read_file tool call", events[1].Type, events[1].Data["tool_name"])
	}
}

func TestScriptedTaskHandler_StopsWhenTheContextIsCancelled(t *testing.T) {
	handler, pub := newTestScriptedHandler(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := handler.ExecuteTask(ctx, "Theo", scriptedTestTask("Long run"))
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for len(pub.snapshot()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first step was never published")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteTask did not return after cancel")
	}
	if got := len(pub.snapshot()); got != 1 {
		t.Fatalf("published %d events, want only the first step before cancel", got)
	}
}

func TestScriptedTaskHandler_NilBusStillReturnsTheResult(t *testing.T) {
	handler := NewScriptedTaskHandler(nil)
	handler.stepDelay = 0

	result, err := handler.ExecuteTask(context.Background(), "Theo", scriptedTestTask("No bus"))
	if err != nil || result != ScriptedTaskResult {
		t.Fatalf("ExecuteTask() = %q, %v; want the scripted result", result, err)
	}
}

func TestScriptedTaskRunsEnabled_OnlyExactlyOne(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "true": false, "yes": false, "1": true, " 1 ": true} {
		t.Setenv(ScriptedTaskRunsEnv, raw)
		if got := ScriptedTaskRunsEnabled(); got != want {
			t.Errorf("ScriptedTaskRunsEnabled() with %q = %v, want %v", raw, got, want)
		}
	}
}
