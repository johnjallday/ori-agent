package backuprecovery

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

const (
	maxReadStderrBytes = 16 << 10
	providerReadLimit  = 20 * time.Second
	scopedQueryLimit   = 30 * time.Second
	completeReadLimit  = 120 * time.Second
	childDrainLimit    = 2 * time.Second
)

var (
	errReadProcess          = errors.New("backup metadata process is unavailable")
	errReadProcessLimit     = errors.New("backup metadata process exceeded output limits")
	errReadProcessDeadline  = errors.New("backup metadata process timed out")
	errReadProcessCancelled = errors.New("backup metadata process was cancelled")
)

// readProcessSpec is a private composition detail, never an API/template/model
// DTO. Callers must select a compiled operation and validate executable trust and
// containment before using it. This runner alone is NOT a provider allowlist or
// sandbox. No production caller is wired yet.
type readProcessSpec struct {
	executable  string
	arguments   []string
	environment []string
	directory   string
	input       []byte
	stdoutLimit int
	timeout     time.Duration
}

type cappedReadOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (output *cappedReadOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if len(data) > output.limit-output.buffer.Len() {
		output.overflow = true
		output.cancel()
		return 0, errReadProcessLimit
	}
	return output.buffer.Write(data)
}

func (output *cappedReadOutput) result() ([]byte, bool) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return bytes.Clone(output.buffer.Bytes()), output.overflow
}

func runReadProcess(parent context.Context, spec readProcessSpec) ([]byte, error) {
	if parent == nil || spec.executable == "" || spec.directory == "" || spec.environment == nil ||
		spec.stdoutLimit < 1 || spec.stdoutLimit > maxBrokerOutputBytes ||
		spec.timeout <= 0 || spec.timeout > completeReadLimit {
		return nil, errReadProcess
	}
	if parent.Err() != nil {
		return nil, errReadProcessCancelled
	}
	ctx, cancel := context.WithTimeout(parent, spec.timeout)
	defer cancel()
	// #nosec G204 -- private runner: executable/operation are selected by compiled host composition, never exposed as request/template/model fields; callers must enforce trust and containment.
	cmd := exec.CommandContext(ctx, spec.executable, spec.arguments...)
	cmd.Dir, cmd.Env = spec.directory, append([]string{}, spec.environment...)
	cmd.Stdin = bytes.NewReader(spec.input)
	cmd.WaitDelay = childDrainLimit
	if !configureReadProcess(cmd) {
		return nil, errReadProcess
	}
	stdout := &cappedReadOutput{limit: spec.stdoutLimit, cancel: cancel}
	stderr := &cappedReadOutput{limit: maxReadStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run() // waits/reaps the owned child and joins bounded pipe copies
	if cmd.Process != nil {
		// A parent can exit while a descendant retains a pipe. WaitDelay bounds
		// the copy wait; also stop any remaining members of our process group.
		_ = cmd.Cancel()
	}
	out, outOverflow := stdout.result()
	diagnostic, errOverflow := stderr.result()
	defer clear(diagnostic)
	if outOverflow || errOverflow {
		clear(out)
		return nil, errReadProcessLimit
	}
	if parent.Err() != nil {
		clear(out)
		return nil, errReadProcessCancelled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		clear(out)
		return nil, errReadProcessDeadline
	}
	if err != nil || len(diagnostic) != 0 {
		clear(out)
		return nil, errReadProcess
	}
	return out, nil
}
