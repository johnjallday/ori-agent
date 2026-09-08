package resetstate

import (
	"errors"
	"os"
	"strings"
)

var ErrRuntimeRootMismatch = errors.New("runtime directories do not identify the owned installation; fully quit Ori and relaunch with matching working and data directories")

// WorkGate is the one cooperating-work gate for every runtime and shell writer
// using this process lease. Access alone is not a Lifecycle or ownership check.
// A nil lease (an unsupported/alternate host) has no host admission capability.
func (l *Lease) WorkGate() *WorkGate {
	if l == nil {
		return nil
	}
	return &l.work
}

// EnterRuntime guards construction BEFORE any application owner is opened.
// Both activated roots must identify the leased installation; no environment
// default, current directory, or recovered receipt path substitutes for it.
// The caller holds the permit until all synchronous construction has finished,
// including when its requesting context times out. Successful entry pins the
// lease until process exit, so this API cannot open stores then release the lock.
//
// A versioned reset owner may authorize a runtime only after validated pre-start
// application completed. Same-process Stop/Start cannot authorize recovery or
// bypass a fence, uncertainty, missing receipt, or partial/blocked result.
func (l *Lease) EnterRuntime(workingDir, dataDir string) (func(), error) {
	if l == nil {
		return nil, ErrWorkUntracked
	}
	release, err := l.work.Enter()
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.uncertain || l.expectsOperation && !l.recoveryReady {
		return nil, ErrRecoveryRequired
	}
	if !l.recoveryReady {
		if err := l.requireCleanStartLocked(); err != nil {
			return nil, err
		}
	}
	if err := l.checkLocked(); err != nil {
		return nil, err
	}
	root, err := l.data.Stat(".")
	if err != nil {
		return nil, ErrUnsafeState
	}
	for _, path := range []string{workingDir, dataDir} {
		if strings.TrimSpace(path) == "" {
			return nil, ErrRuntimeRootMismatch
		}
		actual, err := os.Stat(path)
		if err != nil || !actual.IsDir() || !os.SameFile(root, actual) {
			return nil, ErrRuntimeRootMismatch
		}
	}
	// Releasing admission is NEVER releasing process ownership.
	l.holdForProcessLocked()
	ok = true
	return release, nil
}
