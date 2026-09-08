// Package resetstate owns the installation lease, bounded reset metadata and
// cooperating-work admission primitive. It opens no application stores, discovers
// no credentials and starts no services.
package resetstate

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const Directory = ".ori-reset"
const lockName = "process.lock"

var (
	ErrInUse            = errors.New("another Ori process owns this installation; fully quit it before relaunching")
	ErrUnsafeState      = errors.New("reset ownership metadata is unsafe or unavailable; preserve it and restore owner-only access")
	ErrUnsupported      = errors.New("this platform does not support the reset ownership boundary")
	ErrClosed           = errors.New("reset installation lease is closed")
	ErrPinned           = errors.New("reset lease is held until process exit")
	ErrRecoveryRequired = errors.New("reset recovery is required before opening application stores; preserve .ori-reset and use a compatible Ori version")
)

// Lease must outlive every store, callback and child dispatcher in this process.
// Supported entry points call HoldForProcess before constructing any stores.
// Close is only for tests and abandoned startup BEFORE opening application stores.
// Never unlink process.lock: its stable inode is the cooperating-process lock.
type Lease struct {
	mu               sync.Mutex
	data             *os.Root
	state            *os.Root
	lock             *os.File
	path             string
	identity         string
	pinned           bool
	admission        chan struct{}
	uncertain        bool
	expectsOperation bool
	recoveryReady    bool     // Set only after the versioned owner verifies completion.
	work             WorkGate // Host lifetime, never replaced on server Stop/Start.
	// Filesystem durability seam, not an HTTP/runtime fault-injection option.
	syncMetadata func(*os.Root) error
}

var processLeases struct {
	sync.Mutex
	leases []*Lease // Keep descriptors alive even after the host's last local use.
}

// Acquire requires an existing, non-root installation directory. Aliases resolve
// to the same physical lock. It refuses linked/replaced/private-state entries,
// non-regular lock files and unsafe permissions rather than repairing them.
func Acquire(path string) (*Lease, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if strings.TrimSpace(path) == "" {
		return nil, ErrUnsafeState
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnsafeState
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil || filepath.Dir(path) == path {
		return nil, ErrUnsafeState
	}
	data, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrUnsafeState
	}
	lease := &Lease{data: data, path: path, identity: rand.Text(), syncMetadata: syncDirectory, admission: make(chan struct{}, 1)}
	ok := false
	defer func() {
		if !ok {
			_ = lease.Close()
		}
	}()
	if err := data.Mkdir(Directory, 0o700); err != nil && !os.IsExist(err) {
		return nil, ErrUnsafeState
	}
	info, err := data.Lstat(Directory)
	if err != nil || !info.IsDir() || !privateEntry(info, true) {
		return nil, ErrUnsafeState
	}
	lease.state, err = data.OpenRoot(Directory)
	if err != nil {
		return nil, ErrUnsafeState
	}
	actual, err := lease.state.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return nil, ErrUnsafeState
	}
	if existing, err := lease.state.Lstat(lockName); err == nil {
		if !privateEntry(existing, false) {
			return nil, ErrUnsafeState
		}
	} else if !os.IsNotExist(err) {
		return nil, ErrUnsafeState
	}
	// Exclusive creation avoids the concurrent O_CREAT open race on APFS.
	// Existing locks are opened without creation, truncation or replacement.
	lease.lock, err = lease.state.OpenFile(lockName, os.O_CREATE|os.O_EXCL|os.O_RDWR|noFollowFlag(), 0o600)
	if os.IsExist(err) {
		lease.lock, err = lease.state.OpenFile(lockName, os.O_RDWR|noFollowFlag(), 0)
	}
	if err != nil {
		return nil, ErrUnsafeState
	}
	if err := lease.checkLocked(); err != nil {
		return nil, err
	}
	if err := lockExclusive(lease.lock); err != nil {
		return nil, err
	}
	if err := lease.lock.Sync(); err != nil {
		return nil, ErrUnsafeState
	}
	if err := syncDirectory(lease.state); err != nil {
		return nil, ErrUnsafeState
	}
	// Always sync the parent: the process that created the directory may have
	// lost the lock race (or crashed) before making its entry durable.
	if err := syncDirectory(data); err != nil {
		return nil, ErrUnsafeState
	}
	if err := lease.checkLocked(); err != nil {
		return nil, err
	}
	ok = true
	return lease, nil
}

// WithAdmission serializes coordinator instances sharing this process lease.
// Status reads do not take this gate and cannot restart the callback.
func (l *Lease) WithAdmission(ctx context.Context, fn func() error) error {
	select {
	case l.admission <- struct{}{}:
		defer func() { <-l.admission }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.Check(); err != nil {
		return err
	}
	return fn()
}

// MarkUncertain latches recovery and rejects new instrumented work even if a
// lifecycle panicked before returning its fence result. Already admitted work
// is not cancelled; this is not proof of draining or permission to apply.
func (l *Lease) MarkUncertain() {
	l.mu.Lock()
	l.uncertain = true
	l.work.blockNewWork()
	l.mu.Unlock()
}
func (l *Lease) Uncertain() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.uncertain }

// ExpectOperation remembers admitted evidence across coordinator reconstruction.
// Deleting its file cannot turn this running process back into a fresh runtime.
func (l *Lease) ExpectOperation()       { l.mu.Lock(); l.expectsOperation = true; l.mu.Unlock() }
func (l *Lease) ExpectsOperation() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.expectsOperation }

// AuthorizeRecoveredRuntime is the narrow handoff from the versioned reset
// owner after it has independently reapplied and verified a complete receipt.
// resetstate cannot interpret that private schema; callers must never invoke
// this for blocked, partial, unknown, or merely staged operations.
func (l *Lease) AuthorizeRecoveredRuntime() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkLocked(); err != nil {
		return err
	}
	if !l.expectsOperation || l.uncertain {
		return ErrRecoveryRequired
	}
	l.recoveryReady = true
	return nil
}

func (l *Lease) Path() string { return l.path }

// Identity is a launch nonce, never a PID or a client-supplied value. Pinning
// prevents a same-process close/acquire cycle from becoming a new reset launch.
func (l *Lease) Identity() string { return l.identity }

func (l *Lease) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checkLocked()
}

func (l *Lease) checkLocked() error {
	if l.lock == nil || l.state == nil || l.data == nil {
		return ErrClosed
	}
	rootInfo, err := os.Stat(l.path)
	if err != nil {
		return ErrUnsafeState
	}
	dataInfo, err := l.data.Stat(".")
	if err != nil || !os.SameFile(rootInfo, dataInfo) {
		return ErrUnsafeState
	}
	stateInfo, err := l.data.Lstat(Directory)
	if err != nil || !privateEntry(stateInfo, true) {
		return ErrUnsafeState
	}
	openState, err := l.state.Stat(".")
	if err != nil || !os.SameFile(stateInfo, openState) {
		return ErrUnsafeState
	}
	lockInfo, err := l.state.Lstat(lockName)
	if err != nil || !privateEntry(lockInfo, false) || lockInfo.Size() != 0 {
		return ErrUnsafeState
	}
	openLock, err := l.lock.Stat()
	if err != nil || !os.SameFile(lockInfo, openLock) {
		return ErrUnsafeState
	}
	return nil
}

func (l *Lease) HoldForProcess() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkLocked(); err != nil {
		return err
	}
	l.holdForProcessLocked()
	return nil
}

func (l *Lease) holdForProcessLocked() {
	if !l.pinned {
		processLeases.Lock()
		processLeases.leases = append(processLeases.leases, l)
		processLeases.Unlock()
		l.pinned = true
	}
}

func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pinned {
		return ErrPinned
	}
	var errs []error
	if l.lock != nil {
		errs = append(errs, l.lock.Close())
		l.lock = nil
	}
	if l.state != nil {
		errs = append(errs, l.state.Close())
		l.state = nil
	}
	if l.data != nil {
		errs = append(errs, l.data.Close())
		l.data = nil
	}
	return errors.Join(errs...)
}

func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	return errors.Join(syncErr, file.Close())
}
