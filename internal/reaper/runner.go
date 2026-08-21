package reaper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

const (
	maxRunnerScriptBytes  = 1 << 20
	maxRunnerStatusBytes  = 64 << 10
	maxRunnerReceiptBytes = 8 << 10
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

// Available reports whether the runner exchange is installed and ready. The
// console uses it to degrade track editing to a read-only list rather than
// offering controls that cannot work.
func (r *Runner) Available(ctx context.Context) bool {
	if r == nil || r.roots == nil || r.probe == nil {
		return false
	}
	observation := r.probe.DetectRunner(ctx)
	if observation.State != reapersetup.ProbeReady || !validExecutableCommandID(observation.CommandID) {
		return false
	}
	root, err := r.roots.Resolve()
	return err == nil && filepath.Clean(root) == filepath.Clean(observation.Root)
}

func (r *Runner) RunScript(ctx context.Context, lua string) (ScriptRunResult, error) {
	result, _, err := r.run(ctx, func(string) (string, error) { return lua, nil }, false)
	return result, err
}

// RunTrackEdit generates the guarded Lua for one single-track edit, runs it,
// and returns the receipt the script wrote. The receipt — not the runner
// status — says whether the project actually changed, because a guard that
// deliberately refuses still leaves the script itself reporting ok. The
// receipt path is resolved here and never leaves this package.
func (r *Runner) RunTrackEdit(ctx context.Context, edit TrackEdit) (EditReceipt, error) {
	if err := edit.Validate(); err != nil {
		return EditReceipt{}, err
	}
	_, raw, err := r.run(ctx, edit.Lua, true)
	if err != nil {
		return EditReceipt{}, err
	}
	return ParseEditReceipt(raw)
}

// RunBulkPlan runs a whole guarded bulk plan as one script and returns the
// receipt it wrote: how many edits applied, or which original track indices
// refused their guard. Like RunTrackEdit, the runner reporting ok only means
// the script ran cleanly — the receipt is the authority on whether the
// project actually changed.
func (r *Runner) RunBulkPlan(ctx context.Context, plan BulkPlan) (BulkReceipt, error) {
	if err := plan.Validate(); err != nil {
		return BulkReceipt{}, err
	}
	_, raw, err := r.run(ctx, plan.Lua, true)
	if err != nil {
		return BulkReceipt{}, err
	}
	return ParseBulkReceipt(raw)
}

func (r *Runner) run(ctx context.Context, build func(receiptPath string) (string, error), wantReceipt bool) (ScriptRunResult, []byte, error) {
	if r == nil || r.roots == nil || r.probe == nil || r.client == nil || build == nil {
		return ScriptRunResult{Outcome: "error"}, nil, ErrRunnerUnavailable
	}
	if _, reason := r.client.resolve(ctx); reason != "" {
		return ScriptRunResult{Outcome: "error", ErrorText: "REAPER is not connected. Nothing was run."}, nil, ErrActionDisconnected
	}
	observation := r.probe.DetectRunner(ctx)
	if observation.State != reapersetup.ProbeReady || !validExecutableCommandID(observation.CommandID) {
		return ScriptRunResult{Outcome: "error"}, nil, ErrRunnerUnavailable
	}
	root, err := r.roots.Resolve()
	if err != nil || filepath.Clean(root) != filepath.Clean(observation.Root) {
		return ScriptRunResult{Outcome: "error"}, nil, ErrRunnerUnavailable
	}

	receiptPath := filepath.Join(root, receiptFileName)
	lua, err := build(receiptPath)
	if err != nil || strings.TrimSpace(lua) == "" || len(lua) > maxRunnerScriptBytes {
		return ScriptRunResult{Outcome: "error"}, nil, ErrRunnerUnavailable
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	inboxPath := filepath.Join(root, "inbox.lua")
	statusPath := filepath.Join(root, "last_status.txt")
	if err := removeRunnerStatus(statusPath, root); err != nil {
		return ScriptRunResult{Outcome: "error"}, nil, err
	}
	// Clear the receipt inside the same lock as the status file, so a stale
	// receipt from an earlier run can never be read as this run's result.
	if wantReceipt {
		if err := removeRunnerStatus(receiptPath, root); err != nil {
			return ScriptRunResult{Outcome: "error"}, nil, err
		}
	}
	if err := atomicRunnerWrite(root, inboxPath, []byte(lua)); err != nil {
		return ScriptRunResult{Outcome: "error"}, nil, err
	}
	port, reason := r.client.resolve(ctx)
	if reason != "" {
		return ScriptRunResult{Outcome: "error", ErrorText: "REAPER is not connected. Nothing was run."}, nil, ErrActionDisconnected
	}
	if _, err := r.client.get(ctx, port, observation.CommandID); err != nil {
		return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner did not accept the script."}, nil, ErrRunnerFailed
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
			if result.Outcome != "ok" {
				return result, nil, ErrRunnerFailed
			}
			if !wantReceipt {
				return result, nil, nil
			}
			raw, err := readRunnerReceiptBytes(receiptPath)
			if err != nil {
				return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER edit did not report a result."}, nil, ErrRunnerFailed
			}
			return result, raw, nil
		}
		select {
		case <-waitCtx.Done():
			return ScriptRunResult{Outcome: "error", ErrorText: "The REAPER runner timed out."}, nil, ErrRunnerTimedOut
		case <-ticker.C:
		}
	}
}

// readRunnerReceiptBytes validates the receipt exactly like last_status.txt is
// validated: a regular file, no symlink, at a fixed path under the canonical
// runner root, with a bounded size. Parsing into EditReceipt or BulkReceipt is
// the caller's job — both shapes share this one validated read.
func readRunnerReceiptBytes(path string) ([]byte, error) {
	info, err := os.Lstat(path) // #nosec G304 -- fixed receipt path under canonical runner root
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxRunnerReceiptBytes {
		return nil, ErrInvalidReceipt
	}
	data, err := os.ReadFile(path) // #nosec G304 -- bounded regular exchange receipt file
	if err != nil {
		return nil, ErrInvalidReceipt
	}
	return data, nil
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
	if _, err := temp.Write(data); err != nil {
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
