package resetstate

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
)

const MaxRecordBytes = 64 * 1024

type Record string

const (
	OperationRecord Record = "operation.json"
	PolicyRecord    Record = "policy.json"
)

var ErrRecordLimit = errors.New("reset metadata exceeds its bounded size limit")
var ErrDurabilityUnknown = errors.New("reset metadata durability is unknown; keep ordinary mutations fenced and inspect recovery on relaunch")

func validRecord(record Record) bool { return record == OperationRecord || record == PolicyRecord }

// Read returns (nil, nil) only for an absent record. An empty existing record is
// non-nil and must fail schema validation. Bytes are never executable targets;
// the reset coordinator must strictly decode/version/revalidate their meaning.
func (l *Lease) Read(record Record) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !validRecord(record) {
		return nil, ErrUnsafeState
	}
	if err := l.checkLocked(); err != nil {
		return nil, err
	}
	info, err := l.state.Lstat(string(record))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !privateEntry(info, false) {
		return nil, ErrUnsafeState
	}
	if info.Size() > MaxRecordBytes {
		return nil, ErrRecordLimit
	}
	file, err := l.state.OpenFile(string(record), os.O_RDONLY|noFollowFlag(), 0)
	if err != nil {
		return nil, ErrUnsafeState
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrUnsafeState
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxRecordBytes+1))
	if err != nil {
		return nil, ErrUnsafeState
	}
	if len(data) > MaxRecordBytes {
		return nil, ErrRecordLimit
	}
	return data, l.checkLocked()
}

// Replace durably replaces only a named reset record, never arbitrary paths.
// The caller validates the secret-free payload before writing. A failure after
// rename is NOT rollback: the caller must remain fenced and report uncertainty.
// Orphan temporary files remain recovery evidence after a process crash.
func (l *Lease) Replace(record Record, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !validRecord(record) || len(data) == 0 {
		return ErrUnsafeState
	}
	if len(data) > MaxRecordBytes {
		return ErrRecordLimit
	}
	if err := l.checkLocked(); err != nil {
		return err
	}
	if info, err := l.state.Lstat(string(record)); err == nil {
		if !privateEntry(info, false) {
			return ErrUnsafeState
		}
	} else if !os.IsNotExist(err) {
		return ErrUnsafeState
	}
	temp := ".pending-" + rand.Text()
	file, err := l.state.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollowFlag(), 0o600)
	if err != nil {
		return ErrUnsafeState
	}
	defer func() { _ = file.Close(); _ = l.state.Remove(temp) }()
	if _, err := file.Write(data); err != nil {
		return ErrUnsafeState
	}
	if err := file.Sync(); err != nil {
		return ErrUnsafeState
	}
	if err := file.Close(); err != nil {
		return ErrUnsafeState
	}
	if err := l.checkLocked(); err != nil {
		return err
	}
	if err := l.state.Rename(temp, string(record)); err != nil {
		return ErrUnsafeState
	}
	if err := l.syncMetadata(l.state); err != nil {
		return ErrDurabilityUnknown
	}
	if err := l.checkLocked(); err != nil {
		return ErrDurabilityUnknown
	}
	return nil
}

// RequireCleanStart is the transitional pre-start boundary: until a coordinator
// can interpret/apply/verify the journal, ANY metadata besides the lock fences
// constructors. Missing, corrupt, future and orphan receipts are never repaired,
// deleted or interpreted as an empty installation. Inspection is bounded and
// never opens app files or reads unknown metadata contents.
func (l *Lease) RequireCleanStart() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requireCleanStartLocked()
}

func (l *Lease) requireCleanStartLocked() error {
	if err := l.checkLocked(); err != nil {
		return err
	}
	file, err := l.state.Open(".")
	if err != nil {
		return ErrUnsafeState
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return ErrUnsafeState
	}
	if len(entries) != 1 || entries[0].Name() != lockName {
		return ErrRecoveryRequired
	}
	return nil
}

// BeforeStores is called once by supported process entry points BEFORE the
// menubar shell's settings manager or the server builder. It deliberately pins
// the lease for process lifetime, not server lifetime: existing shutdown paths
// do not join all writers. No defer Close is safe after constructors start.
// Unsupported hosts can run normally only without prior reset metadata; reset
// stays unavailable. They must never ignore a pending operation or import policy.
func BeforeStores(dataDir string) (*Lease, error) {
	if !Supported() {
		root, err := os.OpenRoot(dataDir)
		if err != nil {
			return nil, ErrUnsafeState
		}
		defer func() { _ = root.Close() }()
		if _, err := root.Lstat(Directory); os.IsNotExist(err) {
			return nil, nil
		}
		return nil, ErrRecoveryRequired
	}
	lease, err := Acquire(dataDir)
	if err != nil {
		return nil, err
	}
	if err := lease.RequireCleanStart(); err != nil {
		_ = lease.Close()
		return nil, err
	}
	if err := lease.HoldForProcess(); err != nil {
		_ = lease.Close()
		return nil, err
	}
	return lease, nil
}
