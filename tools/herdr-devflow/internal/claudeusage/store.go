package claudeusage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Store persists and reads the usage records the Claude-side recorder captures.
//
// Records live in the protected user-local Herdr devflow runtime area, one file
// per Claude session, never in Git. Writes are atomic so a helper reading a
// record can never see a half-written one, and reads are bounded so a file that
// grew unexpectedly is refused rather than parsed.
type Store struct {
	// Dir is the usage directory inside the runtime root.
	Dir string
}

// NewStore builds a Store rooted at dir.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

// sessionIDPattern bounds what may become a filename. Session ids arrive from
// Claude, and a value that reaches a path join is untrusted input: this pattern
// is what stops one from escaping the usage directory.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ValidSessionID reports whether value is safe to use as a record name.
func ValidSessionID(value string) bool { return sessionIDPattern.MatchString(value) }

// ErrNoRecord means no record exists for the requested session. It is the
// normal state before the recorder is installed, and it is a refusal to
// proceed, not an error to retry.
var ErrNoRecord = errors.New("no Claude usage record exists for this session")

// maxRecordBytes bounds one persisted record.
const maxRecordBytes = 64 * 1024

// SaveSample writes the newest observation for a session, replacing any older
// one. Only the latest matters: this is a window's current state, not a log.
func (s *Store) SaveSample(sample Sample) error {
	if !ValidSessionID(sample.SessionID) {
		return fmt.Errorf("refusing to store a record for an unrecognized session id")
	}
	return s.write(s.samplePath(sample.SessionID), sample)
}

// SaveFailure writes the newest turn-stopped event for a session.
func (s *Store) SaveFailure(failure Failure) error {
	if !ValidSessionID(failure.SessionID) {
		return fmt.Errorf("refusing to store a record for an unrecognized session id")
	}
	return s.write(s.failurePath(failure.SessionID), failure)
}

// Sample reads the newest observation for a session.
func (s *Store) Sample(sessionID string) (Sample, error) {
	var sample Sample
	if err := s.read(s.samplePath(sessionID), sessionID, &sample); err != nil {
		return Sample{}, err
	}
	if sample.Version > RecordVersion {
		return Sample{}, fmt.Errorf("Claude usage record was written by a newer helper (version %d)", sample.Version)
	}
	if sample.SessionID != sessionID {
		return Sample{}, fmt.Errorf("Claude usage record describes a different session")
	}
	return sample, nil
}

// Failure reads the newest turn-stopped event for a session.
func (s *Store) Failure(sessionID string) (Failure, error) {
	var failure Failure
	if err := s.read(s.failurePath(sessionID), sessionID, &failure); err != nil {
		return Failure{}, err
	}
	if failure.Version > RecordVersion {
		return Failure{}, fmt.Errorf("Claude failure record was written by a newer helper (version %d)", failure.Version)
	}
	if failure.SessionID != sessionID {
		return Failure{}, fmt.Errorf("Claude failure record describes a different session")
	}
	return failure, nil
}

// Installed reports whether the recorder has ever written anything. It is how
// doctor tells "not installed yet" from "installed but this session has not
// reported", which are different problems with different fixes.
func (s *Store) Installed() bool {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			return true
		}
	}
	return false
}

// Prune removes records untouched since before cutoff. Sessions end without
// notice, so their records would otherwise accumulate forever.
func (s *Store) Prune(cutoff time.Time) error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var failed error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir, entry.Name())); err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

func (s *Store) samplePath(sessionID string) string {
	return filepath.Join(s.Dir, sessionID+".json")
}

func (s *Store) failurePath(sessionID string) string {
	return filepath.Join(s.Dir, sessionID+".failure.json")
}

func (s *Store) write(path string, record any) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create the Claude usage directory: %w", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode Claude usage record: %w", err)
	}
	temporary, err := os.CreateTemp(s.Dir, ".usage-*.tmp")
	if err != nil {
		return fmt.Errorf("create Claude usage record: %w", err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect Claude usage record: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Claude usage record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush Claude usage record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Claude usage record: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("publish Claude usage record: %w", err)
	}
	return nil
}

func (s *Store) read(path, sessionID string, into any) error {
	if !ValidSessionID(sessionID) {
		return fmt.Errorf("refusing to read a record for an unrecognized session id")
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoRecord
		}
		return fmt.Errorf("inspect Claude usage record: %w", err)
	}
	if info.Size() > maxRecordBytes {
		return fmt.Errorf("Claude usage record is larger than this adapter accepts")
	}
	// #nosec G304 -- path is built from the store's own directory and a session
	// id validated against sessionIDPattern above.
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Claude usage record: %w", err)
	}
	if err := json.Unmarshal(contents, into); err != nil {
		return fmt.Errorf("decode Claude usage record: %w", err)
	}
	return nil
}
