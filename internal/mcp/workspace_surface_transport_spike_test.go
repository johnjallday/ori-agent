package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const workspaceSurfaceFakeEventLogEnv = "ORI_WORKSPACE_SURFACE_FAKE_EVENT_LOG"

type workspaceSurfaceFakeOutput struct {
	PID       int   `json:"pid"`
	Calls     int64 `json:"calls"`
	Active    int64 `json:"active"`
	MaxActive int64 `json:"max_active"`
	Canceled  int64 `json:"canceled"`
}

type workspaceSurfaceFakeEvent struct {
	Type      string `json:"type"`
	AtUnixNS  int64  `json:"at_unix_ns"`
	PID       int    `json:"pid"`
	Calls     int64  `json:"calls"`
	Active    int64  `json:"active"`
	MaxActive int64  `json:"max_active"`
	Canceled  int64  `json:"canceled"`
}

// TestWorkspaceSurfaceMCPTransportSpike exercises Ori's real Registry, Server,
// SDK CommandTransport, and a real hermetic child process. It intentionally
// characterizes the gaps a Workspace Surface service manager must close rather
// than hiding them behind a fake Registry implementation.
func TestWorkspaceSurfaceMCPTransportSpike(t *testing.T) {
	if testing.Short() {
		t.Skip("transport spike builds and launches a hermetic MCP child process")
	}

	binary := buildWorkspaceSurfaceFake(t)
	eventLog := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(mcpHealthCheckIntervalEnvVar, "25ms")

	registry := NewRegistry()
	const serverName = "workspace-surface-transport-spike"
	if err := registry.AddServer(ServerConfig{
		Name:      serverName,
		Command:   binary,
		Transport: TransportStdio,
		Enabled:   true,
		Env: map[string]string{
			workspaceSurfaceFakeEventLogEnv: eventLog,
		},
	}); err != nil {
		t.Fatalf("AddServer() error = %v", err)
	}
	t.Cleanup(func() { _ = registry.StopAll() })

	// Registry.CallTool has no lazy-start behavior. This is a measured contract,
	// not a desired service-manager behavior.
	if _, err := registry.CallTool(context.Background(), serverName, "probe", nil); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("CallTool() before StartServer error = %v, want not-running refusal", err)
	}

	startedAt := time.Now()
	if err := registry.StartServer(serverName); err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	startupLatency := time.Since(startedAt)

	// Five reads at a one-second cadence prove the process/session can sustain
	// the live-console path without reconnecting or leaking subprocesses.
	cadenceLatencies := make([]time.Duration, 0, 5)
	for index := 0; index < 5; index++ {
		if index > 0 {
			time.Sleep(time.Second)
		}
		callStarted := time.Now()
		if _, err := registry.CallTool(context.Background(), serverName, "probe", map[string]any{}); err != nil {
			t.Fatalf("one-second probe %d error = %v", index+1, err)
		}
		cadenceLatencies = append(cadenceLatencies, time.Since(callStarted))
	}

	// MCP's SDK session supports concurrent calls. The existing Registry does
	// not impose a bound, so all calls can enter the process simultaneously.
	const concurrentCalls = 12
	concurrentStarted := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, concurrentCalls)
	for index := 0; index < concurrentCalls; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := registry.CallTool(context.Background(), serverName, "probe", map[string]any{"delay_ms": 100})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent CallTool() error = %v", err)
		}
	}
	concurrentElapsed := time.Since(concurrentStarted)

	statsResult, err := registry.CallTool(context.Background(), serverName, "probe", map[string]any{})
	if err != nil {
		t.Fatalf("stats probe error = %v", err)
	}
	stats := decodeWorkspaceSurfaceFakeOutput(t, statsResult.StructuredContent)
	if stats.MaxActive < 2 {
		t.Fatalf("max active calls = %d, want proof of request concurrency", stats.MaxActive)
	}

	// Caller cancellation reaches the service handler.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	cancelStarted := time.Now()
	_, cancelErr := registry.CallTool(cancelCtx, serverName, "probe", map[string]any{"delay_ms": 5000})
	cancelLatency := time.Since(cancelStarted)
	if !errors.Is(cancelErr, context.DeadlineExceeded) {
		t.Fatalf("canceled CallTool() error = %v, want context deadline", cancelErr)
	}
	waitForWorkspaceSurfaceEvent(t, eventLog, "call_canceled", 1, time.Second)

	// There is no default per-call timeout in Registry/Server. A background call
	// runs for the full service-selected delay; a future manager must wrap every
	// call in a declared host timeout class.
	unboundedStarted := time.Now()
	if _, err := registry.CallTool(context.Background(), serverName, "probe", map[string]any{"delay_ms": 200}); err != nil {
		t.Fatalf("background delayed CallTool() error = %v", err)
	}
	unboundedElapsed := time.Since(unboundedStarted)
	if unboundedElapsed < 175*time.Millisecond {
		t.Fatalf("background call returned in %v, want proof it had no shorter host timeout", unboundedElapsed)
	}

	// Stop closes the session/process promptly and leaves a stable stopped state.
	stopStarted := time.Now()
	if err := registry.StopServer(serverName); err != nil {
		t.Fatalf("StopServer() error = %v", err)
	}
	stopLatency := time.Since(stopStarted)
	if status, err := registry.GetServerStatus(serverName); err != nil || status != StatusStopped {
		t.Fatalf("status after stop = %q, %v; want stopped", status, err)
	}
	waitForWorkspaceSurfaceEvent(t, eventLog, "stop", 1, time.Second)

	// Start again, crash, and observe the health loop. No automatic restart is
	// attempted by Registry/Server; restart is explicit and unbudgeted.
	if err := registry.StartServer(serverName); err != nil {
		t.Fatalf("second StartServer() error = %v", err)
	}
	if _, err := registry.CallTool(context.Background(), serverName, "crash", map[string]any{}); err == nil {
		t.Fatal("crash CallTool() error = nil, want broken stdio session")
	}
	waitForMCPStatus(t, registry, serverName, StatusError, time.Second)
	time.Sleep(100 * time.Millisecond)
	if starts := countWorkspaceSurfaceEvents(t, eventLog, "start"); starts != 2 {
		t.Fatalf("start events after crash = %d, want no automatic restart", starts)
	}

	restartStarted := time.Now()
	restartErr := registry.RestartServer(serverName)
	restartLatency := time.Since(restartStarted)
	// The current Restart path reports the already-exited child status while it
	// closes the broken session. Stop has still canceled/cleared the session, so
	// a separate Start can recover it. A surface manager must absorb this process
	// exit detail and own a one-restart budget instead of exposing it to callers.
	if restartErr == nil || !strings.Contains(restartErr.Error(), "exit status 23") {
		t.Fatalf("RestartServer() error = %v, want characterized exited-child failure", restartErr)
	}
	recoveryStarted := time.Now()
	if err := registry.StartServer(serverName); err != nil {
		t.Fatalf("StartServer() after failed explicit restart error = %v", err)
	}
	recoveryLatency := time.Since(recoveryStarted)
	if _, err := registry.CallTool(context.Background(), serverName, "probe", map[string]any{}); err != nil {
		t.Fatalf("probe after bounded recovery start error = %v", err)
	}

	// A second crash also remains down. The existing path has no one-restart
	// budget or cooldown; that policy belongs in the new service manager.
	if _, err := registry.CallTool(context.Background(), serverName, "crash", map[string]any{}); err == nil {
		t.Fatal("second crash CallTool() error = nil")
	}
	waitForMCPStatus(t, registry, serverName, StatusError, time.Second)
	time.Sleep(100 * time.Millisecond)
	if starts := countWorkspaceSurfaceEvents(t, eventLog, "start"); starts != 3 {
		t.Fatalf("start events after second crash = %d, want no restart loop", starts)
	}

	median, p95 := latencySummary(cadenceLatencies)
	t.Logf("workspace-surface MCP findings: startup=%v cadence_median=%v cadence_p95=%v cadence_samples=%d concurrent_calls=%d concurrent_elapsed=%v max_active=%d cancel_latency=%v background_200ms_elapsed=%v stop=%v restart_reported_error=%q restart_attempt=%v bounded_recovery_start=%v",
		startupLatency, median, p95, len(cadenceLatencies), concurrentCalls,
		concurrentElapsed, stats.MaxActive, cancelLatency, unboundedElapsed,
		stopLatency, restartErr, restartLatency, recoveryLatency)
}

func BenchmarkWorkspaceSurfaceMCPStatusCall(b *testing.B) {
	binary := buildWorkspaceSurfaceFake(b)
	eventLog := filepath.Join(b.TempDir(), "events.jsonl")
	registry := NewRegistry()
	const serverName = "workspace-surface-status-benchmark"
	if err := registry.AddServer(ServerConfig{
		Name: serverName, Command: binary, Transport: TransportStdio, Enabled: true,
		Env: map[string]string{workspaceSurfaceFakeEventLogEnv: eventLog},
	}); err != nil {
		b.Fatal(err)
	}
	if err := registry.StartServer(serverName); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = registry.StopAll() })

	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := registry.CallTool(context.Background(), serverName, "probe", map[string]any{}); err != nil {
			b.Fatal(err)
		}
	}
}

func buildWorkspaceSurfaceFake(tb testing.TB) string {
	tb.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("runtime.Caller could not resolve the module root")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	binary := filepath.Join(tb.TempDir(), "workspace-surface-mcp-fake")
	cmd := exec.Command("go", "build", "-o", binary, "./internal/mcp/testdata/workspacesurfacefake") // #nosec G204 -- fixed Go tool and package; only output is a private test path
	cmd.Dir = moduleRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		tb.Fatalf("build fake MCP service: %v\n%s", err, output.String())
	}
	return binary
}

func decodeWorkspaceSurfaceFakeOutput(t *testing.T, raw any) workspaceSurfaceFakeOutput {
	t.Helper()
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal structured output: %v", err)
	}
	var output workspaceSurfaceFakeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("decode structured output %s: %v", data, err)
	}
	return output
}

func waitForMCPStatus(t *testing.T, registry *Registry, name string, want ServerStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := registry.GetServerStatus(name)
		if err == nil && status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err := registry.GetServerStatus(name)
	t.Fatalf("server status = %q, %v; want %q within %v", status, err, want, timeout)
}

func waitForWorkspaceSurfaceEvent(t *testing.T, path, eventType string, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countWorkspaceSurfaceEvents(t, path, eventType) >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event %q count = %d, want at least %d within %v", eventType, countWorkspaceSurfaceEvents(t, path, eventType), count, timeout)
}

func countWorkspaceSurfaceEvents(t *testing.T, path, eventType string) int {
	t.Helper()
	count := 0
	for _, event := range readWorkspaceSurfaceEvents(t, path) {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func readWorkspaceSurfaceEvents(t *testing.T, path string) []workspaceSurfaceFakeEvent {
	t.Helper()
	file, err := os.Open(path) // #nosec G304 -- private test-selected event log
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("open fake event log: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close fake event log: %v", err)
		}
	}()

	var events []workspaceSurfaceFakeEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event workspaceSurfaceFakeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode fake event %q: %v", scanner.Text(), err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fake event log: %v", err)
	}
	return events
}

func latencySummary(samples []time.Duration) (median, p95 time.Duration) {
	if len(samples) == 0 {
		return 0, 0
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	median = ordered[len(ordered)/2]
	p95Index := (len(ordered)*95 + 99) / 100
	if p95Index < 1 {
		p95Index = 1
	}
	if p95Index > len(ordered) {
		p95Index = len(ordered)
	}
	return median, ordered[p95Index-1]
}
