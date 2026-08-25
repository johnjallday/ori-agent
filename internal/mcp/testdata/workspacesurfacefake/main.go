// Command workspacesurfacefake is a hermetic MCP stdio process used to
// characterize Ori's existing long-lived process path for Workspace Surfaces.
// It has no network or filesystem authority beyond appending bounded JSON events
// to the test-selected log path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const eventLogEnv = "ORI_WORKSPACE_SURFACE_FAKE_EVENT_LOG"

type probeInput struct {
	DelayMS int `json:"delay_ms,omitempty"`
}

type probeOutput struct {
	PID       int   `json:"pid"`
	Calls     int64 `json:"calls"`
	Active    int64 `json:"active"`
	MaxActive int64 `json:"max_active"`
	Canceled  int64 `json:"canceled"`
}

type emptyInput struct{}

type event struct {
	Type      string `json:"type"`
	AtUnixNS  int64  `json:"at_unix_ns"`
	PID       int    `json:"pid"`
	Calls     int64  `json:"calls"`
	Active    int64  `json:"active"`
	MaxActive int64  `json:"max_active"`
	Canceled  int64  `json:"canceled"`
}

var (
	calls     atomic.Int64
	active    atomic.Int64
	maxActive atomic.Int64
	canceled  atomic.Int64
	logMu     sync.Mutex
)

func main() {
	appendEvent("start")
	defer appendEvent("stop")

	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "ori-workspace-surface-transport-fake",
		Version: "1.0.0",
	}, nil)

	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "probe",
		Description: "Returns counters after an optional cancellable delay.",
	}, probe)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "crash",
		Description: "Terminates the fake process to characterize crash handling.",
	}, crash)

	if err := server.Run(context.Background(), &sdkmcp.StdioTransport{}); err != nil {
		log.Printf("fake MCP server stopped: %v", err)
	}
}

func probe(ctx context.Context, _ *sdkmcp.CallToolRequest, input probeInput) (*sdkmcp.CallToolResult, probeOutput, error) {
	if input.DelayMS < 0 || input.DelayMS > 10000 {
		return nil, probeOutput{}, errors.New("delay_ms is outside the fake service limit")
	}
	calls.Add(1)
	current := active.Add(1)
	updateMax(current)
	appendEvent("call_start")
	defer func() {
		active.Add(-1)
		appendEvent("call_end")
	}()

	if input.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(input.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			canceled.Add(1)
			appendEvent("call_canceled")
			return nil, probeOutput{}, ctx.Err()
		}
	}

	return &sdkmcp.CallToolResult{}, snapshot(), nil
}

func crash(context.Context, *sdkmcp.CallToolRequest, emptyInput) (*sdkmcp.CallToolResult, probeOutput, error) {
	appendEvent("crash")
	// Exit from the handler so no successful protocol response can be mistaken
	// for a crash outcome. The fixed code is intentionally non-zero.
	os.Exit(23)
	return nil, probeOutput{}, errors.New("unreachable")
}

func updateMax(value int64) {
	for {
		old := maxActive.Load()
		if value <= old || maxActive.CompareAndSwap(old, value) {
			return
		}
	}
}

func snapshot() probeOutput {
	return probeOutput{
		PID:       os.Getpid(),
		Calls:     calls.Load(),
		Active:    active.Load(),
		MaxActive: maxActive.Load(),
		Canceled:  canceled.Load(),
	}
}

func appendEvent(kind string) {
	path := os.Getenv(eventLogEnv)
	if path == "" {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304,G703 -- test selects a private temporary path
	if err != nil {
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(event{
		Type:      kind,
		AtUnixNS:  time.Now().UnixNano(),
		PID:       os.Getpid(),
		Calls:     calls.Load(),
		Active:    active.Load(),
		MaxActive: maxActive.Load(),
		Canceled:  canceled.Load(),
	})
}
