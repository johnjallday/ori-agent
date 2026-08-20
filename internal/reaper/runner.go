package reaper

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

const (
	maxRunnerScriptBytes = 1 << 20
	maxRunnerStatusBytes = 64 << 10
)

var (
	ErrRunnerUnavailable = errors.New("REAPER script runner is unavailable")
	ErrRunnerFailed      = errors.New("REAPER script runner failed")
	ErrRunnerTimedOut    = errors.New("REAPER script runner timed out")
)

type ScriptRunResult struct {
	Outcome   string `json:"outcome"`
	ErrorText string `json:"error_text,omitempty"`
}

// Runner executes Lua through the installed Ori runner exchange. It serializes
// writes because inbox.lua and last_status.txt are one global buffer shared by
// every REAPER workspace.
type Runner struct {
	roots   reapersetup.RunnerRootResolver
	probe   reapersetup.RunnerProbe
	client  *Client
	timeout time.Duration
	mu      sync.Mutex
}

func NewRunner(roots reapersetup.RunnerRootResolver, probes reapersetup.ProbeSet, client *Client) *Runner {
	return &Runner{roots: roots, probe: probes.Runner, client: client, timeout: 8 * time.Second}
}

func (r *Runner) RunScript(ctx context.Context, lua string) (ScriptRunResult, error) {
	if r == nil || r.roots == nil || r.probe == nil || r.client == nil || strings.TrimSpace(lua) == "" || len(lua) > maxRunnerScriptBytes {
		return ScriptRunResult{Outcome: "error"}, ErrRunnerUnavailable
	}
	if _, reason := r.client.resolve(ctx); reason != "" {
		return ScriptRunResult{Outcome: "error", ErrorText: "REAPER is not connected. Nothing was run."}, ErrActionDisconnected
	}
	observation := r.probe.DetectRunner(ctx)
	if observation.State != reapersetup.ProbeReady || !validExecutableCommandID(observation.CommandID) {
		return ScriptRunResult{Outcome: "error"}, ErrRunnerUnavailable
	}
	root, err := r.roots.Resolve()
	if err != nil || filepath.Clean(root) != filepath.Clean(observation.Root) {
		return ScriptRunResult{Outcome: "error"}, ErrRunnerUnavailable
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	inboxPath := filepath.Join(root, "inbox.lua")
	statusPath := filepath.Join(root, "last_status.txt")
	if err := removeRunnerStatus(statusPath, root); err != nil {
		return ScriptRunResult{Outcome: "error"}, err
	}
	if err := atomicRunnerWrite(root, inboxPath, []byte(lua)); err != nil {
		return ScriptRunResult{Outcome: "error"}, err
	}
	port, reason := r.client.resolve(ctx)
	if reason != "" {
		return ScriptRunResult{Outcome: "error", ErrorText: "REAPER is not connected. Nothing was run."}, ErrActionDisconnected
	}
	if _, err := r.client.get(ctx, port, observation.CommandID); err != nil {
		return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner did not accept the script."}, ErrRunnerFailed
	}

	timeout := r.timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if timeout > 15*time.Second {
		timeout = 15 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, complete := readRunnerStatus(statusPath)
		if complete {
			if result.Outcome == "ok" {
				return result, nil
			}
			return result, ErrRunnerFailed
		}
		select {
		case <-waitCtx.Done():
			return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner timed out."}, ErrRunnerTimedOut
		case <-ticker.C:
		}
	}
}

func removeRunnerStatus(path, root string) error {
	if filepath.Dir(path) != filepath.Clean(root) {
		return ErrRunnerUnavailable
	}
	info, err := os.Lstat(path) // #nosec G304 -- fixed status path under canonical runner root
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrRunnerUnavailable
	}
	if err := os.Remove(path); err != nil { // #nosec G304 -- exact checked exchange status file
		return ErrRunnerUnavailable
	}
	return nil
}

func atomicRunnerWrite(root, destination string, data []byte) error {
	if filepath.Dir(destination) != filepath.Clean(root) {
		return ErrRunnerUnavailable
	}
	temp, err := os.CreateTemp(root, ".ori-run-*")
	if err != nil {
		return ErrRunnerUnavailable
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return ErrRunnerUnavailable
	}
	if _, err := io.WriteString(temp, string(data)); err != nil {
		_ = temp.Close()
		return ErrRunnerUnavailable
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return ErrRunnerUnavailable
	}
	if err := temp.Close(); err != nil {
		return ErrRunnerUnavailable
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return ErrRunnerUnavailable
	}
	return nil
}

func readRunnerStatus(path string) (ScriptRunResult, bool) {
	info, err := os.Lstat(path) // #nosec G304 -- fixed status path under canonical runner root
	if errors.Is(err, os.ErrNotExist) {
		return ScriptRunResult{}, false
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxRunnerStatusBytes {
		return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner returned an invalid status."}, true
	}
	data, err := os.ReadFile(path) // #nosec G304 -- bounded regular exchange status file
	if err != nil {
		return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner status could not be read."}, true
	}
	status := strings.TrimSpace(string(data))
	if status == "ok" {
		return ScriptRunResult{Outcome: "ok"}, true
	}
	if strings.HasPrefix(status, "error:") {
		message := strings.TrimSpace(strings.TrimPrefix(status, "error:"))
		if len(message) > 2000 {
			message = message[:2000]
		}
		if message == "" {
			message = "The REAPER runner reported an error."
		}
		return ScriptRunResult{Outcome: "error", ErrorText: message}, true
	}
	return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner returned an invalid status."}, true
}
