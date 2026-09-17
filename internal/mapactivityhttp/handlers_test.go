package mapactivityhttp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mapactivity"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type sseEvent struct {
	name    string
	data    string
	comment string
}

// readEvents parses the SSE body line by line and sends each complete event.
func readEvents(body *bufio.Reader, out chan<- sseEvent) {
	defer close(out)
	var current sseEvent
	for {
		line, err := body.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if current != (sseEvent{}) {
				out <- current
			}
			current = sseEvent{}
		case strings.HasPrefix(line, ":"):
			current.comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
		case strings.HasPrefix(line, "event: "):
			current.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			current.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func nextEvent(t *testing.T, events <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("stream closed before the next event")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a stream event")
	}
	return sseEvent{}
}

func openStream(t *testing.T, handler *Handler) (<-chan sseEvent, context.CancelFunc, *http.Response) {
	t.Helper()
	mux := http.NewServeMux()
	handler.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/workspace-map/activity/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	events := make(chan sseEvent, 16)
	go readEvents(bufio.NewReader(resp.Body), events)
	return events, cancel, resp
}

func TestGetActivityReturnsTheSnapshot(t *testing.T) {
	tracker := mapactivity.NewTracker(nil)
	tracker.HandleEvent(workspace.NewTaskEvent(workspace.EventTaskStarted, "ws-1", "task-1", "Theo", map[string]any{"description": "secret"}))
	handler := NewHandler(tracker)

	recorder := httptest.NewRecorder()
	handler.GetActivity(recorder, httptest.NewRequest(http.MethodGet, "/api/workspace-map/activity", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var snapshot mapactivity.Snapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snapshot.Running) != 1 || snapshot.Running[0].AgentName != "Theo" {
		t.Fatalf("running = %+v, want Theo's run", snapshot.Running)
	}
	if snapshot.Parcels == nil {
		t.Fatal("parcels decoded as null, want []")
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("snapshot leaks event content: %s", recorder.Body.String())
	}
}

func TestStreamSendsTheSnapshotFirstThenActivity(t *testing.T) {
	tracker := mapactivity.NewTracker(nil)
	tracker.HandleEvent(workspace.NewTaskEvent(workspace.EventTaskStarted, "ws-1", "task-1", "Theo", nil))
	events, cancel, resp := openStream(t, NewHandler(tracker))
	defer cancel()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	first := nextEvent(t, events)
	if first.name != "snapshot" {
		t.Fatalf("first event = %q, want snapshot", first.name)
	}
	var snapshot mapactivity.Snapshot
	if err := json.Unmarshal([]byte(first.data), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapshot.Running) != 1 {
		t.Fatalf("snapshot running = %+v, want one", snapshot.Running)
	}

	tracker.HandleEvent(workspace.NewTaskEvent(workspace.EventTaskToolCall, "ws-1", "task-1", "Theo", map[string]any{
		"tool_name": "web_search",
		"arguments": map[string]any{"query": "private"},
	}))
	second := nextEvent(t, events)
	if second.name != "activity" {
		t.Fatalf("second event = %q, want activity", second.name)
	}
	var activity mapactivity.ActivityEvent
	if err := json.Unmarshal([]byte(second.data), &activity); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if activity.Phase != mapactivity.PhaseStep || activity.Step == nil || activity.Step.ToolName != "web_search" {
		t.Fatalf("activity = %+v, want the web_search step", activity)
	}
	if strings.Contains(second.data, "arguments") || strings.Contains(second.data, "private") {
		t.Fatalf("activity leaks tool arguments: %s", second.data)
	}
}

func TestStreamSendsKeepalives(t *testing.T) {
	handler := NewHandler(mapactivity.NewTracker(nil))
	handler.keepalive = 20 * time.Millisecond
	events, cancel, _ := openStream(t, handler)
	defer cancel()

	if first := nextEvent(t, events); first.name != "snapshot" {
		t.Fatalf("first event = %q, want snapshot", first.name)
	}
	if ev := nextEvent(t, events); ev.comment != "keepalive" {
		t.Fatalf("event = %+v, want a keepalive comment", ev)
	}
}

func TestStreamUnsubscribesWhenTheClientDisconnects(t *testing.T) {
	tracker := mapactivity.NewTracker(nil)
	baseline := tracker.SubscriberCount()
	events, cancel, _ := openStream(t, NewHandler(tracker))

	nextEvent(t, events)
	if tracker.SubscriberCount() != baseline+1 {
		t.Fatalf("SubscriberCount = %d while streaming, want %d", tracker.SubscriberCount(), baseline+1)
	}
	cancel()

	deadline := time.Now().Add(3 * time.Second)
	for tracker.SubscriberCount() != baseline {
		if time.Now().After(deadline) {
			t.Fatalf("SubscriberCount = %d after disconnect, want baseline %d", tracker.SubscriberCount(), baseline)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStreamEndsWhenTheTrackerStops(t *testing.T) {
	tracker := mapactivity.NewTracker(nil)
	events, cancel, _ := openStream(t, NewHandler(tracker))
	defer cancel()
	nextEvent(t, events)

	tracker.Stop()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("got another event after Stop, want the stream to end")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream stayed open after the tracker stopped")
	}
}

func TestEveryRouteIs404WhenTheFlagIsOff(t *testing.T) {
	t.Setenv("ORI_MAP_SHOW_ENABLED", "false")
	handler := NewHandler(mapactivity.NewTracker(nil))
	mux := http.NewServeMux()
	handler.Register(mux)

	for _, path := range []string{"/api/workspace-map/activity", "/api/workspace-map/activity/stream"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, recorder.Code)
		}
	}
}

func TestUnwiredHandlerIs404(t *testing.T) {
	handler := NewHandler(nil)
	recorder := httptest.NewRecorder()
	handler.StreamActivity(recorder, httptest.NewRequest(http.MethodGet, "/api/workspace-map/activity/stream", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if handler.Wired() {
		t.Fatal("Wired() = true for a nil tracker")
	}
}
